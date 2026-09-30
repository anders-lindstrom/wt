package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/schema"
)

// What a restore does with one pin, decided from what is at its name now.
const (
	// RestoreCreate: the name is free; the ref is made at the pinned object.
	RestoreCreate = "create"
	// RestoreNone: the ref is there at the pinned object already; only the
	// pin goes.
	RestoreNone = "none"
	// RestoreOccupied: the name holds another object here.
	RestoreOccupied = "occupied"
	// RestoreRemoteOccupied: origin has something at the name.
	RestoreRemoteOccupied = "remoteOccupied"
	// RestorePinGone: the pin is not there any more.
	RestorePinGone = "pinGone"
	// RestoreEndpointChanged: a remote ref, and origin now resolves to other
	// URLs than the ones the run deleted it from.
	RestoreEndpointChanged = "endpointChanged"
	// RestoreEndpointUnknown: a remote ref, and the run has no meta saying
	// which origin it deleted it from.
	RestoreEndpointUnknown = "endpointUnknown"
)

// RefRestored is a row of a restore that ran whose ref is back and whose pin
// is gone.
const RefRestored = "restored"

// restoreRow is one pin of the run and what a restore makes of it.
type restoreRow struct {
	sweptRef
	Action   string
	Selected bool
}

// RefsRestorePlan is what wt refs restore <runId> would put back.
type RefsRestorePlan struct {
	Repo, RunID string
	Rows        []restoreRow
	// endpoint is origin as resolved now, when the run has remote pins and
	// origin resolves to one clean URL; every remote call goes to it.
	endpoint *Endpoint
}

// RefsRestoreOptions carries what wt refs restore was asked.
type RefsRestoreOptions struct {
	Only    []string
	DryRun  bool
	Yes     bool
	Confirm func(RefsRestorePlan) bool
	Expect  string
	Journal *RefsRestoreJournal
}

// errNoRun is a run id no pins are under.
func errNoRun(runID string) error {
	return fmt.Errorf("no swept run %s here: wt refs swept lists the runs", runID)
}

// planRefsRestore reads the run's pins and, for each, what is at its name
// now: here, and on origin for the remote ones, asked once.
func planRefsRestore(ctx *Context, runID string) (RefsRestorePlan, error) {
	p := RefsRestorePlan{Repo: ctx.Repo.MainRoot, RunID: runID}
	if !runIDPattern.MatchString(runID) {
		return p, fmt.Errorf("%q is not a run id; they look like 20260930T091500Z-3f2a", runID)
	}
	runs, err := readSwept(p.Repo)
	if err != nil {
		return p, err
	}
	i := slices.IndexFunc(runs, func(r sweptRun) bool { return r.RunID == runID })
	if i < 0 {
		return p, errNoRun(runID)
	}
	run := runs[i]
	var origin *remoteRefs
	var today *Endpoint
	var todayErr error
	resolved := false
	for _, s := range run.Refs {
		row := restoreRow{sweptRef: s}
		if remoteKind(s.Kind) && !resolved {
			today, todayErr = resolveEndpoint(p.Repo)
			resolved = true
			if todayErr == nil {
				p.endpoint = today
			}
		}
		switch {
		case !remoteKind(s.Kind):
		case run.Meta == nil || run.Meta.Endpoint == nil:
			row.Action = RestoreEndpointUnknown
		case todayErr != nil || today.Digest != run.Meta.Endpoint.Digest:
			row.Action = RestoreEndpointChanged
		}
		if row.Action != "" {
			p.Rows = append(p.Rows, row)
			continue
		}
		if remoteKind(s.Kind) {
			if origin == nil {
				rr, err := readRemoteRefs(p.Repo, today.raw)
				if err != nil {
					return p, err
				}
				origin = &rr
			}
			switch now, ok := origin.Values[s.Ref()]; {
			case !ok:
				row.Action = RestoreCreate
			case now == s.Object:
				row.Action = RestoreNone
			default:
				row.Action = RestoreRemoteOccupied
			}
		} else {
			switch now, ok, err := refValue(p.Repo, s.Ref()); {
			case err != nil:
				return p, err
			case !ok:
				row.Action = RestoreCreate
			case now == s.Object:
				row.Action = RestoreNone
			default:
				row.Action = RestoreOccupied
			}
		}
		row.Selected = row.restorable()
		p.Rows = append(p.Rows, row)
	}
	return p, nil
}

func (r restoreRow) restorable() bool { return r.Action == RestoreCreate || r.Action == RestoreNone }

// applyOnly replaces the default selection. An id the run does not have,
// or one whose name is taken, refuses the lot.
func (p *RefsRestorePlan) applyOnly(only []string) error {
	if only == nil {
		return nil
	}
	want := map[string]bool{}
	for _, id := range only {
		want[id] = true
	}
	var unknown, taken []string
	for id := range want {
		i := slices.IndexFunc(p.Rows, func(r restoreRow) bool { return r.ID() == id })
		switch {
		case i < 0:
			unknown = append(unknown, id)
		case !p.Rows[i].restorable():
			taken = append(taken, id+" ("+p.Rows[i].Action+")")
		}
	}
	sort.Strings(unknown)
	sort.Strings(taken)
	switch {
	case len(unknown) > 0:
		return fmt.Errorf("nothing was restored: --only names what run %s does not have: %s",
			p.RunID, strings.Join(unknown, ", "))
	case len(taken) > 0:
		return fmt.Errorf("nothing was restored: --only names what cannot go back: %s", strings.Join(taken, ", "))
	}
	for i := range p.Rows {
		p.Rows[i].Selected = want[p.Rows[i].ID()]
	}
	return nil
}

// restoreToken names a restore plan: the repository, the run, and every pin
// with its object and what would be done with it. Nil when nothing can go
// back.
func restoreToken(p RefsRestorePlan) *string {
	if !slices.ContainsFunc(p.Rows, restoreRow.restorable) {
		return nil
	}
	h := sha256.New()
	h.Write([]byte("wt-refs-restore-1\x00" + p.Repo + "\x00" + p.RunID))
	for _, r := range p.Rows {
		h.Write([]byte("\n" + strings.Join([]string{r.ID(), r.Pin, r.Object, r.Action}, "\x00")))
	}
	token := "rr1-" + hex.EncodeToString(h.Sum(nil))[:32]
	return &token
}

var errRestorePlanChanged = errors.New("the restore plan changed since it was read (a pin went, or a name " +
	"was taken or freed); nothing was restored: read the plan again")

// RefsRestore puts back what a ref sweep moved: each ref is made again at
// its pinned object, only where its name is free, and the pin goes with it.
func RefsRestore(ctx *Context, runID string, opts RefsRestoreOptions, w io.Writer) (err error) {
	j := opts.Journal
	if j != nil {
		defer setInterruptJournal(j)()
		defer watchSignals(w, nil)()
		defer func() {
			j.fail(err)
			j.Finish()
		}()
	}
	plan, err := planRefsRestore(ctx, runID)
	j.planned(plan, nil)
	if err != nil {
		return err
	}
	token := restoreToken(plan)
	j.planned(plan, token)
	if opts.Expect != "" && deref(token) != opts.Expect {
		return errRestorePlanChanged
	}
	if err := plan.applyOnly(opts.Only); err != nil {
		return err
	}
	j.planned(plan, token)
	plan.Render(w)
	n := 0
	for _, r := range plan.Rows {
		if r.Selected {
			n++
		}
	}
	switch {
	case n == 0:
		fmt.Fprintln(w, "Nothing to restore.")
		return nil
	case opts.DryRun:
		fmt.Fprintln(w, "Nothing was restored: --dry-run.")
		return nil
	case opts.Yes:
	case opts.Confirm != nil:
		if !opts.Confirm(plan) {
			fmt.Fprintln(w, "Nothing was restored.")
			return nil
		}
		fresh, err := planRefsRestore(ctx, runID)
		if err != nil {
			return err
		}
		if deref(restoreToken(fresh)) != deref(token) {
			return errRestorePlanChanged
		}
	default:
		fmt.Fprintf(w, "Nothing was restored: there is no terminal to ask. Pass --yes to restore %s.\n", refCount(n))
		return nil
	}
	// Origin is read again right before anything goes to it.
	if plan.endpoint != nil {
		now, err := resolveEndpoint(ctx.Repo.MainRoot)
		if err != nil || now.Digest != plan.endpoint.Digest {
			return errors.New("nothing was restored: origin's URL changed since the plan was made; read the plan again")
		}
	}
	j.begin(plan.url())
	root := ctx.Repo.MainRoot
	failed := 0
	for _, r := range plan.Rows {
		if !r.Selected {
			continue
		}
		j.start(r.ID())
		st := restoreRef(root, plan.url(), r)
		j.settle(r.ID(), st)
		switch {
		case st.kept != "":
			fmt.Fprintf(w, "- kept %s: %s\n", r.ID(), st.kept)
			failed++
		case st.err != nil || !st.restored || !st.pinDeleted:
			why := "not finished"
			if st.err != nil {
				why = oneLine(st.err.Error())
			}
			fmt.Fprintf(w, "! %s: %s\n", r.ID(), why)
			failed++
		default:
			fmt.Fprintf(w, "✓ restored %s\n", r.ID())
		}
	}
	dropEmptyRun(root, runID, w)
	if failed > 0 {
		return fmt.Errorf("%d of %s kept or not finished", failed, refCount(n))
	}
	return nil
}

// dropEmptyRun deletes a run's meta once it has no pin left, so a run
// restored whole leaves nothing under refs/wt-swept/<runId>/.
func dropEmptyRun(root, runID string, w io.Writer) {
	runs, err := readSwept(root)
	if err != nil {
		return
	}
	for _, r := range runs {
		if r.RunID == runID && len(r.Refs) == 0 && r.MetaOID != "" {
			if err := deleteRefAt(root, metaRef(runID), r.MetaOID); err != nil {
				fmt.Fprintf(w, "! could not drop %s: %s\n", metaRef(runID), git.Reason(err))
			}
		}
	}
}

// restoreState is what one row came to, read back.
type restoreState struct {
	kept                 string
	err                  error
	restored, pinDeleted bool
}

// restoreRef puts one ref back. Here that is one transaction: the ref made
// only where it is not, and the pin deleted only at its object. On origin
// it is a push leased on the ref being absent, then the pin.
func restoreRef(root, url string, r restoreRow) restoreState {
	now, ok, err := refValue(root, r.Pin)
	switch {
	case err != nil:
		return restoreState{err: err}
	case !ok || now != r.Object:
		return restoreState{kept: RestorePinGone + ": the pin moved or went after the plan was made"}
	}
	var opErr error
	switch {
	case r.Action == RestoreNone && remoteKind(r.Kind):
		// Only while origin is read back holding it does the pin go.
		switch v, there, verr := remoteValue(root, url, r.Ref(), networkTimeout); {
		case verr != nil:
			return restoreState{err: fmt.Errorf("origin could not be read, so the pin stays: %w", verr)}
		case !there || v != r.Object:
			return restoreState{kept: "origin no longer holds it at the pinned object, so the pin stays"}
		}
		opErr = deleteRefAt(root, r.Pin, now)
	case r.Action == RestoreNone:
		// The pin goes only in the same transaction that sees the ref at it.
		opErr = updateRefs(root, "wt refs restore", "verify "+r.Ref()+" "+r.Object, "delete "+r.Pin+" "+r.Object)
		if opErr != nil {
			if v, there, _ := refValue(root, r.Ref()); !there || v != r.Object {
				return restoreState{kept: "it moved or went after the plan was made, so the pin stays"}
			}
		}
	case remoteKind(r.Kind):
		// The pin goes only once origin is read back holding the object: a
		// push that was rejected, timed out or cannot be confirmed keeps it,
		// and a later restore finds the ref there and only drops the pin.
		_, pushErr := git.RunTimeout(root, networkTimeout, "push", "--quiet", "--no-verify",
			"--force-with-lease="+r.Ref()+":", url, r.Pin+":"+r.Ref())
		v, there, verr := remoteValue(root, url, r.Ref(), networkTimeout)
		switch {
		case pushErr != nil:
			return restoreState{restored: verr == nil && there && v == r.Object,
				err: fmt.Errorf("the push failed, so the pin stays: %s", git.Reason(pushErr))}
		case verr != nil:
			return restoreState{err: fmt.Errorf("pushed, but origin could not be read back, so the pin stays: %w", verr)}
		case !there || v != r.Object:
			return restoreState{err: errors.New("pushed, but origin does not hold it, so the pin stays")}
		}
		opErr = deleteRefAt(root, r.Pin, now)
	default:
		opErr = updateRefs(root, "wt refs restore", "create "+r.Ref()+" "+r.Object, "delete "+r.Pin+" "+r.Object)
		if opErr != nil {
			if v, there, _ := refValue(root, r.Ref()); there && v != r.Object {
				return restoreState{kept: "its name was taken after the plan was made"}
			}
		}
	}
	st := readRestoreState(root, url, r, networkTimeout)
	st.err = opErr
	return st
}

// url is origin's URL for the remote rows; "" when the run has none.
func (p RefsRestorePlan) url() string {
	if p.endpoint == nil {
		return ""
	}
	return p.endpoint.raw
}

// readRestoreState reads back one row: the ref at its object, and the pin.
func readRestoreState(root, url string, r restoreRow, timeout time.Duration) restoreState {
	var st restoreState
	if remoteKind(r.Kind) {
		if v, ok, err := remoteValue(root, url, r.Ref(), timeout); err == nil && ok && v == r.Object {
			st.restored = true
		}
	} else if v, ok, err := refValue(root, r.Ref()); err == nil && ok && v == r.Object {
		st.restored = true
	}
	if _, ok, err := refValue(root, r.Pin); err == nil && !ok {
		st.pinDeleted = true
	}
	return st
}

// Render writes what the restore would do.
func (p RefsRestorePlan) Render(w io.Writer) {
	var rows [][]string
	for _, r := range p.Rows {
		what := map[string]string{RestoreCreate: "goes back", RestoreNone: "is back already; the pin goes",
			RestoreOccupied:        "kept: its name holds another commit here",
			RestoreRemoteOccupied:  "kept: origin has something at its name",
			RestoreEndpointChanged: "kept: origin is not the one it was swept from",
			RestoreEndpointUnknown: "kept: the run does not say which origin it came from"}[r.Action]
		if !r.Selected && r.restorable() {
			what = "not selected"
		}
		rows = append(rows, []string{"  " + r.ID(), git.ShortID(r.Object, 12), what})
	}
	fmt.Fprintf(w, "Run %s:\n", p.RunID)
	_ = printTable(w, rows)
	fmt.Fprintln(w)
}

// RefRestorePlanItem is one pin of a restore plan, for --json.
type RefRestorePlanItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Pin      string `json:"pin"`
	Object   string `json:"object"`
	Action   string `json:"action"`
	Selected bool   `json:"selected"`
}

// RefsRestorePlanOutput is the one object wt refs restore --dry-run --json
// prints.
type RefsRestorePlanOutput struct {
	Schema        int                  `json:"schema"`
	SchemaVersion string               `json:"schemaVersion"`
	Command       string               `json:"command"`
	Repo          *string              `json:"repo"`
	RunID         string               `json:"runId"`
	Token         *string              `json:"token"`
	Error         *string              `json:"error"`
	Items         []RefRestorePlanItem `json:"items"`
}

// RefRestoreItem is what became of one pin, for --json.
type RefRestoreItem struct {
	RefRestorePlanItem
	Result     string  `json:"result"`
	Reason     *string `json:"reason"`
	Restored   bool    `json:"restored"`
	PinDeleted bool    `json:"pinDeleted"`
}

// RefsRestoreResult is the one object wt refs restore --yes --json prints.
type RefsRestoreResult struct {
	Schema        int               `json:"schema"`
	SchemaVersion string            `json:"schemaVersion"`
	Command       string            `json:"command"`
	Repo          *string           `json:"repo"`
	RunID         string            `json:"runId"`
	Token         *string           `json:"token"`
	Outcome       string            `json:"outcome"`
	Error         *string           `json:"error"`
	Recovery      *string           `json:"recovery"`
	Items         []*RefRestoreItem `json:"items"`
}

const refsRestoreCommand = "refs restore"

func restorePlanItem(r restoreRow) RefRestorePlanItem {
	return RefRestorePlanItem{ID: r.ID(), Kind: r.Kind, Name: r.Name, Pin: r.Pin, Object: r.Object,
		Action: r.Action, Selected: r.Selected}
}

// RefsRestorePlanJSON writes wt refs restore <runId> --dry-run --json. It
// changes nothing; a plan that cannot be made is one object too.
func RefsRestorePlanJSON(ctx *Context, runID string, only []string, out, progress io.Writer) error {
	o := RefsRestorePlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("refs-restore-plan"),
		Command: refsRestoreCommand, RunID: runID, Items: []RefRestorePlanItem{}}
	var err error
	if ctx == nil {
		err = ErrNotInRepo
	} else {
		var plan RefsRestorePlan
		plan, err = planRefsRestore(ctx, runID)
		o.Repo = strp(plan.Repo)
		if err == nil {
			o.Token = restoreToken(plan)
			if err = plan.applyOnly(only); err != nil {
				o.Token = nil
			}
			plan.Render(progress)
		}
		for _, r := range plan.Rows {
			o.Items = append(o.Items, restorePlanItem(r))
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

// RefsRestoreJournal writes wt refs restore --yes --json's one object once:
// when the restore returns, or when a signal ends it.
type RefsRestoreJournal struct {
	mu       sync.Mutex
	once     sync.Once
	out      io.Writer
	root     string
	url      string
	res      RefsRestoreResult
	rows     map[string]restoreRow
	byID     map[string]*RefRestoreItem
	inFlight string
	applying bool
}

// NewRefsRestoreJournal is a journal that writes its object to out.
func NewRefsRestoreJournal(out io.Writer, runID string) *RefsRestoreJournal {
	return &RefsRestoreJournal{out: out, rows: map[string]restoreRow{}, byID: map[string]*RefRestoreItem{},
		res: RefsRestoreResult{Schema: 1, SchemaVersion: schema.VersionOf("refs-restore"), Command: refsRestoreCommand,
			RunID: runID, Items: []*RefRestoreItem{}}}
}

func (j *RefsRestoreJournal) planned(p RefsRestorePlan, token *string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.root, j.res.Repo, j.res.Token = p.Repo, strp(p.Repo), token
	j.res.Items = []*RefRestoreItem{}
	for _, r := range p.Rows {
		it := &RefRestoreItem{RefRestorePlanItem: restorePlanItem(r), Result: RefNotRun}
		switch {
		case !r.restorable():
			it.Result, it.Reason = RefResultKept, strp(r.Action)
		case !r.Selected:
			it.Result = RefNotSelected
		}
		j.res.Items = append(j.res.Items, it)
		j.byID[r.ID()], j.rows[r.ID()] = it, r
	}
}

func (j *RefsRestoreJournal) begin(url string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.applying, j.url = true, url
}

func (j *RefsRestoreJournal) start(id string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inFlight = id
}

func (j *RefsRestoreJournal) settle(id string, st restoreState) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inFlight = ""
	if it := j.byID[id]; it != nil {
		settleRestoreItem(it, st)
	}
}

func settleRestoreItem(it *RefRestoreItem, st restoreState) {
	it.Restored, it.PinDeleted = st.restored, st.pinDeleted
	switch {
	case st.kept != "":
		it.Result, it.Reason = RefResultKept, strp(st.kept)
	case st.err == nil && st.restored && st.pinDeleted:
		it.Result, it.Reason = RefRestored, nil
	default:
		it.Result = RefFailed
		why := "not finished"
		if st.err != nil {
			why = oneLine(st.err.Error())
		}
		it.Reason = strp(why)
	}
}

func (j *RefsRestoreJournal) fail(err error) {
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
func (j *RefsRestoreJournal) Fail(err error) {
	j.fail(err)
	j.Finish()
}

// Finish writes the object for a run that returned.
func (j *RefsRestoreJournal) Finish() { j.write(false, "") }

func (j *RefsRestoreJournal) interrupted(_ string, recovery string) { j.write(true, recovery) }

func (j *RefsRestoreJournal) write(signalled bool, recovery string) {
	if j == nil {
		return
	}
	j.once.Do(func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if signalled {
			if it := j.byID[j.inFlight]; it != nil {
				settleRestoreItem(it, readRestoreState(j.root, j.url, j.rows[j.inFlight], 10*time.Second))
				it.Result, it.Reason = RefInterrupted, nil
			}
			j.res.Outcome, j.res.Recovery = OutcomeInterrupted, strp(recovery)
		} else {
			finished, changed := true, false
			for _, it := range j.res.Items {
				if it.PinDeleted || it.Result == RefRestored {
					changed = true
				}
				if it.Selected && it.Result != RefRestored {
					finished = false
				}
			}
			switch {
			case finished && j.res.Error == nil:
				j.res.Outcome = OutcomeDone
			case !changed:
				j.res.Outcome = OutcomeRefused
			default:
				j.res.Outcome = OutcomePartial
			}
		}
		_ = writeJSON(j.out, j.res)
	})
}
