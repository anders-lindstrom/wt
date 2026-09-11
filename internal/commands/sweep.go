package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
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
	// HeldBy is the operation in Worktree holding it, "bisect" or "rebase";
	// "" when Worktree simply has it checked out.
	HeldBy string
	Ahead  int // commits the first base lacks; read only for Gone
}

// SweepPlan is what a sweep is about to do, assembled before anything changes.
type SweepPlan struct {
	Bases    []TrunkBase
	MainRoot string
	// Delete is merged and checked out nowhere: it goes.
	Delete []SweepBranch
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

// planSweep reads every fact a sweep depends on and sorts the branches.
func planSweep(ctx *Context, bases []TrunkBase) (SweepPlan, error) {
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
	inUse, err := checkedOut(ctx)
	if err != nil {
		return SweepPlan{}, err
	}
	originHead, _ := ctx.Repo.OriginHead()

	p := SweepPlan{Bases: bases, MainRoot: ctx.Repo.MainRoot}
	for _, b := range branches {
		if protectedBranch(b.Name, ctx.Config.MainBranch, originHead) {
			continue
		}
		use := inUse[b.Name]
		sb := SweepBranch{Branch: b, MergedInto: merged[b.Name], Worktree: use.Path, HeldBy: use.By}
		switch {
		case sb.MergedInto != "" && sb.Worktree != "":
			p.CheckedOut = append(p.CheckedOut, sb)
		case sb.MergedInto != "":
			p.Delete = append(p.Delete, sb)
		case b.Gone:
			// By tip, not name: a tag with the branch's name would shadow it.
			sb.Ahead, _ = ctx.Repo.CommitsAhead(b.Tip, bases[0].Tip)
			p.Gone = append(p.Gone, sb)
		}
	}
	return p, nil
}

// branchUse is the worktree using a branch, and how.
type branchUse struct {
	Path string
	// By is the operation in Path holding the branch, "bisect" or "rebase";
	// "" when Path simply has it checked out.
	By string
}

// checkedOut maps each branch a worktree has in use to that worktree: the
// branch it has checked out, and the branches its git operations still hold.
// It reads the worktrees rather than for-each-ref's worktreepath, which is
// empty for a branch mid-rebase, for the branch a bisect started from, and
// for the branches a stopped rebase --update-refs will move: only the
// operation's own files name those, and git refuses to delete them all the
// same. A checkout mid-rebase counts as held by that rebase. A branch's own
// checkout wins over a hold elsewhere.
func checkedOut(ctx *Context) (map[string]branchUse, error) {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return nil, fmt.Errorf("could not list worktrees, so cannot tell which branches are in use: %w", err)
	}
	m := map[string]branchUse{}
	for _, wt := range worktrees {
		if wt.Branch != "" {
			use := branchUse{Path: wt.Path}
			if wt.Rebasing {
				use.By = "rebase"
			}
			m[wt.Branch] = use
		}
	}
	for _, wt := range worktrees {
		for _, h := range wt.Holds {
			if _, ok := m[h.Branch]; !ok {
				m[h.Branch] = branchUse{Path: wt.Path, By: h.By}
			}
		}
	}
	return m, nil
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

	if len(p.Delete) == 0 {
		fmt.Fprintln(w, "No merged branches to delete.")
	} else {
		fmt.Fprintf(w, "Will be deleted, %s:\n", branchCount(len(p.Delete)))
		var rows [][]string
		for _, b := range p.Delete {
			rows = append(rows, []string{"  " + b.Name, "merged into " + b.MergedInto, b.Date, b.Subject})
		}
		printSubjectTable(w, rows, width)
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
			rows = append(rows, []string{"  " + b.Name, aheadOf(b.Ahead, p.Bases[0].Name), b.Date, b.Subject})
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

// checkedOutAdvice says how to finish off a merged branch a worktree is using.
// wt remove reads the same bases as this plan, so for a plain checkout it
// deletes the branch with the worktree. A branch a bisect or rebase holds is
// not for wt remove: it finds no worktree on a branch held from elsewhere, and
// on a checkout mid-rebase it throws the rebase away and leaves the branch.
func (p SweepPlan) checkedOutAdvice(b SweepBranch) string {
	switch {
	case b.HeldBy != "":
		return "held by the " + b.HeldBy + " in " + b.Worktree + "; finish or abort it there, then sweep again"
	case samePath(b.Worktree, p.MainRoot):
		return "the main checkout is on it; switch it to trunk, then sweep again"
	}
	return "wt remove " + b.Name + " deletes it with its worktree"
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
	now, exists := ctx.Repo.ResolveRef("refs/heads/" + was.Name)
	head, _ := ctx.Repo.OriginHead()
	switch {
	case !exists:
		return "it was deleted after the plan was made"
	case now != was.Tip:
		return "it moved after the plan was made"
	case protectedBranch(was.Name, ctx.Config.MainBranch, head):
		return "origin's HEAD names it now"
	}
	return "trunk no longer contains it: the merge was undone after the plan was made"
}

func branchCount(n int) string {
	if n == 1 {
		return "1 branch"
	}
	return fmt.Sprintf("%d branches", n)
}

// SweepOptions carries the caller's fetch and confirmation policy.
type SweepOptions struct {
	NoFetch bool
	// Yes deletes without asking. Without it Confirm asks, and with neither
	// the plan is printed and nothing is deleted: a bulk delete does not
	// happen because nobody was there to say no.
	Yes     bool
	Confirm func(SweepPlan) (bool, error)
	// Width is the terminal's column count, 0 when output is not a terminal.
	Width int
}

// Sweep deletes the local branches trunk already contains.
func Sweep(ctx *Context, opts SweepOptions, w io.Writer) error {
	if err := sweepGuard(ctx); err != nil {
		return err
	}
	if err := sweepFetch(ctx, opts.NoFetch, w); err != nil {
		return err
	}
	bases, err := trunkBases(ctx)
	if err != nil {
		return err
	}
	plan, err := planSweep(ctx, bases)
	if err != nil {
		return err
	}
	plan.Render(w, opts.Width)
	if len(plan.Delete) == 0 {
		return nil
	}

	switch {
	case opts.Yes:
	case opts.Confirm != nil:
		ok, err := opts.Confirm(plan)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(w, "Nothing was deleted.")
			return nil
		}
	default:
		fmt.Fprintf(w, "Nothing was deleted: there is no terminal to ask. Pass --yes to delete %s.\n",
			branchCount(len(plan.Delete)))
		return nil
	}
	return plan.apply(ctx, w)
}

// sweepGuard refuses before anything is fetched or deleted: sweep acts on the
// whole repository, so it runs from its main checkout, which has to be a
// checkout, against a trunk somebody named.
func sweepGuard(ctx *Context) error {
	if !samePath(ctx.Repo.Root, ctx.Repo.MainRoot) {
		return fmt.Errorf("wt sweep deletes branches across the whole repository, so it runs "+
			"only from the main checkout, %s\n  wt cd . gets you there", ctx.Repo.MainRoot)
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
		if _, err := git.RunTimeout(ctx.Repo.MainRoot, fetchTimeout, "fetch", "--quiet", "--prune",
			"--no-prune-tags", "--no-tags", "--no-recurse-submodules", "--refmap=",
			"origin", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
			return fmt.Errorf("fetch: %w\n  --no-fetch compares with origin as last fetched", err)
		}
		fmt.Fprintln(w, "fetched origin")
	}
	return nil
}

// apply deletes the planned branches. Trunk and the branches are read again
// first, and a branch that moved or is no longer deletable is kept. Each
// delete then asks once more whether a worktree has the branch, and deletes
// only at the tip the plan showed, so the plan that was shown is the plan
// that runs, branch by branch.
func (p SweepPlan) apply(ctx *Context, w io.Writer) error {
	bases, err := trunkBases(ctx)
	if err != nil {
		return fmt.Errorf("could not re-read trunk before deleting, so nothing was deleted: %w", err)
	}
	fresh, err := planSweep(ctx, bases)
	if err != nil {
		return fmt.Errorf("could not re-read the branches before deleting, so nothing was deleted: %w", err)
	}
	kept := 0
	for _, b := range p.Delete {
		if why := fresh.changedFrom(ctx, b); why != "" {
			fmt.Fprintf(w, "- kept %s: %s\n", b.Name, why)
			kept++
			continue
		}
		// update-ref does not refuse a checked-out branch the way branch -D
		// does, so this is asked as close to the delete as it gets.
		inUse, err := checkedOut(ctx)
		if err != nil {
			fmt.Fprintf(w, "- kept %s: %v\n", b.Name, err)
			kept++
			continue
		}
		if inUse[b.Name].Path != "" {
			fmt.Fprintf(w, "- kept %s: it was checked out after the plan was made\n", b.Name)
			kept++
			continue
		}
		if err := ctx.Repo.DeleteBranchAt(b.Name, b.Tip); err != nil {
			fmt.Fprintf(w, "- kept %s: %s\n", b.Name, gitSaid(err))
			kept++
			continue
		}
		short := b.Tip
		if len(short) > 12 {
			short = short[:12]
		}
		fmt.Fprintf(w, "✓ deleted %s; git branch %s %s restores its commits\n", b.Name, b.Name, short)
	}
	if kept > 0 {
		return fmt.Errorf("%d of %s kept; run wt sweep again to see why", kept, branchCount(len(p.Delete)))
	}
	return nil
}
