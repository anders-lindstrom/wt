package commands

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// SweepBase is a ref a branch can be merged into, and the commit it was at
// when it was read.
type SweepBase struct {
	Name string // "origin/main" or "main"
	Tip  string
}

// SweepBranch is a local branch and what the plan made of it.
type SweepBranch struct {
	repo.Branch
	MergedInto string // the first base containing it, "" when none does
	Worktree   string // the checkout it is on, "" when none
	Ahead      int    // commits the first base lacks; read only for Gone
}

// SweepPlan is what a sweep is about to do, assembled before anything changes.
type SweepPlan struct {
	Bases    []SweepBase
	MainRoot string
	// Delete is merged and checked out nowhere: it goes.
	Delete []SweepBranch
	// CheckedOut is merged but a checkout has it: kept until that checkout is gone.
	CheckedOut []SweepBranch
	// Gone lost its upstream ref but trunk lacks its commits: shown, kept.
	Gone []SweepBranch
}

// sweepBases resolves origin/<trunk>, then <trunk>, to commits. origin comes
// first because merges happen on the remote and the main checkout's trunk is
// often behind; the local trunk still counts, because commits on it are on
// trunk too.
func sweepBases(ctx *Context) ([]SweepBase, error) {
	trunk := ctx.Config.MainBranch
	var bases []SweepBase
	for _, b := range []struct{ name, ref string }{
		{"origin/" + trunk, "refs/remotes/origin/" + trunk},
		{trunk, "refs/heads/" + trunk},
	} {
		if tip, ok := ctx.Repo.ResolveRef(b.ref); ok {
			bases = append(bases, SweepBase{Name: b.name, Tip: tip})
		}
	}
	if len(bases) == 0 {
		return nil, fmt.Errorf("neither origin/%s nor %s exists here, so nothing can be called merged", trunk, trunk)
	}
	return bases, nil
}

// planSweep reads every fact a sweep depends on and sorts the branches.
func planSweep(ctx *Context, bases []SweepBase) (SweepPlan, error) {
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
		sb := SweepBranch{Branch: b, MergedInto: merged[b.Name], Worktree: inUse[b.Name]}
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

// checkedOut maps each branch a worktree has in use to its path: the branch it
// has checked out, and the branches its git operations still hold. It reads
// the worktrees rather than for-each-ref's worktreepath, which is empty for a
// branch mid-rebase, for the branch a bisect started from, and for the
// branches a stopped rebase --update-refs will move: only the operation's own
// files name those, and git refuses to delete them all the same. A branch's
// own checkout wins over a hold.
func checkedOut(ctx *Context) (map[string]string, error) {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return nil, fmt.Errorf("could not list worktrees, so cannot tell which branches are in use: %w", err)
	}
	m := map[string]string{}
	for _, wt := range worktrees {
		if wt.Branch != "" {
			m[wt.Branch] = wt.Path
		}
	}
	for _, wt := range worktrees {
		for _, held := range wt.Holds {
			if _, ok := m[held]; !ok {
				m[held] = wt.Path
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

// Render writes the plan: what goes, and what stays with the reason.
func (p SweepPlan) Render(w io.Writer) {
	names := make([]string, len(p.Bases))
	for i, b := range p.Bases {
		names[i] = b.Name
	}
	fmt.Fprintf(w, "merged means reachable from %s\n\n", strings.Join(names, " or "))

	if len(p.Delete) == 0 {
		fmt.Fprintln(w, "No merged branches to delete.")
	} else {
		fmt.Fprintf(w, "Will be deleted, %s:\n", branchCount(len(p.Delete)))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, b := range p.Delete {
			fmt.Fprintf(tw, "  %s\tmerged into %s\t%s\t%s\n", b.Name, b.MergedInto, b.Date, b.Subject)
		}
		_ = tw.Flush()
	}
	if len(p.CheckedOut) > 0 {
		fmt.Fprintln(w, "\nMerged, but checked out, so kept:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, b := range p.CheckedOut {
			fmt.Fprintf(tw, "  %s\t%s\n", b.Name, p.checkedOutAdvice(b))
		}
		_ = tw.Flush()
	}
	if len(p.Gone) > 0 {
		fmt.Fprintln(w, "\nUpstream gone, but not merged, so kept:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, b := range p.Gone {
			ahead := (Plan{Ahead: b.Ahead, MainBranch: p.Bases[0].Name}).aheadOfMain()
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", b.Name, ahead, b.Date, b.Subject)
		}
		_ = tw.Flush()
	}
	fmt.Fprintln(w)
}

// checkedOutAdvice says how to finish off a merged branch a checkout holds.
// It does not promise that wt remove deletes the branch: remove compares with
// the local trunk only, and may keep one merged only into origin's.
func (p SweepPlan) checkedOutAdvice(b SweepBranch) string {
	if samePath(b.Worktree, p.MainRoot) {
		return "the main checkout is on it; switch it to trunk, then sweep again"
	}
	return "wt remove " + b.Name + ", then sweep again"
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
}

// Sweep deletes the local branches trunk already contains.
func Sweep(ctx *Context, opts SweepOptions, w io.Writer) error {
	if err := sweepGuard(ctx); err != nil {
		return err
	}
	if err := sweepFetch(ctx, opts.NoFetch, w); err != nil {
		return err
	}
	bases, err := sweepBases(ctx)
	if err != nil {
		return err
	}
	plan, err := planSweep(ctx, bases)
	if err != nil {
		return err
	}
	plan.Render(w)
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
	bases, err := sweepBases(ctx)
	if err != nil {
		return fmt.Errorf("could not re-read trunk before deleting, so nothing was deleted: %w", err)
	}
	fresh, err := planSweep(ctx, bases)
	if err != nil {
		return fmt.Errorf("could not re-read the branches before deleting, so nothing was deleted: %w", err)
	}
	still := map[string]string{}
	for _, b := range fresh.Delete {
		still[b.Name] = b.Tip
	}

	kept := 0
	for _, b := range p.Delete {
		if tip, ok := still[b.Name]; !ok || tip != b.Tip {
			fmt.Fprintf(w, "- kept %s: it changed after the plan was made\n", b.Name)
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
		if inUse[b.Name] != "" {
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
