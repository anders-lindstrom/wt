package provision

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/git"
)

func RunNewJSON(t *testing.T, ctx *kit.Context, spec string, opts NewOptions, expect string) (CreateResult, string, error) {
	t.Helper()
	var out, human bytes.Buffer
	err := NewJSON(ctx, spec, opts, expect, NewCreateJournal(&out, "new"), &human)
	return decodeCreate(t, "new", out.Bytes()), human.String(), err
}

func decodeCreate(t *testing.T, name string, out []byte) CreateResult {
	t.Helper()
	testkit.ValidateJSON(t, name, out)
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

func StepOf(r CreateResult, name string) CreateStep {
	for _, s := range r.Steps {
		if s.Step == name {
			return s
		}
	}
	return CreateStep{Step: name, Result: "<missing>"}
}

// A failed provision.sh is created with problems, and still exits non-zero
// as it always has.
func TestNewJSONReportsAFailedProvision(t *testing.T) {
	t.Parallel()
	ctx := ProvisionFixture(t, 1, "true", false)
	r, _, err := RunNewJSON(t, ctx, "fix/x", NewOptions{}, "")
	if err == nil || r.Outcome != CreateCreatedWithProblems {
		t.Errorf("err %v outcome %s", err, r.Outcome)
	}
	if s := StepOf(r, "provision"); s.Result != kit.StepFailed || s.Reason == nil {
		t.Errorf("provision %+v", s)
	}
	if s := StepOf(r, "build"); s.Result != kit.StepDone {
		t.Errorf("build %+v", s)
	}
}

// A failed build and a failed submodule initialisation are failed steps,
// though the exit code stays 0 as it always has.
func TestNewJSONReportsAFailedBuildAndSubmodules(t *testing.T) {
	t.Parallel()
	ctx := ProvisionFixture(t, -1, "exit 3", true)
	r, _, err := RunNewJSON(t, ctx, "fix/x", NewOptions{}, "")
	if err != nil || r.Outcome != CreateCreatedWithProblems {
		t.Errorf("err %v outcome %s", err, r.Outcome)
	}
	for _, name := range []string{"build", "submodules"} {
		if s := StepOf(r, name); s.Result != kit.StepFailed || s.Reason == nil {
			t.Errorf("%s %+v", name, s)
		}
	}
	if s := StepOf(r, "provision"); s.Result != kit.StepSkipped {
		t.Errorf("no provision.sh: %+v", s)
	}
}

// --base <oid>, which is how a git client names the base, cuts the branch
// from that commit and gives it no upstream.
func TestNewFromAnOIDSetsNoUpstream(t *testing.T) {
	t.Parallel()
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	main := ctx.Repo.MainRoot
	testkit.GitIn(t, main, "remote", "add", "origin", main)
	testkit.GitIn(t, main, "fetch", "-q", "origin")
	oid := strings.TrimSpace(testkit.GitOut(t, main, "rev-parse", "origin/main"))
	r, _, err := RunNewJSON(t, ctx, "fix/x", NewOptions{Base: oid, NoSetup: true}, "")
	if err != nil || r.Outcome != CreateCreated || *r.Commit != oid {
		t.Fatalf("err %v result %+v", err, r)
	}
	if out, _ := git.Run(main, "config", "--get", "branch.fix_wt/x.merge"); out != "" {
		t.Errorf("the branch tracks %s", out)
	}
}

func TestCreateJournalWritesOneObjectWhenInterrupted(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	j := NewCreateJournal(&out, "new")
	j.start(kit.StepWorktree)
	j.finish(kit.StepWorktree, kit.StepCreated, "", "")
	j.start(kit.StepProvision)
	j.Interrupted("", "interrupted")
	// Finish would park for good now, as the run does once a signal owns
	// it; the write it would make must not happen a second time.
	j.write(false)
	r := decodeCreate(t, "new", out.Bytes())
	if r.Outcome != CreateInterrupted || StepOf(r, "provision").Result != kit.StepInterrupted ||
		StepOf(r, "build").Result != kit.StepNotRun {
		t.Errorf("%+v", r)
	}
}

// A post-checkout hook that fails makes git worktree add exit non-zero after
// the worktree exists: that is a worktree created with problems, never a
// failure that hides it.
func TestNewJSONReportsAWorktreeAFailingHookLeftBehind(t *testing.T) {
	t.Parallel()
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	hook := filepath.Join(ctx.Repo.MainRoot, ".git", "hooks", "post-checkout")
	testkit.MustWrite(t, hook, "#!/bin/sh\necho 'hook says no' >&2\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	r, _, err := RunNewJSON(t, ctx, "fix/x", NewOptions{}, "")
	if err == nil || r.Outcome != CreateCreatedWithProblems || r.Commit == nil {
		t.Errorf("err %v result %+v", err, r)
	}
	if s := StepOf(r, "worktree"); s.Result != kit.StepCreated || s.Reason == nil {
		t.Errorf("worktree step %+v", s)
	}
	if s := StepOf(r, "config"); s.Result != kit.StepNotRun {
		t.Errorf("config step %+v", s)
	}
}
