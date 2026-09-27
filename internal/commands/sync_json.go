package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
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

// What wt sync run <work> would do with a worktree, wtsync.Preflight's
// verdict.
const (
	VerdictProceed = "proceed"
	VerdictSkip    = "skip"
	VerdictRefuse  = "refuse"
)

// SyncOverview is the one object wt sync --json prints for a repository:
// every worktree, filed as the overview files it.
type SyncOverview struct {
	Schema        int                `json:"schema"`
	SchemaVersion string             `json:"schemaVersion"`
	Command       string             `json:"command"`
	Repo          string             `json:"repo"`
	Name          string             `json:"name"`
	Trunk         *string            `json:"trunk"`
	TrunkRef      *string            `json:"trunkRef"`
	Onto          *string            `json:"onto"`
	Fetched       bool               `json:"fetched"`
	FetchError    *string            `json:"fetchError"`
	Declared      bool               `json:"declared"`
	Token         *string            `json:"token"`
	Error         *string            `json:"error"`
	SessionsError *string            `json:"sessionsError"`
	Worktrees     []OverviewWorktree `json:"worktrees"`
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
	Notes      []string          `json:"notes"`
	Error      *string           `json:"error"`
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
func SyncJSON(ctx *Context, opts SyncOptions, w io.Writer) error {
	defer watchSignals(os.Stderr, nil)()
	o := overviewOf(ctx, opts)
	if err := writeJSON(w, o); err != nil {
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
func SyncAllJSON(u *config.User, sel Selection, opts SyncOptions, w io.Writer) error {
	set, err := SelectRepos(u, sel)
	if err != nil {
		return err
	}
	defer watchSignals(os.Stderr, nil)()
	out := eachRepo(set.Repos, repoParallelism, func(t RepoTarget) SyncOverview {
		if t.Problem == "" {
			ctx, err := Open(t.Path)
			if err == nil {
				return overviewOf(ctx, opts)
			}
			t.Problem = err.Error()
		}
		o := newOverview(t.Name, t.Path)
		o.Error = strp(t.Problem)
		return o
	})
	if out == nil {
		out = []SyncOverview{}
	}
	if err := writeJSON(w, out); err != nil {
		return err
	}
	var failed []string
	for _, o := range out {
		if o.Error != nil {
			failed = append(failed, o.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %s failed: %s", len(failed), repoCount(len(out)), strings.Join(failed, ", "))
	}
	return nil
}

func newOverview(name, mainRoot string) SyncOverview {
	return SyncOverview{Schema: 1, SchemaVersion: schema.VersionOf("sync"), Command: "sync",
		Repo: mainRoot, Name: name, Worktrees: []OverviewWorktree{}}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// overviewOf is what wt sync would print for ctx's repository, as data.
func overviewOf(ctx *Context, opts SyncOptions) SyncOverview {
	o := newOverview(ctx.Repo.Name, ctx.Repo.MainRoot)
	trunk := ctx.Config.MainBranch
	o.Trunk, o.TrunkRef = strp(trunk), strp("origin/"+trunk)
	if !opts.NoFetch {
		if _, err := git.RunTimeout(ctx.Repo.MainRoot, syncFetchTimeout, "fetch", "--quiet", "origin", trunk); err != nil {
			o.FetchError = strp(fetchReason(err))
		} else {
			o.Fetched = true
		}
	}
	// One trunk commit for the whole overview, the declaration read from it
	// too, as a run reads it: a fetch landing meanwhile cannot mix two.
	_, sha, err := trunkTip(ctx)
	if err != nil {
		o.Error = strp(err.Error())
		return o
	}
	cfg, err := wtsync.LoadFromRef(ctx.Repo.MainRoot, sha)
	switch {
	case errors.Is(err, wtsync.ErrNoConfig):
		cfg = nil
	case err != nil:
		o.Error = strp(err.Error())
		return o
	}
	o.Onto, o.Declared = strp(sha), cfg != nil
	agents, err := wtsync.ListOtherAgents()
	if err != nil {
		o.SessionsError = strp(err.Error())
	}
	sv, err := surveyRepo(ctx, sha, cfg, agents)
	if err != nil {
		o.Error = strp(err.Error())
		return o
	}
	o.Worktrees = sv.rows
	o.Token = sv.token(ctx, trunk, sha)
	return o
}

// survey is every worktree of a repository assessed against one trunk
// commit, as the overview reports them: what wt sync --json prints, and what
// a run's --expect recomputes.
type survey struct {
	worktrees   []repo.Worktree
	assessments []wtsync.Assessment
	parents     map[string]string
	ambiguous   map[string][]string
	assessed    map[string]wtsync.Assessment
	rows        []OverviewWorktree
}

// surveyRepo assesses every worktree but the main checkout against trunkSHA
// and files each as the overview does.
func surveyRepo(ctx *Context, trunkSHA string, cfg *wtsync.Config, agents []wtsync.Agent) (survey, error) {
	all, err := ctx.Repo.Worktrees()
	if err != nil {
		return survey{}, err
	}
	worktrees := slices.DeleteFunc(slices.Clone(all), func(wt repo.Worktree) bool { return wt.IsMain })
	assessments := assessAll(ctx.Repo.MainRoot, trunkSHA, cfg, worktrees, agents, assessWorkers)
	r := &runPlan{ctx: ctx, assessed: map[string]wtsync.Assessment{}}
	r.parents, r.ambiguous, err = wtsync.Parents(ctx.Repo.MainRoot, trunkSHA, all)
	if err != nil {
		return survey{}, err
	}
	for i, wt := range worktrees {
		if wt.Branch != "" {
			r.assessed[wt.Branch] = assessments[i]
		}
	}
	sv := survey{worktrees: worktrees, assessments: assessments, parents: r.parents, ambiguous: r.ambiguous,
		assessed: r.assessed, rows: make([]OverviewWorktree, 0, len(worktrees))}
	byBranch := map[string]repo.Worktree{}
	for _, wt := range all {
		if wt.Branch != "" {
			byBranch[wt.Branch] = wt
		}
	}
	for i, wt := range worktrees {
		sv.rows = append(sv.rows, r.overviewRow(wt, assessments[i], byBranch))
	}
	return sv, nil
}

// overviewRow is one worktree as wt sync --json reports it.
func (r *runPlan) overviewRow(wt repo.Worktree, a wtsync.Assessment, byBranch map[string]repo.Worktree) OverviewWorktree {
	ctx := r.ctx
	row := OverviewWorktree{
		Work: worktreeName(ctx, wt.Branch, wt.Path), Branch: strp(wt.Branch), Path: wt.Path,
		Class: a.Class.String(), Verified: !a.Unverified,
		Behind: a.Behind, Ahead: a.Ahead, Dirty: a.Dirty, HandedOver: a.Paused, PlanFile: strp(a.PlanFile),
		Sessions: []PlanSession{}, Stack: []PlanStackMember{}, Stops: []OverviewStop{},
		Strategies: []string{}, Notes: append([]string{}, a.Notes...),
	}
	if a.Err != nil {
		row.Error = strp(a.Err.Error())
	}
	switch {
	case a.Class == wtsync.Current && a.Err == nil:
		row.Group = GroupCurrent
	default:
		row.Group = [...]string{GroupReady, GroupNeedsYou, GroupSkipped}[sectionOf(a)]
	}
	verdict, why := wtsync.Preflight(a)
	row.Verdict = [...]string{VerdictProceed, VerdictSkip, VerdictRefuse}[verdict]
	switch {
	case verdict != wtsync.Proceed:
		row.Reason = strp(why)
	case !r.ready(a):
		row.Reason = strp(notReadyReason(a))
	default:
		if hold := r.stackHold(wt.Branch); hold != "" {
			row.Reason = strp(hold)
		} else {
			row.Runnable = true
		}
	}
	for _, s := range a.Sessions {
		state := "busy"
		if s.Idle() {
			state = "idle"
		}
		row.Sessions = append(row.Sessions, PlanSession{Work: row.Work, Name: s.Name, Kind: "claude", State: state})
	}
	if wt.Branch != "" {
		for _, m := range wtsync.Members(r.parents, wt.Branch) {
			o := byBranch[m]
			row.Stack = append(row.Stack, PlanStackMember{Work: worktreeName(ctx, m, o.Path), Branch: m, Path: o.Path})
		}
	}
	for _, stop := range a.Replay.Stops {
		files, yours := stop.Files, a.Replay.Stop != nil && stop.Index == a.Replay.Stop.Index
		if yours {
			files = a.Files
		}
		st := OverviewStop{Index: stop.Index, Total: stop.Total, Commit: strp(stop.Commit), Subject: stop.Subject,
			Resolved: stop.Resolved, Yours: yours, Files: []OverviewFile{}}
		for _, f := range files {
			st.Files = append(st.Files, OverviewFile{Path: f.Path, Resolved: f.Resolved, Strategy: strp(f.Strategy), Note: strp(f.Note)})
			if f.Resolved && f.Strategy != "" && !slices.Contains(row.Strategies, f.Strategy) {
				row.Strategies = append(row.Strategies, f.Strategy)
			}
		}
		row.Stops = append(row.Stops, st)
	}
	return row
}

// token names what a run would start on: trunk's name, the wt configuration,
// the declaration on trunk, and every worktree a run would start on — its
// branch, class, whether a run with nothing named takes it, and its stack.
// A newer trunk commit is not in it; what it changes about those is. Nil
// when a run would start on nothing.
func (sv survey) token(ctx *Context, trunk, trunkSHA string) *string {
	h := sha256.New()
	h.Write([]byte("wt-sync-1\x00" + trunk + "\x00"))
	h.Write(configFingerprint(ctx))
	decl, _ := git.Run(ctx.Repo.MainRoot, "rev-parse", "--verify", "--quiet", trunkSHA+":"+wtsync.ConfigFile)
	h.Write([]byte("\x00" + decl))
	eligible := 0
	for _, row := range sv.rows {
		if row.Verdict != VerdictProceed || row.Branch == nil {
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
	if eligible == 0 {
		return nil
	}
	return strp("1:" + hex.EncodeToString(h.Sum(nil))[:32])
}
