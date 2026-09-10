package commands

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

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
