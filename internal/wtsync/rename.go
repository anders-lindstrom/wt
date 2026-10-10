package wtsync

import (
	"fmt"
	"strings"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// RenameKeepingPush renames a branch and keeps its pushes where they went.
// Where the old name pushed is asked of the one rule before the rename: its
// own name on a remote, which the remote has or the upstream names, is
// recorded for the new name and returned for the caller to say. Nothing is
// recorded for a branch that had nowhere to push or was never pushed.
//
// A destination already recorded moves with the branch. It is returned
// while it names another branch than the new name, and removed when the
// rename put the branch back where it would push with nothing recorded.
//
// trunk is trunk's name; "" from a caller that has none to give, which
// reads every branch as some branch other than trunk.
func RenameKeepingPush(r *repo.Repo, trunk, from, to string) (kept *repo.PushTo, err error) {
	var was *repo.PushTo
	recorded := false
	if own, err := ReadOwn(r.MainRoot, trunk); err == nil {
		_, recorded = own.pushTo[from]
		was = own.pushedAs(from)
	}
	if err := r.RenameBranch(from, to); err != nil {
		return nil, err
	}
	switch {
	case recorded:
		own, err := ReadOwn(r.MainRoot, trunk)
		if err != nil {
			return nil, nil
		}
		rec, ok := repo.ParsePushTo(own.pushTo[to])
		if !ok {
			// Unreadable before, and no better for the move: left for
			// whoever reads it next to report.
			return nil, nil
		}
		delete(own.pushTo, to)
		if d := own.dest(to); !d.none && !d.unpushable && d.tracking == "refs/remotes/"+rec.Remote+"/"+rec.Branch {
			return nil, r.UnsetPushTo(to)
		}
		if rec.Branch == to {
			return nil, nil
		}
		return &rec, nil
	case was != nil:
		if err := r.SetPushTo(to, *was); err != nil {
			if back := r.RenameBranch(to, from); back != nil {
				return nil, fmt.Errorf("%w; and the branch is left as %s: %v", err, to, back)
			}
			return nil, err
		}
		return was, nil
	}
	return nil, nil
}

// pushedAs is where branch pushes under its own name, when that remote
// branch is its own by more than the name: the remote has it as last
// fetched, or the branch's upstream names it. nil for a branch that pushes
// by a record, has nowhere to push, or was never pushed.
func (o *Own) pushedAs(branch string) *repo.PushTo {
	d := o.dest(branch)
	if d.none || d.unpushable || d.rule == "recorded" {
		return nil
	}
	if _, there := o.tracking[d.tracking]; !there && !d.named {
		return nil
	}
	return &repo.PushTo{Remote: d.remote, Branch: strings.TrimPrefix(d.remoteRef, "refs/heads/")}
}
