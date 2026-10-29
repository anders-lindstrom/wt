// Package commands implements wt list and wt status, and holds the tests
// that cross the command packages.
package commands

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/admin"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/session"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// ListOptions tunes List.
type ListOptions struct {
	// NoPR leaves the pull requests out. The column costs one gh call on the
	// first listing — around 0.6s, against 0.02s for the rest of it — and
	// nothing on the listings the cache answers.
	NoPR bool
	// Refresh asks GitHub even when the cached answer is still young.
	Refresh bool
	// NoSessions leaves the Claude sessions out. The column costs one
	// claude agents --json, around a quarter of a second, on every listing.
	NoSessions bool
	// Sessions is a read several listings share, whose caller prints its
	// Note once. Nil reads them here, and the note goes under this table.
	Sessions *session.ClaudeSessions
	// Wide prints the table as it is printed when piped, whatever the
	// terminal's width: the SESSION column in it, and nothing cut to fit.
	Wide bool
}

// List prints every worktree of the repository, in whatever layout it is in.
// Anything not at the canonical path is marked, and the two marks mean
// different things: "s" is Superset's layout and "t" T3 Code's, which are
// deliberate and must be left alone, while "!" is a layout nothing owns and `wt migrate` can move.
// The PR and SESSION columns are printed only when a worktree has one, and
// on a terminal the SESSION column only when every row still fits its width:
// a listing never wraps a row for the column's sake.
func List(ctx *kit.Context, opts ListOptions, w io.Writer, width int) error {
	names, err := kit.WorkNames(ctx)
	if err != nil {
		return err
	}
	sch := ctx.Scheme()
	// Started before the pull requests are asked for, so the two waits
	// overlap.
	read := opts.Sessions
	if read == nil && !opts.NoSessions {
		read = session.ReadClaudeSessions(ctx, session.SessionsDeadline)
	}
	var prs map[string]string
	var cached time.Time
	if !opts.NoPR {
		prs, cached = listPRs(ctx, names, opts.Refresh)
	}
	paths := make([]string, len(names))
	for i, n := range names {
		paths[i] = n.Path
	}
	var sessions map[string][]session.ClaudeSession
	if !opts.NoSessions {
		sessions = read.ByWorktree(paths)
	}
	header := []string{"", "WORK", "BRANCH", "PATH"}
	if len(prs) > 0 {
		header = []string{"", "WORK", "BRANCH", "PR", "PATH"}
	}
	rows := [][]string{header}
	var seen [4]bool
	for _, n := range names {
		work, branch := "(main)", n.Branch
		if branch == "" {
			branch = "(detached)"
		}
		mark := ""
		if !n.IsMain {
			work = "-"
			layout := naming.Foreign
			if n.Work != "" {
				work, layout = n.Work, sch.Classify(n.Path, n.Type, n.Work)
			} else if sch.UnderT3(n.Path) {
				layout = naming.T3
			}
			seen[layout] = true
			mark = layoutMark(layout)
		}
		row := []string{mark, work, branch}
		if len(prs) > 0 {
			row = append(row, admin.Dash(prs[n.Branch]))
		}
		rows = append(rows, append(row, n.Path))
	}
	if opts.Wide {
		width = 0
	}
	leftOut := false
	if len(sessions) > 0 {
		if with := session.WithSessions(rows, paths, sessions); width <= 0 || session.PathTableFits(with, width) {
			rows = with
		} else {
			leftOut = true
		}
	}
	if err := kit.PrintPathTable(w, rows, width); err != nil {
		return err
	}
	var legend []string
	if seen[naming.Superset] {
		legend = append(legend, "s  Superset's layout — its workspace holds this path; leave it where it is")
	}
	if seen[naming.T3] {
		legend = append(legend, "t  T3 Code's layout — its thread holds this path; leave it where it is")
	}
	if seen[naming.Foreign] {
		legend = append(legend,
			"!  not a layout wt recognises — `wt migrate <work|branch|path>` moves it to the",
			"   canonical path; add a destination to rename or retype it as it goes")
	}
	if line := prAgeLine(cached, "wt list"); line != "" {
		legend = append(legend, line)
	}
	if leftOut {
		legend = append(legend, "   sessions left out at this width — `wt list --wide` shows them")
	}
	if opts.Sessions == nil && read != nil {
		if note := read.Note(); note != "" {
			legend = append(legend, note)
		}
	}
	if len(legend) > 0 {
		fmt.Fprintln(w, "")
		for _, line := range legend {
			fmt.Fprintln(w, line)
		}
	}
	return nil
}

// prColumnAge is how old the oldest cached answer in the PR column is, and ""
// when the column is this listing's own work or fresh enough that its age is
// not worth a line.
func prColumnAge(cached, now time.Time) string {
	if cached.IsZero() || now.Sub(cached) < time.Minute {
		return ""
	}
	return kit.Ago(now.Sub(cached))
}

// prAgeLine is the legend line saying how old a cached PR column is, naming
// the command that asks again, and "" when prColumnAge has nothing to say.
func prAgeLine(cached time.Time, command string) string {
	age := prColumnAge(cached, time.Now())
	if age == "" {
		return ""
	}
	return "   pull requests as of " + age + " — `" + command + " --refresh` asks GitHub again"
}

// listPRs is the PR column of `wt list`, keyed by branch, and when the oldest
// answer it took from the cache was read. Empty when GitHub is not in play
// (off, absent, offline, not this repository) or when no worktree here has a
// pull request, and empty means the column is not printed: `wt list` is then
// byte for byte what it was without it.
func listPRs(ctx *kit.Context, names []kit.WorkName, refresh bool) (map[string]string, time.Time) {
	linked := false
	for _, n := range names {
		linked = linked || (!n.IsMain && n.Branch != "")
	}
	if !linked {
		return nil, time.Time{}
	}
	branches := make([]string, 0, len(names))
	for _, n := range names {
		if !n.IsMain && n.Branch != "" {
			branches = append(branches, n.Branch)
		}
	}
	a := kit.BranchPRs(ctx, branches, kit.PRLookup{
		Warn: "no pull requests shown", Refresh: refresh, Deadline: ctx.GitHubDeadline(), Checks: true,
	})
	if len(a.ByBranch) == 0 {
		return nil, time.Time{}
	}
	trunk := kit.Trunks(ctx)
	out := map[string]string{}
	for _, n := range names {
		if pr, ok := a.ByBranch[n.Branch]; ok && !n.IsMain {
			out[n.Branch] = kit.PRCell(pr, trunk)
		}
	}
	return out, a.Cached
}

func layoutMark(l naming.Layout) string {
	switch l {
	case naming.Superset:
		return "s"
	case naming.T3:
		return "t"
	case naming.Foreign:
		return "!"
	default:
		return ""
	}
}

// Status prints each worktree's branch, whether its checkout is clean, and
// where its branch stands against trunk: origin/<trunk> as last fetched, or
// the local trunk without one, the bases wt remove and wt sweep compare with.
// Nothing is fetched. The first line says which of the two it compared with
// and how old the fetch is. The PR column is `wt list`'s, from the same cache.
func Status(ctx *kit.Context, opts StatusOptions, w io.Writer, width int) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	var prs map[string]string
	var cached time.Time
	if !opts.NoPR {
		names := make([]kit.WorkName, len(worktrees))
		for i, wt := range worktrees {
			names[i] = kit.WorkName{Worktree: wt}
		}
		prs, cached = listPRs(ctx, names, opts.Refresh)
	}
	base, ok := statusBase(ctx)
	fmt.Fprintf(w, "%s\n\n", statusHeader(ctx, base, ok))
	header := []string{"BRANCH", "STATE", "TRUNK", "PATH"}
	if len(prs) > 0 {
		header = []string{"BRANCH", "STATE", "TRUNK", "PR", "PATH"}
	}
	rows := [][]string{header}
	for _, wt := range worktrees {
		branch := wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		trunk := "-"
		if !wt.IsMain && wt.Branch != "" {
			trunk = standingLabel(trunkStanding(ctx, base, ok, wt.Branch))
		}
		row := []string{branch, checkoutState(wt.Path), trunk}
		if len(prs) > 0 {
			row = append(row, admin.Dash(prs[wt.Branch]))
		}
		rows = append(rows, append(row, wt.Path))
	}
	if err := kit.PrintPathTable(w, rows, width); err != nil {
		return err
	}
	if line := prAgeLine(cached, "wt status"); line != "" {
		fmt.Fprintf(w, "\n%s\n", line)
	}
	return nil
}

// checkoutState is what wt status says of a checkout: clean, dirty with
// untracked files counted, or unreadable.
func checkoutState(path string) string {
	dirty, err := repo.Dirty(path, false)
	switch {
	case err != nil:
		return "unreadable"
	case dirty:
		return "dirty"
	}
	return "clean"
}

// statusBase is the trunk status compares with: the first of Repo.TrunkBases,
// origin/<trunk> as last fetched when it is there. ok is false when neither
// it nor the local trunk exists.
func statusBase(ctx *kit.Context) (repo.TrunkBase, bool) {
	bases, err := ctx.Repo.TrunkBases(ctx.Config.MainBranch)
	if err != nil {
		return repo.TrunkBase{}, false
	}
	return bases[0], true
}

// statusHeader is the first line of wt status: the ref compared with, and
// for origin/<trunk> how long ago it was fetched, as wt sync --no-fetch says
// it.
func statusHeader(ctx *kit.Context, base repo.TrunkBase, ok bool) string {
	switch {
	case !ok:
		return repo.NoTrunkHere(ctx.Config.MainBranch) + " to compare with"
	case strings.HasPrefix(base.Name, "origin/"):
		return "against " + base.Name + ", " + synccmd.LastFetched(ctx.Repo.MainRoot, time.Now())
	}
	return "against " + base.Name
}

// trunkStanding counts what branch and base have that the other does not,
// with one rev-list, the count every sync verb reads. ok is false when the
// count cannot be read, or there is no base to count against.
func trunkStanding(ctx *kit.Context, base repo.TrunkBase, ok bool, branch string) (behind, ahead int, counted bool) {
	if !ok {
		return 0, 0, false
	}
	behind, ahead, err := wtsync.BehindAhead(ctx.Repo.MainRoot, base.Tip, "refs/heads/"+branch)
	if err != nil {
		return 0, 0, false
	}
	return behind, ahead, true
}

// standingLabel is the TRUNK column: on trunk, the two counts, or ? when they
// could not be read.
func standingLabel(behind, ahead int, counted bool) string {
	switch {
	case !counted:
		return "?"
	case behind == 0 && ahead == 0:
		return "on trunk"
	}
	return fmt.Sprintf("%d behind · %d ahead", behind, ahead)
}

// StatusOptions tunes Status and StatusWorktree.
type StatusOptions struct {
	// NoPR and Refresh are ListOptions' own, for the PR column of Status and
	// the pr line of StatusWorktree.
	NoPR, Refresh bool
	// Agents are the sessions StatusWorktree looks for in the worktree. Nil
	// lists them; an empty slice means there are none.
	Agents []agents.Agent
}

// StatusWorktree prints one worktree in full, one fact per line, then what
// wt sync would make of it: the same assessment the overview runs, against
// origin/<trunk> as last fetched, without fetching. The verdict is a
// simulation of the rebase, and the line says so.
func StatusWorktree(ctx *kit.Context, arg string, opts StatusOptions, w io.Writer) error {
	wt, err := kit.Locate(ctx, arg)
	if err != nil {
		return err
	}
	branch := wt.Branch
	if branch == "" {
		branch = "(detached)"
	}
	base, ok := statusBase(ctx)
	rows := [][]string{
		{"  branch", branch},
		{"  path", wt.Path},
		{"  state", checkoutState(wt.Path)},
		{"  trunk", trunkFact(ctx, base, ok, wt)},
	}
	if !opts.NoPR {
		if pr := kit.PRFact(ctx, wt.Branch, opts.Refresh); pr != "" {
			rows = append(rows, []string{"  pr", pr})
		}
	}
	listed := opts.Agents
	if listed == nil {
		var aerr error
		if listed, aerr = ctx.ListAgents(); aerr != nil {
			fmt.Fprintf(w, "note: %v\n", aerr)
		}
	}
	if s := agents.SessionsAt(listed, wt.Path); len(s) > 0 {
		rows = append(rows, []string{"  sessions", kit.WhoLabel(s)})
	}
	// The branch against its own remote, as last fetched like the rest.
	own := syncrun.OwnStateOf(wtsync.ReadOwnOrUnknown(ctx.Repo.MainRoot, ctx.Config.MainBranch), wt.Branch)
	work := kit.WorktreeName(ctx, wt.Branch, wt.Path)
	verdict, under, a := syncVerdict(ctx, work, wt, listed, own)
	if line := syncrun.OwnLineOf(a); line != "" {
		rows = append(rows, []string{"  remote", line})
		// Said once: the row has it.
		under = slices.DeleteFunc(under, func(l string) bool { return l == "  "+line })
	}
	if line := syncrun.OwnElsewhere(a.Own); line != "" {
		rows = append(rows, []string{"  push", strings.TrimPrefix(line, "pushes ")})
		under = slices.DeleteFunc(under, func(l string) bool { return l == "  "+line })
	}
	if line := syncrun.OwnStray(a.Own); line != "" {
		rows = append(rows, []string{"  also", line})
	}
	rows = append(rows, []string{"  sync", verdict})

	fmt.Fprintln(w, work)
	if err := kit.PrintTable(w, rows); err != nil {
		return err
	}
	indent := strings.Repeat(" ", keyWidth(rows)+kit.ListPadding)
	for _, line := range under {
		fmt.Fprintf(w, "%s%s\n", indent, line)
	}
	return nil
}

// keyWidth is the width of a two-column table's first column, where the
// second column starts less the padding.
func keyWidth(rows [][]string) int {
	widest := 0
	for _, r := range rows {
		widest = max(widest, utf8.RuneCountInString(r[0]))
	}
	return widest
}

// trunkFact is the trunk line of one worktree: the counts, the ref they are
// against, and for origin/<trunk> how old the fetch is.
func trunkFact(ctx *kit.Context, base repo.TrunkBase, ok bool, wt repo.Worktree) string {
	if wt.Branch == "" {
		return "-"
	}
	if !ok {
		return repo.NoTrunkHere(ctx.Config.MainBranch) + " to compare with"
	}
	behind, ahead, counted := trunkStanding(ctx, base, ok, wt.Branch)
	if !counted {
		return "?"
	}
	fact := standingLabel(behind, ahead, counted)
	if fact == "on trunk" {
		fact = "on " + base.Name
	} else {
		fact += " of " + base.Name
	}
	if strings.HasPrefix(base.Name, "origin/") {
		fact += " " + synccmd.LastFetchedParen(ctx.Repo.MainRoot, time.Now())
	}
	return fact
}

// syncVerdict assesses one worktree the way the overview does and returns
// its verdict in the overview's words: the class with what holds it, then
// the advice the overview's heading gives for its group, and under it the
// overview's summary lines, ending with the reminder that it was simulated
// against trunk as last fetched. A trunk the overview cannot assess against
// gives its reason instead.
func syncVerdict(ctx *kit.Context, work string, wt repo.Worktree, listed []agents.Agent, own wtsync.OwnRemote) (line string, under []string, a wtsync.Assessment) {
	a.Own = own
	onto, _, cfg, err := synccmd.SyncDeclaration(ctx)
	if err != nil {
		return err.Error(), nil, a
	}
	a = wtsync.AssessOwn(ctx.Repo.MainRoot, onto, cfg, wt, listed, own)
	parts := []string{synccmd.ClassLabel(a)}
	if a.Dirty && len(a.Sessions.Busy()) == 0 {
		parts = append(parts, "dirty")
	}
	if a.Paused {
		parts = append(parts, "handed over")
	}
	line = strings.Join(parts, ", ")
	if advice := syncAdvice(work, cfg != nil, a); advice != "" {
		line += " · " + advice
	}
	if cfg == nil {
		under = append(under, synccmd.UndeclaredNotice(ctx, onto))
	}
	for _, l := range synccmd.SummaryLines(a) {
		under = append(under, "  "+l)
	}
	under = append(under, "simulated against "+onto+" as last fetched; wt sync fetches first")
	return line, under, a
}

// syncAdvice is what the overview's heading tells you to do with a worktree
// of this group: run it, look at its detail, or nothing for one the overview
// skips or leaves out.
func syncAdvice(work string, declared bool, a wtsync.Assessment) string {
	if syncrun.OnTrunk(a) {
		return ""
	}
	switch synccmd.SectionOf(a) {
	case synccmd.SectionReady:
		if !declared {
			return synccmd.SectionHeading(synccmd.SectionReady, false)
		}
		return "wt sync rebase " + work
	case synccmd.SectionNeedsYou:
		return "wt sync " + work + " for the detail"
	}
	return ""
}
