package provision_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/superset"
)

func checkoutJSON(t *testing.T, ctx *kit.Context, branch, work string, opts provision.NewOptions, expect string) (provision.CheckoutResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	err := provision.CheckoutJSON(ctx, branch, work, opts, expect, provision.NewCreateJournal(&out, "checkout"), &human)
	testkit.ValidateJSON(t, "checkout", out.Bytes())
	var r provision.CheckoutResult
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	return r, err
}

// The plan says what wt new would make, with a token, and makes nothing.
func TestNewPlanNamesWhatItWouldCreateAndWritesNothing(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	before := testkit.RepoState(t, ctx.Repo.MainRoot)
	p := provisiontest.NewPlanOf(t, ctx, "fix/login-crash", provision.NewOptions{})
	if after := testkit.RepoState(t, ctx.Repo.MainRoot); after != before {
		t.Errorf("the plan wrote to the repository:\nbefore %s\nafter %s", before, after)
	}
	head := strings.TrimSpace(testkit.GitOut(t, ctx.Repo.MainRoot, "rev-parse", "HEAD"))
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
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if _, err := provision.New(ctx, "fix/taken", provision.NewOptions{NoSetup: true}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ctx.Scheme().Dir("fix", "squatted"), 0o755); err != nil {
		t.Fatal(err)
	}
	for spec, tc := range map[string]struct {
		opts provision.NewOptions
		want string
	}{
		"fix/taken":    {provision.NewOptions{}, "branchExists,pathExists"},
		"fix/squatted": {provision.NewOptions{}, "pathExists"},
		"wibble/thing": {provision.NewOptions{}, "unknownType"},
		".":            {provision.NewOptions{}, "invalidName"},
		"a/b/c":        {provision.NewOptions{}, "invalidName"},
		"fix/nobase":   {provision.NewOptions{Base: "no-such-ref"}, "baseMissing"},
	} {
		p := provisiontest.NewPlanOf(t, ctx, spec, tc.opts)
		if got := provisiontest.Codes(p.Problems); got != tc.want {
			t.Errorf("%s: problems %s, want %s", spec, got, tc.want)
		}
		if p.Token != nil {
			t.Errorf("%s: a plan with problems has a token", spec)
		}
	}
}

// The checkout plan pins the branch's commit, and names a branch that is
// missing or already checked out.
func TestCheckoutPlanPinsTheBranch(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	main := ctx.Repo.MainRoot
	testkit.GitIn(t, main, "branch", "release-2.1")
	p := provisiontest.CheckoutPlanOf(t, ctx, "release-2.1", "")
	tip := strings.TrimSpace(testkit.GitOut(t, main, "rev-parse", "release-2.1"))
	if p.Command != "checkout" || p.Token == nil || *p.BranchCommit != tip || *p.Work != "release-2.1" ||
		*p.Path != ctx.Scheme().Dir("feat", "release-2.1") {
		t.Fatalf("plan = %+v", p)
	}
	testkit.GitIn(t, main, "commit", "-q", "--allow-empty", "-m", "on main")
	testkit.GitIn(t, main, "branch", "-f", "release-2.1", "main")
	if moved := provisiontest.CheckoutPlanOf(t, ctx, "release-2.1", ""); moved.Token == nil || *moved.Token == *p.Token {
		t.Error("the token did not change when the branch moved")
	}
	if got := provisiontest.Codes(provisiontest.CheckoutPlanOf(t, ctx, "nope", "").Problems); got != "branchMissing" {
		t.Errorf("missing branch: %s", got)
	}
	if got := provisiontest.Codes(provisiontest.CheckoutPlanOf(t, ctx, "main", "").Problems); got != "branchCheckedOut" {
		t.Errorf("checked-out branch: %s", got)
	}
}

// A clean wt new --json: one object, every side effect with its own result,
// and the worktree on the commit the plan showed.
func TestNewJSONReportsEverySideEffect(t *testing.T) {
	ctx := provision.ProvisionFixture(t, 0, "true", false)
	p := provisiontest.NewPlanOf(t, ctx, "fix/login-crash", provision.NewOptions{})
	r, human, err := provision.RunNewJSON(t, ctx, "fix/login-crash", provision.NewOptions{}, *p.Token)
	if err != nil {
		t.Fatalf("%v\n%s", err, human)
	}
	if r.Outcome != provision.CreateCreated || r.Error != nil || *r.Commit != *p.BaseCommit || *r.Path != *p.Path {
		t.Errorf("result = %+v", r)
	}
	want := map[string]string{"worktree": "created", "branch": "created", "config": "done", "cache": "skipped", "provision": "done",
		"submodules": "skipped", "build": "done", "superset": "skipped"}
	var order []string
	for _, s := range r.Steps {
		order = append(order, s.Step)
		if s.Result != want[s.Step] {
			t.Errorf("%s: %s, want %s (%v)", s.Step, s.Result, want[s.Step], s.Reason)
		}
	}
	if strings.Join(order, ",") != "worktree,branch,config,cache,provision,submodules,build,superset" {
		t.Errorf("steps in order %v", order)
	}
	if b := provision.StepOf(r, "branch"); b.Commit == nil || *b.Commit != *p.BaseCommit {
		t.Errorf("branch step %+v", b)
	}
	if !strings.Contains(human, "Creating fix_wt/login-crash at ") {
		t.Errorf("progress = %q", human)
	}
}

// --expect refuses, creating nothing, when the base moved since the plan.
func TestNewExpectRefusesAMovedBase(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	p := provisiontest.NewPlanOf(t, ctx, "fix/login-crash", provision.NewOptions{})
	testkit.GitIn(t, ctx.Repo.MainRoot, "commit", "-q", "--allow-empty", "-m", "trunk moved")
	before := testkit.RepoState(t, ctx.Repo.MainRoot)
	r, _, err := provision.RunNewJSON(t, ctx, "fix/login-crash", provision.NewOptions{}, *p.Token)
	if err == nil || r.Outcome != provision.CreateRefused || provisiontest.Codes(r.Problems) != "planChanged" {
		t.Errorf("err %v result %+v", err, r)
	}
	if after := testkit.RepoState(t, ctx.Repo.MainRoot); after != before {
		t.Errorf("a refused run wrote to the repository")
	}
	if _, err := os.Stat(*p.Path); err == nil {
		t.Error("a refused run created the worktree")
	}
	if s := provision.StepOf(r, "worktree"); s.Result != kit.StepNotRun {
		t.Errorf("worktree step %+v", s)
	}
}

// A plan problem refuses the run too, with the problem named.
func TestNewJSONRefusesAPlanProblem(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	r, _, err := provision.RunNewJSON(t, ctx, "wibble/thing", provision.NewOptions{}, "")
	if err == nil || r.Outcome != provision.CreateRefused || provisiontest.Codes(r.Problems) != "unknownType" || r.Error == nil {
		t.Errorf("err %v result %+v", err, r)
	}
}

// wt checkout --expect refuses a branch that moved since the plan: the
// exact-name guarantee.
func TestCheckoutExpectRefusesAMovedBranch(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	main := ctx.Repo.MainRoot
	testkit.GitIn(t, main, "branch", "release-2.1")
	p := provisiontest.CheckoutPlanOf(t, ctx, "release-2.1", "rel21")
	testkit.GitIn(t, main, "commit", "-q", "--allow-empty", "-m", "on main")
	testkit.GitIn(t, main, "branch", "-f", "release-2.1", "main")
	r, err := checkoutJSON(t, ctx, "release-2.1", "rel21", provision.NewOptions{}, *p.Token)
	if err == nil || r.Outcome != provision.CreateRefused || provisiontest.Codes(r.Problems) != "planChanged" {
		t.Errorf("err %v result %+v", err, r)
	}
	if _, err := os.Stat(*p.Path); err == nil {
		t.Error("a refused checkout created the worktree")
	}

	p = provisiontest.CheckoutPlanOf(t, ctx, "release-2.1", "rel21")
	r, err = checkoutJSON(t, ctx, "release-2.1", "rel21", provision.NewOptions{NoSetup: true}, *p.Token)
	if err != nil || r.Outcome != provision.CreateCreated {
		t.Fatalf("err %v result %+v", err, r)
	}
	if b := provision.StepOf(r.CreateResult, "branch"); b.Result != kit.StepUntouched || *b.Commit != *p.BranchCommit {
		t.Errorf("branch step %+v", b)
	}
	for _, name := range []string{"config", "cache", "provision", "submodules", "build", "superset"} {
		if s := provision.StepOf(r.CreateResult, name); s.Result != kit.StepSkipped {
			t.Errorf("--no-setup: %s is %s", name, s.Result)
		}
	}
}

// A branch that is not where the plan pinned it once the worktree is on it —
// here a post-checkout hook commits — is created with problems, never a
// quiet success.
func TestNewJSONNamesACommitThatIsNotTheBase(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	hook := filepath.Join(ctx.Repo.MainRoot, ".git", "hooks", "post-checkout")
	testkit.MustWrite(t, hook, "#!/bin/sh\ngit -c user.email=t@e.com -c user.name=T commit -q --allow-empty -m moved\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	p := provisiontest.NewPlanOf(t, ctx, "fix/x", provision.NewOptions{})
	r, _, _ := provision.RunNewJSON(t, ctx, "fix/x", provision.NewOptions{NoSetup: true}, *p.Token)
	if r.Outcome != provision.CreateCreatedWithProblems || provisiontest.Codes(r.Problems) != "headMoved" || *r.Commit == *p.BaseCommit {
		t.Errorf("result %+v", r)
	}
}

// Superset's outcome is a step of its own: registered with a running app that
// knows the repository, skipped when it is not running.
func TestNewJSONReportsSuperset(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	testkit.OptIn(ctx)
	provisiontest.FakeSuperset(t, ctx.Repo.MainRoot, true)
	r, _, err := provision.RunNewJSON(t, ctx, "fix/one", provision.NewOptions{}, "")
	if s := provision.StepOf(r, "superset"); err != nil || s.Result != kit.StepRegistered {
		t.Errorf("err %v superset %+v", err, s)
	}
	if p := provisiontest.NewPlanOf(t, ctx, "fix/two", provision.NewOptions{}); !p.Superset {
		t.Error("the plan does not say Superset registration is on")
	}

	provisiontest.Stub(t, superset.Status{Exe: "/nope/superset"})
	r, _, _ = provision.RunNewJSON(t, ctx, "fix/two", provision.NewOptions{}, "")
	if s := provision.StepOf(r, "superset"); s.Result != kit.StepSkipped || s.Reason == nil || r.Outcome != provision.CreateCreated {
		t.Errorf("auto, host stopped: %+v %s", s, r.Outcome)
	}
	ctx.Config.SupersetRegister = config.SupersetOn
	r, _, _ = provision.RunNewJSON(t, ctx, "fix/three", provision.NewOptions{}, "")
	if s := provision.StepOf(r, "superset"); s.Result != kit.StepFailed || r.Outcome != provision.CreateCreatedWithProblems {
		t.Errorf("on, host stopped: %+v %s", s, r.Outcome)
	}
}

// Only a local branch by that exact name is checked out: a revision
// expression that happens to resolve is not one.
func TestCheckoutPlanTakesOnlyABranchName(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if got := provisiontest.Codes(provisiontest.CheckoutPlanOf(t, ctx, "main~0", "rev").Problems); got != "branchMissing" {
		t.Errorf("main~0: %s", got)
	}
}

// A name git would refuse as a branch is an invalid name in the plan, not a
// token for a run that cannot succeed.
func TestNewPlanRefusesABranchNameGitWouldRefuse(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	for _, spec := range []string{"fix/bad name", "fix/a..b", "fix/x.lock"} {
		if p := provisiontest.NewPlanOf(t, ctx, spec, provision.NewOptions{}); provisiontest.Codes(p.Problems) != "invalidName" || p.Token != nil {
			t.Errorf("%q: problems %s", spec, provisiontest.Codes(p.Problems))
		}
	}
}

// --no-cache reaches the cache step above Setup: the same repository clones
// without it and reports the step skipped with it.
func TestNewJSONNoCacheSkipsTheCacheStep(t *testing.T) {
	for _, tc := range []struct {
		noCache bool
		result  string
		reason  string
	}{{true, "skipped", "--no-cache"}, {false, "done", ""}} {
		main := testkit.CommittedRepo(t, testkit.MinimalConf+"WORKTREE_CACHE_DIRS=(node_modules)\n")
		testkit.MustWrite(t, filepath.Join(main, "node_modules", "left-pad", "index.js"), "module.exports = 1")
		ctx, err := kit.Open(main)
		if err != nil {
			t.Fatal(err)
		}
		provision.WithCloner(t, func(_, to string) error { return os.Mkdir(to, 0o755) })
		opts := provision.NewOptions{NoCache: tc.noCache, SkipBuild: true}
		p := provisiontest.NewPlanOf(t, ctx, "fix/login-crash", opts)
		r, human, err := provision.RunNewJSON(t, ctx, "fix/login-crash", opts, *p.Token)
		if err != nil {
			t.Fatalf("%v\n%s", err, human)
		}
		s := provision.StepOf(r, "cache")
		reason := ""
		if s.Reason != nil {
			reason = *s.Reason
		}
		if s.Result != tc.result || reason != tc.reason {
			t.Errorf("NoCache=%v: cache step %s %q, want %s %q", tc.noCache, s.Result, reason, tc.result, tc.reason)
		}
	}
}
