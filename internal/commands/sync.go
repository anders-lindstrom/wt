package commands

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// Sync prints what a rebase onto trunk would do to every worktree, computed
// by simulating each rebase in the object store. It changes nothing; the
// verbs that do are separate commands.
func Sync(ctx *Context, w io.Writer) error {
	trunk := ctx.Config.MainBranch
	onto := "origin/" + trunk
	cfg, err := wtsync.LoadFromTrunk(ctx.Repo.MainRoot, trunk)
	if err != nil && !errors.Is(err, wtsync.ErrNoConfig) {
		return err
	}
	if cfg == nil {
		fmt.Fprintf(w, "%s declares no %s on %s: reported only, never rebased.\n\n",
			ctx.Repo.Name, wtsync.ConfigFile, onto)
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	agents, err := wtsync.ListAgents()
	if err != nil {
		fmt.Fprintf(w, "note: %v\n", err)
	}

	fmt.Fprintf(w, "against %s (not fetched)\n", onto)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WORK\tCLASS\tBEHIND\tAHEAD\tSTOP\tWHO\tNOTE")
	for _, wt := range worktrees {
		if wt.IsMain {
			continue
		}
		a := wtsync.Assess(ctx.Repo.MainRoot, onto, cfg, wt, agents)
		if a.Class == wtsync.Current && a.Err == nil {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
			workName(ctx, wt.Branch), classColumn(a), a.Behind, a.Ahead, stopColumn(a), whoColumn(a), noteColumn(a))
	}
	return tw.Flush()
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

// classColumn is the class, with a question mark when the replay could not
// be carried to the end. `recipe?` is not `recipe`: a script owns a path,
// and all the simulation could ask it was whether it claims the file.
func classColumn(a wtsync.Assessment) string {
	if a.Unverified {
		return a.Class.String() + "?"
	}
	return a.Class.String()
}

// stopColumn is the stop that decides the class — the first one a person
// owns, or the first of a run that resolves throughout — its subject, and
// what happens to its files: `2/12 "record every sync run" SyncWorker.java✗`.
func stopColumn(a wtsync.Assessment) string {
	stop := a.Replay.Stop
	if stop == nil {
		if len(a.Replay.Stops) == 0 {
			return "-"
		}
		stop = &a.Replay.Stops[0]
	}
	parts := []string{fmt.Sprintf("%d/%d %q", stop.Index, stop.Total, truncate(oneLine(stop.Subject), 32))}
	for _, f := range a.Files {
		mark := "✗"
		if f.Resolved {
			mark = "✓"
		}
		parts = append(parts, shortPath(f.Path)+mark)
	}
	if a.Replay.Stop == nil && len(a.Replay.Stops) > 1 {
		parts = append(parts, fmt.Sprintf("+%d more", len(a.Replay.Stops)-1))
	}
	return strings.Join(parts, " ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// oneLine flattens a note fragment to fit one tabwriter cell: an embedded
// newline, carriage return or tab would end the row early and corrupt every
// row printed after it.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func whoColumn(a wtsync.Assessment) string {
	if a.Agent == nil {
		return "-"
	}
	if a.Agent.Name != "" {
		return a.Agent.Name
	}
	if a.Agent.Kind != "" {
		return a.Agent.Kind
	}
	return "?"
}

func noteColumn(a wtsync.Assessment) string {
	var notes []string
	if a.Err != nil {
		notes = append(notes, oneLine("error: "+a.Err.Error()))
	}
	if a.Dirty {
		notes = append(notes, "dirty")
	}
	for _, f := range a.Files {
		if !f.Resolved && f.Note != "" && f.Note != "unclaimed" {
			notes = append(notes, oneLine(shortPath(f.Path)+": "+f.Note))
		}
	}
	if a.Paused {
		notes = append(notes, "left mid-rebase by wt sync run: wt sync resume")
	}
	if n := len(a.Replay.Stops) - 1; a.Replay.Stop != nil && n > 0 {
		notes = append(notes, fmt.Sprintf("%d earlier stop%s resolved", n, plural(n)))
	}
	for _, d := range a.Divergent {
		notes = append(notes, oneLine(d))
	}
	for _, n := range a.Notes {
		notes = append(notes, oneLine(n))
	}
	if a.NoConfig && a.Class != wtsync.Detached {
		notes = append(notes, "no declaration")
	}
	if len(notes) == 0 {
		return ""
	}
	return strings.Join(notes, "; ")
}

func shortPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
