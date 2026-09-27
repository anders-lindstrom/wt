package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/schema"
)

// Why wt new or wt checkout would not create a worktree, or what is wrong
// with one it created, as --json names it.
const (
	ProblemBranchExists     = "branchExists"
	ProblemPathExists       = "pathExists"
	ProblemUnknownType      = "unknownType"
	ProblemInvalidName      = "invalidName"
	ProblemNoConfiguration  = "noConfiguration"
	ProblemConfigInvalid    = "configurationInvalid"
	ProblemBranchMissing    = "branchMissing"
	ProblemBranchCheckedOut = "branchCheckedOut"
	ProblemBaseMissing      = "baseMissing"
	// ProblemPlanChanged is --expect refusing: the plan is not the one the
	// token was made from.
	ProblemPlanChanged = "planChanged"
	// ProblemHeadMoved is a worktree created on another commit than the
	// plan pinned: the branch or base moved between the check and the add.
	ProblemHeadMoved = "headMoved"
)

// The side effects of creating a worktree, in the order they happen.
const (
	StepWorktree   = "worktree"
	StepBranch     = "branch"
	StepConfig     = "config"
	StepProvision  = "provision"
	StepSubmodules = "submodules"
	StepBuild      = "build"
	StepSuperset   = "superset"
)

var createSteps = []string{StepWorktree, StepBranch, StepConfig, StepProvision, StepSubmodules, StepBuild, StepSuperset}

// What one step came to. Every step ends as exactly one of these.
const (
	StepCreated     = "created"
	StepUntouched   = "untouched"
	StepDone        = "done"
	StepRegistered  = "registered"
	StepSkipped     = "skipped"
	StepFailed      = "failed"
	StepNotRun      = "notRun"
	StepInterrupted = "interrupted"
)

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
func basePlan(ctx *Context, command string, opts NewOptions) CreatePlan {
	p := CreatePlan{Schema: 1, SchemaVersion: schema.VersionOf(command + "-plan"), Command: command,
		Types: append([]string{}, ctx.Config.Types...), DefaultType: strp(ctx.Config.DefaultType),
		Configured: ctx.ConfigError == nil, Problems: []Problem{}}
	switch {
	case errors.Is(ctx.ConfigError, config.ErrNoConfig):
		p.problem(ProblemNoConfiguration, "no wt configuration (bin/worktree/worktree.conf); wt init sets it up")
	case ctx.ConfigError != nil:
		p.problem(ProblemConfigInvalid, "the wt configuration does not parse: %s", oneLine(ctx.ConfigError.Error()))
	}
	if !opts.NoSetup {
		p.Provision = ctx.HasProvisionScript()
		if !opts.SkipBuild && ctx.Config.BuildInitEnabled {
			p.BuildCommand = strp(ctx.Config.BuildInitCommand)
		}
		p.Superset = !opts.NoSuperset && supersetEnabled(ctx)
	}
	return p
}

// PlanNew is what wt new would do for spec, from local state alone. It
// writes nothing.
func PlanNew(ctx *Context, spec string, opts NewOptions) NewPlan {
	p := NewPlan{CreatePlan: basePlan(ctx, "new", opts)}
	base := opts.Base
	if base == "" {
		base = ctx.Config.MainBranch
	}
	p.Base = strp(base)
	if err := dotOrRoot(spec); err != nil {
		p.problem(ProblemInvalidName, "%v", err)
	} else if typ, work, err := naming.ParseSpec(spec, ctx.Config.DefaultType, ctx.Vocab()); err != nil {
		p.problem(ProblemInvalidName, "%v", err)
	} else {
		p.Type, p.Work = strp(typ), strp(work)
		if err := checkType(ctx, typ); err != nil {
			p.problem(ProblemUnknownType, "%v", err)
		} else {
			branch, path := ctx.Scheme().Branch(typ, work), ctx.Scheme().Dir(typ, work)
			p.Branch, p.Path = strp(branch), strp(path)
			if _, err := git.Run(ctx.Repo.MainRoot, "check-ref-format", "refs/heads/"+branch); err != nil {
				p.problem(ProblemInvalidName, "%s is not a name git takes for a branch", branch)
			} else if ctx.Repo.BranchExists(branch) {
				p.problem(ProblemBranchExists, "branch %s already exists", branch)
			}
			if _, err := os.Stat(path); err == nil {
				p.problem(ProblemPathExists, "%s already exists", path)
			}
		}
	}
	if oid, ok := ctx.Repo.ResolveRef(base); ok {
		p.BaseCommit = strp(oid)
	} else {
		p.problem(ProblemBaseMissing, "%q is not a commit here", base)
	}
	if len(p.Problems) == 0 {
		p.Token = strp(createToken(ctx, "new", *p.Branch, *p.Path, *p.BaseCommit))
	}
	return p
}

// PlanCheckout is what wt checkout would do for branch, from local state
// alone. It writes nothing.
func PlanCheckout(ctx *Context, branch, work string, opts NewOptions) CheckoutPlan {
	p := CheckoutPlan{CreatePlan: basePlan(ctx, "checkout", opts)}
	typ := ctx.Config.DefaultType
	p.Type, p.Branch = strp(typ), strp(branch)
	if branch == "" {
		p.problem(ProblemInvalidName, "no branch given")
		return p
	}
	if oid, ok := ctx.Repo.ResolveRef("refs/heads/" + branch); ok && ctx.Repo.BranchExists(branch) {
		p.BranchCommit = strp(oid)
		if all, err := ctx.Repo.Worktrees(); err == nil {
			if wt, ok := all.ByBranch(branch); ok {
				p.problem(ProblemBranchCheckedOut, "branch %s is checked out at %s; git gives a branch only one worktree",
					branch, wt.Path)
			}
		}
	} else {
		p.problem(ProblemBranchMissing, "branch %s does not exist; use `wt new` to create one", branch)
	}
	if work == "" {
		work = WorkNameFromBranch(branch, ctx.Scheme().Suffix)
	}
	if work == "" {
		p.problem(ProblemInvalidName, "could not derive a work name from branch %q", branch)
	} else {
		path := ctx.Scheme().Dir(typ, work)
		p.Work, p.Path = strp(work), strp(path)
		if _, err := os.Stat(path); err == nil {
			p.problem(ProblemPathExists, "%s already exists", path)
		}
	}
	if len(p.Problems) == 0 {
		p.Token = strp(createToken(ctx, "checkout", branch, *p.Path, *p.BranchCommit))
	}
	return p
}

// createToken names the inputs a plan was computed from: the command, the
// branch and path it would create, the commit the worktree would land on —
// the base for wt new, the branch's tip for wt checkout — and the
// configuration file. Anything else moving does not change it.
func createToken(ctx *Context, command, branch, path, commit string) string {
	h := sha256.New()
	h.Write([]byte("wt-create-1\x00" + command + "\x00" + branch + "\x00" + path + "\x00" + commit + "\x00"))
	h.Write(configFingerprint(ctx))
	return "1:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// NewPlanJSON writes wt new --dry-run --json.
func NewPlanJSON(ctx *Context, spec string, opts NewOptions, w io.Writer) error {
	return writeJSON(w, PlanNew(ctx, spec, opts))
}

// CheckoutPlanJSON writes wt checkout --dry-run --json.
func CheckoutPlanJSON(ctx *Context, branch, work string, opts NewOptions, w io.Writer) error {
	return writeJSON(w, PlanCheckout(ctx, branch, work, opts))
}

// NewDryRun is wt new --dry-run for a person: what would be made, or why
// not.
func NewDryRun(ctx *Context, spec string, opts NewOptions, w io.Writer) error {
	p := PlanNew(ctx, spec, opts)
	if err := problemsError(p.Problems); err != nil {
		return err
	}
	fmt.Fprintf(w, "Would create %s from %s (%s)\n  at %s\n", *p.Branch, *p.Base, git.ShortID(*p.BaseCommit, 7), *p.Path)
	return nil
}

// CheckoutDryRun is wt checkout --dry-run for a person.
func CheckoutDryRun(ctx *Context, branch, work string, opts NewOptions, w io.Writer) error {
	p := PlanCheckout(ctx, branch, work, opts)
	if err := problemsError(p.Problems); err != nil {
		return err
	}
	fmt.Fprintf(w, "Would check out %s (%s)\n  at %s\n", branch, git.ShortID(*p.BranchCommit, 7), *p.Path)
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
func NewJSON(ctx *Context, spec string, opts NewOptions, expect string, j *CreateJournal, w io.Writer) error {
	p := PlanNew(ctx, spec, opts)
	j.plan(p.CreatePlan, p.Base, p.BaseCommit)
	if err := j.admit(p.CreatePlan, expect); err != nil {
		return err
	}
	_, err := addAndProvision(ctx, *p.Path, newAdd(ctx, *p.Path, *p.Branch, *p.Base, w), opts, w, j)
	j.Finish()
	return err
}

// CheckoutJSON is wt checkout --json, as NewJSON is wt new --json.
func CheckoutJSON(ctx *Context, branch, work string, opts NewOptions, expect string, j *CreateJournal, w io.Writer) error {
	p := PlanCheckout(ctx, branch, work, opts)
	j.plan(p.CreatePlan, nil, p.BranchCommit)
	if err := j.admit(p.CreatePlan, expect); err != nil {
		return err
	}
	if work == "" && *p.Work != branch {
		fmt.Fprintf(w, "Using work name %q for branch %s\n", *p.Work, branch)
	}
	_, err := addAndProvision(ctx, *p.Path, checkoutAdd(ctx, *p.Path, branch, w), opts, w, j)
	j.Finish()
	return err
}

// CreateJournal collects what one wt new or wt checkout did, step by step,
// so that one object is written at the end — by the command when it returns,
// or by the signal handler when it does not. Every method is safe from both,
// safe on a nil journal (the run without --json), and the object is written
// once.
type CreateJournal struct {
	mu       sync.Mutex
	once     sync.Once
	out      io.Writer
	res      CreateResult
	inFlight string
	// signalled is set by the signal handler as it writes the object: from
	// then on the run parks at its next step rather than go on, or return
	// and exit with another status before the handler's 130.
	signalled bool
}

// NewCreateJournal is a journal for command ("new" or "checkout") that
// writes its object to out. Every step starts as not run.
func NewCreateJournal(out io.Writer, command string) *CreateJournal {
	j := &CreateJournal{out: out, res: CreateResult{Schema: 1, SchemaVersion: schema.VersionOf(command),
		Command: command, Problems: []Problem{}}}
	for _, s := range createSteps {
		j.res.Steps = append(j.res.Steps, CreateStep{Step: s, Result: StepNotRun})
	}
	return j
}

func (j *CreateJournal) plan(p CreatePlan, base, expected *string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.Type, j.res.Work, j.res.Branch, j.res.Path = p.Type, p.Work, p.Branch, p.Path
	j.res.Base, j.res.ExpectedCommit = base, expected
}

// admit refuses the run, and writes the object, when the plan has a problem
// or is not the one expect names.
func (j *CreateJournal) admit(p CreatePlan, expect string) error {
	problems := p.Problems
	if len(problems) == 0 && expect != "" && (p.Token == nil || *p.Token != expect) {
		problems = []Problem{{Code: ProblemPlanChanged,
			Message: "the plan is not the one --expect names: the commit, the name or the configuration changed; plan again"}}
	}
	err := problemsError(problems)
	if err != nil {
		j.mu.Lock()
		j.res.Problems = problems
		j.res.Error = strp(err.Error())
		j.mu.Unlock()
		j.Finish()
	}
	return err
}

// Fail records an error that stopped the run before it could plan — not in a
// repository — and writes the object.
func (j *CreateJournal) Fail(err error) {
	j.mu.Lock()
	j.res.Error = strp(err.Error())
	j.mu.Unlock()
	j.Finish()
}

func (j *CreateJournal) step(name string) *CreateStep {
	for i := range j.res.Steps {
		if j.res.Steps[i].Step == name {
			return &j.res.Steps[i]
		}
	}
	return nil
}

// start marks a step as the one in flight, for a signal to name.
func (j *CreateJournal) start(name string) {
	if j == nil {
		return
	}
	j.park()
	j.mu.Lock()
	j.inFlight = name
	j.mu.Unlock()
}

// park blocks for good once a signal has taken the run over: the handler
// writes the object and exits.
func (j *CreateJournal) park() {
	if j == nil {
		return
	}
	j.mu.Lock()
	signalled := j.signalled
	j.mu.Unlock()
	if signalled {
		select {}
	}
}

// finish records what a step came to.
func (j *CreateJournal) finish(name, result, reason, commit string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if s := j.step(name); s != nil {
		s.Result, s.Reason, s.Commit = result, strp(reason), strp(commit)
	}
	if j.inFlight == name {
		j.inFlight = ""
	}
}

// addFailed records a git worktree add that failed, from what is there
// afterwards. A post-checkout hook failing makes git exit non-zero with the
// worktree made; the steps after it are then not run, as without --json.
// Without a worktree, a branch wt new asked for may have been made all the
// same.
func (j *CreateJournal) addFailed(ctx *Context, path string, err error) {
	if j == nil {
		return
	}
	if all, lerr := ctx.Repo.Worktrees(); lerr == nil {
		if _, ok := all.ByPath(path); ok {
			j.created(ctx, path)
			j.mu.Lock()
			j.step(StepWorktree).Reason = strp(oneLine(err.Error()))
			j.mu.Unlock()
			return
		}
	}
	j.finish(StepWorktree, StepFailed, oneLine(err.Error()), "")
	j.mu.Lock()
	branch, command := j.res.Branch, j.res.Command
	j.mu.Unlock()
	if branch == nil {
		return
	}
	if oid, ok := ctx.Repo.ResolveRef("refs/heads/" + *branch); ok {
		result := StepUntouched
		if command == "new" {
			result = StepCreated
		}
		j.finish(StepBranch, result, "", oid)
	}
}

// created records the worktree git made, read back from it: the commit it
// is on, and whether that is the one the plan pinned.
func (j *CreateJournal) created(ctx *Context, path string) {
	if j == nil {
		return
	}
	head, _ := git.Run(path, "rev-parse", "--verify", "HEAD")
	head = strings.TrimSpace(head)
	j.finish(StepWorktree, StepCreated, "", head)
	j.mu.Lock()
	defer j.mu.Unlock()
	j.res.Commit = strp(head)
	branch := StepCreated
	if j.res.Command != "new" {
		branch = StepUntouched
	}
	if j.res.Branch != nil {
		tip, _ := ctx.Repo.ResolveRef("refs/heads/" + *j.res.Branch)
		if s := j.step(StepBranch); s != nil {
			s.Result, s.Commit = branch, strp(tip)
		}
	}
	if want := j.res.ExpectedCommit; want != nil && head != *want {
		j.res.Problems = append(j.res.Problems, Problem{Code: ProblemHeadMoved,
			Message: fmt.Sprintf("the worktree is at %s, not at %s as planned", git.ShortID(head, 7), git.ShortID(*want, 7))})
	}
}

// skipSetup records every provisioning step as skipped, for --no-setup.
func (j *CreateJournal) skipSetup(why string) {
	for _, s := range []string{StepConfig, StepProvision, StepSubmodules, StepBuild, StepSuperset} {
		j.finish(s, StepSkipped, why, "")
	}
}

// Finish writes the object for a run that returned.
func (j *CreateJournal) Finish() {
	j.park()
	j.write(false)
}

// interrupted writes the object for a run a signal ended, the step in
// flight as interrupted: what makes it interruptible, for watchSignals. The
// handler has stopped every git and script already, so what the steps
// recorded stands. inFlight and recovery are the rebase tracker's and what
// the handler printed, which a create has no use for.
func (j *CreateJournal) interrupted(_, _ string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.signalled = true
	j.mu.Unlock()
	j.write(true)
}

// Watch makes a handled SIGINT or SIGTERM, until the returned function is
// called, stop every git and provisioning script still running with its
// process group, write the object with the step in flight interrupted, and
// exit 130.
func (j *CreateJournal) Watch(w io.Writer) func() {
	unset := setInterruptJournal(j)
	stop := watchSignals(w, nil)
	return func() {
		stop()
		unset()
	}
}

func (j *CreateJournal) write(signalled bool) {
	if j == nil {
		return
	}
	j.once.Do(func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if signalled {
			if s := j.step(j.inFlight); s != nil {
				s.Result = StepInterrupted
				s.Reason = strp("a signal ended wt while this ran")
				if j.inFlight != StepWorktree && j.res.Path != nil {
					s.Reason = strp("a signal ended wt while this ran; `cd " + *j.res.Path + " && wt setup` finishes provisioning")
				}
			}
		}
		j.res.Outcome = j.outcome(signalled)
		_ = writeJSON(j.out, j.res)
	})
}

// outcome is the rule docs/json.md states, read from the steps.
func (j *CreateJournal) outcome(signalled bool) string {
	if signalled {
		return CreateInterrupted
	}
	switch j.step(StepWorktree).Result {
	case StepCreated:
	case StepFailed:
		return CreateFailed
	default:
		return CreateRefused
	}
	if len(j.res.Problems) > 0 {
		return CreateCreatedWithProblems
	}
	for _, s := range j.res.Steps {
		if s.Result == StepFailed || s.Result == StepInterrupted || s.Result == StepNotRun {
			return CreateCreatedWithProblems
		}
	}
	return CreateCreated
}
