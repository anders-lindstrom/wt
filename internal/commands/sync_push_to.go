package commands

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// SyncPushTo is wt sync push-to: it records where a worktree's branch
// pushes, forgets what is recorded, or with neither only says where a push
// of it goes and what decides that, with the push itself when the branch has
// something to push: after a run left it unpushed, that line is the only
// thing that pushes it before its next rebase. Nothing is pushed and no
// remote is asked.
func SyncPushTo(ctx *Context, work, dest string, unset bool, w io.Writer) error {
	wt, err := locateBranch(ctx, work)
	if err != nil {
		return err
	}
	switch {
	case unset:
		if err := ctx.Repo.UnsetPushTo(wt.Branch); err != nil {
			return err
		}
	case dest != "":
		to, err := resolvePushTo(ctx, dest)
		if err != nil {
			return err
		}
		if err := ctx.Repo.SetPushTo(wt.Branch, to); err != nil {
			return err
		}
	}
	own := wtsync.ReadOwnOrUnknown(ctx.Repo.MainRoot, ctx.Config.MainBranch)
	to, state := own.Push(wt.Branch), own.State(wt.Branch)
	if to.Remote == "" {
		fmt.Fprintf(w, "%s has nowhere to push: %s\n", wt.Branch, notPushed(to))
	} else {
		fmt.Fprintf(w, "%s pushes to %s (%s)\n", wt.Branch, to.Ref(), to.Rule)
		if _, there := ctx.Repo.ResolveRef(to.Tracking); !there {
			fmt.Fprintf(w, "  %s is not there as last fetched: the first push makes it\n", to.Ref())
		}
		// wt pushes at the end of a rebase and at no other time, so a
		// branch with something to push now is pushed by this line or not
		// at all until trunk moves again.
		if state.State != wtsync.OwnInSync {
			fmt.Fprintf(w, "  wt pushes it after its next rebase. To push it now:\n  %s\n", pushLine(pushTarget{Branch: wt.Branch, Path: wt.Path}, to))
		}
	}
	if line := ownStray(state); line != "" {
		fmt.Fprintf(w, "  %s\n", line)
	}
	return nil
}

// resolvePushTo reads <remote>/<branch> against the remotes that exist: the
// longest remote name it starts with is the remote, so origin/team/x is
// team/x on origin unless there is a remote origin/team. Trunk is refused:
// wt pushes nothing there.
func resolvePushTo(ctx *Context, dest string) (repo.PushTo, error) {
	remotes, err := ctx.Repo.Remotes()
	if err != nil {
		return repo.PushTo{}, err
	}
	var to repo.PushTo
	for _, r := range remotes {
		if branch, ok := strings.CutPrefix(dest, r+"/"); ok && branch != "" && len(r) > len(to.Remote) {
			to = repo.PushTo{Remote: r, Branch: branch}
		}
	}
	if to.Remote == "" {
		sort.Strings(remotes)
		if len(remotes) == 0 {
			return to, fmt.Errorf("%s names no remote branch: this repository has no remote", dest)
		}
		return to, fmt.Errorf("%s names no remote branch: write it as <remote>/<branch>, with one of the remotes here: %s",
			dest, strings.Join(remotes, ", "))
	}
	if !ctx.Repo.PushToName(to.Branch) {
		return to, fmt.Errorf("%s is not a branch name", to.Branch)
	}
	if to.Branch == ctx.Config.MainBranch {
		return to, fmt.Errorf("%s is trunk: wt pushes nothing there", to)
	}
	return to, nil
}
