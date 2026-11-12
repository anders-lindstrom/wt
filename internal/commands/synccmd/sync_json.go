package synccmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/jsonout"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/reposel"
	"github.com/anders-lindstrom/wt/internal/wtsync"
	"github.com/anders-lindstrom/wt/schema"
)

// The groups wt sync --json files a worktree under: the overview's three,
// and current, which the human overview leaves out.
const (
	GroupReady    = "ready"
	GroupNeedsYou = "needsYou"
	GroupSkipped  = "skipped"
	GroupCurrent  = "current"
)

// What wt sync rebase <work> would do with a worktree, wtsync.Preflight's
// verdict.
const (
	VerdictProceed = "proceed"
	VerdictSkip    = "skip"
	VerdictRefuse  = "refuse"
)

// SyncOverview is the one object wt sync --json prints for a repository:
// every worktree, filed as the overview files it.
type SyncOverview struct {
	Schema        int     `json:"schema"`
	SchemaVersion string  `json:"schemaVersion"`
	Command       string  `json:"command"`
	Repo          string  `json:"repo"`
	Name          string  `json:"name"`
	Trunk         *string `json:"trunk"`
	TrunkSource   *string `json:"trunkSource"`
	TrunkRef      *string `json:"trunkRef"`
	Onto          *string `json:"onto"`
	// Target is what onto is: trunk, or the ref --onto names.
	Target        *syncrun.RunTarget `json:"target"`
	Fetched       bool               `json:"fetched"`
	FetchError    *string            `json:"fetchError"`
	Declared      bool               `json:"declared"`
	Token         *string            `json:"token"`
	Error         *string            `json:"error"`
	SessionsError *string            `json:"sessionsError"`
	// EachCommitDeclared is the each_commit checks trunk declares.
	EachCommitDeclared []DeclaredStep `json:"eachCommitDeclared"`
	// Needs is every need a declared step has, and what its probe said.
	Needs     []NeedProbe        `json:"needs"`
	Worktrees []OverviewWorktree `json:"worktrees"`
	// Stack is the stack held on the line of the worktree --stack named,
	// null without --stack.
	Stack *syncrun.Stack `json:"stack"`
}

// NeedProbe is one need and its probe's answer: probed only when a step
// with the need would run for some worktree of the overview.
type NeedProbe struct {
	Need   string  `json:"need"`
	Probed bool    `json:"probed"`
	OK     bool    `json:"ok"`
	Error  *string `json:"error"`
}

// CheckCommit is one commit a run would check.
type CheckCommit struct {
	Commit  string `json:"commit"`
	Subject string `json:"subject"`
}

// OverviewWorktree is one worktree of the overview.
type OverviewWorktree struct {
	Work       string            `json:"work"`
	Branch     *string           `json:"branch"`
	Path       string            `json:"path"`
	Group      string            `json:"group"`
	Class      string            `json:"class"`
	Verified   bool              `json:"verified"`
	Verdict    string            `json:"verdict"`
	Runnable   bool              `json:"runnable"`
	Reason     *string           `json:"reason"`
	Behind     int               `json:"behind"`
	Ahead      int               `json:"ahead"`
	Dirty      bool              `json:"dirty"`
	HandedOver bool              `json:"handedOver"`
	PlanFile   *string           `json:"planFile"`
	Sessions   []PlanSession     `json:"sessions"`
	Stack      []PlanStackMember `json:"stack"`
	Stops      []OverviewStop    `json:"stops"`
	Strategies []string          `json:"strategies"`
	// DeferredDeclared is the repository's, the same on every worktree.
	DeferredDeclared []DeclaredStep `json:"deferredDeclared"`
	// Checks is how many commits a run would check, CheckCommits which.
	Checks       int               `json:"checks"`
	CheckCommits []CheckCommit     `json:"checkCommits"`
	Notes        []string          `json:"notes"`
	Error        *string           `json:"error"`
	OwnRemote    syncrun.OwnRemote `json:"ownRemote"`
}

// DeclaredStep is one deferred step .wt-sync.yaml on trunk declares, as a
// plan reports it before any rebase: Step is its run line, the string a
// result's deferred[].step carries, and Paths its gate. A step with no paths
// runs after every finished rebase; one with paths only when the rebase
// changed one of them, which nothing knows until the rebase is done.
type DeclaredStep struct {
	Step  string   `json:"step"`
	Paths []string `json:"paths"`
	Needs []string `json:"needs"`
}

// declaredSteps is cfg's deferred steps for --json, in the declared order;
// none for a trunk that declares nothing, which a nil cfg is.
func declaredSteps(cfg *wtsync.Config) []DeclaredStep {
	steps := []DeclaredStep{}
	if cfg == nil {
		return steps
	}
	for _, d := range cfg.Defer {
		steps = append(steps, DeclaredStep{Step: d.Run, Paths: append([]string{}, d.Paths...), Needs: append([]string{}, d.Needs...)})
	}
	return steps
}

// OverviewStop is one stop the simulated rebase reached.
type OverviewStop struct {
	Index    int            `json:"index"`
	Total    int            `json:"total"`
	Commit   *string        `json:"commit"`
	Subject  string         `json:"subject"`
	Resolved bool           `json:"resolved"`
	Yours    bool           `json:"yours"`
	Files    []OverviewFile `json:"files"`
}

// OverviewFile is one conflicted file at a stop.
type OverviewFile struct {
	Path     string  `json:"path"`
	Resolved bool    `json:"resolved"`
	Strategy *string `json:"strategy"`
	Note     *string `json:"note"`
}

// SyncJSON writes wt sync --json: the overview of one repository as one
// object. Like the overview it fetches trunk unless opts.NoFetch, and
// writes nothing else. A trunk that is not there is the object's error, and
// the returned one.
func SyncJSON(ctx *kit.Context, opts SyncOptions, w io.Writer) error {
	defer syncrun.WatchSignals(os.Stderr, nil)()
	o := OverviewOf(ctx, opts)
	if err := jsonout.WriteJSON(w, o); err != nil {
		return err
	}
	if o.Error != nil {
		return fmt.Errorf("%s", *o.Error)
	}
	return nil
}

// SyncAllJSON writes wt sync --json with --all, --roots or --profile: an
// array of one overview object per repository, in the selection's order. A
// repository that could not be read is an object with its error, and the
// returned error names every one.
func SyncAllJSON(u *config.User, sel reposel.Selection, opts SyncOptions, w io.Writer) error {
	set, err := reposel.Select(u, sel)
	if err != nil {
		return err
	}
	defer syncrun.WatchSignals(os.Stderr, nil)()
	out := reposel.Each(set.Repos, reposel.Parallelism, func(t reposel.Target) SyncOverview {
		if t.Problem == "" {
			ctx, err := kit.Open(t.Path, io.Discard)
			if err == nil {
				return OverviewOf(ctx, opts)
			}
			t.Problem = err.Error()
		}
		o := newOverview(t.Name, t.Path)
		o.Error = jsonout.Strp(t.Problem)
		return o
	})
	if out == nil {
		out = []SyncOverview{}
	}
	if err := jsonout.WriteJSON(w, out); err != nil {
		return err
	}
	var failed []string
	for _, o := range out {
		if o.Error != nil {
			failed = append(failed, o.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %s failed: %s", len(failed), kit.Count(len(out), "repository", "repositories"), strings.Join(failed, ", "))
	}
	return nil
}

func newOverview(name, mainRoot string) SyncOverview {
	return SyncOverview{Schema: 1, SchemaVersion: schema.VersionOf("sync"), Command: "sync",
		Repo: mainRoot, Name: name, EachCommitDeclared: []DeclaredStep{}, Needs: []NeedProbe{}, Worktrees: []OverviewWorktree{}, Stack: nil}
}

// OverviewOf is what wt sync would print for ctx's repository, as data.
func OverviewOf(ctx *kit.Context, opts SyncOptions) SyncOverview {
	o := newOverview(ctx.Repo.Name, ctx.Repo.MainRoot)
	if err := ctx.NoTrunk(); err != nil {
		o.Error = jsonout.Strp(err.Error())
		return o
	}
	trunk := ctx.Config.MainBranch
	o.Trunk, o.TrunkRef, o.TrunkSource = jsonout.Strp(trunk), jsonout.Strp("origin/"+trunk), ctx.TrunkSource()
	if opts.Upstream != "" && opts.Onto == "" {
		o.Error = jsonout.Strp("--upstream needs --onto: it says where the commits rebased onto --onto's ref start")
		return o
	}
	// Trunk, and with it the own remote of every worktree's branch. One
	// that could not be fetched is as last fetched, which its
	// ownRemote.fetched says, and its row says why.
	var own *wtsync.Own
	if opts.NoFetch {
		own = wtsync.ReadOwnOrUnknown(ctx.Repo.MainRoot, trunk)
	} else {
		var branches []string
		if all, err := ctx.Repo.Worktrees(); err == nil {
			branches = syncrun.WorktreeBranches(all)
		}
		var err error
		if own, err = syncrun.FetchRemotes(ctx, syncFetchTimeout, branches); err != nil {
			o.FetchError = jsonout.Strp(kit.FetchReason(err))
		} else {
			o.Fetched = true
		}
	}
	// One trunk commit for the whole overview, the declaration read from it
	// too, as a run reads it: a fetch landing meanwhile cannot mix two.
	_, sha, err := TrunkTip(ctx)
	if err != nil {
		o.Error = jsonout.Strp(err.Error())
		return o
	}
	cfg, err := wtsync.LoadFromRef(ctx.Repo.MainRoot, sha)
	switch {
	case errors.Is(err, wtsync.ErrUndeclared):
		cfg = nil
	case err != nil:
		o.Error = jsonout.Strp(err.Error())
		return o
	}
	o.Declared = cfg != nil
	if opts.Onto == "" {
		o.Onto, o.Target = jsonout.Strp(sha), &syncrun.RunTarget{Ref: "origin/" + trunk, Trunk: true}
	}
	var target *OntoTarget
	if opts.Onto != "" {
		if src, remote, err := remoteSource(ctx, opts.Onto); remote && !opts.NoFetch {
			if err != nil {
				o.Error = jsonout.Strp(err.Error())
				return o
			}
			if err := fetchOnto(ctx, src, syncFetchTimeout); err != nil {
				o.FetchError, o.Fetched = jsonout.Strp(kit.FetchReason(err)), false
			}
		}
		t, err := resolveOnto(ctx, opts.Onto, opts.Upstream)
		if err != nil {
			o.Error = jsonout.Strp(err.Error())
			return o
		}
		target = &t
		o.Onto, o.Target = jsonout.Strp(t.SHA), &syncrun.RunTarget{Ref: t.Ref, Upstream: jsonout.Strp(t.Upstream)}
	}
	listed, err := ctx.ListAgents()
	if err != nil {
		o.SessionsError = jsonout.Strp(err.Error())
	}
	var sv Survey
	if target != nil {
		sv, err = SurveyOnto(ctx, opts.Work, *target, sha, cfg, listed, own)
	} else {
		sv, err = SurveyRepo(ctx, sha, cfg, listed, own)
	}
	if err != nil {
		o.Error = jsonout.Strp(err.Error())
		return o
	}
	o.Worktrees = sv.Rows
	o.EachCommitDeclared = eachCommitSteps(cfg)
	o.Needs = needProbes(cfg, sv.dues, syncrun.StepOptions{Probes: opts.Probes}.Prober())
	if o.Token, err = sv.Token(ctx, trunk, sha); err != nil {
		o.Error = jsonout.Strp(err.Error())
	}
	if opts.Stack != nil {
		base := sha
		if target != nil {
			base = target.cutoff()
		}
		if o.Stack, err = stackLookAt(ctx, base, sha, target, opts.Stack, listed); err != nil {
			o.Error = jsonout.Strp(err.Error())
		}
	}
	return o
}

// Survey is every worktree of a repository assessed against one trunk
// commit, as the overview reports them: what wt sync --json prints, and what
// a run's --expect recomputes.
type Survey struct {
	worktrees   []repo.Worktree
	assessments []wtsync.Assessment
	parents     map[string]string
	ambiguous   map[string][]string
	assessed    map[string]wtsync.Assessment
	// dues is what of the declared steps may run for each row.
	dues []wtsync.Due
	Rows []OverviewWorktree
	// divergedOnly is the branches a run refuses for nothing but having
	// diverged from their own remote: --allow-diverged starts on them.
	divergedOnly map[string]bool
	// target is what --onto names, for a survey of one worktree against it;
	// nil for one against trunk.
	target *OntoTarget
}

// SurveyRepo assesses every worktree but the main checkout against trunkSHA
// and files each as the overview does. own is every branch against its own
// remote; nil checks none.
func SurveyRepo(ctx *kit.Context, trunkSHA string, cfg *wtsync.Config, listed []agents.Agent, own *wtsync.Own) (Survey, error) {
	return survey(ctx, wtsync.Target{Onto: trunkSHA}, nil, nil, cfg, listed, own)
}

// SurveyOnto is the survey of the one worktree work names against the
// target --onto names, with the declaration's scripts read from trunkSHA.
// A run with --onto moves the worktree alone, so it has no stack.
func SurveyOnto(ctx *kit.Context, work string, target OntoTarget, trunkSHA string, cfg *wtsync.Config, listed []agents.Agent, own *wtsync.Own) (Survey, error) {
	wt, err := kit.Locate(ctx, work)
	if err != nil {
		return Survey{}, err
	}
	if why := target.refuseFor(ctx, kit.WorktreeName(ctx, wt.Branch, wt.Path), wt.Branch); why != "" {
		return Survey{}, errors.New(why)
	}
	return survey(ctx, target.Target(trunkSHA), &target, &wt, cfg, listed, own)
}

// survey assesses every worktree but the main checkout against t, or the
// one only names, and files each as the overview does.
func survey(ctx *kit.Context, t wtsync.Target, target *OntoTarget, only *repo.Worktree, cfg *wtsync.Config, listed []agents.Agent, own *wtsync.Own) (Survey, error) {
	all, err := ctx.Repo.Worktrees()
	if err != nil {
		return Survey{}, err
	}
	worktrees := slices.DeleteFunc(slices.Clone(all), func(wt repo.Worktree) bool { return wt.IsMain })
	r := &RunPlan{ctx: ctx, assessed: map[string]wtsync.Assessment{}}
	var assessments []wtsync.Assessment
	if only != nil {
		worktrees = []repo.Worktree{*only}
		assessments = []wtsync.Assessment{wtsync.AssessTo(ctx.Repo.MainRoot, t, cfg, *only, listed, syncrun.OwnStateOf(own, only.Branch))}
		r.parents, r.ambiguous = map[string]string{}, map[string][]string{}
	} else {
		assessments = assessAll(ctx.Repo.MainRoot, t.Onto, cfg, worktrees, listed, own, assessWorkers)
		r.parents, r.ambiguous, err = wtsync.Parents(ctx.Repo.MainRoot, t.Onto, all)
		if err != nil {
			return Survey{}, err
		}
	}
	for i, wt := range worktrees {
		if wt.Branch != "" {
			r.assessed[wt.Branch] = assessments[i]
		}
	}
	sv := Survey{worktrees: worktrees, assessments: assessments, parents: r.parents, ambiguous: r.ambiguous,
		assessed: r.assessed, Rows: make([]OverviewWorktree, 0, len(worktrees)), divergedOnly: map[string]bool{}, target: target}
	for i, wt := range worktrees {
		if a := assessments[i]; wt.Branch != "" && a.Own.State == wtsync.OwnDiverged {
			a.Own.Allowed = true
			if v, _ := wtsync.Preflight(a); v == wtsync.Proceed {
				sv.divergedOnly[wt.Branch] = true
			}
		}
	}
	byBranch := map[string]repo.Worktree{}
	for _, wt := range all {
		if wt.Branch != "" {
			byBranch[wt.Branch] = wt
		}
	}
	declared := declaredSteps(cfg)
	for i, wt := range worktrees {
		row := r.overviewRow(wt, assessments[i], byBranch)
		row.DeferredDeclared = declared
		if len(cfg.EachCommitOrNeeds()) > 0 {
			due := stepsDue(t, cfg, r.parents, wt, assessments[i])
			row.Checks, row.CheckCommits = len(due.CheckCommits), checkCommits(due)
			sv.dues = append(sv.dues, due)
		}
		sv.Rows = append(sv.Rows, row)
	}
	return sv, nil
}

// overviewRow is one worktree as wt sync --json reports it.
func (r *RunPlan) overviewRow(wt repo.Worktree, a wtsync.Assessment, byBranch map[string]repo.Worktree) OverviewWorktree {
	ctx := r.ctx
	row := OverviewWorktree{
		Work: kit.WorktreeName(ctx, wt.Branch, wt.Path), Branch: jsonout.Strp(wt.Branch), Path: wt.Path,
		Class: a.Class.String(), Verified: !a.Unverified,
		Behind: a.Behind, Ahead: a.Ahead, Dirty: a.Dirty, HandedOver: a.Paused, PlanFile: jsonout.Strp(a.PlanFile),
		Sessions: []PlanSession{}, Stack: []PlanStackMember{}, Stops: []OverviewStop{},
		Strategies: []string{}, Notes: append([]string{}, a.Notes...), CheckCommits: []CheckCommit{},
		OwnRemote: syncrun.OwnRemoteOf(a.Own),
	}
	if a.Err != nil {
		row.Error = jsonout.Strp(a.Err.Error())
	}
	switch {
	case syncrun.OnTrunk(a):
		row.Group = GroupCurrent
	default:
		row.Group = [...]string{GroupReady, GroupNeedsYou, GroupSkipped}[SectionOf(a)]
	}
	verdict, why := wtsync.Preflight(a)
	row.Verdict = [...]string{VerdictProceed, VerdictSkip, VerdictRefuse}[verdict]
	switch {
	case verdict != wtsync.Proceed:
		row.Reason = jsonout.Strp(why)
	case !r.ready(a):
		row.Reason = jsonout.Strp(notReadyReason(a))
	default:
		if hold := r.stackHold(wt.Branch); hold != "" {
			row.Reason = jsonout.Strp(hold)
		} else {
			row.Runnable = true
		}
	}
	for _, s := range a.Sessions {
		state := "busy"
		if s.Idle() {
			state = "idle"
		}
		row.Sessions = append(row.Sessions, PlanSession{Work: row.Work, Name: s.Name, Kind: s.Program(), State: state})
	}
	if wt.Branch != "" {
		for _, m := range wtsync.Members(r.parents, wt.Branch) {
			o := byBranch[m]
			row.Stack = append(row.Stack, PlanStackMember{Work: kit.WorktreeName(ctx, m, o.Path), Branch: m, Path: o.Path})
		}
	}
	for _, stop := range a.Replay.Stops {
		files, yours := stop.Files, a.Replay.Stop != nil && stop.Index == a.Replay.Stop.Index
		if yours {
			files = a.Files
		}
		st := OverviewStop{Index: stop.Index, Total: stop.Total, Commit: jsonout.Strp(stop.Commit), Subject: stop.Subject,
			Resolved: stop.Resolved, Yours: yours, Files: []OverviewFile{}}
		for _, f := range files {
			st.Files = append(st.Files, OverviewFile{Path: f.Path, Resolved: f.Resolved, Strategy: jsonout.Strp(f.Strategy), Note: jsonout.Strp(f.Note)})
			if f.Resolved && f.Strategy != "" && !slices.Contains(row.Strategies, f.Strategy) {
				row.Strategies = append(row.Strategies, f.Strategy)
			}
		}
		row.Stops = append(row.Stops, st)
	}
	return row
}

// Token names what a run would start on: trunk's name, the wt configuration,
// the declaration on trunk, and every worktree a run would start on — its
// branch, class, whether a run with nothing named takes it, and its stack.
// A newer trunk commit is not in it; what it changes about those is. Nor is
// the commit of a branch's own remote, with one exception: a worktree
// refused only because its branch has diverged from that remote is one a
// run starts on under --allow-diverged, so it is in the token with the
// remote's commit, and a run allowed over the divergence its caller was
// shown is refused over one that moved since. Nil when a run would start on
// nothing. With nothing diverged it is the token an older wt gives, which
// docs/json.md promises: so it is built by hand, not with jsonout.Token. A
// declaration git could not look for is an error, not a token: hashed as
// none, it would match a later read that found one.
func (sv Survey) Token(ctx *kit.Context, trunk, trunkSHA string) (*string, error) {
	h := sha256.New()
	h.Write([]byte("wt-sync-1\x00" + trunk + "\x00"))
	h.Write(kit.ConfigFingerprint(ctx))
	// rev-parse --verify --quiet exits 1, saying nothing, for no declaration.
	decl, err := git.Run(ctx.Repo.MainRoot, "rev-parse", "--verify", "--quiet", trunkSHA+":"+wtsync.ConfigFile)
	if _, err := git.Answer(err, 1); err != nil {
		return nil, fmt.Errorf("cannot look for %s on trunk: %w", wtsync.ConfigFile, err)
	}
	h.Write([]byte("\x00" + decl))
	if o := sv.target; o != nil {
		// Only with --onto: without it the token is what it always was.
		h.Write([]byte("\x00onto\x01" + o.Ref + "\x01" + o.SHA + "\x01" + o.Upstream))
	}
	eligible, covered := 0, 0
	for _, row := range sv.Rows {
		if row.Branch == nil {
			continue
		}
		// The remote commit of every branch a run would refuse for having
		// diverged, whatever else keeps a run off it: --force lifts a busy
		// session at triage, and what the run then goes over must be what
		// the caller was shown.
		if o := row.OwnRemote; o.Blocks != nil && *o.Blocks == wtsync.OwnBlockDiverged && o.Commit != nil {
			h.Write([]byte("\x00diverged\x01" + *row.Branch + "\x01" + *o.Commit))
			covered++
		}
		if row.Verdict != VerdictProceed && !sv.divergedOnly[*row.Branch] {
			continue
		}
		eligible++
		var stack []string
		for _, m := range row.Stack {
			stack = append(stack, m.Branch)
		}
		h.Write([]byte("\x00" + *row.Branch + "\x01" + row.Class + "\x01" + strconv.FormatBool(row.Verified) +
			"\x01" + strconv.FormatBool(row.Runnable) + "\x01" + strings.Join(stack, "\x02")))
	}
	if eligible+covered == 0 {
		return nil, nil
	}
	return jsonout.Strp("1:" + hex.EncodeToString(h.Sum(nil))[:32]), nil
}
