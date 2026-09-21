package commands

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// Doctor reports configuration and worktree health, returning the number of
// problems found so the caller can set an exit code. It replaces
// setup_precheck.sh and additionally finds worktrees that are misplaced,
// unowned, or nested inside the main checkout.
func Doctor(ctx *Context, w io.Writer) (int, error) {
	problems := 0
	report := func(format string, args ...any) {
		problems++
		fmt.Fprintf(w, "  ! "+format+"\n", args...)
	}

	fmt.Fprintf(w, "Repository: %s (%s)\n", ctx.Repo.Name, ctx.Repo.MainRoot)

	// A broken configuration is a finding, not a reason to stop: diagnosing it
	// is the whole point of this command. Everything below is still checked
	// against defaults so one bad key does not hide the rest.
	fmt.Fprintln(w, "Configuration:")
	if ctx.ConfigError != nil {
		for _, line := range strings.Split(strings.TrimSpace(ctx.ConfigError.Error()), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasSuffix(line, ":") {
				continue
			}
			report("%s", strings.TrimPrefix(line, "- "))
		}
		if errors.Is(ctx.ConfigError, config.ErrNoConfig) {
			fmt.Fprintln(w, "  (checking the rest against wt's defaults)")
		} else {
			fmt.Fprintln(w, "  (checking the rest against the values that did parse)")
		}
	} else {
		fmt.Fprintf(w, "  ✓ main branch %s, default type %s\n",
			ctx.Config.MainBranch, ctx.Config.DefaultType)
		// Worth a line only where the two differ: branches named one way and
		// folders another is a surprise wt should own up to, not hide.
		if suffix, origin := ctx.BranchSuffix(); suffix != ctx.Config.TypeSuffix {
			fmt.Fprintf(w, "  ✓ branches carry %q (%s); worktree paths keep %q\n",
				suffix, origin, ctx.Config.TypeSuffix)
		}
	}
	if ctx.UserError != nil {
		report("%v", ctx.UserError)
	}

	fmt.Fprintln(w, "Required tools:")
	if len(ctx.Config.RequiredBins) == 0 {
		fmt.Fprintln(w, "  - none declared")
	}
	for _, bin := range ctx.Config.RequiredBins {
		if _, err := exec.LookPath(bin); err != nil {
			report("%s is declared in REQUIRED_BINS but not on PATH", bin)
		} else {
			fmt.Fprintf(w, "  ✓ %s\n", bin)
		}
	}

	if !ctx.Repo.BranchExists(ctx.Config.MainBranch) {
		report("MAIN_BRANCH %q does not exist locally", ctx.Config.MainBranch)
	}

	doctorSuperset(ctx, w, report)
	doctorGitHub(ctx, w)

	fmt.Fprintln(w, "Worktrees:")
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return problems, err
	}
	sch := ctx.Scheme()
	for _, wt := range worktrees {
		if wt.IsMain {
			continue
		}
		// A worktree inside the main checkout pollutes the parent repo and
		// breaks tooling that walks it.
		if repo.Inside(ctx.Repo.MainRoot, wt.Path, false) {
			report("%s is inside the main checkout", wt.Path)
			continue
		}
		typ, work, layout, ok := sch.ClassifyBranch(wt.Path, wt.Branch)
		if !ok {
			// A branch wt did not name, at a path wt did: `wt checkout` and
			// `wt pr checkout` make these, and the directory names the work.
			if _, work, atPath := sch.ClassifyPath(wt.Path); atPath {
				fmt.Fprintf(w, "  ✓ %s (%s, on %q)\n", wt.Path, work, wt.Branch)
				continue
			}
			fmt.Fprintf(w, "  - %s is on %q, not managed by wt\n", wt.Path, wt.Branch)
			continue
		}
		switch layout {
		case naming.Canonical:
			fmt.Fprintf(w, "  ✓ %s\n", wt.Path)
		case naming.Superset:
			// Not a problem: Superset puts every workspace here and stores the
			// absolute path, so the migrate this used to prescribe would have
			// broken the workspace it was aimed at.
			fmt.Fprintf(w, "  ✓ %s (Superset's layout)\n", wt.Path)
		default:
			report("%s is not at its canonical path (%s); run: wt migrate %s/%s",
				wt.Path, sch.Dir(typ, work), typ, work)
		}
	}

	if problems == 0 {
		fmt.Fprintln(w, "No problems found.")
		return problems, nil
	}
	fmt.Fprintf(w, "%d problem(s) found.\n", problems)
	switch {
	case errors.Is(ctx.ConfigError, config.ErrNoConfig):
		// Nothing above suggested a migrate: without a configuration there is
		// no canonical path to be off, so name the one command that helps.
		fmt.Fprintln(w, "Run `wt init` to create it, then run this again.")
	case ctx.ConfigError != nil:
		// Mutating commands stay strict about configuration, so advising a
		// migrate that will refuse would send you round in a circle.
		fmt.Fprintln(w, "Fix the configuration first — the migrate commands above need it.")
	}
	return problems, nil
}

// doctorSuperset reports whether a new worktree here would reach the Superset
// desktop app, and where it would stop if it would not.
func doctorSuperset(ctx *Context, w io.Writer, report func(string, ...any)) {
	fmt.Fprintln(w, "Superset:")
	// Nothing to probe when the user has not opted in, and no repository
	// setting overrides that.
	if !ctx.UserConfig().Superset {
		fmt.Fprintf(w, "  - off in your wt config; turn it on with `wt config set %s true`\n",
			config.UserKeySuperset)
		return
	}
	mode := ctx.Config.SupersetRegister
	if mode == config.SupersetOff {
		fmt.Fprintf(w, "  - %s=off; new worktrees are not registered\n", config.KeySupersetRegister)
		return
	}
	// Under =auto an inactive Superset is a remark; under =on the repository
	// asked for it, so the same sentence is counted as a problem.
	note := func(format string, args ...any) {
		if mode == config.SupersetOn {
			report(format, args...)
			return
		}
		fmt.Fprintf(w, "  - "+format+"\n", args...)
	}

	status := probeSuperset()
	if !status.Installed() {
		note("no superset on the PATH or at ~/.superset/bin; registration is inactive")
		return
	}
	version := status.CLI().Version()
	if version == "" {
		version = "version unknown"
	}
	switch {
	case status.Err != nil:
		note("%s (%s) would not say whether its host service is running: %v", status.Exe, version, status.Err)
		return
	case !status.Running:
		note("%s (%s) is installed but its host service is not running; start the Superset app", status.Exe, version)
		return
	}
	fmt.Fprintf(w, "  ✓ %s (%s), host service running\n", status.Exe, version)

	projects, err := status.CLI().Projects()
	if err != nil {
		note("could not list Superset projects: %v", err)
		return
	}
	project, ok := superset.ProjectAt(projects, ctx.Repo.MainRoot)
	if !ok {
		note("no Superset project for %s; registration is inactive", ctx.Repo.MainRoot)
		return
	}
	fmt.Fprintf(w, "  ✓ project %q — new worktrees are registered as workspaces\n", project.Name)
}
