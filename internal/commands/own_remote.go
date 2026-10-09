package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// OwnSkipFailed is the fast-forward of a branch to its own remote failing:
// git refused it, or the branch was no longer where the check found it.
// OwnSkipFetchFailed is the branch's own remote that could not be fetched,
// so the run could not look. ownRemoteSync.skippedReason carries them beside
// the wtsync.OwnBlock codes.
const (
	OwnSkipFailed      = "failed"
	OwnSkipFetchFailed = "fetchFailed"
)

// OwnRemote is a branch against its own remote, the remote-tracking ref a
// push of it would replace, as wt status --json and wt sync --json report
// it.
type OwnRemote struct {
	Ref     *string `json:"ref"`
	State   string  `json:"state"`
	Commit  *string `json:"commit"`
	Ahead   *int    `json:"ahead"`
	Behind  *int    `json:"behind"`
	Fetched bool    `json:"fetched"`
	Blocks  *string `json:"blocks"`
	// NoPushReason is why wt would not push the branch, and FixCommand what
	// settles it, when a command does.
	NoPushReason *string  `json:"noPushReason"`
	FixCommand   []string `json:"fixCommand"`
}

func ownRemoteOf(o wtsync.OwnRemote) OwnRemote {
	out := OwnRemote{Ref: strp(o.Ref), State: string(ownState(o)), Commit: strp(o.Commit),
		Fetched: o.Fetched, Blocks: strp(o.Blocks), NoPushReason: strp(o.NoPush)}
	if o.Undecided != nil {
		out.FixCommand = o.Undecided.Fix()
	}
	if o.Compared() {
		out.Ahead, out.Behind = &o.Ahead, &o.Behind
	}
	return out
}

func ownState(o wtsync.OwnRemote) wtsync.OwnState {
	if o.State == "" {
		return wtsync.OwnNone
	}
	return o.State
}

// OwnRemoteSync is what a run found of a participant's own remote and did
// about it, shaped after TrunkSync.
type OwnRemoteSync struct {
	Ref           *string `json:"ref"`
	State         string  `json:"state"`
	Local         *string `json:"local"`
	Remote        *string `json:"remote"`
	LocalAhead    *int    `json:"localAhead"`
	RemoteAhead   *int    `json:"remoteAhead"`
	FastForwarded bool    `json:"fastForwarded"`
	Allowed       bool    `json:"allowed"`
	SkippedReason *string `json:"skippedReason"`
}

func ownRemoteSyncOf(o wtsync.OwnRemote) *OwnRemoteSync {
	out := &OwnRemoteSync{Ref: strp(o.Ref), State: string(ownState(o)), Local: strp(o.Local), Remote: strp(o.Commit)}
	if o.Compared() {
		out.LocalAhead, out.RemoteAhead = &o.Ahead, &o.Behind
	}
	return out
}

// ownStateOf is branch against its own remote; nothing to check with no
// reading or no branch.
func ownStateOf(own *wtsync.Own, branch string) wtsync.OwnRemote {
	if own == nil || branch == "" {
		return wtsync.OwnRemote{State: wtsync.OwnNone}
	}
	return own.State(branch)
}

// fetchRemotes fetches trunk from origin and with it the own-remote ref of
// each of branches. The error is trunk's fetch failing, which leaves
// everything as last fetched. A branch whose own ref could not be fetched is
// recorded in the reading, as that branch's trouble and nobody else's.
// Remotes that cannot be read at all make every branch unknown, with why.
func fetchRemotes(ctx *Context, timeout time.Duration, branches []string) (*wtsync.Own, error) {
	own := wtsync.ReadOwnOrUnknown(ctx.Repo.MainRoot, ctx.Config.MainBranch)
	return own, fetchOwn(ctx, own, timeout, branches, true)
}

// fetchOwn is the fetch itself. A remote with branches to check is first
// asked which of them it has (git ls-remote), and then fetched from with
// exact refspecs for those: two round trips, where a fetch of trunk alone
// is the one command it always was. Naming a ref the remote lacks would
// fail the whole fetch, and a pattern would bring every branch whose name
// starts the same; nothing here needs a git newer than a plain fetch does.
// withTrunk names trunk to origin as well. The error returned is trunk's.
func fetchOwn(ctx *Context, own *wtsync.Own, timeout time.Duration, branches []string, withTrunk bool) (trunkErr error) {
	root, trunk := ctx.Repo.MainRoot, ctx.Config.MainBranch
	asks := own.Asks(branches)
	remotes := wtsync.Remotes(asks)
	if withTrunk && (len(remotes) == 0 || remotes[0] != "origin") {
		remotes = append([]string{"origin"}, remotes...)
	}
	fetch := func(remote string, trunkToo bool, want []wtsync.OwnAsk) error {
		args := []string{"fetch", "--quiet", remote}
		if trunkToo {
			args = append(args, trunk)
		}
		for _, a := range want {
			args = append(args, a.Refspec())
		}
		_, err := git.RunTimeout(root, timeout, args...)
		return err
	}
	for _, remote := range remotes {
		trunkToo := withTrunk && remote == "origin"
		want, lerr := onRemote(root, timeout, own, remote, asks[remote])
		if lerr != nil {
			want = nil
		}
		var err error
		if trunkToo || len(want) > 0 {
			err = fetch(remote, trunkToo, want)
		}
		if err != nil && len(want) > 0 {
			// A branch deleted on the remote since it was listed is gone,
			// not a fetch that failed: listed again, and fetched once more
			// with what is still there.
			if still, serr := onRemote(root, timeout, own, remote, want); serr == nil && len(still) < len(want) {
				if want, err = still, nil; trunkToo || len(want) > 0 {
					err = fetch(remote, trunkToo, want)
				}
			}
		}
		if err != nil && len(want) > 0 {
			// Whose trouble is it: trunk by itself, then each branch by
			// itself, so that one ref that will not fetch is that branch's
			// and neither trunk's nor its neighbours'.
			if err = nil; trunkToo {
				err = fetch(remote, true, nil)
			}
			if err == nil {
				for _, a := range want {
					if ferr := fetch(remote, false, []wtsync.OwnAsk{a}); ferr != nil {
						own.Failed(a, ownFetchReason(ferr))
					} else {
						own.Seen(a)
					}
				}
			}
			want = nil
		}
		if trunkToo && err != nil {
			// The remote itself: trunk's failure to report, and everything
			// of that remote is as last fetched with it, no branch's own
			// trouble.
			trunkErr = err
			continue
		}
		if lerr != nil {
			for _, a := range asks[remote] {
				own.Failed(a, ownFetchReason(lerr))
			}
		}
		if trunkToo {
			own.TrunkFetched()
		}
		for _, a := range want {
			own.Seen(a)
		}
	}
	if err := own.Refresh(); err != nil && trunkErr == nil {
		trunkErr = err
	}
	return trunkErr
}

// ownFetchReason is why a branch's own remote could not be fetched, as one
// whole clause: git ends its commonest line here on "; try running" and
// puts the command on the next, which is advice for another day.
func ownFetchReason(err error) string {
	return strings.TrimSuffix(fetchReason(err), "; try running")
}

// onRemote asks remote which of asks it has, records the ones it lacks as
// gone, and returns the rest. Nothing is asked for no asks.
func onRemote(root string, timeout time.Duration, own *wtsync.Own, remote string, asks []wtsync.OwnAsk) ([]wtsync.OwnAsk, error) {
	if len(asks) == 0 {
		return nil, nil
	}
	args := []string{"ls-remote", "--heads", remote}
	for _, a := range asks {
		args = append(args, a.RemoteRef)
	}
	out, err := git.RunTimeout(root, timeout, args...)
	if err != nil {
		return nil, err
	}
	there := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if _, ref, ok := strings.Cut(line, "\t"); ok {
			there[ref] = true
		}
	}
	var has []wtsync.OwnAsk
	for _, a := range asks {
		if there[a.RemoteRef] {
			has = append(has, a)
		} else {
			own.Gone(a)
		}
	}
	return has, nil
}

// worktreeBranches is the branch of every worktree but the main checkout.
func worktreeBranches(worktrees []repo.Worktree) []string {
	var branches []string
	for _, wt := range worktrees {
		if !wt.IsMain && wt.Branch != "" {
			branches = append(branches, wt.Branch)
		}
	}
	return branches
}

// ownCandidates is the branches whose own remote a run fetches before it
// knows its stacks, which are read against the trunk the same fetch brings:
// with nothing named every worktree's, else each named worktree's and every
// worktree branch that shares an ancestor outside trunk with it. That is the
// stack and possibly more, from two git calls a name rather than the
// stack's own pairwise search. A work that cannot be located adds nothing
// here; choosing the branches says so.
func ownCandidates(ctx *Context, works []string) []string {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return nil
	}
	all := worktreeBranches(worktrees)
	if len(works) == 0 {
		return all
	}
	inWorktree := map[string]bool{}
	for _, b := range all {
		inWorktree[b] = true
	}
	seen := map[string]bool{}
	var out []string
	add := func(b string) {
		if inWorktree[b] && !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	trunkRef := "refs/remotes/origin/" + ctx.Config.MainBranch
	_, hasTrunk := ctx.Repo.ResolveRef(trunkRef)
	heads := func(args ...string) []string {
		lines, _ := git.Lines(ctx.Repo.MainRoot, append([]string{"for-each-ref", "--format=%(refname)"}, args...)...)
		for i, l := range lines {
			lines[i] = strings.TrimPrefix(l, "refs/heads/")
		}
		return lines
	}
	for _, arg := range works {
		wt, err := locateBranch(ctx, arg)
		if err != nil {
			continue
		}
		add(wt.Branch)
		below := []string{"--merged", "refs/heads/" + wt.Branch}
		if hasTrunk {
			below = append(below, "--no-merged", trunkRef)
		}
		var above []string
		for _, b := range heads(append(below, "refs/heads")...) {
			if inWorktree[b] {
				above = append(above, "--contains", "refs/heads/"+b)
			}
		}
		if len(above) == 0 {
			continue
		}
		for _, b := range heads(append(above, "refs/heads")...) {
			add(b)
		}
	}
	return out
}

// ownLine is a branch against its own remote as one line for a person, ""
// when there is nothing to compare with.
func ownLine(o wtsync.OwnRemote) string {
	var line string
	switch ownState(o) {
	case wtsync.OwnNone:
		return ""
	case wtsync.OwnGone:
		return o.Ref + " is gone from the remote"
	case wtsync.OwnUnknown:
		if o.Undecided != nil {
			return "nowhere to push yet: " + o.Undecided.Hint()
		}
		if o.NoPush != "" {
			return "nowhere to push: " + o.NoPush
		}
		return "not checked against its remote: " + o.Why
	case wtsync.OwnInSync:
		line = "in sync with " + o.Ref
	case wtsync.OwnAhead:
		line = fmt.Sprintf("%d ahead of %s", o.Ahead, o.Ref)
	case wtsync.OwnBehind:
		line = fmt.Sprintf("%d behind %s", o.Behind, o.Ref)
		if o.Blocks != "" {
			line += ", which cannot be fast-forwarded: " + o.Why
		} else {
			line += "; a run fast-forwards it first"
		}
	case wtsync.OwnRebased:
		line = fmt.Sprintf("rebased since it was pushed: %s holds the old commits", o.Ref)
	case wtsync.OwnDiverged:
		line = fmt.Sprintf("diverged from %s: %d of its own, and %d there it never had", o.Ref, o.Ahead, o.Behind)
	}
	if !o.Fetched {
		line += " (as last fetched)"
	}
	return line
}

// ownElsewhere is where a branch pushes when that is not a branch of its own
// name, "" when it is: said wherever a push is planned, asked about or made,
// so that nobody has to infer it.
func ownElsewhere(o wtsync.OwnRemote) string {
	if o.PushesTo == "" {
		return ""
	}
	line := fmt.Sprintf("pushes to %s, a branch of another name (%s)", o.PushesTo, o.Rule)
	if o.NoPush != "" {
		line += "; a run would not push it as it stands: " + o.NoPush
	}
	return line
}

// ownStray is the remote branch of the branch's own name that exists beside
// the one it pushes to, "" when there is none. wt deletes neither.
func ownStray(o wtsync.OwnRemote) string {
	switch {
	case o.Stray == "":
		return ""
	case o.PushesTo != "":
		return o.Stray + " exists as well: nothing pushes to it, and wt leaves it there"
	}
	return o.Stray + " exists as well"
}

// ownNote is what the overview puts under a row about the branch's own
// remote: only what changes what a run does, or what it could not check.
func ownNote(a wtsync.Assessment) string {
	switch o := a.Own; {
	case o.Refuses():
		return o.Refusal()
	case o.State == wtsync.OwnBehind, o.State == wtsync.OwnUnknown:
		return ownLineOf(a)
	}
	return ""
}

// ownLineOf is ownLine for an assessed worktree, which knows whether the
// fast-forward is all a run would do with it.
func ownLineOf(a wtsync.Assessment) string {
	if !a.OnlyFastForward() {
		return ownLine(a.Own)
	}
	return fmt.Sprintf("%d behind %s, where there is nothing to rebase: a run fast-forwards it and is done%s",
		a.Own.Behind, a.Own.Ref, asLastFetched(a.Own))
}

// onTrunk reports a worktree with nothing for a run to do and nothing wrong:
// on trunk already. The overview leaves it out. One that is on trunk only at
// its remote's commit, which a run fast-forwards it to, is not that.
func onTrunk(a wtsync.Assessment) bool {
	return a.Class == wtsync.Current && a.Err == nil && !a.OnlyFastForward()
}

// ownHeld is what keeps a run off a branch for its own remote, in the few
// words a row or a list of what was left takes.
func ownHeld(o wtsync.OwnRemote) string {
	if o.FetchFailed != "" {
		return "its remote not fetched"
	}
	if o.State == wtsync.OwnDiverged {
		return "diverged from " + o.Ref
	}
	return "behind " + o.Ref
}

// fastForwardOwn moves a branch that is behind its own remote up to the
// remote's commit, in its checkout, with the run's lock held: git merge
// --ff-only, which touches nothing when it cannot. HEAD is checked first,
// since merge moves whatever HEAD is on.
func fastForwardOwn(path, branch, commit string) error {
	if head, _ := git.Run(path, "symbolic-ref", "--quiet", "HEAD"); head != "refs/heads/"+branch {
		return fmt.Errorf("%s is no longer on %s", path, branch)
	}
	if _, err := git.Run(path, "merge", "--ff-only", "--no-overwrite-ignore", "--quiet", commit); err != nil {
		return fmt.Errorf("%s", mergeRefusal(err))
	}
	return nil
}

// mergeRefusal is why git would not fast-forward, in one line. Its commonest
// reason ends in a colon with the files on the lines after it, so those are
// carried: the first by name and the rest as a count.
func mergeRefusal(err error) string {
	lines := strings.Split(err.Error(), "\n")
	for i, l := range lines {
		if !strings.Contains(l, "would be overwritten by merge") {
			continue
		}
		what := "untracked files in the way"
		if strings.Contains(l, "local changes") {
			what = "local changes in the way"
		}
		var files []string
		for _, f := range lines[i+1:] {
			if !strings.HasPrefix(f, "\t") {
				break
			}
			files = append(files, strings.TrimSpace(f))
		}
		if len(files) == 0 {
			return what
		}
		if n := len(files) - 1; n > 0 {
			return fmt.Sprintf("%s: %s +%d more", what, files[0], n)
		}
		return what + ": " + files[0]
	}
	return oneLine(git.Reason(err, "warning:", "hint:"))
}

// asLastFetched marks a line about a branch's own remote that was not
// fetched by this command.
func asLastFetched(o wtsync.OwnRemote) string {
	if o.Fetched {
		return ""
	}
	return " (as last fetched)"
}
