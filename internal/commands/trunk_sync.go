package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Why local trunk was left where it was, as --json names it in
// trunkSync.skippedReason.
const (
	// TrunkSkipOptedOut is --no-ff-trunk, or ff_trunk false in the user
	// config.
	TrunkSkipOptedOut = "optedOut"
	// TrunkSkipAhead is local trunk with commits origin lacks, and origin
	// with none local trunk lacks.
	TrunkSkipAhead = "ahead"
	// TrunkSkipDiverged is both sides with commits the other lacks.
	TrunkSkipDiverged = "diverged"
	// TrunkSkipDirty is the checkout trunk is on with changes, untracked
	// files included.
	TrunkSkipDirty = "dirty"
	// TrunkSkipOperation is a rebase, merge, cherry-pick, revert or bisect
	// in progress on trunk.
	TrunkSkipOperation = "operation"
	// TrunkSkipSession is a Claude session busy in the checkout trunk is on,
	// or sessions that could not be listed.
	TrunkSkipSession = "session"
	// TrunkSkipNotFastForward is local trunk that moved, or was checked out
	// or switched away from, between the check and the update.
	TrunkSkipNotFastForward = "notFastForward"
	// TrunkSkipFailed is the update failing, git refusing it, or one not
	// safe to try: trunk checked out in more than one worktree.
	TrunkSkipFailed = "failed"
)

// TrunkSync is what a run did to local trunk after fetching it, or for wt
// status what one would do: brought up to origin/<trunk> when that is a pure
// fast-forward and safe, left alone with the reason otherwise.
type TrunkSync struct {
	Local         *string `json:"local"`
	Remote        *string `json:"remote"`
	LocalAhead    *int    `json:"localAhead"`
	RemoteAhead   *int    `json:"remoteAhead"`
	FastForwarded bool    `json:"fastForwarded"`
	SkippedReason *string `json:"skippedReason"`

	trunk  string
	before string // local trunk as found
	detail string // what the skip line adds to its reason
}

// trunkSyncOptions tunes syncLocalTrunk.
type trunkSyncOptions struct {
	optedOut bool
	// apply moves local trunk; false only says what would happen, for wt
	// status.
	apply bool
	// agents lists the sessions to look for in the checkout trunk is on.
	agents func() ([]wtsync.Agent, error)
	// beforeUpdate runs between the checks and the update, for the tests
	// of what a trunk moving in between does.
	beforeUpdate func()
}

// syncLocalTrunk brings local <trunk> to remote, the origin/<trunk> commit
// the caller fetched, when that is a pure fast-forward and safe. Checked out
// nowhere, the ref is moved by compare-and-swap; checked out in a clean
// checkout with nothing in progress and no session busy in it, git merge
// --ff-only moves it there. Anything else leaves it and says why. It never
// fails: what went wrong is the reason.
func syncLocalTrunk(ctx *Context, remote string, o trunkSyncOptions) *TrunkSync {
	trunk := ctx.Config.MainBranch
	ts := &TrunkSync{trunk: trunk, Remote: strp(remote)}
	local, ok := ctx.Repo.ResolveRef("refs/heads/" + trunk)
	if !ok || remote == "" {
		ts.Local = strp(local)
		return ts
	}
	ts.Local, ts.before = strp(local), local
	localAhead, ok1 := ctx.Repo.CommitsAhead(local, remote)
	remoteAhead, ok2 := ctx.Repo.CommitsAhead(remote, local)
	if !ok1 || !ok2 {
		return ts.skip(TrunkSkipFailed, "cannot compare it with origin/"+trunk)
	}
	ts.LocalAhead, ts.RemoteAhead = &localAhead, &remoteAhead
	switch {
	case local == remote:
		return ts
	case localAhead > 0 && remoteAhead > 0:
		return ts.skip(TrunkSkipDiverged, "")
	case localAhead > 0:
		return ts.skip(TrunkSkipAhead, "")
	case o.optedOut:
		return ts.skip(TrunkSkipOptedOut, "")
	}

	owners, hold, err := trunkOwners(ctx, trunk)
	switch {
	case err != nil:
		return ts.skip(TrunkSkipFailed, "cannot list the worktrees: "+oneLine(err.Error()))
	case hold != "":
		return ts.skip(TrunkSkipOperation, hold)
	case len(owners) > 1:
		// A merge in one would leave the others' files behind.
		return ts.skip(TrunkSkipFailed, "it is checked out in more than one worktree")
	}
	var on *repo.Worktree
	if len(owners) == 1 {
		on = &owners[0]
	}
	if on != nil {
		if reason, detail := checkoutBusy(*on, o.agents); reason != "" {
			return ts.skip(reason, detail)
		}
	}
	if !o.apply {
		return ts
	}
	if o.beforeUpdate != nil {
		o.beforeUpdate()
	}
	if on == nil {
		// The swap guards the commit, not the checkout: trunk checked out
		// since the listing, at the same commit, would have its ref moved
		// under files that do not follow.
		if owners, hold, err := trunkOwners(ctx, trunk); err != nil || hold != "" || len(owners) > 0 {
			return ts.skip(TrunkSkipNotFastForward, "it was checked out while wt was updating it")
		}
		_, err = git.Run(ctx.Repo.MainRoot, "update-ref", "-m", "wt: fast-forward to origin/"+trunk,
			"refs/heads/"+trunk, remote, local)
	} else {
		// merge moves whatever HEAD is on: a checkout switched away from
		// trunk since the listing must not have that branch moved instead.
		if head, _ := git.Run(on.Path, "symbolic-ref", "--quiet", "HEAD"); head != "refs/heads/"+trunk {
			return ts.skip(TrunkSkipNotFastForward, on.Path+" is no longer on "+trunk)
		}
		_, err = git.Run(on.Path, "merge", "--ff-only", "--no-overwrite-ignore", "--quiet", remote)
	}
	if err != nil {
		if now, _ := ctx.Repo.ResolveRef("refs/heads/" + trunk); now != local {
			return ts.skip(TrunkSkipNotFastForward, "it moved while wt was updating it")
		}
		return ts.skip(TrunkSkipFailed, oneLine(err.Error()))
	}
	ts.Local, ts.FastForwarded = strp(remote), true
	return ts
}

// trunkOwners is every worktree with trunk checked out, and what holds it
// when a git operation in progress does (a bisect started from it, a rebase
// of it, a rebase --update-refs that will move it).
func trunkOwners(ctx *Context, trunk string) (owners []repo.Worktree, hold string, err error) {
	all, err := ctx.Repo.Worktrees()
	if err != nil {
		return nil, "", err
	}
	for _, w := range all {
		for _, h := range w.Holds {
			if h.Branch == trunk {
				return nil, "a " + h.By + " holds it at " + w.Path, nil
			}
		}
		if w.Branch == trunk {
			owners = append(owners, w)
		}
	}
	return owners, "", nil
}

// checkoutBusy is why the checkout trunk is on cannot take a fast-forward:
// an operation in progress, changes of any kind, or a session busy in it.
// The session wt runs under is not among agents: callers list them with
// wtsync.ListOtherAgents.
func checkoutBusy(on repo.Worktree, agents func() ([]wtsync.Agent, error)) (reason, detail string) {
	if on.Rebasing {
		return TrunkSkipOperation, "a rebase is in progress at " + on.Path
	}
	if gitDir, err := wtsync.GitDir(on.Path); err != nil {
		return TrunkSkipFailed, "cannot read " + on.Path + ": " + oneLine(err.Error())
	} else if op := operationIn(gitDir); op != "" {
		return TrunkSkipOperation, "a " + op + " is in progress at " + on.Path
	}
	// Untracked files named explicitly: status.showUntrackedFiles=no must
	// not make scratch files invisible here.
	switch out, err := git.Run(on.Path, "--no-optional-locks", "status", "--porcelain", "--untracked-files=normal"); {
	case err != nil:
		return TrunkSkipFailed, "cannot read " + on.Path + ": " + oneLine(err.Error())
	case out != "":
		return TrunkSkipDirty, on.Path + " has changes or untracked files"
	}
	list, err := agents()
	if err != nil {
		return TrunkSkipSession, "the sessions cannot be listed, so nobody can say none is busy in " + on.Path
	}
	// An idle session loses nothing: the checkout is clean and the merge
	// overwrites nothing. A busy one may be about to write there.
	if busy := wtsync.SessionsAt(list, on.Path).Busy(); len(busy) > 0 {
		return TrunkSkipSession, "a Claude session is busy in " + on.Path
	}
	return "", ""
}

// operationIn names the git operation in progress in a worktree's git dir:
// the files git itself keeps while one runs. Empty when none is.
//
// A private helper until the repository-wide one lands; the rebase and the
// bisect are also in repo.Worktree, as Rebasing and Holds.
func operationIn(gitDir string) string {
	for _, op := range []struct{ file, name string }{
		{"rebase-merge", "rebase"}, {"rebase-apply", "rebase"},
		{"MERGE_HEAD", "merge"}, {"CHERRY_PICK_HEAD", "cherry-pick"},
		{"REVERT_HEAD", "revert"}, {"BISECT_LOG", "bisect"}, {"BISECT_START", "bisect"},
	} {
		if _, err := os.Stat(filepath.Join(gitDir, op.file)); err == nil {
			return op.name
		}
	}
	return ""
}

func (ts *TrunkSync) skip(reason, detail string) *TrunkSync {
	ts.SkippedReason, ts.detail = &reason, detail
	return ts
}

// Line is the one line a person reads about local trunk, or "" when there is
// nothing to say: it was already where origin is, or there is none.
func (ts *TrunkSync) Line() string {
	if ts == nil {
		return ""
	}
	name := "local " + ts.trunk
	if ts.FastForwarded {
		return fmt.Sprintf("%s  %s → %s (fast-forwarded to origin/%s)", name,
			git.ShortID(ts.before, 7), git.ShortID(*ts.Remote, 7), ts.trunk)
	}
	if ts.SkippedReason == nil {
		return ""
	}
	why := ts.detail
	switch *ts.SkippedReason {
	case TrunkSkipOptedOut:
		why = fmt.Sprintf("origin/%s is %d ahead; fast-forwarding it is off (--no-ff-trunk, ff_trunk)", ts.trunk, *ts.RemoteAhead)
	case TrunkSkipAhead:
		why = fmt.Sprintf("it has %d commit%s origin/%s lacks", *ts.LocalAhead, plural(*ts.LocalAhead), ts.trunk)
	case TrunkSkipDiverged:
		why = fmt.Sprintf("it and origin/%s have diverged (%d and %d commits of their own)", ts.trunk, *ts.LocalAhead, *ts.RemoteAhead)
	}
	return fmt.Sprintf("%s  left at %s: %s", name, git.ShortID(ts.before, 7), why)
}
