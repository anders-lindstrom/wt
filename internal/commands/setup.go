package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// SetupOptions controls provisioning.
type SetupOptions struct {
	// SourceDir is where developer config is copied from; empty means the
	// repository's main checkout.
	SourceDir string
	SkipBuild bool
	// Source names what ran setup, such as "superset". Only printed for now.
	Source string
}

// Setup provisions a worktree: developer config, the repo's own provision.sh,
// submodules, then build initialisation. The order matches the bash
// implementation this replaces.
//
// A build-init failure is reported as a warning and does not fail the command,
// because every agent that provisions a worktree would otherwise start failing
// on a transient dependency problem.
func Setup(ctx *Context, target string, opts SetupOptions, w io.Writer) error {
	return setup(ctx, target, opts, w, nil)
}

// setup is Setup, recording each step in j for --json. What it prints does
// not depend on j.
func setup(ctx *Context, target string, opts SetupOptions, w io.Writer, j *CreateJournal) error {
	if opts.Source != "" {
		fmt.Fprintf(w, "Setup run by %s\n", opts.Source)
	}
	src := opts.SourceDir
	if src == "" {
		src = ctx.Repo.MainRoot
	}

	j.start(StepConfig)
	if _, err := os.Stat(src); err != nil {
		fmt.Fprintf(w, " - source %s not found; skipping config sync\n", src)
		j.finish(StepConfig, StepSkipped, "source "+src+" not found", "")
	} else {
		failed := append(copyConfigDirs(ctx, src, target, w), copyConfigFiles(ctx, src, target, w)...)
		j.finish(StepConfig, failedResult(failed), strings.Join(failed, "; "), "")
	}

	// A failed provision step is reported but does not abort the rest: stopping
	// here would leave a worktree with no submodules and no dependencies
	// either, which is strictly less usable than one that merely lacks
	// secrets. The error still surfaces, so nothing treats this as success.
	j.start(StepProvision)
	provisionErr := runProvision(ctx, target, w, j)
	result, reason := stepOutcome(ctx.HasProvisionScript(), "no bin/worktree/provision.sh", provisionErr)
	j.finish(StepProvision, result, reason, "")
	j.start(StepSubmodules)
	result, reason = stepOutcome(ctx.Repo.HasSubmodules(target), "no .gitmodules", initSubmodules(ctx, target, w))
	j.finish(StepSubmodules, result, reason, "")
	j.start(StepBuild)
	result, reason = runBuildInit(ctx, target, opts, w, j)
	j.finish(StepBuild, result, reason, "")

	if provisionErr != nil {
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "! %v\n", provisionErr)
		fmt.Fprintln(w, "  The worktree is otherwise set up. Fix the cause and finish it with:")
		fmt.Fprintf(w, "      cd %s && wt setup\n", target)
		return provisionErr
	}
	fmt.Fprintln(w, "✓ Worktree setup complete")
	reportLayout(ctx, target, w)
	return nil
}

// reportLayout names the directory that was provisioned, and the layout it is
// in when that is not wt's own. Setup never moves a worktree — it provisions
// whatever it is pointed at, where it stands — but saying nothing left the
// caller of `wt setup` from a Superset workspace with a path they had not
// chosen and no explanation of who chose it.
func reportLayout(ctx *Context, target string, w io.Writer) {
	fmt.Fprintf(w, "  %s\n", target)
	typ, work, layout, ok := ctx.Scheme().ClassifyBranch(target, ctx.Repo.BranchAt(target))
	if !ok {
		return
	}
	switch layout {
	case naming.Superset:
		fmt.Fprintln(w, "  Superset's layout — provisioned where Superset put it; setup never moves a worktree")
	case naming.Foreign:
		fmt.Fprintf(w, "  not a layout wt recognises — provisioned where it is; `wt migrate %s/%s` moves it\n",
			typ, work)
	}
}

// within reports whether candidate stays inside base. A config entry like
// "../../etc/thing" would otherwise have Setup write outside the worktree it
// is provisioning — a typo in worktree.conf silently escaping is a real bug,
// not merely a lint finding.
func within(base, candidate string) bool {
	return repo.Inside(base, candidate, false)
}

// copyConfigDirs copies the declared directories, and returns a line for
// each it could not.
func copyConfigDirs(ctx *Context, src, target string, w io.Writer) (failed []string) {
	for _, d := range ctx.Config.DeveloperConfigDirs {
		from, to := filepath.Join(src, d), filepath.Join(target, d)
		if !within(target, to) {
			fmt.Fprintf(w, " ! %s escapes the worktree, refusing to copy it\n", d)
			failed = append(failed, d+" escapes the worktree")
			continue
		}
		if _, err := os.Stat(from); err != nil {
			fmt.Fprintf(w, " - %s not in source, skipping\n", d)
			continue
		}
		if _, err := os.Stat(to); err == nil {
			fmt.Fprintf(w, " - %s already exists, skipping\n", d)
			continue
		}
		if err := copyTree(from, to); err != nil {
			fmt.Fprintf(w, " ! failed to copy %s: %v\n", d, err)
			failed = append(failed, fmt.Sprintf("%s: %v", d, err))
			continue
		}
		fmt.Fprintf(w, " ✓ copied %s\n", d)
	}
	return failed
}

// copyConfigFiles copies the declared files, and returns a line for each it
// could not.
func copyConfigFiles(ctx *Context, src, target string, w io.Writer) (failed []string) {
	for _, f := range ctx.Config.DeveloperConfigFiles {
		from, to := filepath.Join(src, f), filepath.Join(target, f)
		if !within(target, to) {
			fmt.Fprintf(w, " ! %s escapes the worktree, refusing to copy it\n", f)
			failed = append(failed, f+" escapes the worktree")
			continue
		}
		if st, err := os.Stat(from); err != nil || st.IsDir() {
			fmt.Fprintf(w, " - %s not in source, skipping\n", f)
			continue
		}
		if _, err := os.Stat(to); err == nil {
			fmt.Fprintf(w, " - %s already exists, skipping\n", f)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			fmt.Fprintf(w, " ! failed to create %s: %v\n", filepath.Dir(to), err)
			failed = append(failed, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		if err := copyFile(from, to); err != nil {
			fmt.Fprintf(w, " ! failed to copy %s: %v\n", f, err)
			failed = append(failed, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		fmt.Fprintf(w, " ✓ copied %s\n", f)
	}
	return failed
}

// runProvision executes the repository's own setup step, if it declares one.
// This is what AWS_SETUP_ENABLED became: repo-declared behaviour rather than a
// Telcred-shaped flag in a generic tool. Its failure IS fatal — a worktree
// without decrypted secrets is not usable.
func runProvision(ctx *Context, target string, w io.Writer, j *CreateJournal) error {
	if !ctx.HasProvisionScript() {
		return nil
	}
	script := filepath.Join(ctx.Repo.Root, "bin", "worktree", "provision.sh")
	fmt.Fprintln(w, "Running bin/worktree/provision.sh...")
	cmd := exec.Command(script)
	cmd.Dir = target
	cmd.Stdout, cmd.Stderr = w, w
	if err := runTracked(cmd, j); err != nil {
		return fmt.Errorf("provision.sh failed: %w", err)
	}
	fmt.Fprintln(w, "✓ provision.sh complete")
	return nil
}

// initSubmodules initialises the worktree's submodules, if it declares any.
// A failure is a warning here and an error for the caller to record: the
// command's exit code does not depend on it.
func initSubmodules(ctx *Context, target string, w io.Writer) error {
	if !ctx.Repo.HasSubmodules(target) {
		return nil
	}
	fmt.Fprintln(w, "Initializing git submodules...")
	if _, err := git.Run(target, "submodule", "update", "--init", "--recursive"); err != nil {
		fmt.Fprintf(w, " ! Warning: failed to initialise submodules: %v\n", err)
		return fmt.Errorf("git submodule update --init --recursive failed: %w", err)
	}
	fmt.Fprintln(w, "✓ submodules initialised")
	return nil
}

// runBuildInit runs the build command, and returns what came of it as a step
// result. A failure is a warning, as for submodules.
func runBuildInit(ctx *Context, target string, opts SetupOptions, w io.Writer, j *CreateJournal) (result, reason string) {
	switch {
	case opts.SkipBuild:
		fmt.Fprintln(w, "⏭ build initialisation skipped (--no-build)")
		return StepSkipped, "--no-build"
	case !ctx.Config.BuildInitEnabled:
		fmt.Fprintln(w, "- build initialisation disabled in configuration")
		return StepSkipped, "build initialisation disabled in configuration"
	}
	fmt.Fprintf(w, "Running: %s\n", ctx.Config.BuildInitCommand)
	cmd := exec.Command("sh", "-c", ctx.Config.BuildInitCommand)
	cmd.Dir = target
	cmd.Stdout, cmd.Stderr = w, w
	if err := runTracked(cmd, j); err != nil {
		fmt.Fprintf(w, " ! Warning: build initialisation failed: %v\n", err)
		fmt.Fprintf(w, "   Try running it by hand: %s\n", ctx.Config.BuildInitCommand)
		return StepFailed, fmt.Sprintf("%s failed: %v", ctx.Config.BuildInitCommand, err)
	}
	fmt.Fprintln(w, "✓ build dependencies downloaded")
	return StepDone, ""
}

// runTracked is cmd.Run. In a --json run the command runs in a process
// group of its own that a signal's handler stops, and the run parks rather
// than act on the failure; without --json it runs as it always has.
func runTracked(cmd *exec.Cmd, j *CreateJournal) error {
	if j == nil {
		return cmd.Run()
	}
	_, _, err := git.RunBounded(0, cmd)
	j.park()
	return err
}

// stepOutcome is a step's result: skipped, with why, when it had nothing to
// do; failed with the error; done.
func stepOutcome(applies bool, whyNot string, err error) (result, reason string) {
	switch {
	case !applies:
		return StepSkipped, whyNot
	case err != nil:
		return StepFailed, oneLine(err.Error())
	}
	return StepDone, ""
}

func failedResult(failed []string) string {
	if len(failed) > 0 {
		return StepFailed
	}
	return StepDone
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	st, err := os.Stat(from)
	if err != nil {
		return err
	}
	// #nosec G703 -- callers guard the destination with within(), which keeps
	// it inside the worktree being provisioned. gosec's taint analysis cannot
	// see that guard across the call. Covered by
	// TestSetupRefusesConfigEntriesThatEscapeTheWorktree.
	return os.WriteFile(to, b, st.Mode().Perm())
}

func copyTree(from, to string) error {
	return filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, dst)
		}
		return copyFile(path, dst)
	})
}
