package synccmd

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/anlindstrom/wt/internal/commands/kit"
	"github.com/anlindstrom/wt/internal/commands/syncrun"
	"github.com/anlindstrom/wt/internal/git"
	"github.com/anlindstrom/wt/internal/jsonout"
	"github.com/anlindstrom/wt/internal/repo"
	"github.com/anlindstrom/wt/internal/wtsync"
)

// OntoTarget is the ref --onto names, as a run and a look resolve it: the
// commit it is now, what it is (a remote-tracking ref is fetched with the
// run, anything else is taken as it stands), and the commit --upstream
// names, when one is.
type OntoTarget struct {
	// Ref is the ref as given, "integ" or "origin/release".
	Ref string
	// Full is its full name, "refs/heads/integ"; empty for a commit or a
	// name git resolves to no ref.
	Full string
	SHA  string
	// Remote is the remote a remote-tracking target is fetched from; empty
	// for any other ref.
	Remote string
	// Upstream is the commit --upstream names, "" without it.
	Upstream string
	// UpstreamRef is --upstream as given.
	UpstreamRef string
}

// Short is the target as a line names it: its ref and short commit.
func (o OntoTarget) Short() string { return o.Ref + " " + git.ShortID(o.SHA, 7) }

// Target is the target as wtsync assesses and replays against it, with the
// declaration's scripts read from trunkSHA.
func (o OntoTarget) Target(trunkSHA string) wtsync.Target {
	return wtsync.Target{Onto: o.SHA, Upstream: o.Upstream, Trunk: trunkSHA, Name: o.Ref}
}

// cutoff is where the commits of a rebase onto o start, in the form
// ReadLine and StepsDue count them from: the upstream, or the target.
func (o OntoTarget) cutoff() string {
	if o.Upstream != "" {
		return o.Upstream
	}
	return o.SHA
}

// errOntoUsage is the mistakes no repository is needed to see.
func ontoUsage(works []string, onto, upstream string) error {
	switch {
	case upstream != "" && onto == "":
		return errors.New("--upstream needs --onto: it says where the commits rebased onto --onto's ref start")
	case onto != "" && len(works) == 0:
		return errors.New("--onto rebases the worktrees you name onto it; name them")
	}
	return nil
}

// ontoSource is where a remote-tracking --onto ref is fetched from: the
// ref on Remote that the remote's own fetch refspec writes to Dst.
type ontoSource struct{ Remote, Src, Dst string }

// fetchOnto fetches src into its remote-tracking ref.
func fetchOnto(ctx *kit.Context, src ontoSource, timeout time.Duration) error {
	_, err := git.RunTimeout(ctx.Repo.MainRoot, timeout, "fetch", "--quiet", src.Remote, "+"+src.Src+":"+src.Dst)
	return err
}

// remoteSource is where the remote-tracking ref ref names is fetched from,
// read back through each remote's remote.<name>.fetch, an exact refspec
// before a pattern, as git writes them; ok is false for a ref that is not a
// remote-tracking ref (a local branch, a tag, a commit). It reads the name
// rather than the commit, so a ref never fetched yet is still known for
// what it is. A ref under refs/remotes/<remote>/ that no refspec of that
// remote writes is an error: what to fetch cannot be told.
func remoteSource(ctx *kit.Context, ref string) (src ontoSource, ok bool, err error) {
	root := ctx.Repo.MainRoot
	if strings.HasPrefix(ref, "-") {
		return src, false, nil
	}
	full, rerr := git.Run(root, "rev-parse", "--symbolic-full-name", ref)
	if rerr != nil || full == "" {
		full = "refs/remotes/" + ref
		if _, ferr := git.Run(root, "check-ref-format", full); ferr != nil {
			return src, false, nil //nolint:nilerr // a name that is no ref is no remote-tracking ref to fetch
		}
	}
	if !strings.HasPrefix(full, "refs/remotes/") {
		return src, false, nil
	}
	remotes, lerr := git.Lines(root, "remote")
	if lerr != nil {
		return src, false, nil //nolint:nilerr // no remotes read is no remote-tracking ref to fetch
	}
	specs := map[string][]string{}
	for _, r := range remotes {
		lines, _ := git.Lines(root, "config", "--get-all", "remote."+r+".fetch")
		specs[r] = lines
	}
	for _, exact := range []bool{true, false} {
		for _, r := range remotes {
			if from, found := invertRefspecs(specs[r], full, exact); found {
				return ontoSource{Remote: r, Src: from, Dst: full}, true, nil
			}
		}
	}
	// The longest remote name the ref is under: a remote may have a slash
	// in its name.
	slices.SortFunc(remotes, func(a, b string) int { return len(b) - len(a) })
	for _, r := range remotes {
		if strings.HasPrefix(full, "refs/remotes/"+r+"/") {
			return src, true, fmt.Errorf("--onto %s: no fetch refspec of %s writes %s, so what to fetch for it cannot be told; "+
				"name the branch, or fetch it yourself and pass --no-fetch; nothing is rebased", ref, r, full)
		}
	}
	return src, false, nil
}

// invertRefspecs is the source ref one of specs, a remote's fetch
// refspecs, writes to dst: with exact, only a refspec naming dst itself;
// without, only a pattern. A negative refspec that excludes the source
// leaves it unwritten.
func invertRefspecs(specs []string, dst string, exact bool) (string, bool) {
	var negative []string
	for _, spec := range specs {
		if n, ok := strings.CutPrefix(spec, "^"); ok {
			negative = append(negative, n)
		}
	}
	excluded := func(src string) bool {
		for _, n := range negative {
			if n == src {
				return true
			}
			if pre, suf, glob := strings.Cut(n, "*"); glob && strings.HasPrefix(src, pre) && strings.HasSuffix(src, suf) {
				return true
			}
		}
		return false
	}
	for _, spec := range specs {
		spec = strings.TrimPrefix(spec, "+")
		from, to, ok := strings.Cut(spec, ":")
		if !ok || strings.HasPrefix(spec, "^") || strings.Contains(to, "*") == exact {
			continue
		}
		if exact {
			if to == dst && !excluded(from) {
				return from, true
			}
			continue
		}
		pre, suf, _ := strings.Cut(to, "*")
		if len(dst) <= len(pre)+len(suf) || !strings.HasPrefix(dst, pre) || !strings.HasSuffix(dst, suf) {
			continue
		}
		src := strings.Replace(from, "*", dst[len(pre):len(dst)-len(suf)], 1)
		if !excluded(src) {
			return src, true
		}
	}
	return "", false
}

// resolveOnto reads what --onto and --upstream name, after any fetch. A ref
// that does not resolve to a commit is refused.
func resolveOnto(ctx *kit.Context, ref, upstream string) (OntoTarget, error) {
	root := ctx.Repo.MainRoot
	o := OntoTarget{Ref: ref, UpstreamRef: upstream}
	if strings.HasPrefix(ref, "-") || strings.HasPrefix(upstream, "-") {
		return o, errors.New("--onto and --upstream name a ref or a commit, never something starting with -")
	}
	var err error
	if o.SHA, err = git.Run(root, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}"); err != nil || o.SHA == "" {
		return o, fmt.Errorf("--onto %s: not a commit here; nothing is rebased", ref)
	}
	if full, err := git.Run(root, "rev-parse", "--symbolic-full-name", ref); err == nil {
		o.Full = full
	}
	if src, ok, _ := remoteSource(ctx, ref); ok {
		o.Remote = src.Remote
	}
	if upstream != "" {
		if o.Upstream, err = git.Run(root, "rev-parse", "--verify", "--quiet", "--end-of-options", upstream+"^{commit}"); err != nil || o.Upstream == "" {
			return o, fmt.Errorf("--upstream %s: not a commit here; nothing is rebased", upstream)
		}
	}
	return o, nil
}

// refuseFor is why o is no target for branch, the branch of work: its own
// branch, or an upstream that is not below it. "" when it is one.
func (o OntoTarget) refuseFor(ctx *kit.Context, work, branch string) string {
	if o.Full == "refs/heads/"+branch {
		return fmt.Sprintf("--onto %s is %s's own branch: name the branch to rebase it onto", o.Ref, work)
	}
	if o.Upstream != "" {
		if _, err := git.Run(ctx.Repo.MainRoot, "merge-base", "--is-ancestor", o.Upstream, "refs/heads/"+branch); err != nil {
			return fmt.Sprintf("--upstream %s is not an ancestor of %s: only commits above it on the branch are rebased", o.UpstreamRef, branch)
		}
	}
	return ""
}

// onLine is why o is no target for the stack held on line, read against
// trunkSHA: a local branch whose tip is on the line, between where it leaves
// trunk and its head. "" when it is not one.
func (o OntoTarget) onLine(ctx *kit.Context, line wtsync.Line, trunkSHA, work string) string {
	branch, ok := strings.CutPrefix(o.Full, "refs/heads/")
	if !ok {
		return ""
	}
	root := ctx.Repo.MainRoot
	if _, err := git.Run(root, "merge-base", "--is-ancestor", o.SHA, line.Head); err != nil {
		return ""
	}
	if _, err := git.Run(root, "merge-base", "--is-ancestor", o.SHA, trunkSHA); err == nil {
		return ""
	}
	return fmt.Sprintf("--onto %s is a layer on the line of %s: a stack is not rebased onto a branch of its own", branch, work)
}

// followUps is the command that moves each worktree stacked above named
// onto its parent once named has moved, bottom first: each onto its
// immediate parent, above that parent's old tip. named's old tip is the one
// its safety ref pins; a layer the run does not move is where it is now.
func followUps(ctx *kit.Context, parents map[string]string, named string, oldTip func(string) string) []string {
	var lines []string
	for _, b := range wtsync.Order(parents, wtsync.Descendants(parents, named)) {
		parent := parents[b]
		lines = append(lines, fmt.Sprintf("wt sync rebase %s --onto %s --upstream %s",
			kit.BranchWorkName(ctx, b), parent, git.ShortID(oldTip(parent), 7)))
	}
	return lines
}

// resolveTarget fetches what --onto names when it is a remote-tracking ref
// and the run fetches, and reads it and --upstream; how is how the run's
// first line says it was read.
func (r *RunPlan) resolveTarget() (target OntoTarget, how string, err error) {
	ctx := r.ctx
	how = "(as it stands)"
	src, ok, err := remoteSource(ctx, r.opts.Onto)
	if ok {
		if r.opts.NoFetch {
			how = LastFetchedParen(ctx.Repo.MainRoot, r.opts.Clock())
		} else {
			if err != nil {
				return target, "", err
			}
			if err := fetchOnto(ctx, src, kit.NetworkTimeout); err != nil {
				return target, "", fmt.Errorf("fetch %s: %s; nothing is rebased", r.opts.Onto, kit.FetchReason(err))
			}
			how = "(fetched)"
		}
	}
	target, err = resolveOnto(ctx, r.opts.Onto, r.opts.Upstream)
	return target, how, err
}

// ontoBranches checks the worktrees a run with --onto names, before
// anything moves, and keeps their stack parents for the commands that
// follow them: a run with --onto moves exactly the worktrees named.
func (r *RunPlan) ontoBranches(named []string, worktrees []repo.Worktree) error {
	ctx := r.ctx
	// Read here, not taken from an overview: a survey with --onto holds
	// the one worktree, with no stack.
	parents, _, err := wtsync.Parents(ctx.Repo.MainRoot, r.trunkSHA, worktrees)
	if err != nil {
		return err
	}
	for i, a := range named {
		for _, b := range named[i+1:] {
			lower, upper := a, b
			if slices.Contains(wtsync.Descendants(parents, b), a) {
				lower, upper = b, a
			}
			if slices.Contains(wtsync.Descendants(parents, lower), upper) {
				return fmt.Errorf("%s is stacked on %s: with --onto, name the one to move; the run prints the commands that move what is stacked on it",
					kit.BranchWorkName(ctx, upper), kit.BranchWorkName(ctx, lower))
			}
		}
		if why := r.target.refuseFor(ctx, kit.BranchWorkName(ctx, a), a); why != "" {
			return errors.New(why + "; nothing is rebased")
		}
	}
	r.stacked = parents
	return nil
}

// ontoPrecheck refuses, before anything moves (local trunk included),
// what --onto cannot rebase: a worktree named with one stacked on it, the
// target as a named worktree's own branch, an --upstream not below it, and
// for --stack, the target on the line.
func (r *RunPlan) ontoPrecheck(works []string) error {
	ctx := r.ctx
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	var named []string
	for _, arg := range works {
		wt, err := kit.LocateBranch(ctx, arg)
		if err != nil {
			return err
		}
		named = append(named, wt.Branch)
	}
	if err := r.ontoBranches(named, worktrees); err != nil {
		return err
	}
	if r.opts.Stack == nil || len(named) != 1 {
		return nil
	}
	line, err := readLine(ctx, named[0], r.target.cutoff(), r.opts.Stack.Layers)
	if err != nil {
		return err
	}
	if why := r.target.onLine(ctx, line, r.trunkSHA, kit.BranchWorkName(ctx, named[0])); why != "" {
		return errors.New(why + "; nothing is rebased")
	}
	return nil
}

// printFollowUps lists, for each worktree the run moved onto --onto's
// target, the worktrees stacked on it and the command that moves each.
func (r *RunPlan) printFollowUps() {
	if r.target == nil {
		return
	}
	for _, b := range r.branches {
		p := r.parts[b]
		if len(wtsync.Descendants(r.stacked, b)) == 0 {
			continue
		}
		switch {
		case p.result != nil && p.result.Left != nil:
			fmt.Fprintf(r.w, "\n%s has worktrees stacked on it, which --onto did not move; wt sync resume %s prints the commands that move them once it finishes\n", p.work, p.work)
		case p.result == nil || p.result.Restored:
			fmt.Fprintf(r.w, "\n%s has worktrees stacked on it; --onto did not move them, and they follow once it is rebased\n", p.work)
		default:
			syncrun.PrintFollowUps(r.w, p.work, r.followUpLines(b, p, *p.result))
		}
	}
}

// followUpLines is the command that moves each worktree stacked on b once
// b is rebased, b's old tip being the one its safety ref pins; nil without
// --onto or with nothing stacked on b.
func (r *RunPlan) followUpLines(b string, p *participant, res wtsync.Result) []string {
	if r.target == nil || len(wtsync.Descendants(r.stacked, b)) == 0 {
		return nil
	}
	main := r.ctx.Repo.MainRoot
	return followUps(r.ctx, r.stacked, b, func(x string) string {
		if x == b {
			return p.foundTip(res)
		}
		tip, _ := git.Run(main, "rev-parse", "--verify", "refs/heads/"+x)
		return tip
	})
}

// ontoRef is the target's ref as given, "" for trunk.
func (r *RunPlan) ontoRef() string {
	if r.target == nil {
		return ""
	}
	return r.target.Ref
}

// targetName is what the run's lines call what it rebases onto.
func (r *RunPlan) targetName() string {
	if r.target == nil {
		return "trunk"
	}
	return r.target.Ref
}

// targetJSON is the run's target for --json.
func (r *RunPlan) targetJSON() (*syncrun.RunTarget, string) {
	if r.target == nil {
		return &syncrun.RunTarget{Ref: r.onto, Trunk: true}, r.trunkSHA
	}
	t := &syncrun.RunTarget{Ref: r.target.Ref}
	if r.target.Upstream != "" {
		t.Upstream = jsonout.Strp(r.target.Upstream)
	}
	return t, r.target.SHA
}
