package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// TrunkBase is a ref a branch can be merged into, and the commit it was at
// when it was read.
type TrunkBase struct {
	Name string // "origin/main" or "main"
	Tip  string
}

// SweepBranch is a local branch and what the plan made of it.
type SweepBranch struct {
	repo.Branch
	MergedInto string // the first base containing it, "" when none does
	Worktree   string // the worktree using it, "" when none
	Work       string // the work name of Worktree, "" when none
	// HeldBy is the operation in Worktree holding it, "bisect" or "rebase";
	// "" when Worktree simply has it checked out.
	HeldBy string
	// Kept is why the worktree on it is not removed: "dirty", the session in
	// it, the lock somebody holds. "" for a bisect, a rebase, or the main
	// checkout, which have their own rows. Dirty and LockHeld are the two of
	// those reasons the advice turns on; Missing is a worktree git lists
	// whose directory is no longer there.
	Kept     string
	Dirty    bool
	LockHeld bool
	Missing  bool
	// Detached is a worktree with no branch whose HEAD trunk contains: Name
	// is "(detached)", Tip its HEAD, and only its path names it.
	Detached bool
	Ahead    int // commits the first base lacks; read only for Gone
	// PR is what GitHub says about this branch's pull request, "#34 merged"
	// or "#35 open", and "" when there is none or GitHub is not in play.
	PR string
	// MergedPR is a pull request GitHub merged at exactly this branch's tip
	// while trunk does not contain the branch — a squash or rebase merge,
	// which git cannot see. It is what makes such a branch a candidate.
	MergedPR int
	// AppliedTo is the first base holding every commit of the branch under
	// another id — rebased or cherry-picked there — when neither trunk nor a
	// pull request says it landed. "" otherwise.
	AppliedTo string
	// GitHub is the pull request PR describes, nil when there is none.
	GitHub *github.PR
	// KeptCodes are the reasons it is kept, as --json names them: the Kept*
	// constants. Empty for a branch or worktree that goes.
	KeptCodes []string
}

// done reports that the work on this branch has landed: trunk contains it,
// GitHub merged its pull request at this very tip, or trunk has every commit.
func (b SweepBranch) done() bool { return b.MergedInto != "" || b.MergedPR > 0 || b.AppliedTo != "" }

// why is why a branch counts as done with, in the words the plan prints.
func (b SweepBranch) why() string {
	if b.MergedPR > 0 {
		return fmt.Sprintf("#%d merged on GitHub (squashed or rebased, so git cannot see it)", b.MergedPR)
	}
	if b.AppliedTo != "" {
		return withPR("every commit is on "+b.AppliedTo+" under a new id (rebased or cherry-picked)", b.PR)
	}
	return withPR("merged into "+b.MergedInto, b.PR)
}

// withPR adds what GitHub says about a branch to a reason, when anything here
// knows a pull request for it.
func withPR(reason, pr string) string {
	if pr == "" {
		return reason
	}
	return reason + " · " + pr
}

// SweepWorktree is a worktree whose branch trunk contains and that is safe
// to remove, with removal's own plan for it.
type SweepWorktree struct {
	SweepBranch
	Plan Plan
}

// SweepPlan is what a sweep is about to do, assembled before anything changes.
type SweepPlan struct {
	Bases    []TrunkBase
	MainRoot string
	// Delete is merged and checked out nowhere: it goes.
	Delete []SweepBranch
	// Remove is merged and checked out in a worktree nothing is using: the
	// worktree goes, and the branch with it, as wt remove would do it.
	Remove []SweepWorktree
	// CheckedOut is merged but a worktree is using it: kept until that ends.
	CheckedOut []SweepBranch
	// Gone lost its upstream ref but trunk lacks its commits: shown, kept.
	Gone []SweepBranch
}

// trunkBases resolves origin/<trunk> as last fetched, then <trunk>, to
// commits: what wt sweep and wt remove call merged. origin comes first
// because merges happen on the remote and the main checkout's trunk is often
// behind; the local trunk still counts, because commits on it are on trunk
// too. Nothing is fetched here.
func trunkBases(ctx *Context) ([]TrunkBase, error) {
	trunk := ctx.Config.MainBranch
	var bases []TrunkBase
	for _, b := range []struct{ name, ref string }{
		{"origin/" + trunk, "refs/remotes/origin/" + trunk},
		{trunk, "refs/heads/" + trunk},
	} {
		if tip, ok := ctx.Repo.ResolveRef(b.ref); ok {
			bases = append(bases, TrunkBase{Name: b.name, Tip: tip})
		}
	}
	if len(bases) == 0 {
		return nil, fmt.Errorf("neither origin/%s nor %s exists here, so nothing can be called merged", trunk, trunk)
	}
	return bases, nil
}

// planSweep reads every fact a sweep depends on and sorts the branches. list
// is the agent sessions to check the worktrees against; it is asked only
// when a merged branch has a worktree that could go. prs is what GitHub says
// about each branch, which is empty whenever GitHub is not in play.
func planSweep(ctx *Context, bases []TrunkBase, list func() ([]wtsync.Agent, error),
	prs func([]string) map[string]github.PR) (SweepPlan, error) {
	branches, err := ctx.Repo.Branches()
	if err != nil {
		return SweepPlan{}, fmt.Errorf("could not list branches: %w", err)
	}
	// Later bases are written first so that the first base's name wins.
	merged := map[string]string{}
	for i := len(bases) - 1; i >= 0; i-- {
		names, err := ctx.Repo.MergedInto(bases[i].Tip)
		if err != nil {
			return SweepPlan{}, fmt.Errorf("could not compare branches with %s: %w", bases[i].Name, err)
		}
		for _, n := range names {
			merged[n] = bases[i].Name
		}
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return SweepPlan{}, fmt.Errorf("%w: %w", repo.ErrWorktreesUnknown, err)
	}
	inUse := worktrees.Users()
	originHead, _ := ctx.Repo.OriginHead()

	// Only the branches that can reach a row of the plan: merged into trunk,
	// upstream gone, or holding a worktree — which is where a squash merge
	// git cannot see shows up. Anything else is work in progress.
	var asked []string
	for _, b := range branches {
		if protectedBranch(b.Name, ctx.Config.MainBranch, originHead) {
			continue
		}
		if merged[b.Name] != "" || b.Gone || inUse[b.Name].Path != "" {
			asked = append(asked, b.Name)
		}
	}
	byBranch := prs(asked)
	trunkNames := trunks(ctx)

	p := SweepPlan{Bases: bases, MainRoot: ctx.Repo.MainRoot}
	var candidates []SweepBranch
	for _, b := range branches {
		if protectedBranch(b.Name, ctx.Config.MainBranch, originHead) {
			continue
		}
		use := inUse[b.Name]
		sb := SweepBranch{Branch: b, MergedInto: merged[b.Name], Worktree: use.Path, HeldBy: use.By}
		if pr, ok := byBranch[b.Name]; ok {
			sb.PR, sb.GitHub = prLabel(pr, trunkNames), &pr
			// Only where git cannot tell: trunk containing the branch is the
			// stronger fact and the one every other command reads.
			if sb.MergedInto == "" && pr.Landed(b.Tip, trunkNames...) {
				sb.MergedPR = pr.Number
			}
		}
		// A branch GitHub rebased before merging carries the old ids, so
		// neither answer above sees it. Asked only of the branches that can
		// reach a row, as the pull requests are: git cherry is not free.
		if !sb.done() && (b.Gone || use.Path != "") {
			sb.AppliedTo = appliedTo(ctx, b.Tip, bases)
		}
		switch {
		case sb.done() && sb.Worktree != "":
			sb.Work = worktreeName(ctx, b.Name, sb.Worktree)
			if sb.HeldBy != "" || repo.SamePath(sb.Worktree, p.MainRoot) {
				sb.KeptCodes = []string{KeptMainCheckout}
				if sb.HeldBy != "" {
					sb.KeptCodes = []string{heldByCode(sb.HeldBy)}
				}
				p.CheckedOut = append(p.CheckedOut, sb)
			} else {
				candidates = append(candidates, sb)
			}
		case sb.done():
			p.Delete = append(p.Delete, sb)
		case b.Gone:
			// By tip, not name: a tag with the branch's name would shadow it.
			sb.Ahead, _ = ctx.Repo.CommitsAhead(b.Tip, bases[0].Tip)
			sb.KeptCodes = []string{KeptNotMerged}
			p.Gone = append(p.Gone, sb)
		}
	}
	// A detached worktree holds no branch, so nothing above names it, yet a
	// checkout left detached at a merged tip is the same litter as a merged
	// branch's worktree. It is always kept, since a worktree with no branch
	// is wt remove's to take by its path, but it is checked like the rest so
	// the row names what else holds it rather than advising a remove that
	// would refuse.
	for _, wt := range worktrees {
		if wt.IsMain || !wt.Detached || wt.Branch != "" || len(wt.Holds) > 0 {
			continue
		}
		head, base := detachedOn(ctx, wt.Path, bases)
		if base == "" {
			continue
		}
		candidates = append(candidates, SweepBranch{
			Branch: repo.Branch{Name: "(detached)", Tip: head}, MergedInto: base, Worktree: wt.Path, Detached: true,
		})
	}
	if len(candidates) == 0 {
		return p, nil
	}
	agents, listErr := list()
	if agents == nil {
		// Never nil from here on: planFor takes nil for "ask claude", and the
		// listing has been done, or has failed, once already.
		agents = []wtsync.Agent{}
	}
	for _, sb := range candidates {
		if _, err := os.Stat(sb.Worktree); err != nil {
			// git still lists it, so wt remove finds no worktree there and
			// the branch counts as in use until the record is pruned.
			sb.Kept, sb.Missing = "its directory is gone", true
			sb.KeptCodes = []string{KeptDirectoryMissing}
			p.CheckedOut = append(p.CheckedOut, sb)
			continue
		}
		wt, _ := worktrees.ByPath(sb.Worktree)
		wt.Path = sb.Worktree
		// git reads a squash- or rebase-merged branch as unmerged, so without
		// this the plan would rename it aside. Sweep has just read GitHub, so
		// it answers from that rather than from the cache remove falls back on.
		rp := planFor(ctx, wt, RemoveOptions{Agents: agents, AgentsErr: listErr,
			Landed: landedFrom(byBranch, trunkNames)})
		sb.Kept, sb.KeptCodes = unsafeToRemove(sb, rp)
		sb.Dirty, sb.LockHeld = rp.Dirty, rp.Locked && rp.LockHeld
		if sb.Detached {
			reason := "no branch, and its HEAD " + git.ShortID(sb.Tip, 12) + " is on " + sb.MergedInto
			if sb.Kept != "" {
				reason += ", " + sb.Kept
			}
			sb.Kept = reason
			sb.KeptCodes = append([]string{KeptDetached}, sb.KeptCodes...)
		}
		if sb.Kept != "" {
			p.CheckedOut = append(p.CheckedOut, sb)
			continue
		}
		p.Remove = append(p.Remove, SweepWorktree{SweepBranch: sb, Plan: rp})
	}
	return p, nil
}

// detachedOn is the HEAD of a detached worktree and the first base that
// contains it; base is "" when none does, or HEAD cannot be read.
func detachedOn(ctx *Context, path string, bases []TrunkBase) (head, base string) {
	head, err := git.Run(path, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil || head == "" {
		return "", ""
	}
	for _, b := range bases {
		if n, ok := ctx.Repo.CommitsAhead(head, b.Tip); ok && n == 0 {
			return head, b.Name
		}
	}
	return head, ""
}

// unsafeToRemove is why sweep leaves a worktree alone, "" when wt remove
// without --force would take it and delete its branch: every reason wt
// remove has to refuse, which includes an agent session in it at all, idle
// or busy — a sweep is not the moment to pull a directory out from under
// one. Every reason that applies is named, so the row says all of what holds
// it, and codes names each for --json.
func unsafeToRemove(b SweepBranch, rp Plan) (reason string, codes []string) {
	var why []string
	add := func(code, reason string) {
		why = append(why, reason)
		// Idle and busy sessions are a problem each, and one code.
		if !slices.Contains(codes, code) {
			codes = append(codes, code)
		}
	}
	for _, pr := range rp.problems() {
		add(pr.code, pr.text)
	}
	if !b.Detached && (rp.Branch != b.Name || rp.Outcome != BranchDeleted) {
		add(KeptBranchNotDeletable, "wt remove would not delete the branch: "+rp.standing())
	}
	return strings.Join(why, ", "), codes
}

// heldLock is a held lock as the kept row states it: by the pid the reason
// names, by the session wt remove found behind a reason naming none, or by
// the reason's own words, which is all that is known about it.
func heldLock(rp Plan) string {
	switch {
	case rp.LockPid > 0:
		return fmt.Sprintf("lock held by pid %d", rp.LockPid)
	case rp.LockReason == "":
		return "locked, with no reason given"
	case rp.LockHolder != rp.LockReason:
		return "lock held by " + rp.LockHolder
	}
	return "lock held: " + rp.LockReason
}

// protectedBranch names what sweep never deletes, merged or not: the trunks
// it was given (an empty one names nothing) and the long-lived branches kept
// beside them. A release branch merged into trunk is still where the next
// patch release is cut from.
func protectedBranch(name string, trunks ...string) bool {
	for _, t := range trunks {
		if t != "" && name == t {
			return true
		}
	}
	switch name {
	case "main", "master", "develop", "development", "staging", "production":
		return true
	}
	return strings.HasPrefix(name, "release")
}

// minSubjectWidth keeps a cut commit subject long enough to recognise; a
// terminal narrower than that wraps the row instead.
const minSubjectWidth = 20

// Render writes the plan: what goes, and what stays with the reason. width is
// the terminal's column count, or 0 when output is not a terminal. Above 0,
// commit subjects are cut from the right so each row fits; at 0 they are
// printed whole.
func (p SweepPlan) Render(w io.Writer, width int) {
	names := make([]string, len(p.Bases))
	for i, b := range p.Bases {
		names[i] = b.Name
	}
	fmt.Fprintf(w, "merged means reachable from %s\n\n", strings.Join(names, " or "))

	if len(p.Remove) > 0 {
		heading := "Will be removed with their branches, %s:\n"
		if len(p.Remove) == 1 {
			heading = "Will be removed with its branch, %s:\n"
		}
		fmt.Fprintf(w, heading, worktreeCount(len(p.Remove)))
		var rows [][]string
		for _, b := range p.Remove {
			rows = append(rows, []string{"  " + b.Work, b.Name, b.Worktree, b.why()})
		}
		_ = printTable(w, rows)
		if len(p.Delete) > 0 {
			fmt.Fprintln(w)
		}
	}
	switch {
	case len(p.Delete) > 0:
		fmt.Fprintf(w, "Will be deleted, %s:\n", branchCount(len(p.Delete)))
		var rows [][]string
		for _, b := range p.Delete {
			rows = append(rows, []string{"  " + b.Name, b.why(), b.Date, b.Subject})
		}
		printSubjectTable(w, rows, width)
	case len(p.Remove) == 0:
		fmt.Fprintln(w, "No merged branches or worktrees to sweep.")
	}
	if len(p.CheckedOut) > 0 {
		fmt.Fprintln(w, "\nMerged, but in use in a worktree, so kept:")
		var rows [][]string
		for _, b := range p.CheckedOut {
			rows = append(rows, []string{"  " + b.Name, p.checkedOutAdvice(b)})
		}
		_ = printTable(w, rows)
	}
	if len(p.Gone) > 0 {
		fmt.Fprintln(w, "\nUpstream gone, but not merged, so kept:")
		var rows [][]string
		for _, b := range p.Gone {
			rows = append(rows, []string{"  " + b.Name, withPR(aheadOf(b.Ahead, p.Bases[0].Name), b.PR), b.Date, b.Subject})
		}
		printSubjectTable(w, rows, width)
	}
	fmt.Fprintln(w)
}

// printSubjectTable writes rows whose last column is a commit subject, cut to
// fit a terminal width columns wide; at width 0 the subject is left whole.
func printSubjectTable(w io.Writer, rows [][]string, width int) {
	if width > 0 {
		fitLastColumn(rows, width, minSubjectWidth, elideRight)
	}
	_ = printTable(w, rows)
}

// elideRight shortens s to at most limit runes, keeping its start.
func elideRight(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

// checkedOutAdvice says why a merged branch a worktree is using is kept, and
// how to finish it off. A branch a bisect or rebase holds is not for wt
// remove: it finds no worktree on a branch held from elsewhere, and on a
// checkout mid-rebase it throws the rebase away and leaves the branch. A
// detached worktree has no branch to delete, and only its path names it to
// wt remove. For the rest, wt remove reads the same bases as this plan, so
// it deletes the branch with the worktree; --force is what breaks a held
// lock, and nothing removes a checkout with uncommitted changes.
func (p SweepPlan) checkedOutAdvice(b SweepBranch) string {
	return withPR(p.keptBecause(b), b.PR)
}

func (p SweepPlan) keptBecause(b SweepBranch) string {
	target := b.Work
	if b.Detached {
		target = b.Worktree
	}
	switch {
	case b.HeldBy != "":
		return "held by the " + b.HeldBy + " in " + b.Worktree + "; finish or abort it there, then sweep again"
	case repo.SamePath(b.Worktree, p.MainRoot):
		return "the main checkout is on it; switch it to trunk, then sweep again"
	case b.Missing:
		return b.Kept + "; git worktree prune, then sweep again"
	case slices.Contains(b.KeptCodes, KeptStatusUnknown):
		return b.Kept + "; see git status there, then sweep again"
	case b.Dirty:
		return b.Kept + "; commit or discard the changes, then sweep again"
	case slices.Contains(b.KeptCodes, KeptOperation):
		return b.Kept + "; finish or abort it there, then sweep again"
	case slices.Contains(b.KeptCodes, KeptNestedWorktree):
		return b.Kept + "; remove that one first, then sweep again"
	case slices.Contains(b.KeptCodes, KeptSubmoduleUnreachable), slices.Contains(b.KeptCodes, KeptHeadUnreachable):
		return b.Kept + "; push or branch those commits, then sweep again"
	case slices.ContainsFunc(b.KeptCodes, needsForce):
		return b.Kept + "; wt remove " + target + " --force"
	}
	return b.Kept + "; wt remove " + target
}

// needsForce is a reason to keep a worktree that wt remove --force goes past.
func needsForce(code string) bool {
	switch code {
	case KeptLockHeld, KeptSession, KeptSessionsUnknown, KeptHiddenChanges:
		return true
	}
	return false
}

// changedFrom says what became, by the time of this fresh plan, of a branch an
// earlier plan was going to delete; "" when it can still go at the same tip.
func (p SweepPlan) changedFrom(ctx *Context, was SweepBranch) string {
	for _, b := range p.Delete {
		if b.Name == was.Name && b.Tip == was.Tip {
			return ""
		}
	}
	for _, b := range p.CheckedOut {
		if b.Name != was.Name {
			continue
		}
		if b.HeldBy != "" {
			return fmt.Sprintf("the %s in %s took hold of it after the plan was made", b.HeldBy, b.Worktree)
		}
		return fmt.Sprintf("it was checked out in %s after the plan was made", b.Worktree)
	}
	for _, b := range p.Remove {
		if b.Name == was.Name {
			return fmt.Sprintf("it was checked out in %s after the plan was made", b.Worktree)
		}
	}
	now, exists := ctx.Repo.ResolveRef("refs/heads/" + was.Name)
	head, _ := ctx.Repo.OriginHead()
	switch {
	case !exists:
		return "it was deleted after the plan was made"
	case now != was.Tip:
		return "it moved after the plan was made"
	case protectedBranch(was.Name, ctx.Config.MainBranch, head):
		return "origin's HEAD names it now"
	case was.MergedPR > 0:
		// The branch is where it was and nothing else claims it, so what
		// changed is the answer about the pull request — usually that GitHub
		// could not be asked again, not that it said something new.
		return fmt.Sprintf("#%d could not be read as merged at this tip a second time", was.MergedPR)
	case was.AppliedTo != "":
		return "its commits are no longer all on " + was.AppliedTo
	}
	return "trunk no longer contains it: the merge was undone after the plan was made"
}

// worktreeChangedFrom says what became, by the time of this fresh plan, of a
// worktree an earlier plan was going to remove, and hands back the fresh
// removal plan when it can still go: same branch at the same tip, in the
// same place, and still safe.
func (p SweepPlan) worktreeChangedFrom(ctx *Context, was SweepWorktree) (Plan, string) {
	for _, b := range p.Remove {
		if b.Name == was.Name && b.Tip == was.Tip && repo.SamePath(b.Worktree, was.Worktree) {
			return b.Plan, ""
		}
	}
	for _, b := range p.CheckedOut {
		if b.Name != was.Name {
			continue
		}
		switch {
		case b.Kept != "":
			return Plan{}, b.Kept
		case b.HeldBy != "":
			return Plan{}, fmt.Sprintf("the %s in %s took hold of it after the plan was made", b.HeldBy, b.Worktree)
		}
		return Plan{}, "the main checkout is on it now"
	}
	for _, b := range p.Delete {
		if b.Name != was.Name {
			continue
		}
		// Merged and in no worktree now: the checkout moved off the branch,
		// or git no longer lists it. Either way the branch goes next time.
		if now := ctx.Repo.BranchAt(was.Worktree); now != "" {
			return Plan{}, fmt.Sprintf("its worktree is on %s now; the branch goes next time", now)
		}
		return Plan{}, "its worktree is no longer registered; the branch goes next time"
	}
	return Plan{}, p.changedFrom(ctx, was.SweepBranch)
}

func branchCount(n int) string {
	if n == 1 {
		return "1 branch"
	}
	return fmt.Sprintf("%d branches", n)
}

func worktreeCount(n int) string {
	if n == 1 {
		return "1 worktree"
	}
	return fmt.Sprintf("%d worktrees", n)
}

// counts is what the plan acts on, by kind: "2 branches and 1 worktree".
func (p SweepPlan) counts() string {
	var parts []string
	if len(p.Delete) > 0 {
		parts = append(parts, branchCount(len(p.Delete)))
	}
	if len(p.Remove) > 0 {
		parts = append(parts, worktreeCount(len(p.Remove)))
	}
	return strings.Join(parts, " and ")
}

// Action is what the plan will do, as a verb phrase for the question and the
// refusal: "delete 2 branches and remove 1 worktree". Empty when there is
// nothing to do.
func (p SweepPlan) Action() string {
	var parts []string
	if n := len(p.Delete); n == 1 {
		parts = append(parts, "delete this branch")
	} else if n > 1 {
		parts = append(parts, "delete these "+branchCount(n))
	}
	if n := len(p.Remove); n == 1 {
		parts = append(parts, "remove this worktree")
	} else if n > 1 {
		parts = append(parts, "remove these "+worktreeCount(n))
	}
	return strings.Join(parts, " and ")
}

// Question is the one question a sweep asks, for the whole plan.
func (p SweepPlan) Question() string {
	a := p.Action()
	if a == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(a)
	return string(unicode.ToUpper(r)) + a[size:] + "?"
}

// Empty reports that the plan has nothing to delete or remove.
func (p SweepPlan) Empty() bool {
	return len(p.Delete) == 0 && len(p.Remove) == 0
}

// SweepOptions carries the caller's fetch and confirmation policy.
type SweepOptions struct {
	NoFetch bool
	// DryRun prints the plan and stops, asked or not.
	DryRun bool
	// Yes deletes without asking. Without it Confirm asks, and with neither
	// the plan is printed and nothing is deleted: a bulk delete does not
	// happen because nobody was there to say no.
	Yes     bool
	Confirm func(SweepPlan) (bool, error)
	// Width is the terminal's column count, 0 when output is not a terminal.
	Width int
	// Agents are the sessions to check the worktrees against. Nil asks
	// `claude agents`; an empty slice means there are none. Relist lists them
	// again before anything is removed; nil lists them the way Agents did.
	Agents []wtsync.Agent
	Relist func() ([]wtsync.Agent, error)
	// PRs is the pull requests by branch, for a test that has no gh. Nil asks
	// GitHub; an empty map means it had nothing to say.
	PRs map[string]github.PR
	// Journal records what the sweep does, row by row, for --json; nil
	// records nothing.
	Journal *SweepJournal
	// Expect is the token wt sweep --dry-run --json gave: the sweep refuses,
	// touching nothing, unless the plan it makes now has that token.
	Expect string
	// Quarantine is a new folder, absolute, that the worktrees are moved
	// into, one folder each, instead of deleted; "" deletes them.
	Quarantine string
	// KeepSuperset leaves each removed worktree's Superset workspace alone.
	KeepSuperset bool
}

// pullRequests is what GitHub says about the branches a sweep considers,
// fetched fresh because a sweep decides what to delete, and left in the cache
// `wt list` reads. Empty whenever GitHub is not in play.
//
// The branches are named in the question, so a pull request of any age is
// found, and only the branches that can appear in the plan are asked about.
func (o SweepOptions) pullRequests(ctx *Context) func([]string) map[string]github.PR {
	if o.PRs != nil {
		return func([]string) map[string]github.PR { return o.PRs }
	}
	return func(branches []string) map[string]github.PR {
		return branchPRs(ctx, branches, prLookup{
			warn:    "sweeping without GitHub's answers, so a squash-merged branch is not recognised",
			refresh: true,
		}).byBranch
	}
}

// listAgents is the first listing of the sessions, for the plan.
func (o SweepOptions) listAgents() ([]wtsync.Agent, error) {
	if o.Agents != nil {
		return o.Agents, nil
	}
	return listSessions()
}

// relistAgents is the second listing, for the check before anything goes.
func (o SweepOptions) relistAgents() ([]wtsync.Agent, error) {
	if o.Agents == nil && o.Relist == nil {
		return listSessions()
	}
	return listAgain(o.Agents, o.Relist)
}

// Sweep deletes the local branches trunk already contains, and removes the
// worktrees on such branches that nothing is using.
func Sweep(ctx *Context, opts SweepOptions, w io.Writer) (err error) {
	if j := opts.Journal; j != nil {
		j.open(ctx)
		defer setInterruptJournal(j)()
		defer watchSignals(w, nil)()
		defer func() {
			j.fail(err)
			j.Finish()
		}()
	}
	plan, fetched, err := prepareSweep(ctx, opts, w)
	opts.Journal.fetched(fetched)
	if err != nil {
		return err
	}
	token := sweepToken(ctx, plan, opts)
	if opts.Expect != "" && deref(token) != opts.Expect {
		return errors.New("the sweep plan changed since it was read (a branch or worktree moved, " +
			"was merged, or gained or lost a reason to be kept); nothing was swept: read the plan again")
	}
	opts.Journal.begin(ctx, plan, deref(token))
	opts.Journal.quarantineIn(opts.Quarantine)
	plan.Render(w, opts.Width)
	if plan.Empty() {
		return nil
	}
	if err := quarantineReady(ctx, opts.Quarantine, plan); err != nil {
		return err
	}
	if opts.Quarantine != "" && len(plan.Remove) > 0 {
		fmt.Fprintf(w, "The worktrees go to %s, a folder each, instead of being deleted.\n", opts.Quarantine)
	}

	switch {
	case opts.DryRun:
		fmt.Fprintln(w, "Nothing was swept: --dry-run.")
		return nil
	case opts.Yes:
	case opts.Confirm != nil:
		ok, err := opts.Confirm(plan)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "Nothing was swept.")
			return nil
		}
	default:
		fmt.Fprintf(w, "Nothing was swept: there is no terminal to ask. Pass --yes to sweep %s.\n",
			plan.counts())
		return nil
	}
	return plan.apply(ctx, opts, w)
}

// sweepGuard refuses before anything is fetched or deleted: sweep acts on the
// whole repository, so it runs from its main checkout, which has to be a
// checkout, against a trunk somebody named.
func sweepGuard(ctx *Context) error {
	if !repo.SamePath(ctx.Repo.Root, ctx.Repo.MainRoot) {
		return fmt.Errorf("wt sweep deletes branches across the whole repository, so it runs "+
			"only from the main checkout, %s\n  wt cd / gets you there", ctx.Repo.MainRoot)
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return fmt.Errorf("could not list worktrees: %w", err)
	}
	if len(worktrees) == 0 || worktrees[0].Bare {
		return fmt.Errorf("wt sweep runs from a main checkout, and %s is a bare repository", ctx.Repo.MainRoot)
	}
	// Without either, MainBranch is only what the main checkout has checked
	// out, and a feature branch taken for trunk makes everything cut from it
	// look merged.
	if head, _ := ctx.Repo.OriginHead(); !ctx.Config.MainBranchSet && head != ctx.Config.MainBranch {
		return fmt.Errorf("wt sweep cannot tell which branch is trunk: MAIN_BRANCH is not set in "+
			"bin/worktree/worktree.conf and origin's HEAD does not name %s\n"+
			"  set MAIN_BRANCH, or run git remote set-head origin --auto", ctx.Config.MainBranch)
	}
	return nil
}

// sweepFetch brings origin up to date and prunes the remote branches deleted
// there, which is what makes an upstream show as gone.
//
// The refspec is explicit and the refmap empty, so the fetch writes and
// prunes only refs/remotes/origin/* whatever remote.origin.fetch maps: a
// mapping into refs/heads would otherwise prune local branches before
// anything was asked (verified against git 2.55). --no-prune-tags overrides
// fetch.pruneTags, --no-tags stops tags being followed in, and
// --no-recurse-submodules keeps the prune out of submodules.
func sweepFetch(ctx *Context, noFetch bool, w io.Writer) error {
	switch {
	case !ctx.Repo.HasRemote("origin"):
		fmt.Fprintln(w, "no origin remote: comparing with local branches only")
	case noFetch:
		fmt.Fprintln(w, "not fetched: comparing with origin as last fetched")
	default:
		if _, err := git.RunTimeout(ctx.Repo.MainRoot, networkTimeout, "fetch", "--quiet", "--prune",
			"--no-prune-tags", "--no-tags", "--no-recurse-submodules", "--refmap=",
			"origin", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
			return fmt.Errorf("fetch: %w\n  --no-fetch compares with origin as last fetched", err)
		}
		fmt.Fprintln(w, "fetched origin")
	}
	return nil
}

// apply carries out the plan: the worktrees first, then the plain branches.
// Trunk, the branches, the worktrees and the sessions are read again first,
// and anything that moved or is no longer safe is kept. Each worktree goes
// the way wt remove takes it, with the branch after the checkout, so a
// removal that fails leaves its branch where it was. Each branch delete then
// asks once more whether a worktree has the branch, and deletes only at the
// tip the plan showed, so the plan that was shown is the plan that runs.
func (p SweepPlan) apply(ctx *Context, opts SweepOptions, w io.Writer) error {
	bases, err := trunkBases(ctx)
	if err != nil {
		return fmt.Errorf("could not re-read trunk before deleting, so nothing was deleted: %w", err)
	}
	fresh, err := planSweep(ctx, bases, opts.relistAgents, opts.pullRequests(ctx))
	if err != nil {
		return fmt.Errorf("could not re-read the branches before deleting, so nothing was deleted: %w", err)
	}
	j := opts.Journal
	if opts.Quarantine != "" && len(p.Remove) > 0 {
		if err := quarantine.MakeDir(opts.Quarantine); err != nil {
			return fmt.Errorf("nothing was swept: the quarantine folder: %w", err)
		}
	}
	j.startApply()
	// Every branch the sweep deletes, with a worktree or without, is moved
	// into one run under refs/wt-swept/, except a quarantined worktree's,
	// which the quarantine pins.
	pins := &branchPins{command: "wt sweep", begun: j.ran}
	kept := 0
	folders := map[string]bool{}
	for i, wt := range p.Remove {
		key := sweepKey(wt.SweepBranch)
		rp, why := fresh.worktreeChangedFrom(ctx, wt)
		if why == "" && i > 0 {
			rp, why = changedDuringSweep(ctx, rp, opts)
		}
		if why != "" {
			fmt.Fprintf(w, "- kept %s: %s\n", wt.Work, why)
			j.keep(key, why)
			kept++
			continue
		}
		if opts.Quarantine != "" {
			rp.Quarantine = quarantineFolder(opts.Quarantine, rp.Path, folders)
			rp.quarantineBy = "sweep"
			j.quarantined(key, rp.Quarantine)
		} else {
			rp.pins = pins
		}
		rp.KeepSuperset, rp.relist = opts.KeepSuperset, opts.relistAgents
		j.start(key)
		res, err := rp.run(ctx, w)
		j.superset(key, res)
		j.settle(key, err)
		if err != nil {
			// Said as what happened: only a worktree nothing touched was kept.
			switch res.Outcome {
			case RemovePartial:
				fmt.Fprintf(w, "! %s is partly done: %s\n", wt.Work, err)
			case RemoveRemovedWithBranchProblem:
				fmt.Fprintf(w, "- removed %s, but its branch was kept: %s\n", wt.Work, err)
			default:
				fmt.Fprintf(w, "- kept %s: %s\n", wt.Work, err)
			}
			kept++
		}
	}
	for _, b := range p.Delete {
		key := sweepKey(b)
		if why := fresh.changedFrom(ctx, b); why != "" {
			fmt.Fprintf(w, "- kept %s: %s\n", b.Name, why)
			j.keep(key, why)
			kept++
			continue
		}
		// The move asks the worktrees again immediately before the delete,
		// which is as close to it as that question gets.
		j.start(key)
		pin, err := pins.move(ctx, b.Name, b.Tip)
		j.settle(key, err)
		if err != nil {
			fmt.Fprintf(w, "- kept %s: %s\n", b.Name, whyKept(err))
			kept++
			continue
		}
		fmt.Fprintf(w, "✓ deleted %s, pinned at %s; %s puts it back\n", b.Name, pin,
			strings.Join(restoreFromRun(pins.runID, b.Name), " "))
	}
	// A run that pinned nothing is dropped, and the result names none.
	runID := pins.finish(ctx, w)
	j.ran(runID)
	if runID != "" {
		fmt.Fprintf(w, "wt refs restore %s puts the deleted branches back; wt refs purge %s deletes them for good\n",
			runID, runID)
	}
	if dropped, err := quarantine.DropDiscarded(ctx.Repo); err != nil {
		fmt.Fprintf(w, "! could not read the pins of past quarantines: %v\n", err)
	} else {
		for _, dir := range dropped {
			fmt.Fprintf(w, "- dropped the pins of %s: that quarantine has been deleted\n", dir)
		}
	}
	if kept > 0 {
		return fmt.Errorf("%d of %s kept or not finished; run wt sweep again to see why", kept, p.counts())
	}
	return nil
}

// quarantineReady refuses a sweep into dir before anything changes, even
// one that only deletes branches: dir must be new, and every worktree the
// plan removes, with its admin dir, on the volume dir goes on.
func quarantineReady(ctx *Context, dir string, p SweepPlan) error {
	if dir == "" {
		return nil
	}
	if err := quarantineOutside(ctx, dir); err != nil {
		return fmt.Errorf("nothing was swept: %w", err)
	}
	var paths []string
	for _, wt := range p.Remove {
		admin, err := repo.AdminDir(wt.Worktree)
		if err != nil {
			return fmt.Errorf("nothing was swept: %s cannot be quarantined: %w", wt.Worktree, err)
		}
		paths = append(paths, wt.Worktree, admin)
	}
	if err := quarantine.Check(dir, paths...); err != nil {
		return fmt.Errorf("nothing was swept: %w", err)
	}
	return nil
}

// quarantineFolder is the folder in dir a worktree at path goes to: its
// directory's name, numbered when another worktree of the sweep has it.
func quarantineFolder(dir, path string, taken map[string]bool) string {
	name := filepath.Base(path)
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s-%d", filepath.Base(path), n)
	}
	taken[name] = true
	return filepath.Join(dir, name)
}

// changedDuringSweep reads a worktree once more immediately before it goes,
// for every worktree after the first: removing the ones before it takes
// time, and a checkout can gain a change, a lock or a session meanwhile.
// Its status is read first, strictly, and a status that cannot be read
// keeps it; then the sessions are listed again; then the removal plan is
// made again and has to be the one the fresh plan made. It returns that
// latest reading, which is the one to carry out, and why is "" when it can
// still go.
func changedDuringSweep(ctx *Context, rp Plan, opts SweepOptions) (Plan, string) {
	st, err := repo.DirtyStrict(rp.Path)
	switch {
	case err != nil:
		return rp, "cannot read its status (" + oneLine(gitSaid(err)) + ")"
	case st.Dirty:
		return rp, "it gained uncommitted changes during the sweep"
	}
	agents, err := opts.relistAgents()
	if agents == nil {
		agents = []wtsync.Agent{}
	}
	s, err := sessionsIn(agents, err, rp.Path)
	switch {
	case err != nil:
		return rp, fmt.Sprintf("cannot list agent sessions (%v)", err)
	case len(s) > 0:
		return rp, whoLabel(s) + " in it"
	}
	// The pull request is the one the fresh plan read: it was asked seconds
	// ago, and a merge does not come undone.
	landed := func(branch, tip string) int {
		if branch == rp.Branch && tip == rp.Tip {
			return rp.MergedPR
		}
		return 0
	}
	now := planFor(ctx, worktreeRecord(ctx, rp.Path), RemoveOptions{Agents: agents, Landed: landed, Force: rp.Force})
	if !now.same(rp) {
		return rp, "it changed during the sweep"
	}
	return now, ""
}

// whyKept says, in sweep's words, why a delete was refused: a worktree took
// the branch while the plan was being carried out, the worktrees could not be
// read at all, or git refused the ref itself.
func whyKept(err error) string {
	var inUse *repo.BranchInUseError
	switch {
	case errors.As(err, &inUse):
		return "it was checked out after the plan was made"
	case errors.Is(err, repo.ErrWorktreesUnknown):
		return err.Error()
	}
	return gitSaid(err)
}

// landedFrom answers planFor's landed question from a listing already read, so
// a sweep decides on the pull requests it just fetched.
func landedFrom(byBranch map[string]github.PR, trunkNames []string) func(branch, tip string) int {
	return func(branch, tip string) int {
		pr, ok := byBranch[branch]
		if !ok || !pr.Landed(tip, trunkNames...) {
			return 0
		}
		return pr.Number
	}
}
