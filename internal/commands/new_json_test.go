package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/superset"
)

func newPlanOf(t *testing.T, ctx *Context, spec string, opts NewOptions) NewPlan {
	t.Helper()
	var buf bytes.Buffer
	if err := NewPlanJSON(ctx, spec, opts, &buf); err != nil {
		t.Fatal(err)
	}
	validateJSON(t, "new-plan", buf.Bytes())
	var p NewPlan
	if err := json.Unmarshal(buf.Bytes(), &p); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return p
}

func checkoutPlanOf(t *testing.T, ctx *Context, branch, work string) CheckoutPlan {
	t.Helper()
	var buf bytes.Buffer
	if err := CheckoutPlanJSON(ctx, branch, work, NewOptions{}, &buf); err != nil {
		t.Fatal(err)
	}
	validateJSON(t, "checkout-plan", buf.Bytes())
	var p CheckoutPlan
	if err := json.Unmarshal(buf.Bytes(), &p); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return p
}

func newJSON(t *testing.T, ctx *Context, spec string, opts NewOptions, expect string) (CreateResult, string, error) {
	t.Helper()
	var out, human bytes.Buffer
	err := NewJSON(ctx, spec, opts, expect, NewCreateJournal(&out, "new"), &human)
	return decodeCreate(t, "new", out.Bytes()), human.String(), err
}

func checkoutJSON(t *testing.T, ctx *Context, branch, work string, opts NewOptions, expect string) (CreateResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	err := CheckoutJSON(ctx, branch, work, opts, expect, NewCreateJournal(&out, "checkout"), &human)
	return decodeCreate(t, "checkout", out.Bytes()), err
}

func decodeCreate(t *testing.T, name string, out []byte) CreateResult {
	t.Helper()
	validateJSON(t, name, out)
	dec := json.NewDecoder(bytes.NewReader(out))
	var r CreateResult
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
	}
	if dec.More() {
		t.Errorf("more than one object on stdout:\n%s", out)
	}
	return r
}

func stepOf(r CreateResult, name string) CreateStep {
	for _, s := range r.Steps {
		if s.Step == name {
			return s
		}
	}
	return CreateStep{Step: name, Result: "<missing>"}
}

func codes(ps []Problem) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return strings.Join(out, ",")
}

// The plan says what wt new would make, with a token, and makes nothing.
func TestNewPlanNamesWhatItWouldCreateAndWritesNothing(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	before := repoState(t, ctx.Repo.MainRoot)
	p := newPlanOf(t, ctx, "fix/login-crash", NewOptions{})
	if after := repoState(t, ctx.Repo.MainRoot); after != before {
		t.Errorf("the plan wrote to the repository:\nbefore %s\nafter %s", before, after)
	}
	head := strings.TrimSpace(gitOut(t, ctx.Repo.MainRoot, "rev-parse", "HEAD"))
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "login-crash")
	switch {
	case p.Command != "new" || p.Token == nil || len(p.Problems) != 0 || !p.Configured:
		t.Errorf("plan = %+v", p)
	case *p.Type != "fix" || *p.Work != "login-crash" || *p.Branch != "fix_wt/login-crash" || *p.Path != want:
		t.Errorf("names: %v %v %v %v", *p.Type, *p.Work, *p.Branch, *p.Path)
	case *p.Base != "main" || *p.BaseCommit != head:
		t.Errorf("base %v %v, want main %s", *p.Base, *p.BaseCommit, head)
	case p.DefaultType == nil || *p.DefaultType != "feat" || len(p.Types) == 0:
		t.Errorf("types %v default %v", p.Types, p.DefaultType)
	}
	if _, err := os.Stat(want); err == nil {
		t.Error("the plan created the worktree")
	}
}

// Every problem the plan knows of is named, and a plan with one has no token.
func TestNewPlanNamesItsProblems(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	if _, err := New(ctx, "fix/taken", NewOptions{NoSetup: true}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ctx.Scheme().Dir("fix", "squatted"), 0o755); err != nil {
		t.Fatal(err)
	}
	for spec, tc := range map[string]struct {
		opts NewOptions
		want string
	}{
		"fix/taken":    {NewOptions{}, "branchExists,pathExists"},
		"fix/squatted": {NewOptions{}, "pathExists"},
		"wibble/thing": {NewOptions{}, "unknownType"},
		".":            {NewOptions{}, "invalidName"},
		"a/b/c":        {NewOptions{}, "invalidName"},
		"fix/nobase":   {NewOptions{Base: "no-such-ref"}, "baseMissing"},
	} {
		p := newPlanOf(t, ctx, spec, tc.opts)
		if got := codes(p.Problems); got != tc.want {
			t.Errorf("%s: problems %s, want %s", spec, got, tc.want)
		}
		if p.Token != nil {
			t.Errorf("%s: a plan with problems has a token", spec)
		}
	}
}

// A repository with no wt configuration is a plan that says so, not an error.
func TestNewPlanWithoutConfiguration(t *testing.T) {
	main := committedRepo(t, minimalConf)
	if err := os.RemoveAll(filepath.Join(main, "bin")); err != nil {
		t.Fatal(err)
	}
	ctx := OpenLenient(main, &bytes.Buffer{})
	p := newPlanOf(t, ctx, "fix/x", NewOptions{})
	if p.Configured || codes(p.Problems) != "noConfiguration" {
		t.Errorf("configured %v problems %s", p.Configured, codes(p.Problems))
	}
}

// The checkout plan pins the branch's commit, and names a branch that is
// missing or already checked out.
func TestCheckoutPlanPinsTheBranch(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	main := ctx.Repo.MainRoot
	gitIn(t, main, "branch", "release-2.1")
	p := checkoutPlanOf(t, ctx, "release-2.1", "")
	tip := strings.TrimSpace(gitOut(t, main, "rev-parse", "release-2.1"))
	if p.Command != "checkout" || p.Token == nil || *p.BranchCommit != tip || *p.Work != "release-2.1" ||
		*p.Path != ctx.Scheme().Dir("feat", "release-2.1") {
		t.Fatalf("plan = %+v", p)
	}
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "on main")
	gitIn(t, main, "branch", "-f", "release-2.1", "main")
	if moved := checkoutPlanOf(t, ctx, "release-2.1", ""); moved.Token == nil || *moved.Token == *p.Token {
		t.Error("the token did not change when the branch moved")
	}
	if got := codes(checkoutPlanOf(t, ctx, "nope", "").Problems); got != "branchMissing" {
		t.Errorf("missing branch: %s", got)
	}
	if got := codes(checkoutPlanOf(t, ctx, "main", "").Problems); got != "branchCheckedOut" {
		t.Errorf("checked-out branch: %s", got)
	}
}

// A clean wt new --json: one object, every side effect with its own result,
// and the worktree on the commit the plan showed.
func TestNewJSONReportsEverySideEffect(t *testing.T) {
	ctx := provisionFixture(t, 0, "true", false)
	p := newPlanOf(t, ctx, "fix/login-crash", NewOptions{})
	r, human, err := newJSON(t, ctx, "fix/login-crash", NewOptions{}, *p.Token)
	if err != nil {
		t.Fatalf("%v\n%s", err, human)
	}
	if r.Outcome != CreateCreated || r.Error != nil || *r.Commit != *p.BaseCommit || *r.Path != *p.Path {
		t.Errorf("result = %+v", r)
	}
	want := map[string]string{"worktree": "created", "branch": "created", "config": "done", "provision": "done",
		"submodules": "skipped", "build": "done", "superset": "skipped"}
	var order []string
	for _, s := range r.Steps {
		order = append(order, s.Step)
		if s.Result != want[s.Step] {
			t.Errorf("%s: %s, want %s (%v)", s.Step, s.Result, want[s.Step], s.Reason)
		}
	}
	if strings.Join(order, ",") != "worktree,branch,config,provision,submodules,build,superset" {
		t.Errorf("steps in order %v", order)
	}
	if b := stepOf(r, "branch"); b.Commit == nil || *b.Commit != *p.BaseCommit {
		t.Errorf("branch step %+v", b)
	}
	if !strings.Contains(human, "Creating fix_wt/login-crash at ") {
		t.Errorf("progress = %q", human)
	}
}

// --expect refuses, creating nothing, when the base moved since the plan.
func TestNewExpectRefusesAMovedBase(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	p := newPlanOf(t, ctx, "fix/login-crash", NewOptions{})
	gitIn(t, ctx.Repo.MainRoot, "commit", "-q", "--allow-empty", "-m", "trunk moved")
	before := repoState(t, ctx.Repo.MainRoot)
	r, _, err := newJSON(t, ctx, "fix/login-crash", NewOptions{}, *p.Token)
	if err == nil || r.Outcome != CreateRefused || codes(r.Problems) != "planChanged" {
		t.Errorf("err %v result %+v", err, r)
	}
	if after := repoState(t, ctx.Repo.MainRoot); after != before {
		t.Errorf("a refused run wrote to the repository")
	}
	if _, err := os.Stat(*p.Path); err == nil {
		t.Error("a refused run created the worktree")
	}
	if s := stepOf(r, "worktree"); s.Result != StepNotRun {
		t.Errorf("worktree step %+v", s)
	}
}

// A plan problem refuses the run too, with the problem named.
func TestNewJSONRefusesAPlanProblem(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	r, _, err := newJSON(t, ctx, "wibble/thing", NewOptions{}, "")
	if err == nil || r.Outcome != CreateRefused || codes(r.Problems) != "unknownType" || r.Error == nil {
		t.Errorf("err %v result %+v", err, r)
	}
}

// wt checkout --expect refuses a branch that moved since the plan: the
// exact-name guarantee.
func TestCheckoutExpectRefusesAMovedBranch(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	main := ctx.Repo.MainRoot
	gitIn(t, main, "branch", "release-2.1")
	p := checkoutPlanOf(t, ctx, "release-2.1", "rel21")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "on main")
	gitIn(t, main, "branch", "-f", "release-2.1", "main")
	r, err := checkoutJSON(t, ctx, "release-2.1", "rel21", NewOptions{}, *p.Token)
	if err == nil || r.Outcome != CreateRefused || codes(r.Problems) != "planChanged" {
		t.Errorf("err %v result %+v", err, r)
	}
	if _, err := os.Stat(*p.Path); err == nil {
		t.Error("a refused checkout created the worktree")
	}

	p = checkoutPlanOf(t, ctx, "release-2.1", "rel21")
	r, err = checkoutJSON(t, ctx, "release-2.1", "rel21", NewOptions{NoSetup: true}, *p.Token)
	if err != nil || r.Outcome != CreateCreated {
		t.Fatalf("err %v result %+v", err, r)
	}
	if b := stepOf(r, "branch"); b.Result != StepUntouched || *b.Commit != *p.BranchCommit {
		t.Errorf("branch step %+v", b)
	}
	for _, name := range []string{"config", "provision", "submodules", "build", "superset"} {
		if s := stepOf(r, name); s.Result != StepSkipped {
			t.Errorf("--no-setup: %s is %s", name, s.Result)
		}
	}
}

// A failed provision.sh is created with problems, and still exits non-zero
// as it always has.
func TestNewJSONReportsAFailedProvision(t *testing.T) {
	ctx := provisionFixture(t, 1, "true", false)
	r, _, err := newJSON(t, ctx, "fix/x", NewOptions{}, "")
	if err == nil || r.Outcome != CreateCreatedWithProblems {
		t.Errorf("err %v outcome %s", err, r.Outcome)
	}
	if s := stepOf(r, "provision"); s.Result != StepFailed || s.Reason == nil {
		t.Errorf("provision %+v", s)
	}
	if s := stepOf(r, "build"); s.Result != StepDone {
		t.Errorf("build %+v", s)
	}
}

// A failed build and a failed submodule initialisation are failed steps,
// though the exit code stays 0 as it always has.
func TestNewJSONReportsAFailedBuildAndSubmodules(t *testing.T) {
	ctx := provisionFixture(t, -1, "exit 3", true)
	r, _, err := newJSON(t, ctx, "fix/x", NewOptions{}, "")
	if err != nil || r.Outcome != CreateCreatedWithProblems {
		t.Errorf("err %v outcome %s", err, r.Outcome)
	}
	for _, name := range []string{"build", "submodules"} {
		if s := stepOf(r, name); s.Result != StepFailed || s.Reason == nil {
			t.Errorf("%s %+v", name, s)
		}
	}
	if s := stepOf(r, "provision"); s.Result != StepSkipped {
		t.Errorf("no provision.sh: %+v", s)
	}
}

// --base <oid>, which is how a git client names the base, cuts the branch
// from that commit and gives it no upstream.
func TestNewFromAnOIDSetsNoUpstream(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	main := ctx.Repo.MainRoot
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")
	oid := strings.TrimSpace(gitOut(t, main, "rev-parse", "origin/main"))
	r, _, err := newJSON(t, ctx, "fix/x", NewOptions{Base: oid, NoSetup: true}, "")
	if err != nil || r.Outcome != CreateCreated || *r.Commit != oid {
		t.Fatalf("err %v result %+v", err, r)
	}
	if out, _ := git.Run(main, "config", "--get", "branch.fix_wt/x.merge"); out != "" {
		t.Errorf("the branch tracks %s", out)
	}
}

// A branch that is not where the plan pinned it once the worktree is on it —
// here a post-checkout hook commits — is created with problems, never a
// quiet success.
func TestNewJSONNamesACommitThatIsNotTheBase(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	hook := filepath.Join(ctx.Repo.MainRoot, ".git", "hooks", "post-checkout")
	mustWrite(t, hook, "#!/bin/sh\ngit -c user.email=t@e.com -c user.name=T commit -q --allow-empty -m moved\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	p := newPlanOf(t, ctx, "fix/x", NewOptions{})
	r, _, _ := newJSON(t, ctx, "fix/x", NewOptions{NoSetup: true}, *p.Token)
	if r.Outcome != CreateCreatedWithProblems || codes(r.Problems) != "headMoved" || *r.Commit == *p.BaseCommit {
		t.Errorf("result %+v", r)
	}
}

// Superset's outcome is a step of its own: registered with a running app that
// knows the repository, skipped when it is not running.
func TestNewJSONReportsSuperset(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	optIn(ctx)
	fakeSuperset(t, ctx.Repo.MainRoot, true)
	r, _, err := newJSON(t, ctx, "fix/one", NewOptions{}, "")
	if s := stepOf(r, "superset"); err != nil || s.Result != StepRegistered {
		t.Errorf("err %v superset %+v", err, s)
	}
	if p := newPlanOf(t, ctx, "fix/two", NewOptions{}); !p.Superset {
		t.Error("the plan does not say Superset registration is on")
	}

	stub(t, superset.Status{Exe: "/nope/superset"})
	r, _, _ = newJSON(t, ctx, "fix/two", NewOptions{}, "")
	if s := stepOf(r, "superset"); s.Result != StepSkipped || s.Reason == nil || r.Outcome != CreateCreated {
		t.Errorf("auto, host stopped: %+v %s", s, r.Outcome)
	}
	ctx.Config.SupersetRegister = config.SupersetOn
	r, _, _ = newJSON(t, ctx, "fix/three", NewOptions{}, "")
	if s := stepOf(r, "superset"); s.Result != StepFailed || r.Outcome != CreateCreatedWithProblems {
		t.Errorf("on, host stopped: %+v %s", s, r.Outcome)
	}
}

// A signal writes the one object: the step in flight interrupted, and
// nothing written a second time.
var _ interruptible = (*CreateJournal)(nil)

func TestCreateJournalWritesOneObjectWhenInterrupted(t *testing.T) {
	var out bytes.Buffer
	j := NewCreateJournal(&out, "new")
	j.start(StepWorktree)
	j.finish(StepWorktree, StepCreated, "", "")
	j.start(StepProvision)
	j.interrupted("", "interrupted")
	// Finish would park for good now, as the run does once a signal owns
	// it; the write it would make must not happen a second time.
	j.write(false)
	r := decodeCreate(t, "new", out.Bytes())
	if r.Outcome != CreateInterrupted || stepOf(r, "provision").Result != StepInterrupted ||
		stepOf(r, "build").Result != StepNotRun {
		t.Errorf("%+v", r)
	}
}

// A post-checkout hook that fails makes git worktree add exit non-zero after
// the worktree exists: that is a worktree created with problems, never a
// failure that hides it.
func TestNewJSONReportsAWorktreeAFailingHookLeftBehind(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	hook := filepath.Join(ctx.Repo.MainRoot, ".git", "hooks", "post-checkout")
	mustWrite(t, hook, "#!/bin/sh\necho 'hook says no' >&2\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	r, _, err := newJSON(t, ctx, "fix/x", NewOptions{}, "")
	if err == nil || r.Outcome != CreateCreatedWithProblems || r.Commit == nil {
		t.Errorf("err %v result %+v", err, r)
	}
	if s := stepOf(r, "worktree"); s.Result != StepCreated || s.Reason == nil {
		t.Errorf("worktree step %+v", s)
	}
	if s := stepOf(r, "config"); s.Result != StepNotRun {
		t.Errorf("config step %+v", s)
	}
}

// Only a local branch by that exact name is checked out: a revision
// expression that happens to resolve is not one.
func TestCheckoutPlanTakesOnlyABranchName(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	if got := codes(checkoutPlanOf(t, ctx, "main~0", "rev").Problems); got != "branchMissing" {
		t.Errorf("main~0: %s", got)
	}
}

// A name git would refuse as a branch is an invalid name in the plan, not a
// token for a run that cannot succeed.
func TestNewPlanRefusesABranchNameGitWouldRefuse(t *testing.T) {
	ctx, _ := Open(committedRepo(t, minimalConf))
	for _, spec := range []string{"fix/bad name", "fix/a..b", "fix/x.lock"} {
		if p := newPlanOf(t, ctx, spec, NewOptions{}); codes(p.Problems) != "invalidName" || p.Token != nil {
			t.Errorf("%q: problems %s", spec, codes(p.Problems))
		}
	}
}
