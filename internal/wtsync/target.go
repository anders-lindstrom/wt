package wtsync

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// Target is what a rebase is assessed against and replayed onto: trunk, a
// stack parent's new tip, or a ref wt sync --onto names.
type Target struct {
	// Onto is the commit the replayed commits are applied onto.
	Onto string
	// Upstream is where the replayed range starts, git's rebase --onto
	// <onto> <upstream>: only the commits above it are replayed. Empty
	// starts the range where the branch leaves Onto, their merge base.
	Upstream string
	// Trunk is the commit the declaration's scripts are read from; empty
	// is Onto, as it is when Onto is trunk.
	Trunk string
	// Name is what a person calls Onto, for the reasons a verdict gives;
	// empty is trunk.
	Name string
}

// cutoff is where the replayed range starts, in the form every reader of
// it takes: upstream...branch is upstream..branch for an upstream on the
// branch, and onto...branch is the commits above their merge base.
func (t Target) cutoff() string { return cmp.Or(t.Upstream, t.Onto) }

func (t Target) scripts() string { return cmp.Or(t.Trunk, t.Onto) }

// name is what the target is called in a verdict.
func (t Target) name() string { return cmp.Or(t.Name, "trunk") }

// behindAhead counts the commits branch lacks from t.Onto, and the commits
// a rebase of it onto t would replay.
func (t Target) behindAhead(mainRoot, branch string) (behind, ahead int, err error) {
	if t.Upstream == "" {
		return BehindAhead(mainRoot, t.Onto, branch)
	}
	count := func(rng string) (int, error) {
		out, err := gitEnv(mainRoot, nil, nil, "rev-list", "--count", rng, "--")
		if err != nil {
			return 0, err
		}
		n, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil {
			return 0, fmt.Errorf("unexpected rev-list output %q", out)
		}
		return n, nil
	}
	if behind, err = count(branch + ".." + t.Onto); err != nil {
		return 0, 0, err
	}
	if ahead, err = count(t.Upstream + ".." + branch); err != nil {
		return 0, 0, err
	}
	return behind, ahead, nil
}

// commits is the commits a rebase of branch onto t replays, as git's own
// list reads them: cutoff...branch, the right side, with OnTrunk marking a
// commit whose patch is on the left side. Without an upstream that is the
// target's side; with one it is the upstream's, which holds nothing above
// itself, so git keeps every commit above it in the list, and a commit the
// target already carries comes out empty when it is replayed (Replay.Emptied).
func (t Target) commits(dir, branch string, args ...string) ([]commitPaths, error) {
	return logWithPaths(dir, append(args, t.cutoff()+"..."+branch, "--")...)
}

// base is the commit the replayed range starts from: the upstream, or the
// merge base of branch and Onto.
func (t Target) base(mainRoot, branch string) (string, error) {
	if t.Upstream != "" {
		return t.Upstream, nil
	}
	return gitEnv(mainRoot, nil, nil, "merge-base", t.Onto, branch, "--")
}

// settled is Current or Stale when a rebase of tip onto t has nothing to
// do, and Unknown when it has. Without an upstream that is the counts:
// nothing behind is on the target, nothing ahead is nothing of its own.
// With one the counts are the replay's, so the target's ancestry decides
// apart from them: a target that holds the tip is stale against it (current
// at the tip itself), nothing above the cutoff is stale, and a cutoff that
// is the target itself is current. Anything else is replayed, a target
// below the tip included: A—B—C onto A above B drops B.
func (t Target) settled(mainRoot, tip string, behind, ahead int) (Class, error) {
	if t.Upstream == "" {
		switch {
		case behind == 0:
			return Current, nil
		case ahead == 0:
			return Stale, nil
		}
		return Unknown, nil
	}
	onto, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", t.Onto+"^{commit}", "--")
	if err != nil {
		return Unknown, err
	}
	at, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", tip+"^{commit}", "--")
	if err != nil {
		return Unknown, err
	}
	if at == onto {
		return Current, nil
	}
	if _, code, err := gitEnvAllow(mainRoot, nil, nil, 1, "merge-base", "--is-ancestor", at, onto); err != nil {
		return Unknown, err
	} else if code == 0 {
		return Stale, nil
	}
	if ahead == 0 {
		return Stale, nil
	}
	cutoff, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", t.Upstream+"^{commit}", "--")
	if err != nil {
		return Unknown, err
	}
	if cutoff == onto {
		return Current, nil
	}
	return Unknown, nil
}
