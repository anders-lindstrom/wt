package retire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/schema"
)

// The parts of a sweep plan, for --json.
const (
	// SweepRemove is a merged worktree that goes, and its branch with it.
	SweepRemove = "remove"
	// SweepDelete is a merged branch in no worktree, which goes.
	SweepDelete = "delete"
	// SweepInUse is merged, but its worktree is kept; the item says why.
	SweepInUse = "inUse"
	// SweepUpstreamGone lost its upstream but trunk lacks its commits: kept.
	SweepUpstreamGone = "upstreamGone"
)

// How a branch counts as merged, for --json.
const (
	// MergedAncestor is a base that contains the tip.
	MergedAncestor = "ancestor"
	// MergedPatch is every commit on a base under another id, byte for byte.
	MergedPatch = "patch"
	// MergedPullRequest is a pull request GitHub merged into trunk at the tip.
	MergedPullRequest = "pullRequest"
)

// Why a sweep keeps something, for --json. An item names every one that
// applies.
const (
	KeptDirty                = "dirty"
	KeptStatusUnknown        = "statusUnknown"
	KeptDetached             = "detached"
	KeptSession              = "session"
	KeptSessionsUnknown      = "sessionsUnknown"
	KeptLockHeld             = "lockHeld"
	KeptDirectoryMissing     = "directoryMissing"
	KeptHeldByRebase         = "heldByRebase"
	KeptHeldByBisect         = "heldByBisect"
	KeptMainCheckout         = "mainCheckout"
	KeptBranchNotDeletable   = "branchNotDeletable"
	KeptNotMerged            = "notMerged"
	KeptOperation            = "operation"
	KeptHiddenChanges        = "hiddenChanges"
	KeptHeadUnreachable      = "headUnreachable"
	KeptReachUnknown         = "reachUnknown"
	KeptSubmoduleUnreachable = "submoduleUnreachable"
	KeptNestedWorktree       = "nestedWorktree"
	// KeptProcesses is processes running in the worktree, which a sweep
	// without --reap leaves alone; KeptProcessesUnknown is not being able
	// to tell, which keeps it with --reap too.
	KeptProcesses        = "processes"
	KeptProcessesUnknown = "processesUnknown"
)

// What became of one item of a sweep that ran. The set is exhaustive.
const (
	// SweepRemoved is a worktree removed and its branch deleted.
	SweepRemoved = "removed"
	// SweepDeleted is a branch deleted.
	SweepDeleted = "deleted"
	// SweepKept is left as it was on purpose: the plan kept it, or it
	// changed after the plan was made, or somebody took it meanwhile.
	SweepKept = "kept"
	// SweepFailed is tried and not finished; the item says what did happen.
	SweepFailed = "failed"
	// SweepNotRun is not reached: the run ended before it.
	SweepNotRun = "notRun"
	// SweepInterrupted is the item a signal caught.
	SweepInterrupted = "interrupted"
)

// HeldByCode is the code of what holds a branch in a worktree.
func HeldByCode(by string) string {
	if by == "bisect" {
		return KeptHeldByBisect
	}
	return KeptHeldByRebase
}

// SweepBase is one ref merged is measured against.
type SweepBase struct {
	Name string `json:"name"`
	Tip  string `json:"tip"`
}

// SweepMerged is the evidence that a branch is merged.
type SweepMerged struct {
	How         string `json:"how"`
	Into        string `json:"into"`
	PullRequest *int   `json:"pullRequest"`
}

// SweepPR is what GitHub says about a branch's pull request.
type SweepPR struct {
	Number int     `json:"number"`
	State  string  `json:"state"`
	Base   *string `json:"base"`
	URL    *string `json:"url"`
}

// SweepItem is one row of the plan.
type SweepItem struct {
	Category    string       `json:"category"`
	Branch      *string      `json:"branch"`
	Tip         *string      `json:"tip"`
	Work        *string      `json:"work"`
	Path        *string      `json:"path"`
	Merged      *SweepMerged `json:"merged"`
	PullRequest *SweepPR     `json:"pullRequest"`
	Kept        []string     `json:"kept"`
	Reason      string       `json:"reason"`
	Ahead       *int         `json:"ahead"`
	Subject     *string      `json:"subject"`
	// Processes are the processes working in its worktree: stopped when a
	// remove row goes under --reap, the reason an inUse row is kept
	// without it.
	Processes []RemoveProcess `json:"processes"`
}

// SweepPlanOutput is the one object wt sweep --dry-run --json prints.
type SweepPlanOutput struct {
	Schema        int         `json:"schema"`
	SchemaVersion string      `json:"schemaVersion"`
	Command       string      `json:"command"`
	Repo          *string     `json:"repo"`
	Trunk         *string     `json:"trunk"`
	TrunkSource   *string     `json:"trunkSource"`
	Bases         []SweepBase `json:"bases"`
	Fetched       bool        `json:"fetched"`
	Token         *string     `json:"token"`
	Error         *string     `json:"error"`
	Items         []SweepItem `json:"items"`
	Quarantine    *string     `json:"quarantine"`
	KeepSuperset  bool        `json:"keepSuperset"`
	Reap          bool        `json:"reap"`
	// ConfigFingerprint is the wt configuration the token covers: the
	// SHA-256 of the file, nil when there is none.
	ConfigFingerprint *string `json:"configFingerprint"`
}

// SweepResultItem is what became of one row of the plan.
type SweepResultItem struct {
	Category        string  `json:"category"`
	Branch          *string `json:"branch"`
	Tip             *string `json:"tip"`
	Work            *string `json:"work"`
	Path            *string `json:"path"`
	Result          string  `json:"result"`
	Reason          *string `json:"reason"`
	WorktreeRemoved bool    `json:"worktreeRemoved"`
	BranchDeleted   bool    `json:"branchDeleted"`
	// Pin is where the branch was moved to, read back; nil when it was not.
	Pin            *string  `json:"pin"`
	RestoreCommand []string `json:"restoreCommand"`
	// Quarantine is the folder the worktree went to under --move-to,
	// and which of its two moves are done; nil otherwise.
	Quarantine *SweepQuarantine `json:"quarantine"`
	// Superset is what came of the removed worktree's Superset workspace;
	// nil when no worktree was removed.
	Superset *SweepSuperset `json:"superset"`
	// Reaped is the processes --reap stopped, or tried to, before the
	// worktree went.
	Reaped []ReapedProcess `json:"reaped"`
}

// SweepSuperset is one removed worktree's Superset step.
type SweepSuperset struct {
	// Result is provision.StepDeregistered, provision.StepNotRegistered,
	// kit.StepSkipped or kit.StepFailed.
	Result string  `json:"result"`
	Reason *string `json:"reason"`
}

// SweepQuarantine is one worktree's folder in a sweep's quarantine.
type SweepQuarantine struct {
	Dir           string `json:"dir"`
	CheckoutMoved bool   `json:"checkoutMoved"`
	AdminMoved    bool   `json:"adminMoved"`
}

// SweepResult is the one object wt sweep --yes --json prints.
type SweepResult struct {
	Schema        int                `json:"schema"`
	SchemaVersion string             `json:"schemaVersion"`
	Command       string             `json:"command"`
	Repo          *string            `json:"repo"`
	Trunk         *string            `json:"trunk"`
	Fetched       bool               `json:"fetched"`
	Token         *string            `json:"token"`
	Outcome       string             `json:"outcome"`
	Error         *string            `json:"error"`
	Items         []*SweepResultItem `json:"items"`
	Recovery      *string            `json:"recovery"`
	Quarantine    *string            `json:"quarantine"`
	// RunID is the run under refs/wt-swept/ the deleted branches were
	// moved into; nil when none was.
	RunID *string `json:"runId"`
}

// sweepItem is one row of the plan as --json reports it.
func sweepItem(category string, b SweepBranch, reason string, reap bool) SweepItem {
	it := SweepItem{Category: category, Tip: kit.Strp(b.Tip), Path: kit.Strp(b.Worktree), Reason: reason,
		Kept: append([]string{}, b.KeptCodes...), Subject: kit.Strp(b.Subject),
		Processes: removeProcesses(b.Processes, reap && category == SweepRemove)}
	if !b.Detached {
		it.Branch, it.Work = kit.Strp(b.Name), kit.Strp(b.Work)
	}
	switch {
	case b.MergedInto != "":
		it.Merged = &SweepMerged{How: MergedAncestor, Into: b.MergedInto}
	case b.MergedPR > 0:
		n := b.MergedPR
		it.Merged = &SweepMerged{How: MergedPullRequest, Into: b.GitHub.BaseRefName, PullRequest: &n}
	case b.AppliedTo != "":
		it.Merged = &SweepMerged{How: MergedPatch, Into: b.AppliedTo}
	}
	if pr := b.GitHub; pr != nil {
		it.PullRequest = &SweepPR{Number: pr.Number, State: pr.State, Base: kit.Strp(pr.BaseRefName), URL: kit.Strp(pr.URL)}
	}
	if category == SweepUpstreamGone {
		n := b.Ahead
		it.Ahead = &n
	}
	return it
}

// PlanItemsOf is every row of the plan, in the order wt sweep prints them.
func PlanItemsOf(p SweepPlan) []SweepItem {
	items := []SweepItem{}
	for _, w := range p.Remove {
		items = append(items, sweepItem(SweepRemove, w.SweepBranch, w.removeReason(), p.Reap))
	}
	for _, b := range p.Delete {
		items = append(items, sweepItem(SweepDelete, b, b.why(), p.Reap))
	}
	for _, b := range p.CheckedOut {
		items = append(items, sweepItem(SweepInUse, b, p.checkedOutAdvice(b), p.Reap))
	}
	for _, b := range p.Gone {
		items = append(items, sweepItem(SweepUpstreamGone, b, withPR(aheadOf(b.Ahead, p.Bases[0].Name), b.PR), p.Reap))
	}
	return items
}

// sweepToken names a plan: the repository, trunk, the configuration, and
// every row with what it stands on — its part, branch, commit, worktree,
// evidence, what GitHub says about it, and its reasons, codes and words —
// the quarantine folder the worktrees go to, when there is one, and
// --keep-superset. Any difference in any of them is a different token. Nil
// when the plan has nothing to remove or delete.
func sweepToken(ctx *kit.Context, p SweepPlan, opts SweepOptions) *string {
	return sweepTokenWith(ctx, p, opts, kit.ConfigFingerprint(ctx))
}

// sweepTokenWith is sweepToken over a configuration fingerprint already read.
func sweepTokenWith(ctx *kit.Context, p SweepPlan, opts SweepOptions, fingerprint []byte) *string {
	if p.Empty() {
		return nil
	}
	h := sha256.New()
	h.Write([]byte("wt-sweep-1\x00" + ctx.Repo.MainRoot + "\x00" + ctx.Config.MainBranch + "\x00"))
	h.Write(fingerprint)
	if opts.Quarantine != "" {
		h.Write([]byte("\nquarantine\x00" + opts.Quarantine))
	}
	if opts.KeepSuperset {
		h.Write([]byte("\nkeepSuperset"))
	}
	if opts.Reap {
		h.Write([]byte("\nreap"))
	}
	for _, it := range PlanItemsOf(p) {
		fields := []string{it.Category, Deref(it.Branch), Deref(it.Tip), Deref(it.Path),
			strings.Join(it.Kept, ","), it.Reason}
		if m := it.Merged; m != nil {
			pr := ""
			if m.PullRequest != nil {
				pr = fmt.Sprint(*m.PullRequest)
			}
			fields = append(fields, "merged", m.How, m.Into, pr)
		}
		if pr := it.PullRequest; pr != nil {
			fields = append(fields, "pr", fmt.Sprint(pr.Number), pr.State, Deref(pr.Base))
		}
		// Only what --reap stops is hashed. The processes of a row that is
		// kept are listed, not acted on, and its reason names up to three.
		for _, pr := range it.Processes {
			if pr.Reap {
				fields = append(fields, "process", fmt.Sprint(pr.PID), pr.Command)
			}
		}
		h.Write([]byte("\n" + strings.Join(fields, "\x00")))
	}
	token := "1:" + hex.EncodeToString(h.Sum(nil))[:32]
	return &token
}

// Deref is an optional --json string, empty for none.
func Deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// prepareSweep is a sweep up to its plan: the checks, the fetch, the plan.
// progress gets what the fetch says. fetched reports that origin was
// fetched.
func prepareSweep(ctx *kit.Context, opts SweepOptions, progress io.Writer) (plan SweepPlan, fetched bool, err error) {
	if err := sweepGuard(ctx); err != nil {
		return SweepPlan{}, false, err
	}
	if err := SweepFetch(ctx, opts.NoFetch, progress); err != nil {
		return SweepPlan{}, false, err
	}
	fetched = !opts.NoFetch && ctx.Repo.HasRemote("origin")
	bases, err := TrunkBases(ctx)
	if err != nil {
		return SweepPlan{}, fetched, err
	}
	plan, err = PlanSweep(ctx, bases, func() ([]agents.Agent, error) { return opts.listAgents(ctx) }, opts.pullRequests(ctx), opts.Reap)
	return plan, fetched, err
}

// SweepPlanJSON writes wt sweep --dry-run --json: the plan, every row with
// its evidence or its reasons, and the token that holds a later sweep to it.
// It fetches unless opts.NoFetch, and deletes nothing. A plan that cannot be
// made is still one object, with error set, and the error is returned too.
func SweepPlanJSON(ctx *kit.Context, opts SweepOptions, out, progress io.Writer) error {
	res := SweepPlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("sweep-plan"), Command: "sweep",
		Bases: []SweepBase{}, Items: []SweepItem{}}
	if ctx == nil {
		return writeSweepPlan(out, res, kit.ErrNotInRepo)
	}
	res.Repo, res.Trunk, res.TrunkSource = kit.Strp(ctx.Repo.MainRoot), kit.Strp(ctx.Config.MainBranch), ctx.TrunkSource()
	res.Quarantine, res.KeepSuperset, res.Reap = kit.Strp(opts.Quarantine), opts.KeepSuperset, opts.Reap
	// Read once, for the field and the token alike.
	fingerprint := kit.ConfigFingerprint(ctx)
	res.ConfigFingerprint = kit.Strp(hex.EncodeToString(fingerprint))
	plan, fetched, err := prepareSweep(ctx, opts, progress)
	res.Fetched = fetched
	if err != nil {
		return writeSweepPlan(out, res, err)
	}
	if !plan.Empty() {
		if err := quarantineReady(ctx, opts.Quarantine, plan, opts.QuarantineHooks); err != nil {
			res.Items = PlanItemsOf(plan)
			return writeSweepPlan(out, res, err)
		}
	}
	for _, b := range plan.Bases {
		res.Bases = append(res.Bases, SweepBase(b))
	}
	res.Items, res.Token = PlanItemsOf(plan), sweepTokenWith(ctx, plan, opts, fingerprint)
	return writeSweepPlan(out, res, nil)
}

// SweepPlanFailed writes the plan object for a sweep that could not even
// open the repository.
func SweepPlanFailed(out io.Writer, err error) {
	_ = writeSweepPlan(out, SweepPlanOutput{Schema: 1, SchemaVersion: schema.VersionOf("sweep-plan"),
		Command: "sweep", Bases: []SweepBase{}, Items: []SweepItem{}}, err)
}

func writeSweepPlan(w io.Writer, p SweepPlanOutput, err error) error {
	if err != nil {
		p.Error = kit.Strp(err.Error())
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if werr := enc.Encode(p); werr != nil && err == nil {
		return werr
	}
	return err
}

// SweepJournal collects what a sweep did, item by item, as it does it, so
// that one object is written at the end — by the sweep when it returns, or by
// the signal handler when it does not. Every method is safe from both, and
// the object is written once. A nil journal records nothing.
type SweepJournal struct {
	mu       sync.Mutex
	once     sync.Once
	out      io.Writer
	ctx      *kit.Context
	res      SweepResult
	byKey    map[string]*SweepResultItem
	inFlight string
	applying bool
	runID    string
}

// NewSweepJournal is a journal that writes its object to out.
func NewSweepJournal(out io.Writer) *SweepJournal {
	return &SweepJournal{out: out, byKey: map[string]*SweepResultItem{},
		res: SweepResult{Schema: 1, SchemaVersion: schema.VersionOf("sweep"), Command: "sweep",
			Items: []*SweepResultItem{}}}
}

// sweepKey names one row across the plan and its fresh re-read: a worktree
// by its path, a branch in none by its name.
func sweepKey(b SweepBranch) string {
	if b.Worktree != "" {
		return "worktree:" + b.Worktree
	}
	return "branch:" + b.Name
}

// fetched records that origin was fetched, as soon as it has been.
func (j *SweepJournal) fetched(ok bool) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.Fetched = ok
}

// open records the repository, before anything can fail.
func (j *SweepJournal) open(ctx *kit.Context) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.ctx = ctx
	j.res.Repo, j.res.Trunk = kit.Strp(ctx.Repo.MainRoot), kit.Strp(ctx.Config.MainBranch)
}

// begin records the plan about to be carried out: every row to remove or
// delete starts as not run, which is what a run that stops before reaching
// it leaves it as; every row the plan keeps is kept, with its reason.
func (j *SweepJournal) begin(ctx *kit.Context, p SweepPlan, token string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.ctx, j.res.Token = ctx, kit.Strp(token)
	j.res.Repo, j.res.Trunk = kit.Strp(ctx.Repo.MainRoot), kit.Strp(ctx.Config.MainBranch)
	add := func(key string, it SweepItem) {
		r := &SweepResultItem{Category: it.Category, Branch: it.Branch, Tip: it.Tip, Work: it.Work, Path: it.Path,
			Result: SweepNotRun, Reaped: []ReapedProcess{}}
		if it.Category == SweepInUse || it.Category == SweepUpstreamGone {
			r.Result, r.Reason = SweepKept, kit.Strp(it.Reason)
		}
		j.res.Items = append(j.res.Items, r)
		if key != "" {
			j.byKey[key] = r
		}
	}
	items := PlanItemsOf(p)
	i := 0
	for _, w := range p.Remove {
		add(sweepKey(w.SweepBranch), items[i])
		i++
	}
	for _, b := range p.Delete {
		add(sweepKey(b), items[i])
		i++
	}
	for ; i < len(items); i++ {
		add("", items[i])
	}
}

// quarantineIn records the folder the worktrees go to, "" for none.
func (j *SweepJournal) quarantineIn(dir string) {
	if j == nil || dir == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.Quarantine = kit.Strp(dir)
}

// quarantined records the folder one row's worktree is about to go to.
func (j *SweepJournal) quarantined(key, dir string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if r := j.byKey[key]; r != nil {
		r.Quarantine = &SweepQuarantine{Dir: dir}
	}
}

// superset records what came of a removed worktree's Superset workspace.
func (j *SweepJournal) superset(key string, res RemoveResult) {
	if j == nil || res.Superset == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if r := j.byKey[key]; r != nil {
		r.Superset = &SweepSuperset{Result: res.Superset, Reason: kit.Strp(res.SupersetReason)}
	}
}

// reaped records the processes stopped before a row's worktree went.
func (j *SweepJournal) reaped(key string, reaped []agents.Reaped) {
	if j == nil || len(reaped) == 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if r := j.byKey[key]; r != nil {
		r.Reaped = reapedProcesses(reaped)
	}
}

// ran records the run the branches are moved into: as it is begun, and
// again, "" for none, once a run that pinned nothing is dropped.
func (j *SweepJournal) ran(runID string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.runID = runID
	j.res.RunID = kit.Strp(runID)
}

// startApply marks the point after which an error is an item's, not the
// run's.
func (j *SweepJournal) startApply() {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.applying = true
}

// start marks the row about to be touched: the one a signal would catch.
func (j *SweepJournal) start(key string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inFlight = key
}

// keep records a row left alone because it changed since the plan.
func (j *SweepJournal) keep(key, why string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if r := j.byKey[key]; r != nil {
		r.Result, r.Reason = SweepKept, kit.Strp(why)
	}
}

// settle records what a row came to once it was tried, from the state the
// repository is in now rather than from what was said: whether the worktree
// is gone and whether the branch is.
//
// The state is read before the lock is taken: git run under it would hold
// the lock through a signal, which parks that git's caller for good, and
// the handler could then never write the object.
func (j *SweepJournal) settle(key string, err error) {
	if j == nil {
		return
	}
	j.mu.Lock()
	r, ctx, runID := j.byKey[key], j.ctx, j.runID
	var probe SweepResultItem
	if r != nil {
		probe = *r
	}
	j.mu.Unlock()
	if r == nil {
		return
	}
	readState(ctx, runID, &probe)

	j.mu.Lock()
	defer j.mu.Unlock()
	j.inFlight = ""
	r.WorktreeRemoved, r.BranchDeleted, r.RestoreCommand = probe.WorktreeRemoved, probe.BranchDeleted, probe.RestoreCommand
	r.Pin = probe.Pin
	r.Quarantine = probe.Quarantine
	complete := r.BranchDeleted && (r.Category != SweepRemove || r.WorktreeRemoved)
	switch {
	case err == nil && complete && r.Category == SweepRemove:
		r.Result = SweepRemoved
	case err == nil && complete:
		r.Result = SweepDeleted
	case !r.WorktreeRemoved && !r.BranchDeleted && refusedToDelete(err):
		r.Result, r.Reason = SweepKept, kit.Strp(whyKept(err))
	default:
		r.Result = SweepFailed
		why := "not finished"
		if err != nil {
			why = kit.OneLine(err.Error())
		}
		r.Reason = kit.Strp(why)
	}
}

// refusedToDelete is a delete git or wt turned down on purpose: the branch
// is in use, the worktrees could not be read, or it moved off its tip.
func refusedToDelete(err error) bool {
	var inUse *repo.BranchInUseError
	return errors.As(err, &inUse) || errors.Is(err, repo.ErrWorktreesUnknown) ||
		(err != nil && strings.Contains(err.Error(), "moved after the plan was made"))
}

// readState fills in what is true of a row now, from the repository: its
// branch's pin is looked for in run.
func readState(ctx *kit.Context, runID string, r *SweepResultItem) {
	if ctx == nil {
		return
	}
	if r.Category == SweepRemove && r.Path != nil {
		r.WorktreeRemoved = worktreeGone(ctx, *r.Path)
	}
	if q := r.Quarantine; q != nil {
		moved := *q
		_, cerr := os.Lstat(filepath.Join(q.Dir, "checkout"))
		_, aerr := os.Lstat(filepath.Join(q.Dir, "admin"))
		moved.CheckoutMoved, moved.AdminMoved = cerr == nil, aerr == nil
		r.Quarantine = &moved
	}
	if r.Branch != nil && r.Tip != nil {
		// A branch git cannot read is not called deleted, but its pin is
		// still looked for: if it is gone, the pin is where its tip is held.
		_, ok, err := ctx.Repo.ResolveRef("refs/heads/" + *r.Branch)
		r.BranchDeleted = !ok && err == nil
		r.Pin, r.RestoreCommand = nil, nil
		pin := ""
		if !ok {
			pin = refpins.PinHolding(ctx.Repo.MainRoot, runID, *r.Branch, *r.Tip)
		}
		switch {
		case ok:
		case pin != "":
			r.Pin, r.RestoreCommand = kit.Strp(pin), refpins.RestoreFromRun(runID, *r.Branch)
		case r.Quarantine != nil:
			// The quarantine pins the branch, and puts it back with the
			// worktree.
			r.RestoreCommand = []string{"wt", "restore", r.Quarantine.Dir}
		}
	}
}

// fail records an error that ended the run before anything was touched.
func (j *SweepJournal) fail(err error) {
	if j == nil || err == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.applying {
		j.res.Error = kit.Strp(err.Error())
	}
}

// Fail records an error that stopped the run before it began, and writes
// the object: what the caller does when it cannot even open the repository.
func (j *SweepJournal) Fail(err error) {
	j.fail(err)
	j.Finish()
}

// Finish writes the object for a run that returned.
func (j *SweepJournal) Finish() {
	j.write(false, "")
}

// Interrupted writes the object for a run a signal ended: the row in flight
// is interrupted, with what is true of it now, the rest stand as recorded.
// inFlight is the rebase tracker's, which a sweep has none of.
func (j *SweepJournal) Interrupted(_ string, recovery string) {
	j.write(true, recovery)
}

func (j *SweepJournal) write(signalled bool, recovery string) {
	if j == nil {
		return
	}
	j.once.Do(func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if signalled {
			// The handler's own git is not parked, and nothing else holds
			// the lock while running one.
			if r := j.byKey[j.inFlight]; r != nil {
				readState(j.ctx, j.runID, r)
				r.Result = SweepInterrupted
			}
			j.res.Outcome, j.res.Recovery = kit.OutcomeInterrupted, kit.Strp(recovery)
		} else {
			j.res.Outcome = sweepOutcome(j.res.Items, j.res.Error != nil)
		}
		enc := json.NewEncoder(j.out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(j.res)
	})
}

// sweepOutcome is the rule: done when every row to remove or delete went
// and nothing stopped the run first; refused when nothing was removed or
// deleted; partial otherwise.
func sweepOutcome(items []*SweepResultItem, failedEarly bool) string {
	finished, changed := true, false
	for _, r := range items {
		if r.Result == SweepRemoved || r.Result == SweepDeleted || r.WorktreeRemoved || r.BranchDeleted {
			changed = true
		}
		if (r.Category == SweepRemove || r.Category == SweepDelete) && r.Result != SweepRemoved && r.Result != SweepDeleted {
			finished = false
		}
	}
	switch {
	case finished && !failedEarly:
		return kit.OutcomeDone
	case !changed:
		return kit.OutcomeRefused
	}
	return kit.OutcomePartial
}
