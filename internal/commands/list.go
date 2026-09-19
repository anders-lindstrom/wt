package commands

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// listPadding is the gap tabwriter leaves after every column but the last.
const listPadding = 2

// minPathWidth keeps a shortened path long enough to still tell worktrees
// apart; a terminal narrower than that wraps the row instead.
const minPathWidth = 24

// ListOptions tunes List.
type ListOptions struct {
	// NoPR leaves the pull requests out. The column costs one gh call: 0.8s
	// against a repository whose listing otherwise takes 0.02s.
	NoPR bool
	// Refresh asks GitHub even when the cached answer is still young.
	Refresh bool
}

// List prints every worktree of the repository, in whatever layout it is in.
// Anything not at the canonical path is marked, and the two marks mean
// different things: "s" is Superset's layout, which is deliberate and must be
// left alone, while "!" is a layout nothing owns and `wt migrate` can move.
func List(ctx *Context, opts ListOptions, w io.Writer, width int) error {
	names, err := WorkNames(ctx)
	if err != nil {
		return err
	}
	sch := ctx.Scheme()
	var prs map[string]string
	if !opts.NoPR {
		prs = listPRs(ctx, names, opts.Refresh)
	}
	header := []string{"", "WORK", "BRANCH", "PATH"}
	if len(prs) > 0 {
		header = []string{"", "WORK", "BRANCH", "PR", "PATH"}
	}
	rows := [][]string{header}
	var seen [3]bool
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
			}
			seen[layout] = true
			mark = layoutMark(layout)
		}
		row := []string{mark, work, branch}
		if len(prs) > 0 {
			row = append(row, dash(prs[n.Branch]))
		}
		rows = append(rows, append(row, n.Path))
	}
	if err := printPathTable(w, rows, width); err != nil {
		return err
	}
	if seen[naming.Superset] || seen[naming.Foreign] {
		fmt.Fprintln(w, "")
	}
	if seen[naming.Superset] {
		fmt.Fprintln(w, "s  Superset's layout — its workspace holds this path; leave it where it is")
	}
	if seen[naming.Foreign] {
		fmt.Fprintln(w, "!  not a layout wt recognises — `wt migrate <work|branch|path>` moves it to the")
		fmt.Fprintln(w, "   canonical path; add a destination to rename or retype it as it goes")
	}
	return nil
}

// listPRs is the PR column of `wt list`, keyed by branch. Empty when GitHub
// is not in play (off, absent, offline, not this repository) or when no
// worktree here has a pull request, and empty means the column is not
// printed: `wt list` is then byte for byte what it was without it.
func listPRs(ctx *Context, names []WorkName, refresh bool) map[string]string {
	linked := false
	for _, n := range names {
		linked = linked || (!n.IsMain && n.Branch != "")
	}
	if !linked {
		return nil
	}
	byBranch := worktreePRs(ctx, listQuery(github.ListDeadline), refresh)
	if len(byBranch) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, n := range names {
		if pr, ok := byBranch[n.Branch]; ok && !n.IsMain {
			out[n.Branch] = prLabel(pr)
		}
	}
	return out
}

func layoutMark(l naming.Layout) string {
	switch l {
	case naming.Superset:
		return "s"
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
// and how old the fetch is.
func Status(ctx *Context, w io.Writer, width int) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	base, ok := statusBase(ctx)
	fmt.Fprintf(w, "%s\n\n", statusHeader(ctx, base, ok))
	rows := [][]string{{"BRANCH", "STATE", "TRUNK", "PATH"}}
	for _, wt := range worktrees {
		branch := wt.Branch
		if branch == "" {
			branch = "(detached)"
		}
		trunk := "-"
		if !wt.IsMain && wt.Branch != "" {
			trunk = standingLabel(trunkStanding(ctx, base, ok, wt.Branch))
		}
		rows = append(rows, []string{branch, checkoutState(wt.Path), trunk, wt.Path})
	}
	return printPathTable(w, rows, width)
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

// statusBase is the trunk status compares with: the first of trunkBases,
// origin/<trunk> as last fetched when it is there. ok is false when neither
// it nor the local trunk exists.
func statusBase(ctx *Context) (TrunkBase, bool) {
	bases, err := trunkBases(ctx)
	if err != nil {
		return TrunkBase{}, false
	}
	return bases[0], true
}

// statusHeader is the first line of wt status: the ref compared with, and
// for origin/<trunk> how long ago it was fetched, as wt sync --no-fetch says
// it.
func statusHeader(ctx *Context, base TrunkBase, ok bool) string {
	switch {
	case !ok:
		return noTrunkHere(ctx.Config.MainBranch) + " to compare with"
	case strings.HasPrefix(base.Name, "origin/"):
		return "against " + base.Name + ", " + lastFetched(ctx.Repo.MainRoot, time.Now())
	}
	return "against " + base.Name
}

// trunkStanding counts what branch and base have that the other does not,
// with one rev-list, the count every sync verb reads. ok is false when the
// count cannot be read, or there is no base to count against.
func trunkStanding(ctx *Context, base TrunkBase, ok bool, branch string) (behind, ahead int, counted bool) {
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

// StatusOptions tunes StatusWorktree.
type StatusOptions struct {
	// Agents are the sessions to look for in the worktree. Nil asks `claude
	// agents`; an empty slice means there are none.
	Agents []wtsync.Agent
}

// StatusWorktree prints one worktree in full, one fact per line, then what
// wt sync would make of it: the same assessment the overview runs, against
// origin/<trunk> as last fetched, without fetching. The verdict is a
// simulation of the rebase, and the line says so.
func StatusWorktree(ctx *Context, arg string, opts StatusOptions, w io.Writer) error {
	wt, err := Locate(ctx, arg)
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
	if pr := prFact(ctx, wt.Branch); pr != "" {
		rows = append(rows, []string{"  pr", pr})
	}
	agents := opts.Agents
	if agents == nil {
		var aerr error
		if agents, aerr = wtsync.ListOtherAgents(); aerr != nil {
			fmt.Fprintf(w, "note: %v\n", aerr)
		}
	}
	if s := wtsync.SessionsAt(agents, wt.Path); len(s) > 0 {
		rows = append(rows, []string{"  sessions", whoLabel(s)})
	}
	work := workName(ctx, wt.Branch)
	verdict, under := syncVerdict(ctx, work, wt, agents)
	rows = append(rows, []string{"  sync", verdict})

	fmt.Fprintln(w, work)
	if err := printTable(w, rows); err != nil {
		return err
	}
	indent := strings.Repeat(" ", keyWidth(rows)+listPadding)
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
func trunkFact(ctx *Context, base TrunkBase, ok bool, wt repo.Worktree) string {
	if wt.Branch == "" {
		return "-"
	}
	if !ok {
		return noTrunkHere(ctx.Config.MainBranch) + " to compare with"
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
		fact += " " + lastFetchedParen(ctx.Repo.MainRoot, time.Now())
	}
	return fact
}

// syncVerdict assesses one worktree the way the overview does and returns
// its verdict in the overview's words: the class with what holds it, then
// the advice the overview's heading gives for its group, and under it the
// overview's summary lines, ending with the reminder that it was simulated
// against trunk as last fetched. A trunk the overview cannot assess against
// gives its reason instead.
func syncVerdict(ctx *Context, work string, wt repo.Worktree, agents []wtsync.Agent) (line string, under []string) {
	onto, _, cfg, err := syncDeclaration(ctx)
	if err != nil {
		return err.Error(), nil
	}
	a := wtsync.Assess(ctx.Repo.MainRoot, onto, cfg, wt, agents)
	parts := []string{classLabel(a)}
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
		under = append(under, undeclaredNotice(ctx, onto))
	}
	for _, l := range summaryLines(a) {
		under = append(under, "  "+l)
	}
	under = append(under, "simulated against "+onto+" as last fetched; wt sync fetches first")
	return line, under
}

// syncAdvice is what the overview's heading tells you to do with a worktree
// of this group: run it, look at its detail, or nothing for one the overview
// skips or leaves out.
func syncAdvice(work string, declared bool, a wtsync.Assessment) string {
	if a.Class == wtsync.Current && a.Err == nil {
		return ""
	}
	switch sectionOf(a) {
	case sectionReady:
		if !declared {
			return sectionHeading(sectionReady, false)
		}
		return "wt sync run " + work
	case sectionNeedsYou:
		return "wt sync " + work + " for the detail"
	}
	return ""
}

// printPathTable writes rows as aligned columns: the first row is the header
// and the last column of every row is a path. width is the terminal's column
// count, or 0 when output is not a terminal. Above 0, paths are shown from ~
// and shortened from the left so each row fits; at 0 they are printed whole,
// because a printed path is an argument to wt.
func printPathTable(w io.Writer, rows [][]string, width int) error {
	if width > 0 {
		home, _ := os.UserHomeDir()
		fitLastColumn(rows, width, minPathWidth, func(path string, limit int) string {
			return elideLeft(abbreviateHome(path, home), limit)
		})
	}
	return printTable(w, rows)
}

// printTable writes rows as aligned columns.
func printTable(w io.Writer, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, listPadding, ' ', 0)
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	return tw.Flush()
}

// fitLastColumn shortens the last column of every row with shorten so the
// table fits in width columns, but never below floor. The other columns stay
// whole: they are what a reader types back into wt. A header in the last
// column is shorter than any floor, so shortening leaves it alone.
func fitLastColumn(rows [][]string, width, floor int, shorten func(s string, limit int) string) {
	if len(rows) == 0 {
		return
	}
	last := len(rows[0]) - 1
	lead := 0
	for col := range last {
		widest := 0
		for _, r := range rows {
			widest = max(widest, utf8.RuneCountInString(r[col]))
		}
		lead += widest + listPadding
	}
	room := max(width-lead, floor)
	for _, r := range rows {
		r[last] = shorten(r[last], room)
	}
}

func abbreviateHome(path, home string) string {
	if home == "" || home == "/" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+"/"); ok {
		return "~/" + rest
	}
	return path
}

// elideLeft shortens path to at most limit runes by dropping leading
// directories, which every worktree of a repository shares, and keeps whole
// trailing ones. A last component longer than limit is cut mid-name.
func elideLeft(path string, limit int) string {
	if utf8.RuneCountInString(path) <= limit {
		return path
	}
	for rest := path; ; {
		_, after, ok := strings.Cut(rest, "/")
		if !ok {
			break
		}
		rest = after
		if utf8.RuneCountInString(rest)+2 <= limit {
			return "…/" + rest
		}
	}
	r := []rune(path)
	return "…" + string(r[len(r)-(limit-1):])
}
