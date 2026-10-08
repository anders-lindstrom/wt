package commands

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/schema"
)

// RestoreOptions carries what wt restore was asked.
type RestoreOptions struct {
	// DryRun prints what the restore would do and changes nothing.
	DryRun bool
	// Result, when set, receives what the restore did.
	Result *quarantine.RestoreResult
}

// Restore puts back the worktree wt remove --move-to moved into dir.
// ctx is the repository wt was run in, nil outside one: it is only where a
// quarantine with no recovery.json is looked for; one with a record names
// its own repository.
func Restore(ctx *Context, dir string, opts RestoreOptions, w io.Writer) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	var rp *repo.Repo
	if ctx != nil {
		rp = ctx.Repo
	}
	plan := quarantine.PlanRestore(dir, rp)
	renderRestorePlan(plan, w)
	res := quarantine.RestoreResult{Outcome: quarantine.OutcomeRefused, Action: plan.Action,
		OccupiedBy: plan.OccupiedBy, Checkout: plan.Checkout, Admin: plan.Admin}
	report := func(err error) error {
		if err != nil {
			res.Error = err.Error()
		}
		if opts.Result != nil {
			*opts.Result = res
		}
		return err
	}
	if why := plan.Refusal(); why != "" {
		return report(fmt.Errorf("nothing was restored: %s", why))
	}
	if opts.DryRun {
		fmt.Fprintln(w, "Nothing was restored: --dry-run.")
		return report(nil)
	}
	res, err = quarantine.DoRestore(dir, rp)
	if err != nil {
		if res.Outcome == quarantine.OutcomePartial {
			fmt.Fprintf(w, "! partly restored; wt restore %s finishes it\n", dir)
		}
		return report(err)
	}
	path := plan.Orphan
	if plan.Record != nil {
		path = plan.Record.Checkout.Path
	}
	fmt.Fprintf(w, "✓ restored %s%s\n", path, branchRestored(plan.Record, res))
	registerRestored(ctx, path, w)
	return report(nil)
}

// registerRestored registers the worktree a restore put back with Superset,
// the way wt new registers a new one; its removal deleted the workspace.
// The repository is the worktree's own, which need not be the one wt ran
// in; the person's settings are the ones this run read.
func registerRestored(ctx *Context, path string, w io.Writer) {
	rctx, err := Open(path)
	if err != nil {
		return
	}
	if ctx != nil {
		rctx.User, rctx.UserError = ctx.User, ctx.UserError
	}
	registerSuperset(rctx, path, w)
}

// renderRestorePlan writes what a restore finds and would do.
func renderRestorePlan(p quarantine.RestorePlan, w io.Writer) {
	rows := [][]string{{"  folder", p.Dir}}
	switch {
	case p.Record != nil:
		r := p.Record
		rows = append(rows,
			[]string{"  checkout", r.Checkout.Path + " — " + placeWords(p.Checkout)},
			[]string{"  git dir", r.Admin.Path + " — " + placeWords(p.Admin)})
		if r.Branch != nil && p.Action != "" {
			rows = append(rows, []string{"  branch", r.Branch.Name + " — " + actionWords(r, p.Action, p.OccupiedBy)})
		}
	case p.Orphan != "":
		rows = append(rows, []string{"  checkout", p.Orphan + " — never moved; its lock comes off"})
	}
	_ = printTable(w, rows)
	for _, pr := range p.Problems {
		fmt.Fprintf(w, "  ! %s\n", pr.Text)
	}
	fmt.Fprintln(w)
}

func placeWords(at string) string {
	switch at {
	case quarantine.AtOriginal:
		return "in place"
	case quarantine.InQuarantine:
		return "in the folder, goes back"
	case quarantine.Taken:
		return "its path is taken"
	}
	return "missing"
}

// actionWords says what the restore does to the branch.
func actionWords(r *quarantine.Record, action, occupiedBy string) string {
	b := r.Branch
	short := git.ShortID(b.Tip, 12)
	switch action {
	case quarantine.ActionNone:
		return "there at " + short + "; nothing to do"
	case quarantine.ActionRenameBack:
		return fmt.Sprintf("renamed back from %s", deref(b.KeepAs))
	case quarantine.ActionRecreate:
		return "made again at " + short + ", with its config"
	case quarantine.ActionAttach:
		return fmt.Sprintf("its name is another commit's now; HEAD names %s, still at %s", deref(b.KeepAs), short)
	}
	head := ""
	if r.Head != nil {
		head = git.ShortID(*r.Head, 12)
	}
	if occupiedBy != "" {
		return fmt.Sprintf("in use in %s; not touched, HEAD detached at %s", occupiedBy, head)
	}
	return fmt.Sprintf("moved on since; not touched, HEAD detached at %s", head)
}

// branchRestored is the clause the success line ends with.
func branchRestored(r *quarantine.Record, res quarantine.RestoreResult) string {
	if r == nil || r.Branch == nil {
		return ""
	}
	return "; branch " + r.Branch.Name + " " + actionWords(r, res.Action, res.OccupiedBy)
}

// RestorePlace is one of the two directories, for --json.
type RestorePlace struct {
	Path        string `json:"path"`
	Quarantined string `json:"quarantined"`
	Location    string `json:"location"`
}

// RestoreBranch is the branch, for --json.
type RestoreBranch struct {
	Name       string  `json:"name"`
	Tip        string  `json:"tip"`
	KeepAs     *string `json:"keepAs"`
	Removal    *string `json:"removal"`
	Action     *string `json:"action"`
	OccupiedBy *string `json:"occupiedBy"`
}

// RestoreProblem is one reason a restore refuses, for --json.
type RestoreProblem struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// RestorePlanOutput is the one object wt restore --dry-run --json prints.
type RestorePlanOutput struct {
	Schema        int              `json:"schema"`
	SchemaVersion string           `json:"schemaVersion"`
	Command       string           `json:"command"`
	Dir           string           `json:"dir"`
	Repo          *string          `json:"repo"`
	Checkout      *RestorePlace    `json:"checkout"`
	Admin         *RestorePlace    `json:"admin"`
	Head          *string          `json:"head"`
	Branch        *RestoreBranch   `json:"branch"`
	Locked        bool             `json:"locked"`
	Orphan        *string          `json:"orphan"`
	Problems      []RestoreProblem `json:"problems"`
	Error         *string          `json:"error"`
}

// RestoreOutput is the one object wt restore --json prints.
type RestoreOutput struct {
	Schema        int               `json:"schema"`
	SchemaVersion string            `json:"schemaVersion"`
	Command       string            `json:"command"`
	Dir           string            `json:"dir"`
	Repo          *string           `json:"repo"`
	Outcome       string            `json:"outcome"`
	Error         *string           `json:"error"`
	Checkout      *RestorePlace     `json:"checkout"`
	Admin         *RestorePlace     `json:"admin"`
	Branch        *RestoreBranch    `json:"branch"`
	Unlocked      bool              `json:"unlocked"`
	Steps         []quarantine.Step `json:"steps"`
	Recovery      *string           `json:"recovery"`
}

func restorePlace(pl quarantine.Place, at string) *RestorePlace {
	return &RestorePlace{Path: pl.Path, Quarantined: pl.Quarantined, Location: at}
}

func restoreBranchOf(r *quarantine.Record, action, occupiedBy string) *RestoreBranch {
	if r == nil || r.Branch == nil {
		return nil
	}
	return &RestoreBranch{Name: r.Branch.Name, Tip: r.Branch.Tip, KeepAs: r.Branch.KeepAs,
		Removal: r.Branch.Result, Action: strp(action), OccupiedBy: strp(occupiedBy)}
}

// restorePlanOutput is the plan as --json reports it.
func restorePlanOutput(p quarantine.RestorePlan) RestorePlanOutput {
	out := RestorePlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("restore-plan"), Command: "restore",
		Dir: p.Dir, Locked: p.Locked, Orphan: strp(p.Orphan), Problems: []RestoreProblem{}}
	for _, pr := range p.Problems {
		out.Problems = append(out.Problems, RestoreProblem(pr))
	}
	if r := p.Record; r != nil {
		out.Repo, out.Head = strp(r.Repo), r.Head
		out.Checkout, out.Admin = restorePlace(r.Checkout, p.Checkout), restorePlace(r.Admin, p.Admin)
		out.Branch = restoreBranchOf(r, p.Action, p.OccupiedBy)
	}
	return out
}

// RestoreJSON is wt restore --json: with DryRun the plan, otherwise the
// restore and its result, as one object on out; the human output goes to
// progress. An error is in the object and returned too.
func RestoreJSON(ctx *Context, dir string, opts RestoreOptions, out, progress io.Writer) error {
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}
	var rp *repo.Repo
	if ctx != nil {
		rp = ctx.Repo
	}
	if opts.DryRun {
		plan := quarantine.PlanRestore(dir, rp)
		renderRestorePlan(plan, progress)
		p := restorePlanOutput(plan)
		if err != nil {
			p.Error = strp(err.Error())
		} else if why := plan.Refusal(); why != "" {
			p.Error = strp(why)
		}
		if werr := writeJSON(out, p); werr != nil {
			return werr
		}
		if p.Error != nil {
			return errors.New(*p.Error)
		}
		return nil
	}
	j := &restoreJournal{out: out, dir: dir, rp: rp}
	defer setInterruptJournal(j)()
	defer watchSignals(progress, nil)()
	var res quarantine.RestoreResult
	opts.Result = &res
	if err == nil {
		err = Restore(ctx, dir, opts, progress)
	}
	j.finish(res, err)
	return err
}

// restoreJournal writes wt restore --json's one object once: when the
// restore returns, or when a signal ends it.
type restoreJournal struct {
	once sync.Once
	out  io.Writer
	dir  string
	rp   *repo.Repo
}

func (j *restoreJournal) finish(res quarantine.RestoreResult, err error) {
	j.write(res.Outcome, res, err, "")
}

func (j *restoreJournal) interrupted(_ string, recovery string) {
	j.write(OutcomeInterrupted, quarantine.RestoreResult{}, nil, recovery)
}

// write reads the quarantine as it is now, so every location, the branch
// and the steps say what is true, whatever the run got to.
func (j *restoreJournal) write(outcome string, res quarantine.RestoreResult, err error, recovery string) {
	j.once.Do(func() {
		if outcome == "" {
			outcome = quarantine.OutcomeRefused
		}
		o := RestoreOutput{Schema: 1, SchemaVersion: schema.VersionOf("restore"), Command: "restore",
			Dir: j.dir, Outcome: outcome, Unlocked: res.Unlocked, Steps: []quarantine.Step{},
			Recovery: strp(recovery)}
		if err != nil {
			o.Error = strp(err.Error())
		}
		if r, lerr := quarantine.Load(j.dir); lerr == nil {
			o.Repo = strp(r.Repo)
			o.Checkout = restorePlace(r.Checkout, quarantine.Locate(r.Checkout))
			o.Admin = restorePlace(r.Admin, quarantine.Locate(r.Admin))
			action, occupied := res.Action, res.OccupiedBy
			if r.Restore != nil {
				action, occupied = deref(r.Restore.Action), deref(r.Restore.OccupiedBy)
				o.Steps = append(o.Steps, r.Restore.Steps...)
			}
			o.Branch = restoreBranchOf(r, action, occupied)
		} else if j.rp != nil {
			o.Repo = strp(j.rp.MainRoot)
		}
		_ = writeJSON(j.out, o)
	})
}
