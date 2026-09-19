package commands

import (
	"fmt"
	"io"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// probeSuperset is how these commands find Superset. A variable so a test can
// put a state in front of it without a Superset on the machine running it.
var probeSuperset = superset.Probe

// registerSuperset adopts a worktree wt has just created as a workspace in the
// Superset desktop app.
//
// Nothing here can fail the command it hangs off: every outcome is one line on
// w, which is stderr, and the exit code is untouched. The user's own `superset`
// setting is checked first, so until they opt in wt starts no subprocess and
// says nothing, whatever worktree.conf asks for.
func registerSuperset(ctx *Context, path string, w io.Writer) {
	if !ctx.UserConfig().Superset {
		return
	}
	mode := ctx.Config.SupersetRegister
	if mode != config.SupersetAuto && mode != config.SupersetOn {
		return
	}
	status := probeSuperset()
	if !status.Installed() {
		if mode == config.SupersetOn {
			fmt.Fprintf(w, "! %s=on but no superset on the PATH or at ~/.superset/bin; not registered\n",
				config.KeySupersetRegister)
		}
		return
	}
	say := func(format string, args ...any) {
		mark := "-"
		if mode == config.SupersetOn {
			mark = "!"
		}
		fmt.Fprintf(w, mark+" "+format+"\n", args...)
	}
	switch {
	case status.Err != nil:
		say("%v; not registered", status.Err)
		return
	case !status.Running:
		// Starting it is the person's call, not wt's: `wt new` must not bring
		// a desktop app up behind them.
		say("Superset's host service is not running; not registered")
		return
	}

	branch := ctx.Repo.BranchAt(path)
	if branch == "" {
		say("%s is not on a branch; not registered with Superset", path)
		return
	}
	cli := status.CLI()
	projects, err := cli.Projects()
	if err != nil {
		say("%v; not registered", err)
		return
	}
	// A repository Superset has never heard of is an ordinary state, and an
	// integration that does not apply here says nothing — so under auto this
	// is silence, and only a repository that asked for registration with
	// SUPERSET_REGISTER=on hears about it. `wt doctor` always says it.
	// Creating the project would be choosing for the person which
	// repositories their app tracks, so wt never does that.
	project, ok := superset.ProjectAt(projects, ctx.Repo.MainRoot)
	if !ok {
		if mode == config.SupersetOn {
			say("Superset has no project for %s; not registered", ctx.Repo.MainRoot)
		}
		return
	}
	reg, err := cli.Register(project.ID, branch)
	if err != nil {
		say("%v; not registered", err)
		return
	}
	if reg.AlreadyExists {
		fmt.Fprintf(w, "- Superset already had a workspace for %s\n", branch)
		return
	}
	fmt.Fprintf(w, "✓ registered with Superset as a workspace of project %s\n", project.Name)
}
