package provision

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/schema"
)

var createSteps = []string{kit.StepWorktree, kit.StepBranch, kit.StepConfig, kit.StepCache, kit.StepProvision, kit.StepSubmodules, kit.StepBuild, kit.StepSuperset}

// What a whole wt new or wt checkout came to.
const (
	// CreateCreated is the worktree made and every step done or skipped.
	CreateCreated = "created"
	// CreateCreatedWithProblems is the worktree made, and a step after it
	// failed or it is not on the commit the plan pinned.
	CreateCreatedWithProblems = "createdWithProblems"
	// CreateRefused is nothing made: a problem in the plan, or --expect.
	CreateRefused = "refused"
	// CreateFailed is git worktree add failing.
	CreateFailed = "failed"
	// CreateInterrupted is a signal ending the run.
	CreateInterrupted = "interrupted"
)

// Problem is one reason a plan cannot be carried out.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CreatePlan is what wt new and wt checkout --dry-run --json share.
type CreatePlan struct {
	Schema        int       `json:"schema"`
	SchemaVersion string    `json:"schemaVersion"`
	Command       string    `json:"command"`
	Token         *string   `json:"token"`
	TrunkSource   *string   `json:"trunkSource"`
	Configured    bool      `json:"configured"`
	Types         []string  `json:"types"`
	DefaultType   *string   `json:"defaultType"`
	Type          *string   `json:"type"`
	Work          *string   `json:"work"`
	Branch        *string   `json:"branch"`
	Path          *string   `json:"path"`
	Provision     bool      `json:"provision"`
	BuildCommand  *string   `json:"buildCommand"`
	Superset      bool      `json:"superset"`
	Problems      []Problem `json:"problems"`
}

// NewPlan is the one object wt new --dry-run --json prints.
type NewPlan struct {
	CreatePlan
	Base       *string `json:"base"`
	BaseCommit *string `json:"baseCommit"`
}

// CheckoutPlan is the one object wt checkout --dry-run --json prints.
type CheckoutPlan struct {
	CreatePlan
	BranchCommit *string `json:"branchCommit"`
	CheckoutSource
	RemoteCommit *string `json:"remoteCommit"`
}

// CheckoutSource is where wt checkout's branch comes from, in its plan and
// its result: SourceLocal, or SourceRemote with the remote and its
// remote-tracking ref. All null when the branch was not found.
type CheckoutSource struct {
	Source    *string `json:"source"`
	Remote    *string `json:"remote"`
	RemoteRef *string `json:"remoteRef"`
}

// CheckoutResult is the one object wt checkout --json prints.
type CheckoutResult struct {
	CreateResult
	CheckoutSource
	// Upstream is the branch's upstream ref read back after the run.
	Upstream *string `json:"upstream"`
}

// CreateStep is one side effect, as the result reports it.
type CreateStep struct {
	Step   string  `json:"step"`
	Result string  `json:"result"`
	Commit *string `json:"commit"`
	Reason *string `json:"reason"`
}

// CreateResult is the one object wt new --json and wt checkout --json print.
type CreateResult struct {
	Schema         int          `json:"schema"`
	SchemaVersion  string       `json:"schemaVersion"`
	Command        string       `json:"command"`
	Outcome        string       `json:"outcome"`
	Error          *string      `json:"error"`
	Problems       []Problem    `json:"problems"`
	Type           *string      `json:"type"`
	Work           *string      `json:"work"`
	Branch         *string      `json:"branch"`
	Path           *string      `json:"path"`
	Base           *string      `json:"base"`
	ExpectedCommit *string      `json:"expectedCommit"`
	Commit         *string      `json:"commit"`
	Steps          []CreateStep `json:"steps"`
}

func (p *CreatePlan) problem(code, format string, args ...any) {
	p.Problems = append(p.Problems, Problem{Code: code, Message: fmt.Sprintf(format, args...)})
}

// basePlan is the part of a plan that does not depend on the name: the
// configuration, and which provisioning steps these options would run.
func basePlan(ctx *kit.Context, command string, opts NewOptions) CreatePlan {
	p := CreatePlan{Schema: 1, SchemaVersion: schema.VersionOf(command + "-plan"), Command: command,
		Types: append([]string{}, ctx.Config.Types...), DefaultType: kit.Strp(ctx.Config.DefaultType),
		TrunkSource: ctx.TrunkSource(), Configured: ctx.Configured(), Problems: []Problem{}}
	switch {
	case errors.Is(ctx.ConfigError, config.ErrNoConfig):
		p.problem(kit.ProblemNoConfiguration, "%s", kit.OneLine(ctx.ConfigError.Error()))
	case ctx.ConfigError != nil:
		p.problem(kit.ProblemConfigInvalid, "the wt configuration does not parse: %s", kit.OneLine(ctx.ConfigError.Error()))
	}
	if !opts.NoSetup {
		p.Provision = ctx.HasProvisionScript()
		if !opts.SkipBuild && ctx.Config.BuildInitEnabled {
			p.BuildCommand = kit.Strp(ctx.Config.BuildInitCommand)
		}
		p.Superset = !opts.NoSuperset && SupersetEnabled(ctx)
	}
	return p
}

// PlanNew is what wt new would do for spec, from local state alone. It
// writes nothing.
func PlanNew(ctx *kit.Context, spec string, opts NewOptions) NewPlan {
	p := NewPlan{CreatePlan: basePlan(ctx, "new", opts)}
	base := opts.Base
	if base == "" {
		base = ctx.Config.MainBranch
	}
	p.Base = kit.Strp(base)
	if err := dotOrRoot(spec); err != nil {
		p.problem(kit.ProblemInvalidName, "%v", err)
	} else if typ, work, err := naming.ParseSpec(spec, ctx.Config.DefaultType, ctx.Vocab()); err != nil {
		p.problem(kit.ProblemInvalidName, "%v", err)
	} else {
		p.Type, p.Work = kit.Strp(typ), kit.Strp(work)
		if err := kit.CheckType(ctx, typ); err != nil {
			p.problem(kit.ProblemUnknownType, "%v", err)
		} else {
			branch, path := ctx.Scheme().Branch(typ, work), ctx.Scheme().Dir(typ, work)
			p.Branch, p.Path = kit.Strp(branch), kit.Strp(path)
			if _, err := git.Run(ctx.Repo.MainRoot, "check-ref-format", "refs/heads/"+branch); err != nil {
				p.problem(kit.ProblemInvalidName, "%s is not a name git takes for a branch", branch)
			} else if ctx.Repo.BranchExists(branch) {
				p.problem(kit.ProblemBranchExists, "branch %s already exists", branch)
			}
			if _, err := os.Stat(path); err == nil {
				p.problem(kit.ProblemPathExists, "%s already exists", path)
			}
		}
	}
	// No base at all is no trunk and no --base: noConfiguration says so.
	if oid, ok, err := ctx.Repo.ResolveRef(base); err != nil {
		// No code says "git failed"; baseMissing refuses, and the message says why.
		p.problem(kit.ProblemBaseMissing, "git could not read %q: %s", base, git.Reason(err))
	} else if ok {
		p.BaseCommit = kit.Strp(oid)
	} else if base != "" {
		p.problem(kit.ProblemBaseMissing, "%q is not a commit here", base)
	}
	if len(p.Problems) == 0 {
		p.Token = kit.Strp(createToken(ctx, "new", *p.Branch, *p.Path, *p.BaseCommit))
	}
	return p
}

// PlanCheckout is what wt checkout would do for arg, from local state
// alone, the remote-tracking refs as last fetched. It writes nothing.
func PlanCheckout(ctx *kit.Context, arg, work string, opts NewOptions) CheckoutPlan {
	p := CheckoutPlan{CreatePlan: basePlan(ctx, "checkout", opts)}
	typ := ctx.Config.DefaultType
	t, prob := ResolveCheckout(ctx, arg)
	p.Type, p.Branch = kit.Strp(typ), kit.Strp(t.Branch)
	p.CheckoutSource = t.source()
	switch t.Source {
	case SourceLocal:
		p.BranchCommit = kit.Strp(t.Commit)
		if all, err := ctx.Repo.Worktrees(); err == nil {
			if wt, ok := all.ByBranch(t.Branch); ok {
				p.problem(kit.ProblemBranchCheckedOut, "branch %s is checked out at %s; git gives a branch only one worktree",
					t.Branch, wt.Path)
			}
		}
	case SourceRemote:
		p.RemoteCommit = kit.Strp(t.Commit)
	}
	if prob != nil {
		p.Problems = append(p.Problems, *prob)
	}
	if arg == "" {
		return p
	}
	if work == "" {
		work = WorkNameFromBranch(t.Branch, ctx.Scheme().Suffix)
	}
	if work == "" {
		p.problem(kit.ProblemInvalidName, "could not derive a work name from branch %q", t.Branch)
	} else {
		path := ctx.Scheme().Dir(typ, work)
		p.Work, p.Path = kit.Strp(work), kit.Strp(path)
		if _, err := os.Stat(path); err == nil {
			p.problem(kit.ProblemPathExists, "%s already exists", path)
		}
	}
	if len(p.Problems) == 0 {
		p.Token = kit.Strp(createToken(ctx, "checkout", t.tokenName(), *p.Path, t.Commit))
	}
	return p
}

// source is t as the plan and the result name it.
func (t CheckoutTarget) source() CheckoutSource {
	var s CheckoutSource
	if t.Source != "" {
		s.Source = kit.Strp(t.Source)
	}
	if t.Source == SourceRemote {
		s.Remote, s.RemoteRef = kit.Strp(t.Remote), kit.Strp(t.RemoteRef)
	}
	return s
}

// tokenName is the branch as the token names it: a branch to be created
// from a remote also names the remote-tracking ref, so a local branch of
// the same name appearing changes the token.
func (t CheckoutTarget) tokenName() string {
	if t.Source == SourceRemote {
		return t.Branch + "\x00remote\x00" + t.RemoteRef
	}
	return t.Branch
}

// createToken names the inputs a plan was computed from: the command, the
// branch and path it would create, the commit the worktree would land on —
// the base for wt new, the branch's tip or the remote-tracking ref's for wt
// checkout — and the configuration file. Anything else moving does not
// change it.
func createToken(ctx *kit.Context, command, branch, path, commit string) string {
	h := sha256.New()
	h.Write([]byte("wt-create-1\x00" + command + "\x00" + branch + "\x00" + path + "\x00" + commit + "\x00"))
	h.Write(kit.ConfigFingerprint(ctx))
	return "1:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// NewPlanJSON writes wt new --dry-run --json.
func NewPlanJSON(ctx *kit.Context, spec string, opts NewOptions, w io.Writer) error {
	return kit.WriteJSON(w, PlanNew(ctx, spec, opts))
}

// CheckoutPlanJSON writes wt checkout --dry-run --json.
func CheckoutPlanJSON(ctx *kit.Context, branch, work string, opts NewOptions, w io.Writer) error {
	return kit.WriteJSON(w, PlanCheckout(ctx, branch, work, opts))
}

// NewDryRun is wt new --dry-run for a person: what would be made, or why
// not.
func NewDryRun(ctx *kit.Context, spec string, opts NewOptions, w io.Writer) error {
	p := PlanNew(ctx, spec, opts)
	if err := problemsError(p.Problems); err != nil {
		return err
	}
	fmt.Fprintf(w, "Would create %s from %s (%s)\n  at %s\n", *p.Branch, *p.Base, git.ShortID(*p.BaseCommit, 7), *p.Path)
	return nil
}

// CheckoutDryRun is wt checkout --dry-run for a person.
func CheckoutDryRun(ctx *kit.Context, arg, work string, opts NewOptions, w io.Writer) error {
	p := PlanCheckout(ctx, arg, work, opts)
	if err := problemsError(p.Problems); err != nil {
		return err
	}
	if p.RemoteCommit != nil {
		fmt.Fprintf(w, "Would create %s from %s (%s), tracking it\n  at %s\n", *p.Branch,
			strings.TrimPrefix(*p.RemoteRef, "refs/remotes/"), git.ShortID(*p.RemoteCommit, 7), *p.Path)
		return nil
	}
	fmt.Fprintf(w, "Would check out %s (%s)\n  at %s\n", *p.Branch, git.ShortID(*p.BranchCommit, 7), *p.Path)
	return nil
}

func problemsError(ps []Problem) error {
	if len(ps) == 0 {
		return nil
	}
	var msgs []string
	for _, p := range ps {
		msgs = append(msgs, p.Message)
	}
	return errors.New(strings.Join(msgs, "; "))
}

// NewJSON is wt new --json: the plan recomputed, --expect held to it, then
// the same creation as without --json, recorded step by step in j.
func NewJSON(ctx *kit.Context, spec string, opts NewOptions, expect string, j *CreateJournal, w io.Writer) error {
	p := PlanNew(ctx, spec, opts)
	j.plan(p.CreatePlan, p.Base, p.BaseCommit)
	if err := j.admit(p.CreatePlan, expect); err != nil {
		return err
	}
	_, err := AddAndProvision(ctx, *p.Path, newAdd(ctx, *p.Path, *p.Branch, *p.Base, w), opts, w, j)
	j.Finish()
	return err
}

// CheckoutJSON is wt checkout --json, as NewJSON is wt new --json.
func CheckoutJSON(ctx *kit.Context, arg, work string, opts NewOptions, expect string, j *CreateJournal, w io.Writer) error {
	p := PlanCheckout(ctx, arg, work, opts)
	expected := p.BranchCommit
	if p.RemoteCommit != nil {
		expected = p.RemoteCommit
	}
	j.plan(p.CreatePlan, nil, expected)
	j.checkoutSource(p.CheckoutSource)
	if err := j.admit(p.CreatePlan, expect); err != nil {
		return err
	}
	if work == "" && *p.Work != *p.Branch {
		fmt.Fprintf(w, "Using work name %q for branch %s\n", *p.Work, *p.Branch)
	}
	t := CheckoutTarget{Branch: *p.Branch, Source: *p.Source, Commit: *expected}
	if p.Remote != nil {
		t.Remote, t.RemoteRef = *p.Remote, *p.RemoteRef
	}
	_, err := AddAndProvision(ctx, *p.Path, CheckoutAdd(ctx, *p.Path, t, j, w), opts, w, j)
	j.Finish()
	return err
}

// CreateJournal collects what one wt new or wt checkout did, step by step,
// so that one object is written at the end — by the command when it returns,
// or by the signal handler when it does not. Every method is safe from both,
// safe on a nil journal (the run without --json), and the object is written
// once. Once the signal handler has written it, the run parks at its next
// step rather than go on, or return and exit with another status before the
// handler's signal status.
type CreateJournal struct {
	*kit.Journal[CreateResult]
	// co is what wt checkout adds to the result; nil for wt new.
	co *CheckoutResult
}

// NewCreateJournal is a journal for command ("new" or "checkout") that
// writes its object to out. Every step starts as not run.
func NewCreateJournal(out io.Writer, command string) *CreateJournal {
	res := CreateResult{Schema: 1, SchemaVersion: schema.VersionOf(command), Command: command, Problems: []Problem{}}
	for _, s := range createSteps {
		res.Steps = append(res.Steps, CreateStep{Step: s, Result: kit.StepNotRun})
	}
	j := &CreateJournal{}
	if command == "checkout" {
		j.co = &CheckoutResult{}
	}
	j.Journal = kit.NewJournal(out, res, kit.JournalRules[CreateResult]{
		Error: func(r *CreateResult, err error) { r.Error = kit.Strp(err.Error()) },
		Caught: func(r *CreateResult, inFlight, _ string) {
			if s := r.step(inFlight); s != nil {
				s.Result = kit.StepInterrupted
				s.Reason = kit.Strp("a signal ended wt while this ran")
				if inFlight != kit.StepWorktree && r.Path != nil {
					s.Reason = kit.Strp("a signal ended wt while this ran; `cd " + *r.Path + " && wt setup` finishes provisioning")
				}
			}
		},
		Close: j.close})
	return j
}

// checkoutSource records where wt checkout's branch comes from.
func (j *CreateJournal) checkoutSource(s CheckoutSource) {
	if j == nil || j.co == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	j.co.CheckoutSource = s
}

// createsBranch is whether this run makes its branch: wt new always, wt
// checkout from a remote. Called with mu held.
func (j *CreateJournal) createsBranch() bool {
	if j.co == nil {
		return j.Res.Command == "new"
	}
	return j.co.Source != nil && *j.co.Source == SourceRemote
}

// branchMade records the branch wt checkout has just created from a
// remote, before its worktree is added.
func (j *CreateJournal) branchMade(ctx *kit.Context) {
	if j == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	j.branchRead(ctx, kit.StepCreated)
}

// branchRead records the branch step from the branch as it is after the
// run: its tip, and for wt checkout its upstream. Called with mu held.
func (j *CreateJournal) branchRead(ctx *kit.Context, result string) {
	if j.Res.Branch == nil {
		return
	}
	// A report of what was made: a tip git cannot read is recorded as none.
	tip, _, _ := ctx.Repo.ResolveRef("refs/heads/" + *j.Res.Branch)
	s := j.step(kit.StepBranch)
	s.Result, s.Commit = result, kit.Strp(tip)
	if j.co == nil {
		return
	}
	up := ctx.Repo.Upstream(*j.Res.Branch)
	j.co.Upstream = kit.Strp(up)
	if j.co.RemoteRef != nil && result == kit.StepCreated {
		name := strings.TrimPrefix(*j.co.RemoteRef, "refs/remotes/")
		if up == *j.co.RemoteRef {
			s.Reason = kit.Strp("tracking " + name)
		} else {
			s.Reason = kit.Strp("its upstream could not be set to " + name)
		}
	}
}

func (j *CreateJournal) plan(p CreatePlan, base, expected *string) {
	if j == nil {
		return
	}
	j.Lock()
	defer j.Unlock()
	j.Res.Type, j.Res.Work, j.Res.Branch, j.Res.Path = p.Type, p.Work, p.Branch, p.Path
	j.Res.Base, j.Res.ExpectedCommit = base, expected
}

// admit refuses the run, and writes the object, when the plan has a problem
// or is not the one expect names: planChanged then comes first, followed by
// what the new plan finds wrong.
func (j *CreateJournal) admit(p CreatePlan, expect string) error {
	problems := p.Problems
	if expect != "" && (p.Token == nil || *p.Token != expect) {
		problems = append([]Problem{{Code: kit.ProblemPlanChanged,
			Message: "the plan is not the one --expect names: the commit, the name or the configuration changed; plan again"}},
			p.Problems...)
	}
	err := problemsError(problems)
	if err != nil {
		j.Lock()
		j.Res.Problems = problems
		j.Res.Error = kit.Strp(err.Error())
		j.Unlock()
		j.Finish()
	}
	return err
}

// Fail records an error that stopped the run before it could plan — not in a
// repository — and writes the object.
func (j *CreateJournal) Fail(err error) {
	j.RunError(err)
	j.Finish()
}

func (j *CreateJournal) step(name string) *CreateStep { return j.Res.step(name) }

func (r *CreateResult) step(name string) *CreateStep {
	for i := range r.Steps {
		if r.Steps[i].Step == name {
			return &r.Steps[i]
		}
	}
	return nil
}

// start marks a step as the one in flight, for a signal to name.
func (j *CreateJournal) start(name string) {
	if j == nil {
		return
	}
	j.Park()
	j.Start(name)
}

// finish records what a step came to.
func (j *CreateJournal) finish(name, result, reason, commit string) {
	if j == nil {
		return
	}
	j.Settle(name, func(r *CreateResult) {
		if s := r.step(name); s != nil {
			s.Result, s.Reason, s.Commit = result, kit.Strp(reason), kit.Strp(commit)
		}
	})
}

// AddFailed records a git worktree add that failed, from what is there
// afterwards. A post-checkout hook failing makes git exit non-zero with the
// worktree made; the steps after it are then not run, as without --json.
// Without a worktree, a branch wt new asked for may have been made all the
// same.
func (j *CreateJournal) AddFailed(ctx *kit.Context, path string, err error) {
	if j == nil {
		return
	}
	if errors.Is(err, repo.ErrBranchExists) {
		j.Settle(kit.StepWorktree, func(r *CreateResult) {
			r.Problems = append(r.Problems, Problem{Code: kit.ProblemPlanChanged, Message: kit.OneLine(err.Error())})
			r.Error = kit.Strp(kit.OneLine(err.Error()))
		})
		return
	}
	if all, lerr := ctx.Repo.Worktrees(); lerr == nil {
		if _, ok := all.ByPath(path); ok {
			j.created(ctx, path)
			j.Lock()
			j.step(kit.StepWorktree).Reason = kit.Strp(kit.OneLine(err.Error()))
			j.Unlock()
			return
		}
	}
	j.finish(kit.StepWorktree, kit.StepFailed, kit.OneLine(err.Error()), "")
	j.Lock()
	defer j.Unlock()
	if j.Res.Branch == nil {
		return
	}
	// A report of what was made: a branch git cannot read is left unrecorded.
	if _, ok, _ := ctx.Repo.ResolveRef("refs/heads/" + *j.Res.Branch); ok {
		result := kit.StepUntouched
		if j.createsBranch() {
			result = kit.StepCreated
		}
		j.branchRead(ctx, result)
	}
}

// created records the worktree git made, read back from it: the commit it
// is on, and whether that is the one the plan pinned.
func (j *CreateJournal) created(ctx *kit.Context, path string) {
	if j == nil {
		return
	}
	head, _ := git.Run(path, "rev-parse", "--verify", "HEAD")
	head = strings.TrimSpace(head)
	j.finish(kit.StepWorktree, kit.StepCreated, "", head)
	j.Lock()
	defer j.Unlock()
	j.Res.Commit = kit.Strp(head)
	branch := kit.StepUntouched
	if j.createsBranch() {
		branch = kit.StepCreated
	}
	j.branchRead(ctx, branch)
	if want := j.Res.ExpectedCommit; want != nil && head != *want {
		j.Res.Problems = append(j.Res.Problems, Problem{Code: kit.ProblemHeadMoved,
			Message: fmt.Sprintf("the worktree is at %s, not at %s as planned", git.ShortID(head, 7), git.ShortID(*want, 7))})
	}
}

// skipSetup records every provisioning step as skipped, for --no-setup.
func (j *CreateJournal) skipSetup(why string) {
	for _, s := range []string{kit.StepConfig, kit.StepCache, kit.StepProvision, kit.StepSubmodules, kit.StepBuild, kit.StepSuperset} {
		j.finish(s, kit.StepSkipped, why, "")
	}
}

// Finish writes the object for a run that returned.
func (j *CreateJournal) Finish() {
	if j == nil {
		return
	}
	j.Park()
	j.Journal.Finish()
}

// Interrupted writes the object for a run a signal ended.
func (j *CreateJournal) Interrupted(inFlight, recovery string) {
	if j != nil {
		j.Journal.Interrupted(inFlight, recovery)
	}
}

func (j *CreateJournal) close(r *CreateResult, signalled bool, _ string) any {
	r.Outcome = r.outcome(signalled)
	if j.co != nil {
		j.co.CreateResult = *r
		return j.co
	}
	return r
}

// Outcome is the rule docs/json.md states, read from the steps.
func (j *CreateJournal) Outcome(signalled bool) string { return j.Res.outcome(signalled) }

func (r *CreateResult) outcome(signalled bool) string {
	if signalled {
		return CreateInterrupted
	}
	switch r.step(kit.StepWorktree).Result {
	case kit.StepCreated:
	case kit.StepFailed:
		return CreateFailed
	default:
		return CreateRefused
	}
	if len(r.Problems) > 0 {
		return CreateCreatedWithProblems
	}
	for _, s := range r.Steps {
		if s.Result == kit.StepFailed || s.Result == kit.StepInterrupted || s.Result == kit.StepNotRun {
			return CreateCreatedWithProblems
		}
	}
	return CreateCreated
}
