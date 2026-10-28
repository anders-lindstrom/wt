package provision

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// ProbeSuperset is how these commands find Superset. A variable so a test
// binary can put one state in front of all its tests, in its TestMain,
// without a Superset on the machine running it; one test's own goes on its
// Context instead. A serial test that wants the real Probe swaps it per test,
// as provisiontest.SupersetSaying and StaleSuperset do.
var ProbeSuperset = superset.Probe

// SupersetStatus is Superset as ctx sees it: its SupersetFake when a test set
// one, ProbeSuperset otherwise.
func SupersetStatus(ctx *kit.Context) superset.Status {
	if ctx.SupersetFake != nil {
		return *ctx.SupersetFake
	}
	return ProbeSuperset()
}

// SupersetHostCommand reads a host pid's command line, for `wt doctor` alone.
// A variable so a test can answer for a process that is not there.
// WORKAROUND(superset-stale-status)
var SupersetHostCommand = superset.ProcessCommand

// RegisterSuperset adopts a worktree wt has just created as a workspace in the
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
func RegisterSuperset(ctx *kit.Context, path string, w io.Writer) (result, reason string) {
	if !ctx.UserConfig().Superset {
		return kit.StepSkipped, "Superset is not switched on for you (wt config set superset true)"
	}
	mode := ctx.Config.SupersetRegister
	if mode != config.SupersetAuto && mode != config.SupersetOn {
		return kit.StepSkipped, config.KeySupersetRegister + "=" + string(mode)
	}
	// missed is Superset not being there to take the worktree: a skip under
	// auto, a failure under on.
	missed := func(why string) (string, string) {
		if mode == config.SupersetOn {
			return kit.StepFailed, why
		}
		return kit.StepSkipped, why
	}
	status := SupersetStatus(ctx)
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
		return kit.StepFailed, say("%v; not registered", status.Err)
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
		return kit.StepFailed, say("%v; not registered", err)
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
		return kit.StepFailed, say("%v; not registered", err)
	}
	if reg.AlreadyExists {
		fmt.Fprintf(w, "- Superset already had a workspace for %s\n", branch)
		return kit.StepRegistered, "Superset already had a workspace for " + branch
	}
	fmt.Fprintf(w, "✓ registered with Superset as a workspace of project %s\n", project.Name)
	return kit.StepRegistered, ""
}

// SupersetEnabled is whether wt new would try to register a worktree here:
// the person opted in and the repository does not say off. Whether Superset
// is installed, running and tracks the repository is not asked.
func SupersetEnabled(ctx *kit.Context) bool {
	mode := ctx.Config.SupersetRegister
	return ctx.UserConfig().Superset && (mode == config.SupersetAuto || mode == config.SupersetOn)
}

// What removal's superset step came to, beside kit.StepSkipped and kit.StepFailed.
const (
	// StepDeregistered is the worktree's Superset workspace deleted.
	StepDeregistered = "deregistered"
	// StepNotRegistered is a running Superset with no workspace there.
	StepNotRegistered = "notRegistered"
	// StepOptedOut is --keep-superset: Superset was not asked anything.
	StepOptedOut = "optedOut"
)

// SupersetWorkspaces is what a removal found in Superset for the worktree it
// is about to remove. It is read before the removal, while the path still
// resolves through its symlinks, and acted on after.
type SupersetWorkspaces struct {
	cli   superset.CLI
	found []superset.ListedWorkspace
	// result is what the step comes to without a delete: a Superset that
	// is off, absent, down or unreadable, or has no workspace there. ""
	// when found holds the workspaces to delete. say is its one line, ""
	// for silence.
	result, reason, say string
}

// KeptSupersetWorkspaces is the step under --keep-superset: the workspace,
// if any, is left, and Superset is not asked. It says so only when the
// integration would otherwise have asked.
func KeptSupersetWorkspaces(ctx *kit.Context) SupersetWorkspaces {
	why := "--keep-superset: its Superset workspace, if any, was left"
	s := SupersetWorkspaces{result: StepOptedOut, reason: why}
	if SupersetEnabled(ctx) {
		s.say = "- " + why
	}
	return s
}

// FindSupersetWorkspaces finds the live Superset workspaces whose checkout
// is path, when the person opted in and the repository does not say off —
// the same switch wt new registers by. It prints nothing: a removal that is
// then refused has nothing to say about Superset.
func FindSupersetWorkspaces(ctx *kit.Context, path string) SupersetWorkspaces {
	if !SupersetEnabled(ctx) {
		return SupersetWorkspaces{result: kit.StepSkipped, reason: "the Superset integration is off"}
	}
	status := SupersetStatus(ctx)
	switch {
	case !status.Installed():
		return SupersetWorkspaces{result: kit.StepSkipped, reason: "no superset on the PATH or at ~/.superset/bin"}
	case status.Err != nil:
		why := fmt.Sprintf("%v; its Superset workspace, if any, was left", status.Err)
		return SupersetWorkspaces{result: kit.StepFailed, reason: why, say: "! " + why}
	case !status.Running:
		why := "Superset's host service is not running; its workspace, if any, was left"
		return SupersetWorkspaces{result: kit.StepSkipped, reason: why, say: "- " + why}
	}
	cli := status.CLI()
	ws, err := cli.Workspaces()
	if err != nil {
		why := fmt.Sprintf("%v; its Superset workspace, if any, was left", err)
		return SupersetWorkspaces{result: kit.StepFailed, reason: why, say: "! " + why}
	}
	found := superset.WorkspacesAt(ws, path)
	if len(found) == 0 {
		return SupersetWorkspaces{result: StepNotRegistered}
	}
	return SupersetWorkspaces{cli: cli, found: found}
}

// Deregister deletes the workspaces found at path, once the worktree is gone.
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
func (s SupersetWorkspaces) Deregister(ctx *kit.Context, path string, w io.Writer) (result, reason string) {
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
			return kit.StepSkipped, why
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
		return kit.StepFailed, why
	}
	if len(warnings) > 0 {
		fmt.Fprintf(w, "✓ deleted its Superset workspace; Superset warned: %s\n", kit.OneLine(strings.Join(warnings, "; ")))
		return StepDeregistered, ""
	}
	fmt.Fprintln(w, "✓ deleted its Superset workspace")
	return StepDeregistered, ""
}

// worktreeStillAt says what is still there of a worktree at any of paths:
// a directory, or git's registration. "" when nothing is, and only when that
// could be read: a path that cannot be looked at, or a worktree list that
// cannot be read, counts as there.
func worktreeStillAt(ctx *kit.Context, paths ...string) string {
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
