package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/schema"
)

// RefLost is a pin whose commits nothing outside the purge reaches.
type RefLost struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

// purgeRun is one run a purge deletes the pins of.
type purgeRun struct {
	sweptRun
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
	Confirm   func(RefsPurgePlan) bool
	// NoTerminal is a purge with nobody to ask and no --yes: it refuses.
	NoTerminal bool
	Expect     string
	Now        func() time.Time
	Journal    *RefsPurgeJournal
}

// planRefsPurge picks the runs, named or older than --older-than, and
// counts what each pin alone holds.
func planRefsPurge(ctx *Context, opts RefsPurgeOptions) (RefsPurgePlan, error) {
	p := RefsPurgePlan{Repo: ctx.Repo.MainRoot, OlderThan: opts.OlderThan}
	switch {
	case len(opts.RunIDs) > 0 && opts.OlderThan != "":
		return p, errors.New("name runs or give --older-than, not both")
	case len(opts.RunIDs) == 0 && opts.OlderThan == "":
		return p, errors.New("name the runs to purge, or give --older-than; wt refs swept lists them")
	}
	runs, err := readSwept(p.Repo)
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
				p.Runs = append(p.Runs, purgeRun{sweptRun: r})
			}
		}
	} else {
		for _, id := range opts.RunIDs {
			i := slices.IndexFunc(runs, func(r sweptRun) bool { return r.RunID == id })
			if i < 0 {
				return p, errNoRun(id)
			}
			if !slices.ContainsFunc(p.Runs, func(r purgeRun) bool { return r.RunID == id }) {
				p.Runs = append(p.Runs, purgeRun{sweptRun: runs[i]})
			}
		}
	}
	return p, p.countLost(ctx)
}

// countLost fills in, for each pin, the commits no ref outside the purge
// and no worktree's HEAD reaches: what git gc may take once it goes. A
// commit two purged pins share counts for both.
func (p *RefsPurgePlan) countLost(ctx *Context) error {
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
	known := map[string]objectInfo{}
	tipOf := map[string]string{}
	var tips []string
	for _, r := range p.Runs {
		for _, s := range r.Refs {
			if tip := peelCommit(p.Repo, s.Object, known); tip != "" {
				tipOf[s.Pin] = tip
				tips = append(tips, tip)
			}
		}
	}
	if len(tips) == 0 {
		return nil
	}
	counts, err := uniqueCommits(p.Repo, tips, keep)
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

// purgeRefsToken names a purge plan: the repository and every pin of every
// run, with its object. Nil when there is nothing to purge.
func purgeRefsToken(p RefsPurgePlan) *string {
	if len(p.Runs) == 0 {
		return nil
	}
	h := sha256.New()
	h.Write([]byte("wt-refs-purge-1\x00" + p.Repo))
	for _, r := range p.Runs {
		h.Write([]byte("\nrun\x00" + r.RunID + "\x00" + r.MetaOID))
		for _, s := range r.Refs {
			h.Write([]byte("\n" + s.Pin + "\x00" + s.Object))
		}
	}
	token := "rp1-" + hex.EncodeToString(h.Sum(nil))[:32]
	return &token
}

var errRefsPurgePlanChanged = errors.New("the purge plan changed since it was read (a pin moved, went or " +
	"was added); nothing was purged: read the plan again")

var errRefsPurgeNoTerminal = errors.New("nothing was purged: there is no terminal to ask, " +
	"and a purge deletes for good: pass --yes")

// RefsPurge deletes, for good, the pins of the runs picked: each only while
// it holds the object the plan read.
func RefsPurge(ctx *Context, opts RefsPurgeOptions, w io.Writer) (err error) {
	j := opts.Journal
	if j != nil {
		defer setInterruptJournal(j)()
		defer watchSignals(w, nil)()
		defer func() {
			j.fail(err)
			j.Finish()
		}()
	}
	plan, err := planRefsPurge(ctx, opts)
	j.planned(plan, nil)
	if err != nil {
		return err
	}
	token := purgeRefsToken(plan)
	j.planned(plan, token)
	if opts.Expect != "" && deref(token) != opts.Expect {
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
		if deref(purgeRefsToken(fresh)) != deref(token) {
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
			if _, there, rerr := refValue(root, s.Pin); rerr == nil && !there {
				res = j.settle(s.Pin, PinAbsent, nil)
			} else {
				derr = deleteRefAt(root, s.Pin, s.Object)
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
				fmt.Fprintf(w, "- kept %s: the run has pins the plan did not list\n", metaRef(r.RunID))
			} else if derr := deleteRefAt(root, metaRef(r.RunID), r.MetaOID); derr != nil {
				left++
				fmt.Fprintf(w, "- kept %s: %s\n", metaRef(r.RunID), git.Reason(derr))
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
	lines, err := git.Lines(root, "for-each-ref", "--format=%(refname)", SweptPrefix+runID+"/")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range lines {
		if l != metaRef(runID) {
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
		_ = printTable(w, rows)
		for _, l := range r.Lost {
			fmt.Fprintf(w, "  lost: %s on %s become unreachable; gc takes them\n", commitsWord(l.Count), l.ID)
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
	RunID       string        `json:"runId"`
	SweptAt     int64         `json:"sweptAt"`
	Meta        *string       `json:"meta"`
	Endpoint    *Endpoint     `json:"endpoint"`
	Refs        []RefPurgePin `json:"refs"`
	Unreachable []RefLost     `json:"unreachable"`
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

func purgePin(s sweptRef) RefPurgePin { return RefPurgePin{Pin: s.Pin, ID: s.ID(), Object: s.Object} }

// RefsPurgePlanJSON writes wt refs purge --dry-run --json. It deletes
// nothing; a plan that cannot be made is one object too.
func RefsPurgePlanJSON(ctx *Context, opts RefsPurgeOptions, out, progress io.Writer) error {
	o := RefsPurgePlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("refs-purge-plan"), Command: refsPurgeCommand,
		OlderThan: strp(opts.OlderThan), Runs: []RefPurgeRunPlan{}}
	var err error
	if ctx == nil {
		err = ErrNotInRepo
	} else {
		var plan RefsPurgePlan
		plan, err = planRefsPurge(ctx, opts)
		o.Repo = strp(plan.Repo)
		if err == nil {
			o.Token = purgeRefsToken(plan)
			plan.Render(progress)
			for _, r := range plan.Runs {
				run := RefPurgeRunPlan{RunID: r.RunID, SweptAt: r.At.Unix(), Refs: []RefPurgePin{},
					Unreachable: append([]RefLost{}, r.Lost...), Endpoint: runEndpoint(r.sweptRun)}
				if r.MetaOID != "" {
					run.Meta = strp(metaRef(r.RunID))
				}
				for _, s := range r.Refs {
					run.Refs = append(run.Refs, purgePin(s))
				}
				o.Runs = append(o.Runs, run)
			}
		}
	}
	if err != nil {
		o.Error = strp(err.Error())
	}
	if werr := writeJSON(out, o); werr != nil && err == nil {
		return werr
	}
	return err
}

// RefsPurgeJournal writes wt refs purge --yes --json's one object once.
type RefsPurgeJournal struct {
	mu       sync.Mutex
	once     sync.Once
	out      io.Writer
	root     string
	res      RefsPurgeResult
	byPin    map[string]*RefPurgePinResult
	inFlight string
	applying bool
}

// NewRefsPurgeJournal is a journal that writes its object to out.
func NewRefsPurgeJournal(out io.Writer) *RefsPurgeJournal {
	return &RefsPurgeJournal{out: out, byPin: map[string]*RefPurgePinResult{},
		res: RefsPurgeResult{Schema: 1, SchemaVersion: schema.VersionOf("refs-purge"), Command: refsPurgeCommand,
			Runs: []RefPurgeRun{}}}
}

func (j *RefsPurgeJournal) planned(p RefsPurgePlan, token *string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.root, j.res.Repo, j.res.Token = p.Repo, strp(p.Repo), token
	j.res.Runs = []RefPurgeRun{}
	for _, r := range p.Runs {
		run := RefPurgeRun{RunID: r.RunID, SweptAt: r.At.Unix(), Refs: []*RefPurgePinResult{}}
		if r.MetaOID != "" {
			run.Meta = strp(metaRef(r.RunID))
		}
		for _, s := range r.Refs {
			it := &RefPurgePinResult{RefPurgePin: purgePin(s), Result: RefNotRun}
			run.Refs = append(run.Refs, it)
			j.byPin[s.Pin] = it
		}
		j.res.Runs = append(j.res.Runs, run)
	}
}

func (j *RefsPurgeJournal) begin() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.applying = true
}

// metaDone reads back whether a run's meta is gone.
func (j *RefsPurgeJournal) metaDone(runID string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	root := j.root
	j.mu.Unlock()
	_, there, err := refValue(root, metaRef(runID))
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := range j.res.Runs {
		if j.res.Runs[i].RunID == runID {
			j.res.Runs[i].MetaDeleted = err == nil && !there
		}
	}
}

func (j *RefsPurgeJournal) start(pin string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inFlight = pin
}

// settle records what became of a pin: absent when it was gone before the
// purge reached it, else read back. The read happens before the lock is
// taken.
func (j *RefsPurgeJournal) settle(pin, result string, err error) string {
	var reason *string
	if result == "" {
		root := ""
		if j != nil {
			j.mu.Lock()
			root = j.root
			j.mu.Unlock()
		}
		result, reason = readPurgePin(root, pin, err)
	}
	if j == nil {
		return result
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inFlight = ""
	if it := j.byPin[pin]; it != nil {
		it.Result, it.Reason = result, reason
	}
	return result
}

// readPurgePin is what became of a pin: deleted when it is gone now, kept
// with why when it is still there or cannot be read.
func readPurgePin(root, pin string, err error) (string, *string) {
	_, ok, rerr := refValue(root, pin)
	switch {
	case rerr != nil:
		return PinKept, strp(oneLine(rerr.Error()))
	case !ok:
		return PinDeleted, nil
	case err != nil:
		return PinKept, strp(git.Reason(err))
	}
	return PinKept, strp("still there")
}

func (j *RefsPurgeJournal) fail(err error) {
	if j == nil || err == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.applying {
		j.res.Error = strp(err.Error())
	}
}

// Fail records an error that stopped the run before it began, and writes
// the object.
func (j *RefsPurgeJournal) Fail(err error) {
	j.fail(err)
	j.Finish()
}

// Finish writes the object for a run that returned.
func (j *RefsPurgeJournal) Finish() { j.write(false, "") }

func (j *RefsPurgeJournal) interrupted(_ string, recovery string) { j.write(true, recovery) }

func (j *RefsPurgeJournal) write(signalled bool, recovery string) {
	if j == nil {
		return
	}
	j.once.Do(func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if signalled {
			if it := j.byPin[j.inFlight]; it != nil {
				it.Result, it.Reason = readPurgePin(j.root, it.Pin, nil)
				if it.Result != PinDeleted {
					it.Result, it.Reason = RefInterrupted, nil
				}
			}
			j.res.Outcome, j.res.Recovery = OutcomeInterrupted, strp(recovery)
		} else {
			done, changed := true, false
			for _, r := range j.res.Runs {
				if r.Meta != nil && !r.MetaDeleted {
					done = false
				}
				for _, it := range r.Refs {
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
			case done && j.res.Error == nil:
				j.res.Outcome = OutcomePurged
			case !changed:
				j.res.Outcome = OutcomeRefused
			default:
				j.res.Outcome = OutcomePartial
			}
		}
		_ = writeJSON(j.out, j.res)
	})
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
	RunID    string        `json:"runId"`
	SweptAt  int64         `json:"sweptAt"`
	Endpoint *Endpoint     `json:"endpoint"`
	Refs     []RefSweptRef `json:"refs"`
}

// runEndpoint is the origin a run's meta names, nil for none.
func runEndpoint(r sweptRun) *Endpoint {
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
func RefsSwept(ctx *Context, asJSON bool, out io.Writer) error {
	o := RefsSweptOutput{Schema: 1, SchemaVersion: schema.VersionOf("refs-swept"), Command: "refs swept",
		Runs: []RefSweptRun{}}
	var runs []sweptRun
	var err error
	if ctx == nil {
		err = ErrNotInRepo
	} else {
		o.Repo = strp(ctx.Repo.MainRoot)
		runs, err = readSwept(ctx.Repo.MainRoot)
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
			o.Error = strp(err.Error())
		}
		if werr := writeJSON(out, o); werr != nil && err == nil {
			return werr
		}
		return err
	}
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Fprintln(out, "No swept refs: nothing under "+SweptPrefix)
		return nil
	}
	for _, r := range runs {
		fmt.Fprintf(out, "%s  swept %s, %s\n", r.RunID, r.At.Local().Format("2006-01-02 15:04"), refCount(len(r.Refs)))
		var rows [][]string
		for _, s := range r.Refs {
			rows = append(rows, []string{"  " + s.ID(), git.ShortID(s.Object, 12)})
		}
		_ = printTable(out, rows)
	}
	fmt.Fprintln(out, "\nwt refs restore <run> puts a run back; wt refs purge <run> deletes it for good")
	return nil
}
