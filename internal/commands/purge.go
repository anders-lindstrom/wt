package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/quarantine"
	"github.com/anders-lindstrom/wt/schema"
)

// errPurgePlanChanged is --expect, or the second read after the question,
// refusing a purge whose plan is not the one read.
var errPurgePlanChanged = errors.New("the purge plan changed since it was read (the folder, its " +
	"recovery.json, its pins or what they hold); nothing was purged: read the plan again")

// errPurgeNoTerminal is a purge with nobody to ask and no --yes.
var errPurgeNoTerminal = errors.New("nothing was purged: there is no terminal to ask, " +
	"and a purge deletes for good: pass --yes")

// PurgeOptions carries what wt quarantine purge was asked.
type PurgeOptions struct {
	// DryRun prints the plan and deletes nothing.
	DryRun bool
	// Expect refuses the purge unless the plan made now has this token.
	Expect string
	// Confirm, when set, is asked once the plan is printed; false purges
	// nothing.
	Confirm func() bool
	// NoTerminal is a purge with nobody to ask and no --yes: it refuses.
	NoTerminal bool
	// Result, when set, receives what the purge did.
	Result *quarantine.PurgeResult
	// Planned, when set, is told each plan the purge reads, the last one
	// being the one it went by.
	Planned func(quarantine.PurgePlan)
}

// QuarantinePurge deletes, for good, the quarantine wt remove --quarantine
// or wt sweep --quarantine made at dir: its pins, then the folder.
func QuarantinePurge(dir string, opts PurgeOptions, w io.Writer) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	plan := quarantine.PlanPurge(dir)
	if opts.Planned != nil {
		opts.Planned(plan)
	}
	renderPurgePlan(plan, w)
	res := quarantine.PurgeResult{Outcome: quarantine.OutcomeRefused}
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
		return report(fmt.Errorf("nothing was purged: %s", why))
	}
	token := purgeToken(plan)
	if opts.Expect != "" && opts.Expect != deref(token) {
		return report(errPurgePlanChanged)
	}
	switch {
	case opts.DryRun:
		fmt.Fprintln(w, "Nothing was purged: --dry-run.")
		return report(nil)
	case opts.NoTerminal:
		return report(errPurgeNoTerminal)
	case opts.Confirm != nil:
		if !opts.Confirm() {
			fmt.Fprintln(w, "Nothing was purged.")
			return report(nil)
		}
		// A question can sit for minutes: the purge goes by the plan read
		// now, and only if it is the one that was asked about.
		plan = quarantine.PlanPurge(dir)
		if opts.Planned != nil {
			opts.Planned(plan)
		}
		if deref(purgeToken(plan)) != deref(token) {
			return report(errPurgePlanChanged)
		}
	}
	res, err = quarantine.DoPurge(plan)
	if err != nil {
		if res.Outcome == quarantine.OutcomePartial {
			fmt.Fprintf(w, "! partly purged; wt quarantine purge %s finishes it\n", dir)
		}
		return report(err)
	}
	fmt.Fprintf(w, "✓ purged %s%s\n", dir, purgedWords(plan))
	return report(nil)
}

// purgedWords is the clause the success line ends with.
func purgedWords(p quarantine.PurgePlan) string {
	n := 0
	for _, pin := range p.Pins {
		if pin.OID != "" {
			n++
		}
	}
	switch n {
	case 0:
		return ": the folder is deleted"
	case 1:
		return ": the folder and 1 pin are deleted"
	}
	return fmt.Sprintf(": the folder and %d pins are deleted", n)
}

// renderPurgePlan writes what a purge finds and would delete.
func renderPurgePlan(p quarantine.PurgePlan, w io.Writer) {
	rows := [][]string{{"  quarantine", p.Dir + purgeStateWords(p.State)}}
	if r := p.Record; r != nil {
		rows = append(rows, []string{"  worktree", r.Checkout.Path + " (" + r.Command + ", " + r.CreatedAt + ")"})
		if r.Branch != nil {
			rows = append(rows, []string{"  branch", r.Branch.Name + " at " + git.ShortID(r.Branch.Tip, 12)})
		}
	}
	if len(p.Entries) > 0 {
		rows = append(rows, []string{"  deletes", strings.Join(p.Entries, ", ") + sizeWords(p.Bytes)})
	}
	if p.RepoGone && p.Record != nil {
		rows = append(rows, []string{"  pins", "its repository's git dir " + p.Record.CommonDir + " is gone: deleted, " +
			"its pins went with it; moved, they stay there until wt sweep in it drops them"})
	}
	for i, pin := range p.Pins {
		if p.RepoGone {
			break
		}
		label := ""
		if i == 0 {
			label = "  pins"
		}
		at := "gone already"
		if pin.OID != "" {
			at = "at " + git.ShortID(pin.OID, 12) + ", deleted"
		}
		rows = append(rows, []string{label, pin.Ref + " — " + at})
	}
	for _, l := range p.Lost {
		rows = append(rows, []string{"  lost", fmt.Sprintf("%s on its %s %s become unreachable; gc takes them",
			commitsWord(l.Count), l.Kind, git.ShortID(l.OID, 12))})
	}
	if p.ReachError != "" {
		rows = append(rows, []string{"  lost", "cannot tell what becomes unreachable: " + p.ReachError})
	}
	_ = printTable(w, rows)
	for _, pr := range p.Problems {
		fmt.Fprintf(w, "  ! %s\n", pr.Text)
	}
	fmt.Fprintln(w)
}

func purgeStateWords(state string) string {
	switch state {
	case quarantine.StateQuarantined:
		return " — quarantined; the worktree goes for good"
	case quarantine.StateRestored:
		return " — restored already; only what it left goes"
	case quarantine.StatePurging:
		return " — a purge stopped here; this finishes it"
	case quarantine.StateEmpty:
		return " — empty; nothing names what it was"
	}
	return ""
}

func sizeWords(n int64) string {
	switch {
	case n < 0:
		return ""
	case n < 1024:
		return fmt.Sprintf(" (%d bytes)", n)
	case n < 1024*1024:
		return fmt.Sprintf(" (%.1f KB)", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf(" (%.1f MB)", float64(n)/(1024*1024))
	}
	return fmt.Sprintf(" (%.1f GB)", float64(n)/(1024*1024*1024))
}

func commitsWord(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

// purgeToken names a plan by its fingerprint: the folder by path and
// identity, its state, its record byte for byte, every path in it, each pin
// and where it is, and what would be lost. Nil when the purge would refuse.
func purgeToken(p quarantine.PurgePlan) *string {
	fp := p.Fingerprint()
	if fp == "" {
		return nil
	}
	token := "1:" + fp[:32]
	return &token
}

// PurgePinPlan is one pin, for --json.
type PurgePinPlan struct {
	Name string  `json:"name"`
	Ref  string  `json:"ref"`
	OID  *string `json:"oid"`
}

// PurgeLost is a commit only a pin holds, for --json.
type PurgeLost struct {
	Kind  string `json:"kind"`
	OID   string `json:"oid"`
	Count int    `json:"count"`
}

// PurgeProblem is one reason a purge refuses, for --json.
type PurgeProblem struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// PurgePlanOutput is the one object wt quarantine purge --dry-run --json
// prints.
type PurgePlanOutput struct {
	Schema        int            `json:"schema"`
	SchemaVersion string         `json:"schemaVersion"`
	Command       string         `json:"command"`
	Dir           string         `json:"dir"`
	Repo          *string        `json:"repo"`
	State         *string        `json:"state"`
	Token         *string        `json:"token"`
	Worktree      *string        `json:"worktree"`
	Branch        *string        `json:"branch"`
	CreatedAt     *string        `json:"createdAt"`
	Entries       []string       `json:"entries"`
	Bytes         *int64         `json:"bytes"`
	Pins          []PurgePinPlan `json:"pins"`
	Unreachable   []PurgeLost    `json:"unreachable"`
	ReachError    *string        `json:"reachError"`
	RepoGone      bool           `json:"repositoryGone"`
	Problems      []PurgeProblem `json:"problems"`
	Error         *string        `json:"error"`
}

// PurgePinResult is one pin and what became of it, for --json.
type PurgePinResult struct {
	Name   string  `json:"name"`
	Ref    string  `json:"ref"`
	OID    *string `json:"oid"`
	Result string  `json:"result"`
}

// What became of a pin.
const (
	PinDeleted = "deleted"
	PinAbsent  = "absent"
	PinKept    = "kept"
)

// PurgeOutput is the one object wt quarantine purge --yes --json prints.
type PurgeOutput struct {
	Schema        int              `json:"schema"`
	SchemaVersion string           `json:"schemaVersion"`
	Command       string           `json:"command"`
	Dir           string           `json:"dir"`
	Repo          *string          `json:"repo"`
	State         *string          `json:"state"`
	Token         *string          `json:"token"`
	Outcome       string           `json:"outcome"`
	Error         *string          `json:"error"`
	Problems      []PurgeProblem   `json:"problems"`
	Pins          []PurgePinResult `json:"pins"`
	FolderDeleted bool             `json:"folderDeleted"`
	Recovery      *string          `json:"recovery"`
}

const purgeCommand = "quarantine purge"

func purgePlanOutput(p quarantine.PurgePlan) PurgePlanOutput {
	o := PurgePlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("quarantine-purge-plan"), Command: purgeCommand,
		Dir: p.Dir, State: strp(p.State), Token: purgeToken(p), Entries: []string{}, Pins: []PurgePinPlan{},
		Unreachable: []PurgeLost{}, ReachError: strp(p.ReachError), RepoGone: p.RepoGone, Problems: purgeProblems(p)}
	o.Entries = append(o.Entries, p.Entries...)
	if p.Bytes >= 0 {
		n := p.Bytes
		o.Bytes = &n
	}
	if r := p.Record; r != nil {
		o.Repo, o.Worktree, o.CreatedAt = strp(r.Repo), strp(r.Checkout.Path), strp(r.CreatedAt)
		if r.Branch != nil {
			o.Branch = strp(r.Branch.Name)
		}
	}
	for _, pin := range p.Pins {
		o.Pins = append(o.Pins, PurgePinPlan{Name: pin.Name, Ref: pin.Ref, OID: strp(pin.OID)})
	}
	for _, l := range p.Lost {
		o.Unreachable = append(o.Unreachable, PurgeLost(l))
	}
	if why := p.Refusal(); why != "" {
		o.Error = strp(why)
	}
	return o
}

func purgeProblems(p quarantine.PurgePlan) []PurgeProblem {
	out := []PurgeProblem{}
	for _, pr := range p.Problems {
		out = append(out, PurgeProblem(pr))
	}
	return out
}

// QuarantinePurgePlanJSON writes wt quarantine purge --dry-run --json: the
// plan, every reason it would refuse and the token that holds a purge to
// it. It deletes nothing; the plan as wt prints it goes to progress. A plan
// the purge would refuse is one object too, with error set, and the error
// is returned.
func QuarantinePurgePlanJSON(dir string, out, progress io.Writer) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	plan := quarantine.PlanPurge(abs)
	renderPurgePlan(plan, progress)
	o := purgePlanOutput(plan)
	if werr := writeJSON(out, o); werr != nil {
		return werr
	}
	if o.Error != nil {
		return errors.New(*o.Error)
	}
	return nil
}

// QuarantinePurgeJSON is wt quarantine purge --yes --json: the purge, and
// one object on out saying what became of each pin and of the folder —
// when it returns, or when a signal ends it. The plan as wt prints it and
// the progress go to progress.
func QuarantinePurgeJSON(dir string, opts PurgeOptions, out, progress io.Writer) error {
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}
	j := &purgeJournal{out: out, dir: dir}
	defer setInterruptJournal(j)()
	defer watchSignals(progress, nil)()
	var res quarantine.PurgeResult
	opts.Confirm, opts.NoTerminal, opts.DryRun, opts.Result, opts.Planned = nil, false, false, &res, j.planned
	if err == nil {
		err = QuarantinePurge(dir, opts, progress)
	}
	j.write(res.Outcome, err, "")
	return err
}

// purgeJournal writes wt quarantine purge --json's one object once: when
// the purge returns, or when a signal ends it.
type purgeJournal struct {
	mu   sync.Mutex
	once sync.Once
	out  io.Writer
	dir  string
	plan quarantine.PurgePlan
}

// planned records the plan the purge read.
func (j *purgeJournal) planned(p quarantine.PurgePlan) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.plan = p
}

func (j *purgeJournal) interrupted(_ string, recovery string) {
	j.write(OutcomeInterrupted, nil, recovery)
}

// write reads the pins and the folder as they are now, so what it says
// became of each is true whatever the run got to. The object is read
// outside once: a signal parks the git a returning run is reading with, and
// the handler must still be able to write.
func (j *purgeJournal) write(outcome string, err error, recovery string) {
	j.mu.Lock()
	p := j.plan
	j.mu.Unlock()
	o := purgeOutput(j.dir, p, outcome, err, recovery)
	j.once.Do(func() { _ = writeJSON(j.out, o) })
}

func purgeOutput(dir string, p quarantine.PurgePlan, outcome string, err error, recovery string) PurgeOutput {
	if outcome == "" {
		outcome = quarantine.OutcomeRefused
	}
	o := PurgeOutput{Schema: 1, SchemaVersion: schema.VersionOf("quarantine-purge"), Command: purgeCommand,
		Dir: dir, State: strp(p.State), Token: purgeToken(p), Outcome: outcome, Problems: purgeProblems(p),
		Pins: []PurgePinResult{}, Recovery: strp(recovery)}
	if err != nil {
		o.Error = strp(err.Error())
		if errors.Is(err, errPurgePlanChanged) || errors.Is(err, quarantine.ErrPurgeChanged) {
			o.Problems = append(o.Problems, PurgeProblem{Code: ProblemPlanChanged, Text: err.Error()})
		}
	}
	if p.Record != nil {
		o.Repo = strp(p.Record.Repo)
	}
	_, serr := os.Lstat(dir)
	o.FolderDeleted = p.Dir != "" && errors.Is(serr, os.ErrNotExist)
	for _, pin := range p.Pins {
		r := PurgePinResult{Name: pin.Name, Ref: pin.Ref, OID: strp(pin.OID), Result: PinAbsent}
		if pin.OID != "" && !p.RepoGone {
			r.Result = PinKept
			// A pin that cannot be read is not known to be gone.
			if now, err := quarantine.ReadPin(p.Record.Repo, pin.Ref); err == nil && now == "" {
				r.Result = PinDeleted
			}
		}
		o.Pins = append(o.Pins, r)
	}
	return o
}
