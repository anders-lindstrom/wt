package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
)

// SetupOptions controls provisioning.
type SetupOptions struct {
	Source    string
	SkipBuild bool
}

// Setup provisions a worktree: developer config, the repo's own provision.sh,
// submodules, then build initialisation. The order matches the bash
// implementation this replaces.
//
// A build-init failure is reported as a warning and does not fail the command,
// because every agent that provisions a worktree would otherwise start failing
// on a transient dependency problem.
func Setup(ctx *Context, target string, opts SetupOptions, w io.Writer) error {
	src := opts.Source
	if src == "" {
		src = ctx.Repo.MainRoot
	}

	if _, err := os.Stat(src); err != nil {
		fmt.Fprintf(w, " - source %s not found; skipping config sync\n", src)
	} else {
		copyConfigDirs(ctx, src, target, w)
		copyConfigFiles(ctx, src, target, w)
	}

	if err := runProvision(ctx, target, w); err != nil {
		return err
	}
	initSubmodules(target, w)
	runBuildInit(ctx, target, opts, w)

	fmt.Fprintln(w, "✓ Worktree setup complete")
	return nil
}

// within reports whether candidate stays inside base. A config entry like
// "../../etc/thing" would otherwise have Setup write outside the worktree it
// is provisioning — a typo in worktree.conf silently escaping is a real bug,
// not merely a lint finding.
func within(base, candidate string) bool {
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func copyConfigDirs(ctx *Context, src, target string, w io.Writer) {
	for _, d := range ctx.Config.DeveloperConfigDirs {
		from, to := filepath.Join(src, d), filepath.Join(target, d)
		if !within(target, to) {
			fmt.Fprintf(w, " ! %s escapes the worktree, refusing to copy it\n", d)
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
			continue
		}
		fmt.Fprintf(w, " ✓ copied %s\n", d)
	}
}

func copyConfigFiles(ctx *Context, src, target string, w io.Writer) {
	for _, f := range ctx.Config.DeveloperConfigFiles {
		from, to := filepath.Join(src, f), filepath.Join(target, f)
		if !within(target, to) {
			fmt.Fprintf(w, " ! %s escapes the worktree, refusing to copy it\n", f)
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
			continue
		}
		if err := copyFile(from, to); err != nil {
			fmt.Fprintf(w, " ! failed to copy %s: %v\n", f, err)
			continue
		}
		fmt.Fprintf(w, " ✓ copied %s\n", f)
	}
}

// runProvision executes the repository's own setup step, if it declares one.
// This is what AWS_SETUP_ENABLED became: repo-declared behaviour rather than a
// Telcred-shaped flag in a generic tool. Its failure IS fatal — a worktree
// without decrypted secrets is not usable.
func runProvision(ctx *Context, target string, w io.Writer) error {
	if !ctx.HasProvisionScript() {
		return nil
	}
	script := filepath.Join(ctx.Repo.MainRoot, "bin", "worktree", "provision.sh")
	fmt.Fprintln(w, "Running bin/worktree/provision.sh...")
	cmd := exec.Command(script)
	cmd.Dir = target
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("provision.sh failed: %w", err)
	}
	fmt.Fprintln(w, "✓ provision.sh complete")
	return nil
}

func initSubmodules(target string, w io.Writer) {
	if _, err := os.Stat(filepath.Join(target, ".gitmodules")); err != nil {
		return
	}
	fmt.Fprintln(w, "Initializing git submodules...")
	if _, err := git.Run(target, "submodule", "update", "--init", "--recursive"); err != nil {
		fmt.Fprintf(w, " ! Warning: failed to initialise submodules: %v\n", err)
		return
	}
	fmt.Fprintln(w, "✓ submodules initialised")
}

func runBuildInit(ctx *Context, target string, opts SetupOptions, w io.Writer) {
	switch {
	case opts.SkipBuild:
		fmt.Fprintln(w, "⏭ build initialisation skipped (--skip-build)")
		return
	case !ctx.Config.BuildInitEnabled:
		fmt.Fprintln(w, "- build initialisation disabled in configuration")
		return
	}
	fmt.Fprintf(w, "Running: %s\n", ctx.Config.BuildInitCommand)
	cmd := exec.Command("sh", "-c", ctx.Config.BuildInitCommand)
	cmd.Dir = target
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(w, " ! Warning: build initialisation failed: %v\n", err)
		fmt.Fprintf(w, "   Try running it by hand: %s\n", ctx.Config.BuildInitCommand)
		return
	}
	fmt.Fprintln(w, "✓ build dependencies downloaded")
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
