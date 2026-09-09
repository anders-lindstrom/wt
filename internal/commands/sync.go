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
			workName(ctx, wt.Branch), a.Class, a.Behind, a.Ahead, stopColumn(a), whoColumn(a), noteColumn(a))
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

// stopColumn is the first commit the rebase stops at, its subject, and what
// happens to its files: `2/12 "record every sync run" SyncWorker.java✗`.
func stopColumn(a wtsync.Assessment) string {
	if a.Replay.Stop == nil {
		return "-"
	}
	parts := []string{fmt.Sprintf("%d/%d %q", a.Replay.Stop.Index, a.Replay.Stop.Total, truncate(oneLine(a.Replay.Stop.Subject), 32))}
	for _, f := range a.Files {
		mark := "✗"
		if f.Resolved {
			mark = "✓"
		}
		parts = append(parts, shortPath(f.Path)+mark)
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
	return a.Agent.Name
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
