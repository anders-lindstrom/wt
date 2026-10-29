package purge

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/commands/refs"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/jsonout"
	"github.com/anders-lindstrom/wt/schema"
)

// RefLost is a pin whose commits nothing outside the purge reaches.
type RefLost struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

// purgeRun is one run a purge deletes the pins of.
type purgeRun struct {
	refpins.SweptRun
	Lost []RefLost
}

// RefsPurgePlan is what wt refs purge would delete.
type RefsPurgePlan struct {
	Repo      string
	OlderThan string
	Runs      []purgeRun
}

// RefsPurgeOptions carries what wt refs purge was asked.
type RefsPurgeOptions struct {
	RunIDs    []string
	OlderThan string
	DryRun    bool
	Yes       bool
	Confirm   kit.Confirm[RefsPurgePlan]
	// NoTerminal is a purge with nobody to ask and no --yes: it refuses.
	NoTerminal bool
	Expect     string
	Now        func() time.Time
	Journal    *RefsPurgeJournal
}

// planRefsPurge picks the runs, named or older than --older-than, and
// counts what each pin alone holds.
func planRefsPurge(ctx *kit.Context, opts RefsPurgeOptions) (RefsPurgePlan, error) {
	p := RefsPurgePlan{Repo: ctx.Repo.MainRoot, OlderThan: opts.OlderThan}
	switch {
	case len(opts.RunIDs) > 0 && opts.OlderThan != "":
		return p, errors.New("name runs or give --older-than, not both")
	case len(opts.RunIDs) == 0 && opts.OlderThan == "":
		return p, errors.New("name the runs to purge, or give --older-than; wt refs swept lists them")
	}
	runs, err := refpins.ReadSwept(p.Repo)
	if err != nil {
		return p, err
	}
	if opts.OlderThan != "" {
		age, err := config.ParseAge(opts.OlderThan)
		if err != nil {
			return p, fmt.Errorf("--older-than: %w", err)
		}
		now := time.Now()
		if opts.Now != nil {
			now = opts.Now()
		}
		for _, r := range runs {
			if !r.At.IsZero() && r.At.Before(now.Add(-age)) {
				p.Runs = append(p.Runs, purgeRun{SweptRun: r})
			}
		}
	} else {
		for _, id := range opts.RunIDs {
			i := slices.IndexFunc(runs, func(r refpins.SweptRun) bool { return r.RunID == id })
			if i < 0 {
				if !refpins.RunIDPattern.MatchString(id) && isFolder(ctx, id) {
					return p, fmt.Errorf("%s is a folder, not a run: a worktree moved there is deleted for good "+
						"with wt purge %s; wt refs swept lists the runs", id, id)
				}
				return p, refs.ErrNoRun(id)
			}
			if !slices.ContainsFunc(p.Runs, func(r purgeRun) bool { return r.RunID == id }) {
				p.Runs = append(p.Runs, purgeRun{SweptRun: runs[i]})
			}
		}
	}
	return p, p.countLost(ctx)
}

// isFolder is whether arg, as typed where wt was run, names a folder.
func isFolder(ctx *kit.Context, arg string) bool {
	if !filepath.IsAbs(arg) {
		arg = filepath.Join(ctx.Cwd, arg)
	}
	info, err := os.Stat(arg)
	return err == nil && info.IsDir()
}

// countLost fills in, for each pin, the commits no ref outside the purge
// and no worktree's HEAD reaches: what git gc may take once it goes. A
// commit two purged pins share counts for both.
func (p *RefsPurgePlan) countLost(ctx *kit.Context) error {
	var drop []string
	for _, r := range p.Runs {
		for _, s := range r.Refs {
			drop = append(drop, s.Pin)
		}
	}
	if len(drop) == 0 {
		return nil
	}
	keep, err := ctx.Repo.Survivors(drop, "")
	if err != nil {
		return fmt.Errorf("could not read what else holds the pinned commits: %w", err)
	}
	known := map[string]refpins.ObjectInfo{}
	tipOf := map[string]string{}
	var tips []string
	for _, r := range p.Runs {
		for _, s := range r.Refs {
			if tip := refpins.PeelCommit(p.Repo, s.Object, known); tip != "" {
				tipOf[s.Pin] = tip
				tips = append(tips, tip)
			}
		}
	}
	if len(tips) == 0 {
		return nil
	}
	counts, err := refpins.UniqueCommits(p.Repo, tips, keep)
	if err != nil {
		return err
	}
	for i := range p.Runs {
		for _, s := range p.Runs[i].Refs {
			if n := counts[tipOf[s.Pin]]; n > 0 {
				p.Runs[i].Lost = append(p.Runs[i].Lost, RefLost{ID: s.ID(), Count: n})
			}
		}
	}
	return nil
}

// purgeRefsToken names a purge plan: the repository, every pin of every
// run with its object, and what deleting each pin would lose. Nil when there
// is nothing to purge.
func purgeRefsToken(p RefsPurgePlan) *string { return purgeRefsTokenOf(p).Strp() }

func purgeRefsTokenOf(p RefsPurgePlan) *jsonout.Token {
	if len(p.Runs) == 0 {
		return nil
	}
	t := jsonout.NewToken("rp1-", "wt-refs-purge").Str("Repo", p.Repo)
	for _, r := range p.Runs {
		t.Str("Runs.RunID", r.RunID).Str("Runs.MetaOID", r.MetaOID)
		for _, s := range r.Refs {
			t.Str("Runs.Refs.Pin", s.Pin).Str("Runs.Refs.Object", s.Object)
		}
		for _, l := range r.Lost {
			t.Str("Runs.Lost.ID", l.ID).Int("Runs.Lost.Count", int64(l.Count))
		}
	}
	return t
}

var errRefsPurgePlanChanged = errors.New("the purge plan changed since it was read (a pin moved, went or " +
	"was added); nothing was purged: read the plan again")

var errRefsPurgeNoTerminal = errors.New("nothing was purged: there is no terminal to ask, " +
	"and a purge deletes for good: pass --yes")

// RefsPurge deletes, for good, the pins of the runs picked: each only while
// it holds the object the plan read.
func RefsPurge(ctx *kit.Context, opts RefsPurgeOptions, w io.Writer) (err error) {
	j := opts.Journal
	if j != nil {
		defer kit.SetInterruptJournal(j)()
		defer kit.WatchSignals(w, nil)()
		defer func() { j.Fail(err) }()
	}
	plan, err := planRefsPurge(ctx, opts)
	j.planned(plan, nil)
	if err != nil {
		return err
	}
	token := purgeRefsToken(plan)
	j.planned(plan, token)
	if opts.Expect != "" && jsonout.Deref(token) != opts.Expect {
		return errRefsPurgePlanChanged
	}
	plan.Render(w)
	switch {
	case len(plan.Runs) == 0:
		fmt.Fprintln(w, "Nothing to purge.")
		return nil
	case opts.DryRun:
		fmt.Fprintln(w, "Nothing was purged: --dry-run.")
		return nil
	case opts.Yes:
	case opts.NoTerminal || opts.Confirm == nil:
		return errRefsPurgeNoTerminal
	default:
		if !opts.Confirm(plan) {
			fmt.Fprintln(w, "Nothing was purged.")
			return nil
		}
		fresh, err := planRefsPurge(ctx, opts)
		if err != nil {
			return err
		}
		if jsonout.Deref(purgeRefsToken(fresh)) != jsonout.Deref(token) {
			return errRefsPurgePlanChanged
		}
	}
	j.begin()
	root := ctx.Repo.MainRoot
	kept := 0
	for _, r := range plan.Runs {
		left := 0
		for _, s := range r.Refs {
			j.start(s.Pin)
			var res string
			var derr error
			if _, there, rerr := refpins.RefValue(root, s.Pin); rerr == nil && !there {
				res = j.settle(s.Pin, PinAbsent, nil)
			} else {
				derr = refpins.DeleteRefAt(root, s.Pin, s.Object)
				res = j.settle(s.Pin, "", derr)
			}
			if res != PinDeleted && res != PinAbsent {
				left++
				fmt.Fprintf(w, "- kept %s: %s\n", s.Pin, git.Reason(errOr(derr, "not deleted")))
			}
		}
		// The meta goes last, so a run whose pins are not all gone can still
		// be restored to the origin it came from.
		if left == 0 && r.MetaOID != "" {
			// Read again: a sweep still running under this id adds pins
			// after its meta, and those need the meta to go back.
			if pins, perr := runPins(root, r.RunID); perr != nil || pins > 0 {
				left++
				fmt.Fprintf(w, "- kept %s: the run has pins the plan did not list\n", refpins.MetaRef(r.RunID))
			} else if derr := refpins.DeleteRefAt(root, refpins.MetaRef(r.RunID), r.MetaOID); derr != nil {
				left++
				fmt.Fprintf(w, "- kept %s: %s\n", refpins.MetaRef(r.RunID), git.Reason(derr))
			}
			j.metaDone(r.RunID)
		}
		kept += left
		if left == 0 {
			fmt.Fprintf(w, "✓ purged run %s\n", r.RunID)
		}
	}
	if kept > 0 {
		return fmt.Errorf("%d pins were not deleted; wt refs purge again finishes it", kept)
	}
	return nil
}

// runPins counts the pins under a run now, its meta aside.
func runPins(root, runID string) (int, error) {
	lines, err := git.Lines(root, "for-each-ref", "--format=%(refname)", refpins.SweptPrefix+runID+"/")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range lines {
		if l != refpins.MetaRef(runID) {
			n++
		}
	}
	return n, nil
}

func errOr(err error, why string) error {
	if err != nil {
		return err
	}
	return errors.New(why)
}

// Render writes what the purge deletes and what gc may then take.
func (p RefsPurgePlan) Render(w io.Writer) {
	for _, r := range p.Runs {
		fmt.Fprintf(w, "Run %s (%s):\n", r.RunID, r.At.Local().Format("2006-01-02 15:04"))
		var rows [][]string
		for _, s := range r.Refs {
			rows = append(rows, []string{"  " + s.ID(), git.ShortID(s.Object, 12)})
		}
		_ = kit.PrintTable(w, rows)
		for _, l := range r.Lost {
			fmt.Fprintf(w, "  lost: %s on %s become unreachable; gc takes them\n", kit.CommitsWord(l.Count), l.ID)
		}
	}
	if len(p.Runs) > 0 {
		fmt.Fprintln(w)
	}
}

// RefPurgePin is one pin of a purge plan, for --json.
type RefPurgePin struct {
	Pin    string `json:"pin"`
	ID     string `json:"id"`
	Object string `json:"object"`
}

// RefPurgeRunPlan is one run of a purge plan, for --json.
type RefPurgeRunPlan struct {
	RunID       string            `json:"runId"`
	SweptAt     int64             `json:"sweptAt"`
	Meta        *string           `json:"meta"`
	Endpoint    *refpins.Endpoint `json:"endpoint"`
	Refs        []RefPurgePin     `json:"refs"`
	Unreachable []RefLost         `json:"unreachable"`
}

// RefsPurgePlanOutput is the one object wt refs purge --dry-run --json prints.
type RefsPurgePlanOutput struct {
	Schema        int               `json:"schema"`
	SchemaVersion string            `json:"schemaVersion"`
	Command       string            `json:"command"`
	Repo          *string           `json:"repo"`
	OlderThan     *string           `json:"olderThan"`
	Token         *string           `json:"token"`
	Error         *string           `json:"error"`
	Runs          []RefPurgeRunPlan `json:"runs"`
}

// RefPurgePinResult is what became of one pin, for --json.
type RefPurgePinResult struct {
	RefPurgePin
	Result string  `json:"result"`
	Reason *string `json:"reason"`
}

// RefPurgeRun is one run of a purge that ran, for --json.
type RefPurgeRun struct {
	RunID   string               `json:"runId"`
	SweptAt int64                `json:"sweptAt"`
	Refs    []*RefPurgePinResult `json:"refs"`
	// Meta is the run's meta ref, nil when it had none; MetaDeleted says it
	// is gone, read back.
	Meta        *string `json:"meta"`
	MetaDeleted bool    `json:"metaDeleted"`
}

// RefsPurgeResult is the one object wt refs purge --yes --json prints.
type RefsPurgeResult struct {
	Schema        int           `json:"schema"`
	SchemaVersion string        `json:"schemaVersion"`
	Command       string        `json:"command"`
	Repo          *string       `json:"repo"`
	Token         *string       `json:"token"`
	Outcome       string        `json:"outcome"`
	Error         *string       `json:"error"`
	Recovery      *string       `json:"recovery"`
	Runs          []RefPurgeRun `json:"runs"`
}

const refsPurgeCommand = "refs purge"

func purgePin(s refpins.SweptRef) RefPurgePin {
	return RefPurgePin{Pin: s.Pin, ID: s.ID(), Object: s.Object}
}

// RefsPurgePlanJSON writes wt refs purge --dry-run --json. It deletes
// nothing; a plan that cannot be made is one object too.
func RefsPurgePlanJSON(ctx *kit.Context, opts RefsPurgeOptions, out, progress io.Writer) error {
	o := RefsPurgePlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("refs-purge-plan"), Command: refsPurgeCommand,
		OlderThan: jsonout.Strp(opts.OlderThan), Runs: []RefPurgeRunPlan{}}
	var err error
	if ctx == nil {
		err = kit.ErrNotInRepo
	} else {
		var plan RefsPurgePlan
		plan, err = planRefsPurge(ctx, opts)
		o.Repo = jsonout.Strp(plan.Repo)
		if err == nil {
			o.Token = purgeRefsToken(plan)
			plan.Render(progress)
			for _, r := range plan.Runs {
				run := RefPurgeRunPlan{RunID: r.RunID, SweptAt: r.At.Unix(), Refs: []RefPurgePin{},
					Unreachable: append([]RefLost{}, r.Lost...), Endpoint: runEndpoint(r.SweptRun)}
				if r.MetaOID != "" {
					run.Meta = jsonout.Strp(refpins.MetaRef(r.RunID))
				}
				for _, s := range r.Refs {
					run.Refs = append(run.Refs, purgePin(s))
				}
				o.Runs = append(o.Runs, run)
			}
		}
	}
	if err != nil {
		o.Error = jsonout.Strp(err.Error())
	}
	if werr := jsonout.WriteJSON(out, o); werr != nil && err == nil {
		return werr
	}
	return err
}

// RefsPurgeJournal writes wt refs purge --yes --json's one object once.
type RefsPurgeJournal struct {
	*jsonout.Journal[RefsPurgeResult]
	root  string
	byPin map[string]*RefPurgePinResult
}

// NewRefsPurgeJournal is a journal that writes its object to out.
func NewRefsPurgeJournal(out io.Writer) *RefsPurgeJournal {
	j := &RefsPurgeJournal{byPin: map[string]*RefPurgePinResult{}}
	j.Journal = jsonout.NewJournal(out, RefsPurgeResult{Schema: 1, SchemaVersion: schema.VersionOf("refs-purge"),
		Command: refsPurgeCommand, Runs: []RefPurgeRun{}},
		jsonout.JournalRules[RefsPurgeResult]{Error: func(r *RefsPurgeResult, err error) { r.Error = jsonout.Strp(err.Error()) },
			Caught: j.caught, Close: closeRefsPurge})
	return j
}

// Fail records an error that stopped the run before it began, and writes
// the object.
func (j *RefsPurgeJournal) Fail(err error) {
	if j != nil {
		j.Journal.Fail(err)
	}
}

// Finish writes the object for a run that returned.
func (j *RefsPurgeJournal) Finish() {
	if j != nil {
		j.Journal.Finish()
	}
}

// Interrupted writes the object for a run a signal ended.
func (j *RefsPurgeJournal) Interrupted(inFlight, recovery string) {
	if j != nil {
		j.Journal.Interrupted(inFlight, recovery)
	}
}

func (j *RefsPurgeJournal) planned(p RefsPurgePlan, token *string) {
	if j == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	j.root, j.Res.Repo, j.Res.Token = p.Repo, jsonout.Strp(p.Repo), token
	j.Res.Runs = []RefPurgeRun{}
	for _, r := range p.Runs {
		run := RefPurgeRun{RunID: r.RunID, SweptAt: r.At.Unix(), Refs: []*RefPurgePinResult{}}
		if r.MetaOID != "" {
			run.Meta = jsonout.Strp(refpins.MetaRef(r.RunID))
		}
		for _, s := range r.Refs {
			it := &RefPurgePinResult{RefPurgePin: purgePin(s), Result: refs.RefNotRun}
			run.Refs = append(run.Refs, it)
			j.byPin[s.Pin] = it
		}
		j.Res.Runs = append(j.Res.Runs, run)
	}
}

func (j *RefsPurgeJournal) begin() {
	if j == nil {
		return
	}
	j.Begin(nil)
}

// rootDir is the repository the plan read, "" for none.
func (j *RefsPurgeJournal) rootDir() string {
	if j == nil {
		return ""
	}
	j.Lock()
	defer j.Unlock()
	return j.root
}

// metaDone reads back whether a run's meta is gone.
func (j *RefsPurgeJournal) metaDone(runID string) {
	if j == nil {
		return
	}
	_, there, err := refpins.RefValue(j.rootDir(), refpins.MetaRef(runID))
	j.Edit(func(res *RefsPurgeResult) {
		for i := range res.Runs {
			if res.Runs[i].RunID == runID {
				res.Runs[i].MetaDeleted = err == nil && !there
			}
		}
	})
}

func (j *RefsPurgeJournal) start(pin string) {
	if j == nil {
		return
	}
	j.Start(pin)
}

// settle records what became of a pin: absent when it was gone before the
// purge reached it, else read back. The read happens before the lock is
// taken.
func (j *RefsPurgeJournal) settle(pin, result string, err error) string {
	var reason *string
	if result == "" {
		result, reason = readPurgePin(j.rootDir(), pin, err)
	}
	if j == nil {
		return result
	}
	j.Settle(pin, func(*RefsPurgeResult) {
		if it := j.byPin[pin]; it != nil {
			it.Result, it.Reason = result, reason
		}
	})
	return result
}

// readPurgePin is what became of a pin: deleted when it is gone now, kept
// with why when it is still there or cannot be read.
func readPurgePin(root, pin string, err error) (string, *string) {
	_, ok, rerr := refpins.RefValue(root, pin)
	switch {
	case rerr != nil:
		return PinKept, jsonout.Strp(kit.OneLine(rerr.Error()))
	case !ok:
		return PinDeleted, nil
	case err != nil:
		return PinKept, jsonout.Strp(git.Reason(err))
	}
	return PinKept, jsonout.Strp("still there")
}

// caught reads back the pin a signal caught in flight: deleted when it is
// gone, else interrupted.
func (j *RefsPurgeJournal) caught(_ *RefsPurgeResult, inFlight, _ string) {
	if it := j.byPin[inFlight]; it != nil {
		it.Result, it.Reason = readPurgePin(j.root, it.Pin, nil)
		if it.Result != PinDeleted {
			it.Result, it.Reason = refs.RefInterrupted, nil
		}
	}
}

func closeRefsPurge(r *RefsPurgeResult, signalled bool, recovery string) any {
	if signalled {
		r.Outcome, r.Recovery = jsonout.OutcomeInterrupted, jsonout.Strp(recovery)
		return r
	}
	done, changed := true, false
	for _, run := range r.Runs {
		if run.Meta != nil && !run.MetaDeleted {
			done = false
		}
		for _, it := range run.Refs {
			switch it.Result {
			case PinDeleted:
				changed = true
			case PinAbsent:
			default:
				done = false
			}
		}
	}
	switch {
	case done && r.Error == nil:
		r.Outcome = OutcomePurged
	case !changed:
		r.Outcome = jsonout.OutcomeRefused
	default:
		r.Outcome = jsonout.OutcomePartial
	}
	return r
}

// OutcomePurged is a refs purge that deleted every pin it planned to.
const OutcomePurged = "purged"

// RefSweptRef is one pin of a run, for wt refs swept --json.
type RefSweptRef struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Pin    string `json:"pin"`
	Object string `json:"object"`
}

// RefSweptRun is one run, for wt refs swept --json.
type RefSweptRun struct {
	RunID    string            `json:"runId"`
	SweptAt  int64             `json:"sweptAt"`
	Endpoint *refpins.Endpoint `json:"endpoint"`
	Refs     []RefSweptRef     `json:"refs"`
}

// runEndpoint is the origin a run's meta names, nil for none.
func runEndpoint(r refpins.SweptRun) *refpins.Endpoint {
	if r.Meta == nil {
		return nil
	}
	return r.Meta.Endpoint
}

// RefsSweptOutput is the one object wt refs swept --json prints.
type RefsSweptOutput struct {
	Schema        int           `json:"schema"`
	SchemaVersion string        `json:"schemaVersion"`
	Command       string        `json:"command"`
	Repo          *string       `json:"repo"`
	Error         *string       `json:"error"`
	Runs          []RefSweptRun `json:"runs"`
}

// RefsSwept lists every run whose pins are there, oldest first: as JSON on
// out with asJSON, else as a table.
func RefsSwept(ctx *kit.Context, asJSON bool, out io.Writer) error {
	o := RefsSweptOutput{Schema: 1, SchemaVersion: schema.VersionOf("refs-swept"), Command: "refs swept",
		Runs: []RefSweptRun{}}
	var runs []refpins.SweptRun
	var err error
	if ctx == nil {
		err = kit.ErrNotInRepo
	} else {
		o.Repo = jsonout.Strp(ctx.Repo.MainRoot)
		runs, err = refpins.ReadSwept(ctx.Repo.MainRoot)
	}
	for _, r := range runs {
		run := RefSweptRun{RunID: r.RunID, SweptAt: r.At.Unix(), Endpoint: runEndpoint(r), Refs: []RefSweptRef{}}
		for _, s := range r.Refs {
			run.Refs = append(run.Refs, RefSweptRef{ID: s.ID(), Kind: s.Kind, Name: s.Name, Pin: s.Pin, Object: s.Object})
		}
		o.Runs = append(o.Runs, run)
	}
	if asJSON {
		if err != nil {
			o.Error = jsonout.Strp(err.Error())
		}
		if werr := jsonout.WriteJSON(out, o); werr != nil && err == nil {
			return werr
		}
		return err
	}
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Fprintln(out, "No swept refs: nothing under "+refpins.SweptPrefix)
		return nil
	}
	for _, r := range runs {
		fmt.Fprintf(out, "%s  swept %s, %s\n", r.RunID, r.At.Local().Format("2006-01-02 15:04"), kit.Count(len(r.Refs), "ref"))
		var rows [][]string
		for _, s := range r.Refs {
			rows = append(rows, []string{"  " + s.ID(), git.ShortID(s.Object, 12)})
		}
		_ = kit.PrintTable(out, rows)
	}
	fmt.Fprintln(out, "\nwt refs restore <run> puts a run back; wt refs purge <run> deletes it for good")
	return nil
}
