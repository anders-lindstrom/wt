package commands

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// probeSuperset is how these commands find Superset. A variable so a test can
// put a state in front of it without a Superset on the machine running it.
var probeSuperset = superset.Probe

// supersetHostCommand reads a host pid's command line, for `wt doctor` alone.
// A variable so a test can answer for a process that is not there.
// WORKAROUND(superset-stale-status)
var supersetHostCommand = superset.ProcessCommand

// registerSuperset adopts a worktree wt has just created as a workspace in the
// Superset desktop app.
//
// Nothing here can fail the command it hangs off: every outcome is one line on
// w, which is stderr, and the exit code is untouched. The user's own `superset`
// setting is checked first, so until they opt in wt starts no subprocess and
// says nothing, whatever worktree.conf asks for.
//
// It returns what came of it as a --json step result: registered; skipped
// when registration is off or, under auto, Superset is not there to take it;
// failed when Superset errs, or under SUPERSET_REGISTER=on did not take it.
func registerSuperset(ctx *Context, path string, w io.Writer) (result, reason string) {
	if !ctx.UserConfig().Superset {
		return StepSkipped, "Superset is not switched on for you (wt config set superset true)"
	}
	mode := ctx.Config.SupersetRegister
	if mode != config.SupersetAuto && mode != config.SupersetOn {
		return StepSkipped, config.KeySupersetRegister + "=" + string(mode)
	}
	// missed is Superset not being there to take the worktree: a skip under
	// auto, a failure under on.
	missed := func(why string) (string, string) {
		if mode == config.SupersetOn {
			return StepFailed, why
		}
		return StepSkipped, why
	}
	status := probeSuperset()
	if !status.Installed() {
		if mode == config.SupersetOn {
			fmt.Fprintf(w, "! %s=on but no superset on the PATH or at ~/.superset/bin; not registered\n",
				config.KeySupersetRegister)
		}
		return missed("no superset on the PATH or at ~/.superset/bin")
	}
	say := func(format string, args ...any) string {
		mark := "-"
		if mode == config.SupersetOn {
			mark = "!"
		}
		fmt.Fprintf(w, mark+" "+format+"\n", args...)
		return fmt.Sprintf(format, args...)
	}
	switch {
	case status.Err != nil:
		return StepFailed, say("%v; not registered", status.Err)
	case !status.Running:
		// Starting it is the person's call, not wt's: `wt new` must not bring
		// a desktop app up behind them.
		return missed(say("Superset's host service is not running; not registered"))
	}

	branch := ctx.Repo.BranchAt(path)
	if branch == "" {
		return missed(say("%s is not on a branch; not registered with Superset", path))
	}
	cli := status.CLI()
	projects, err := cli.Projects()
	if err != nil {
		return StepFailed, say("%v; not registered", err)
	}
	// A repository Superset has never heard of is an ordinary state, and an
	// integration that does not apply here says nothing — so under auto this
	// is silence, and only a repository that asked for registration with
	// SUPERSET_REGISTER=on hears about it. `wt doctor` always says it.
	// Creating the project would be choosing for the person which
	// repositories their app tracks, so wt never does that.
	project, ok := superset.ProjectAt(projects, ctx.Repo.MainRoot)
	if !ok {
		why := fmt.Sprintf("Superset has no project for %s; not registered", ctx.Repo.MainRoot)
		if mode == config.SupersetOn {
			say("%s", why)
		}
		return missed(why)
	}
	reg, err := cli.Register(project.ID, branch)
	if err != nil {
		return StepFailed, say("%v; not registered", err)
	}
	if reg.AlreadyExists {
		fmt.Fprintf(w, "- Superset already had a workspace for %s\n", branch)
		return StepRegistered, "Superset already had a workspace for " + branch
	}
	fmt.Fprintf(w, "✓ registered with Superset as a workspace of project %s\n", project.Name)
	return StepRegistered, ""
}

// supersetEnabled is whether wt new would try to register a worktree here:
// the person opted in and the repository does not say off. Whether Superset
// is installed, running and tracks the repository is not asked.
func supersetEnabled(ctx *Context) bool {
	mode := ctx.Config.SupersetRegister
	return ctx.UserConfig().Superset && (mode == config.SupersetAuto || mode == config.SupersetOn)
}

// What removal's superset step came to, beside StepSkipped and StepFailed.
const (
	// StepDeregistered is the worktree's Superset workspace deleted.
	StepDeregistered = "deregistered"
	// StepNotRegistered is a running Superset with no workspace there.
	StepNotRegistered = "notRegistered"
	// StepOptedOut is --keep-superset: Superset was not asked anything.
	StepOptedOut = "optedOut"
)

// supersetWorkspaces is what a removal found in Superset for the worktree it
// is about to remove. It is read before the removal, while the path still
// resolves through its symlinks, and acted on after.
type supersetWorkspaces struct {
	cli   superset.CLI
	found []superset.ListedWorkspace
	// result is what the step comes to without a delete: a Superset that
	// is off, absent, down or unreadable, or has no workspace there. ""
	// when found holds the workspaces to delete. say is its one line, ""
	// for silence.
	result, reason, say string
}

// keptSupersetWorkspaces is the step under --keep-superset: the workspace,
// if any, is left, and Superset is not asked. It says so only when the
// integration would otherwise have asked.
func keptSupersetWorkspaces(ctx *Context) supersetWorkspaces {
	why := "--keep-superset: its Superset workspace, if any, was left"
	s := supersetWorkspaces{result: StepOptedOut, reason: why}
	if supersetEnabled(ctx) {
		s.say = "- " + why
	}
	return s
}

// findSupersetWorkspaces finds the live Superset workspaces whose checkout
// is path, when the person opted in and the repository does not say off —
// the same switch wt new registers by. It prints nothing: a removal that is
// then refused has nothing to say about Superset.
func findSupersetWorkspaces(ctx *Context, path string) supersetWorkspaces {
	if !supersetEnabled(ctx) {
		return supersetWorkspaces{result: StepSkipped, reason: "the Superset integration is off"}
	}
	status := probeSuperset()
	switch {
	case !status.Installed():
		return supersetWorkspaces{result: StepSkipped, reason: "no superset on the PATH or at ~/.superset/bin"}
	case status.Err != nil:
		why := fmt.Sprintf("%v; its Superset workspace, if any, was left", status.Err)
		return supersetWorkspaces{result: StepFailed, reason: why, say: "! " + why}
	case !status.Running:
		why := "Superset's host service is not running; its workspace, if any, was left"
		return supersetWorkspaces{result: StepSkipped, reason: why, say: "- " + why}
	}
	cli := status.CLI()
	ws, err := cli.Workspaces()
	if err != nil {
		why := fmt.Sprintf("%v; its Superset workspace, if any, was left", err)
		return supersetWorkspaces{result: StepFailed, reason: why, say: "! " + why}
	}
	found := superset.WorkspacesAt(ws, path)
	if len(found) == 0 {
		return supersetWorkspaces{result: StepNotRegistered}
	}
	return supersetWorkspaces{cli: cli, found: found}
}

// deregister deletes the workspaces found at path, once the worktree is gone.
//
// Superset's delete force-removes the workspace's worktree, past every check
// wt makes, so it is only asked once nothing is there to remove: no directory
// at path or at the path Superset recorded, and git listing neither. That is
// read again here, right before the call. The check and the delete are not
// atomic: a worktree made at the same path in between would be removed by
// Superset. Its CLI has no delete that leaves the checkout alone.
//
// Like registration it never fails the removal: every outcome is at most one
// line on w.
func (s supersetWorkspaces) deregister(ctx *Context, path string, w io.Writer) (result, reason string) {
	if s.result != "" {
		if s.say != "" {
			fmt.Fprintln(w, s.say)
		}
		return s.result, s.reason
	}
	var failed, warnings []string
	for _, ws := range s.found {
		if still := worktreeStillAt(ctx, path, ws.WorktreePath); still != "" {
			why := still + "; its Superset workspace was left"
			fmt.Fprintf(w, "! %s\n", why)
			return StepSkipped, why
		}
		warned, err := s.cli.Delete(ws.ID)
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}
		warnings = append(warnings, warned...)
	}
	if len(failed) > 0 {
		why := strings.Join(failed, "; ") + "; its Superset workspace was left"
		fmt.Fprintf(w, "! %s\n", why)
		return StepFailed, why
	}
	if len(warnings) > 0 {
		fmt.Fprintf(w, "✓ deleted its Superset workspace; Superset warned: %s\n", oneLine(strings.Join(warnings, "; ")))
		return StepDeregistered, ""
	}
	fmt.Fprintln(w, "✓ deleted its Superset workspace")
	return StepDeregistered, ""
}

// worktreeStillAt says what is still there of a worktree at any of paths:
// a directory, or git's registration. "" when nothing is, and only when that
// could be read: a path that cannot be looked at, or a worktree list that
// cannot be read, counts as there.
func worktreeStillAt(ctx *Context, paths ...string) string {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return "git's worktree list cannot be read"
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			return p + " is still there"
		}
		if _, ok := worktrees.ByPath(p); ok {
			return "git still lists " + p
		}
	}
	return ""
}
