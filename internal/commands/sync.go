package commands

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// noteWidth caps a free-text note under a row of the overview; wt sync <work>
// prints it whole.
const noteWidth = 100

// shownFiles is how many files a stop names in the overview before it counts
// the rest.
const shownFiles = 3

// Sync prints what a rebase onto trunk would do to every worktree, computed
// by simulating each rebase in the object store, grouped by what to do about
// it. It changes nothing; the verbs that do are separate commands.
func Sync(ctx *Context, w io.Writer) error {
	onto, cfg, agents, err := syncInputs(ctx, w)
	if err != nil {
		return err
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	var groups [syncSections][]syncEntry
	var all []syncEntry
	for _, wt := range worktrees {
		if wt.IsMain {
			continue
		}
		a := wtsync.Assess(ctx.Repo.MainRoot, onto, cfg, wt, agents)
		if a.Class == wtsync.Current && a.Err == nil {
			continue
		}
		e := syncEntry{work: workName(ctx, wt.Branch), a: a}
		s := sectionOf(a)
		groups[s] = append(groups[s], e)
		all = append(all, e)
	}
	if len(all) == 0 {
		fmt.Fprintln(w, "every worktree is on trunk")
		return nil
	}
	cols := measureRows(all)
	for s, entries := range groups {
		if len(entries) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s\n", sectionHeading(syncSection(s), cfg != nil))
		for _, e := range entries {
			fmt.Fprintf(w, "  %s\n", cols.row(e))
			for _, line := range summaryLines(e.a) {
				fmt.Fprintf(w, "    %s\n", line)
			}
		}
	}
	return nil
}

// SyncWorktree prints everything wt sync knows about one worktree with
// nothing cut short, one item per line: every stop the replay reached and its
// files, every key a collision is made of, every dependency file both sides
// changed.
func SyncWorktree(ctx *Context, arg string, w io.Writer) error {
	wt, err := Locate(ctx, arg)
	if err != nil {
		return err
	}
	onto, cfg, agents, err := syncInputs(ctx, w)
	if err != nil {
		return err
	}
	a := wtsync.Assess(ctx.Repo.MainRoot, onto, cfg, wt, agents)
	printDetail(w, workName(ctx, wt.Branch), a)
	return nil
}

// syncInputs reads what every assessment needs and prints the header: the
// ref compared against, and a notice when trunk declares nothing.
func syncInputs(ctx *Context, w io.Writer) (onto string, cfg *wtsync.Config, agents []wtsync.Agent, err error) {
	trunk := ctx.Config.MainBranch
	onto = "origin/" + trunk
	cfg, err = wtsync.LoadFromTrunk(ctx.Repo.MainRoot, trunk)
	if err != nil && !errors.Is(err, wtsync.ErrNoConfig) {
		return "", nil, nil, err
	}
	if cfg == nil {
		fmt.Fprintf(w, "%s declares no %s on %s: reported only, never rebased.\n\n",
			ctx.Repo.Name, wtsync.ConfigFile, onto)
	}
	agents, aerr := wtsync.ListOtherAgents()
	if aerr != nil {
		fmt.Fprintf(w, "note: %v\n", aerr)
	}
	fmt.Fprintf(w, "against %s (not fetched)\n", onto)
	return onto, cfg, agents, nil
}

func workName(ctx *Context, branch string) string {
	if branch == "" {
		return "(detached)"
	}
	if _, work, ok := naming.ParseBranch(branch, ctx.Config.TypeSuffix); ok {
		return work
	}
	return branch
}

// syncSection is where the overview files a worktree: what to do about it.
type syncSection int

const (
	sectionReady syncSection = iota
	sectionNeedsYou
	sectionSkipped
	syncSections
)

// sectionOf follows wtsync.Preflight, except that a session in the worktree
// outranks its state: whatever is in there is that session's to finish, so
// the worktree is left be rather than put to you.
func sectionOf(a wtsync.Assessment) syncSection {
	switch {
	case a.Err != nil:
		return sectionNeedsYou
	case a.Agent != nil, a.Class == wtsync.Detached, a.Class == wtsync.Stale:
		return sectionSkipped
	case a.Class == wtsync.Clean && !a.Dirty && !a.Paused, a.Class == wtsync.Recipe && !a.Dirty && !a.Paused:
		return sectionReady
	}
	return sectionNeedsYou
}

func sectionHeading(s syncSection, declared bool) string {
	switch s {
	case sectionReady:
		if !declared {
			return "ready, once trunk declares " + wtsync.ConfigFile
		}
		return "ready · wt sync run <work>"
	case sectionNeedsYou:
		return "needs you · wt sync <work> for the detail"
	}
	return "skipped"
}

type syncEntry struct {
	work string
	a    wtsync.Assessment
}

// syncColumns is the width of each column of the one-line rows, measured
// across every section so rows line up from one section to the next.
type syncColumns struct{ work, class, behind, ahead int }

func measureRows(entries []syncEntry) syncColumns {
	var c syncColumns
	for _, e := range entries {
		c.work = max(c.work, utf8.RuneCountInString(e.work))
		c.class = max(c.class, len(classLabel(e.a)))
		c.behind = max(c.behind, len(strconv.Itoa(e.a.Behind)))
		c.ahead = max(c.ahead, len(strconv.Itoa(e.a.Ahead)))
	}
	return c
}

// row is `work  class  N behind  M ahead`, then what keeps a run off the
// worktree when that is not its class.
func (c syncColumns) row(e syncEntry) string {
	line := fmt.Sprintf("%-*s  %-*s  %*d behind  %*d ahead",
		c.work, e.work, c.class, classLabel(e.a), c.behind, e.a.Behind, c.ahead, e.a.Ahead)
	if held := heldBy(e.a); held != "" {
		line += "  " + held
	}
	return line
}

// classLabel is the class, with a question mark when the replay could not
// be carried to the end. `recipe?` is not `recipe`: a script owns a path,
// and all the simulation could ask it was whether it claims the file.
func classLabel(a wtsync.Assessment) string {
	if a.Unverified {
		return a.Class.String() + "?"
	}
	return a.Class.String()
}

func heldBy(a wtsync.Assessment) string {
	switch {
	case a.Agent != nil && a.Agent.Name == "" && a.Agent.Kind == "":
		return sessionLabel(a.Agent)
	case a.Agent != nil:
		return "session " + sessionLabel(a.Agent)
	case a.Dirty:
		return "dirty"
	}
	return ""
}

// summaryLines is the overview's detail under a row, one fact per line, with
// every list cut to a count.
func summaryLines(a wtsync.Assessment) []string {
	var lines []string
	if a.Err != nil {
		lines = append(lines, oneLine("error: "+a.Err.Error()))
	}
	if a.Paused {
		lines = append(lines, "left mid-rebase by wt sync run: wt sync resume, or wt sync undo")
	}
	for _, c := range a.Divergent {
		lines = append(lines, shortPath(c.Path)+": both sides changed "+wtsync.KeyCounts(c.Groups))
	}
	if s := stopSummary(a); s != "" {
		lines = append(lines, s)
	}
	if n := len(a.Replay.Stops) - 1; a.Replay.Stop != nil && n > 0 {
		lines = append(lines, fmt.Sprintf("%d earlier stop%s resolved", n, plural(n)))
	}
	for _, f := range a.Files {
		if note := fileNote(f); note != "" {
			lines = append(lines, truncate(oneLine(shortPath(f.Path)+": "+note), noteWidth))
		}
	}
	if g := a.Graph; g != nil {
		lines = append(lines, fmt.Sprintf("dependency graph changed on both sides: %d file%s on branch, %d on trunk",
			len(g.Branch), plural(len(g.Branch)), len(g.Trunk)))
	}
	for _, n := range a.Notes {
		lines = append(lines, truncate(oneLine(n), noteWidth))
	}
	return lines
}

// stopSummary is the stop that decides the class: the one a person owns, with
// its subject and files, or, when every stop resolved, how many there were
// and the files of the first.
func stopSummary(a wtsync.Assessment) string {
	if stop := a.Replay.Stop; stop != nil {
		return strings.TrimSpace(fmt.Sprintf("%d/%d %q  %s",
			stop.Index, stop.Total, truncate(oneLine(stop.Subject), 32), fileMarks(a.Files)))
	}
	n := len(a.Replay.Stops)
	if n == 0 {
		return ""
	}
	first := a.Replay.Stops[0]
	lead := fmt.Sprintf("1 stop, resolved · %d/%d", first.Index, first.Total)
	if n > 1 {
		lead = fmt.Sprintf("%d stops, all resolved · first %d/%d", n, first.Index, first.Total)
	}
	if marks := fileMarks(a.Files); marks != "" {
		return lead + ": " + marks
	}
	return lead
}

// fileMarks names a stop's files marked ✓ resolved or ✗ yours, the ones that
// are yours first, and counts the rest past shownFiles.
func fileMarks(files []wtsync.FileOutcome) string {
	sorted := slices.Clone(files)
	slices.SortStableFunc(sorted, func(x, y wtsync.FileOutcome) int {
		switch {
		case x.Resolved == y.Resolved:
			return 0
		case !x.Resolved:
			return -1
		}
		return 1
	})
	shown := sorted
	if len(sorted) > shownFiles {
		shown = sorted[:shownFiles-1]
	}
	parts := make([]string, 0, len(shown)+1)
	for _, f := range shown {
		parts = append(parts, shortPath(f.Path)+fileMark(f.Resolved))
	}
	if rest := len(sorted) - len(shown); rest > 0 {
		parts = append(parts, fmt.Sprintf("+%d more", rest))
	}
	return strings.Join(parts, " ")
}

func fileMark(resolved bool) string {
	if resolved {
		return "✓"
	}
	return "✗"
}

// fileNote is why a file a strategy refused is yours, when that is more than
// nothing claiming it.
func fileNote(f wtsync.FileOutcome) string {
	switch {
	case f.Resolved, f.Note == "", f.Note == "unclaimed":
		return ""
	case len(f.Groups) > 0:
		return "both sides changed " + wtsync.KeyCounts(f.Groups)
	}
	return f.Note
}

// printDetail is wt sync <work>: the row, where the worktree is and what a run
// would do with it, then each kind of finding under its own heading.
func printDetail(w io.Writer, work string, a wtsync.Assessment) {
	e := syncEntry{work: work, a: a}
	fmt.Fprintf(w, "\n%s\n", measureRows([]syncEntry{e}).row(e))
	branch := a.Branch
	if branch == "" {
		branch = "(detached)"
	}
	fmt.Fprintf(w, "  branch  %s\n", branch)
	fmt.Fprintf(w, "  path    %s\n", a.Path)
	fmt.Fprintf(w, "  run     %s\n", runVerdict(work, a))

	if a.Err != nil {
		fmt.Fprintln(w, "\nerror")
		printLines(w, 1, a.Err.Error())
	}
	if len(a.Divergent) > 0 {
		fmt.Fprintln(w, "\ndivergent: the openapi strategy refuses these at the endpoint")
		for _, c := range a.Divergent {
			fmt.Fprintf(w, "  %s\n", c.Path)
			printKeyGroups(w, 2, c.Groups)
		}
	}
	if len(a.Replay.Stops) > 0 {
		fmt.Fprintln(w, "\nstops the replay reached")
		for _, stop := range a.Replay.Stops {
			files, yours := stop.Files, ""
			if a.Replay.Stop != nil && stop.Index == a.Replay.Stop.Index {
				files, yours = a.Files, "  ← yours"
			}
			fmt.Fprintf(w, "  %d/%d  %s%s\n", stop.Index, stop.Total, oneLine(stop.Subject), yours)
			for _, f := range files {
				printFileDetail(w, f)
			}
		}
	}
	if g := a.Graph; g != nil {
		fmt.Fprintln(w, "\ndependency graph: both sides changed it")
		printList(w, 1, "on the branch", g.Branch)
		printList(w, 1, "on trunk", g.Trunk)
	}
	if len(a.Notes) > 0 {
		fmt.Fprintln(w, "\nnotes")
		for _, n := range a.Notes {
			printLines(w, 1, n)
		}
	}
}

// runVerdict is what wt sync run would do with the worktree, in
// wtsync.Preflight's words.
func runVerdict(work string, a wtsync.Assessment) string {
	switch v, why := wtsync.Preflight(a); v {
	case wtsync.Proceed:
		if stop := a.Replay.Stop; a.Class == wtsync.Contested && stop != nil {
			return fmt.Sprintf("wt sync run %s rebases up to %d/%d and hands that stop to you", work, stop.Index, stop.Total)
		}
		return "wt sync run " + work
	case wtsync.SkipRun:
		return "skipped: " + why
	default:
		return "refused: " + why
	}
}

// printFileDetail is one conflicted file: ✓ and the strategy that resolved
// it, or ✗ and why it is yours, then any keys it collided on.
func printFileDetail(w io.Writer, f wtsync.FileOutcome) {
	fmt.Fprintf(w, "    %s %s  %s\n", fileMark(f.Resolved), f.Path, fileVerdict(f))
	if len(f.Groups) > 0 {
		printKeyGroups(w, 3, f.Groups)
		return
	}
	if _, rest, ok := strings.Cut(f.Note, "\n"); ok && !f.Resolved {
		printLines(w, 3, rest)
	}
}

func fileVerdict(f wtsync.FileOutcome) string {
	first, _, _ := strings.Cut(f.Note, "\n")
	switch {
	case f.Resolved && f.Strategy != "":
		return "resolved by " + f.Strategy
	case f.Resolved:
		return "resolved"
	case f.Note == "unclaimed", f.Note == "" && f.Strategy == "":
		return "yours: no strategy claims it"
	case len(f.Groups) > 0:
		return f.Strategy + " refuses: both sides changed " + wtsync.KeyCounts(f.Groups)
	case f.Strategy != "":
		return f.Strategy + " refuses: " + first
	}
	return first
}

func printKeyGroups(w io.Writer, depth int, groups []wtsync.KeyGroup) {
	for _, g := range groups {
		printList(w, depth, g.Section, g.Keys)
	}
}

// printList is a label with its count, then its items one per line beneath.
func printList(w io.Writer, depth int, label string, items []string) {
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(w, "%s%s (%d)\n", indent, label, len(items))
	for _, item := range items {
		fmt.Fprintf(w, "%s  %s\n", indent, item)
	}
}

func printLines(w io.Writer, depth int, text string) {
	indent := strings.Repeat("  ", depth)
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		fmt.Fprintf(w, "%s%s\n", indent, line)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// oneLine flattens a note fragment to fit one line of the overview: an
// embedded newline, carriage return or tab would break the layout of every
// line printed after it.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func shortPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
