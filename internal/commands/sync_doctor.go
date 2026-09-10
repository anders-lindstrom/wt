package commands

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// DoctorOptions tunes SyncDoctor for callers and tests.
type DoctorOptions struct{ Fix, Prune bool }

// SyncDoctor checks what a run needs and reports it as a table. It never
// fixes anything on its own; --fix and --prune opt into that, one check at
// a time. A check that blocks a run (trunk, declaration, scripts) and has no
// fix makes the returned error non-nil; everything else is advisory.
func SyncDoctor(ctx *Context, opts DoctorOptions, w io.Writer) error {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	checks, err := wtsync.Doctor(ctx.Repo.MainRoot, ctx.Config.MainBranch, worktrees, wtsync.DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour})
	if err != nil {
		return err
	}
	// The plan row is built here rather than in wtsync: it names the resume
	// command, and only this package knows a worktree's work name.
	holders, err := wtsync.PlanHolders(worktrees)
	if err != nil {
		return err
	}
	plan := wtsync.Check{Name: "plan", OK: len(holders) == 0, Detail: "no worktree is waiting on a person"}
	if len(holders) > 0 {
		var lines []string
		for _, wt := range holders {
			name := workName(ctx, wt.Branch)
			lines = append(lines, name+": wt sync resume "+name)
		}
		plan.Detail = strings.Join(lines, "; ")
	}
	checks = append(checks, plan)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHECK\tSTATE\tDETAIL")
	var blocking []string
	for _, c := range checks {
		state := "ok"
		if !c.OK {
			state = "warn"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Name, state, oneLine(c.Detail))
		if !c.OK && c.Fix == nil && (c.Name == "trunk" || c.Name == "declaration" || c.Name == "scripts") {
			blocking = append(blocking, c.Name)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, c := range checks {
		if c.OK || c.Fix == nil {
			continue
		}
		want := (opts.Fix && c.Name != "safety-refs") || (opts.Prune && c.Name == "safety-refs")
		if !want {
			continue
		}
		if err := c.Fix(); err != nil {
			return fmt.Errorf("fix %s: %w", c.Name, err)
		}
		fmt.Fprintf(w, "fixed %s\n", c.Name)
	}
	if len(blocking) > 0 {
		return fmt.Errorf("run is blocked by: %s", strings.Join(blocking, ", "))
	}
	return nil
}
