package refs_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/commands/refs"
	"github.com/anders-lindstrom/wt/internal/commands/refs/refstest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// A sweep pins each selected ref and deletes it in one step, drops a
// branch's config, and says how to put each back.
func TestRefsSweepPinsAndDeletes(t *testing.T) {
	t.Parallel()
	ctx, main := testkit.BackupsRepo(t)
	plan, err := refs.RefsPlanJSON(t, ctx, refs.Options{NoFetch: true})
	if err != nil {
		t.Fatal(err)
	}
	r, human, err := refstest.RefsSweepJSON(t, ctx, refs.Options{NoFetch: true, Expect: *plan.Token})
	if err != nil {
		t.Fatalf("%v\n%s", err, human)
	}
	if r.Outcome != kit.OutcomeDone || r.RunID == nil || !refpins.RunIDPattern.MatchString(*r.RunID) || r.Error != nil {
		t.Fatalf("result = %+v\n%s", r, human)
	}
	run := *r.RunID
	for _, id := range []string{"refs/heads/backup/contained", "refs/heads/backup/old", "refs/tags/safe-tag"} {
		it := refs.RefResultOf(t, r, id)
		if it.Result != refs.RefSwept || !it.Pinned || !it.Deleted || it.Pin == nil || len(it.RestoreCommand) != 7 ||
			it.RestoreCommand[3] != run {
			t.Errorf("%s = %+v", id, it)
			continue
		}
		if got := testkit.GitOut(t, main, "rev-parse", *it.Pin); got != it.Object {
			t.Errorf("%s: pin at %s, want %s", id, got, it.Object)
		}
	}
	if pin := retire.Deref(refs.RefResultOf(t, r, "refs/tags/safe-tag").Pin); pin != refpins.SweptPrefix+run+"/tags/safe-tag" {
		t.Errorf("tag pin = %s", pin)
	}
	if it := refs.RefResultOf(t, r, "refs/heads/backup/rebased"); it.Result != refs.RefNotSelected || it.Pinned {
		t.Errorf("young = %+v", it)
	}
	if it := refs.RefResultOf(t, r, "refs/heads/backup/checked"); it.Result != refs.RefResultKept || retire.Deref(it.Reason) != refs.KeptWorktree {
		t.Errorf("kept = %+v", it)
	}
	if ctx.Repo.BranchExists("backup/old") || !ctx.Repo.BranchExists("backup/rebased") {
		t.Error("backup/old must be gone and backup/rebased kept")
	}
	if out, _ := exec.Command("git", "-C", main, "config", "branch.backup/contained.description").Output(); len(out) > 0 {
		t.Errorf("the branch's config is left: %s", out)
	}
}

// --expect refuses a sweep whose plan changed, moving nothing.
func TestRefsSweepExpectRefusesAChangedPlan(t *testing.T) {
	t.Parallel()
	ctx, main := testkit.BackupsRepo(t)
	plan, _ := refs.RefsPlanJSON(t, ctx, refs.Options{NoFetch: true})
	testkit.GitIn(t, main, "branch", "backup/new", "main")
	before := testkit.RepoState(t, main)
	r, _, err := refstest.RefsSweepJSON(t, ctx, refs.Options{NoFetch: true, Expect: *plan.Token})
	if !errors.Is(err, refs.ErrRefsPlanChanged) || r.Outcome != kit.OutcomeRefused || r.Error == nil || r.RunID != nil {
		t.Fatalf("err = %v, result = %+v", err, r)
	}
	if testkit.RepoState(t, main) != before {
		t.Error("a refused sweep changed the repository")
	}
}

// --only replaces the selection, under the same token; it takes a young
// backup, and refuses the lot when it names one that is kept or unknown.
func TestRefsSweepOnly(t *testing.T) {
	t.Parallel()
	ctx, _ := testkit.BackupsRepo(t)
	full, _ := refs.RefsPlanJSON(t, ctx, refs.Options{NoFetch: true})
	only := []string{"refs/heads/backup/rebased"}
	p, err := refs.RefsPlanJSON(t, ctx, refs.Options{NoFetch: true, Only: only})
	if err != nil || retire.Deref(p.Token) != retire.Deref(full.Token) {
		t.Fatalf("--only changed the token or failed: %v", err)
	}
	for _, it := range p.Items {
		if it.Selected != (it.ID == only[0]) {
			t.Errorf("%s selected = %v", it.ID, it.Selected)
		}
	}
	for _, bad := range []string{"refs/heads/backup/checked", "refs/heads/nope"} {
		p, err := refs.RefsPlanJSON(t, ctx, refs.Options{NoFetch: true, Only: []string{only[0], bad}})
		if err == nil || p.Token != nil || !strings.Contains(retire.Deref(p.Error), bad) {
			t.Errorf("--only %s: err %v, plan %+v", bad, err, p.Error)
		}
		r, _, err := refstest.RefsSweepJSON(t, ctx, refs.Options{NoFetch: true, Only: []string{only[0], bad}})
		if err == nil || r.Outcome != kit.OutcomeRefused || !ctx.Repo.BranchExists("backup/rebased") {
			t.Errorf("--only %s swept: %v %+v", bad, err, r)
		}
	}
	r, _, err := refstest.RefsSweepJSON(t, ctx, refs.Options{NoFetch: true, Only: only, Expect: *full.Token})
	if err != nil || r.Outcome != kit.OutcomeDone || refs.RefResultOf(t, r, only[0]).Result != refs.RefSwept ||
		refs.RefResultOf(t, r, "refs/heads/backup/old").Result != refs.RefNotSelected {
		t.Fatalf("%v %+v", err, r)
	}
}

// A restore puts each ref back at its object where the name is free, keeps
// the one whose name was taken, and leaves no pin of what went back.
func TestRefsRestorePutsBack(t *testing.T) {
	t.Parallel()
	ctx, main := testkit.BackupsRepo(t)
	r := refstest.SweepAll(t, ctx, refs.Options{})
	run := *r.RunID
	old := refs.RefResultOf(t, r, "refs/heads/backup/old")
	testkit.GitIn(t, main, "branch", "backup/contained", "main")
	p, err := refstest.RestorePlanJSON(t, ctx, run, nil)
	if err != nil || p.Token == nil || !strings.HasPrefix(*p.Token, "rr1-") {
		t.Fatalf("%v %+v", err, p)
	}
	actions := map[string]string{}
	for _, it := range p.Items {
		actions[it.ID] = it.Action
	}
	if actions["refs/heads/backup/old"] != refs.RestoreCreate || actions["refs/heads/backup/contained"] != refs.RestoreOccupied ||
		actions["refs/tags/safe-tag"] != refs.RestoreCreate {
		t.Fatalf("actions = %v", actions)
	}
	res, err := refstest.RestoreJSON(t, ctx, run, refs.RestoreRefsOptions{Expect: *p.Token})
	if err != nil || res.Outcome != kit.OutcomeDone {
		t.Fatalf("%v %+v", err, res)
	}
	if got := testkit.GitOut(t, main, "rev-parse", "refs/heads/backup/old"); got != old.Object {
		t.Errorf("backup/old back at %s, want %s", got, old.Object)
	}
	if testkit.GitOut(t, main, "cat-file", "-t", "refs/tags/safe-tag") != "tag" {
		t.Error("the annotated tag must come back as the tag object")
	}
	pins := testkit.GitOut(t, main, "for-each-ref", "--format=%(refname)", refpins.SweptPrefix+run+"/")
	if pins != refpins.SweptPrefix+run+"/heads/backup/contained\n"+refpins.SweptPrefix+run+"/meta" {
		t.Errorf("pins left: %q", pins)
	}
	if _, err := refstest.RestorePlanJSON(t, ctx, run, []string{"refs/heads/backup/contained"}); err == nil {
		t.Error("--only on a name that is taken must refuse")
	}
	if _, err := refstest.RestorePlanJSON(t, ctx, "20200101T000000Z-0000", nil); err == nil {
		t.Error("a run that is not there must be an error")
	}
}

// --only takes one id per flag, and a ref name may carry a comma.
func TestRefsOnlyTakesAnIDWithAComma(t *testing.T) {
	t.Parallel()
	ctx, main := testkit.BackupsRepo(t)
	testkit.GitIn(t, main, "branch", "backup/a,b", "main")
	id := "refs/heads/backup/a,b"
	r := refstest.SweepAll(t, ctx, refs.Options{Only: []string{id}})
	it := refs.RefResultOf(t, r, id)
	if it.Result != refs.RefSwept || !slices.Equal(it.RestoreCommand, []string{"wt", "refs", "restore", *r.RunID, "--only", id, "--yes"}) {
		t.Fatalf("%+v", it)
	}
	res, err := refstest.RestoreJSON(t, ctx, *r.RunID, refs.RestoreRefsOptions{Only: []string{id}})
	if err != nil || res.Outcome != kit.OutcomeDone || !ctx.Repo.BranchExists("backup/a,b") {
		t.Fatalf("%v %+v", err, res)
	}
}

// With --remote, origin's backups are swept only when origin's own refs
// contain them, never with an open pull request or when GitHub cannot say;
// a swept one is pinned here, gone from origin, and restore pushes it back.
func TestRefsSweepRemote(t *testing.T) {
	t.Parallel()
	ctx, main, origin := testkit.SweepRepo(t)
	first := testkit.GitOut(t, main, "rev-parse", "main")
	testkit.GitIn(t, main, "commit", "-q", "--allow-empty", "-m", "second")
	testkit.GitIn(t, main, "branch", "backup/landed", first)
	testkit.GitIn(t, main, "branch", "backup/pr", first)
	testkit.GitIn(t, main, "branch", "backup/unique", "main")
	testkit.CommitOn(t, main, "backup/unique", "only here and there", time.Now())
	testkit.GitIn(t, main, "tag", "-a", "-m", "safety", "safe-remote", first)
	testkit.GitIn(t, main, "push", "-q", "origin", "main", "backup/landed", "backup/pr", "backup/unique", "safe-remote")
	testkit.GitIn(t, main, "fetch", "-q", "origin")
	for _, b := range []string{"backup/landed", "backup/pr", "backup/unique"} {
		testkit.GitIn(t, main, "branch", "-D", b)
	}
	testkit.GitIn(t, main, "tag", "-d", "safe-remote")
	testkit.GitIn(t, main, "fetch", "-q", "origin", "refs/tags/safe-remote:refs/tags/elsewhere")

	prs := func([]string) (map[string]bool, error) { return map[string]bool{"backup/pr": true}, nil }
	p, err := refs.RefsPlanJSON(t, ctx, refs.Options{Remote: true, OpenPRs: prs})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Remote || !p.Fetched {
		t.Errorf("plan = %+v", p)
	}
	if it := refs.RefPlanItem(t, p, "origin:refs/heads/backup/landed"); it.Kind != refpins.RefRemoteBranch ||
		it.Category != refs.RefBackupContained || !it.Selected || retire.Deref(it.ContainedIn) != "origin:refs/heads/main" {
		t.Errorf("landed = %+v", it)
	}
	if it := refs.RefPlanItem(t, p, "origin:refs/heads/backup/pr"); it.Category != refs.RefKept ||
		!slices.Equal(it.Kept, []string{refs.KeptPullRequestOpen}) {
		t.Errorf("pr = %+v", it)
	}
	if it := refs.RefPlanItem(t, p, "origin:refs/heads/backup/unique"); it.Category != refs.RefKept ||
		!slices.Equal(it.Kept, []string{refs.KeptNotContained}) {
		t.Errorf("unique = %+v", it)
	}
	if it := refs.RefPlanItem(t, p, "origin:refs/tags/safe-remote"); it.Kind != refpins.RefRemoteTag || it.Category != refs.RefBackupContained ||
		it.Annotated == nil || !*it.Annotated {
		t.Errorf("tag = %+v", it)
	}
	unknown := func([]string) (map[string]bool, error) { return nil, errors.New("gh is not on your PATH") }
	if p, _ := refs.RefsPlanJSON(t, ctx, refs.Options{Remote: true, NoFetch: true, OpenPRs: unknown}); !slices.Contains(
		refs.RefPlanItem(t, p, "origin:refs/heads/backup/landed").Kept, refs.KeptPullRequestUnknown) {
		t.Error("without GitHub every remote branch is kept")
	}

	r, human, err := refstest.RefsSweepJSON(t, ctx, refs.Options{Remote: true, OpenPRs: prs, Expect: *p.Token})
	if err != nil || r.Outcome != kit.OutcomeDone {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	run := *r.RunID
	for _, ref := range []string{"refs/heads/backup/landed", "refs/tags/safe-remote"} {
		if out := testkit.GitOut(t, origin, "for-each-ref", ref); out != "" {
			t.Errorf("origin still has %s", ref)
		}
		it := refs.RefResultOf(t, r, "origin:"+ref)
		if it.Result != refs.RefSwept || !it.Pinned || !it.Deleted {
			t.Errorf("%s = %+v", ref, it)
		}
	}
	if pin := refpins.PinOf(run, refpins.RefRemoteBranch, "backup/landed"); testkit.GitOut(t, main, "rev-parse", pin) != first {
		t.Errorf("pin %s is not at %s", pin, first)
	}
	res, err := refstest.RestoreJSON(t, ctx, run, refs.RestoreRefsOptions{})
	if err != nil || res.Outcome != kit.OutcomeDone {
		t.Fatalf("%v %+v", err, res)
	}
	if testkit.GitOut(t, origin, "rev-parse", "refs/heads/backup/landed") != first ||
		testkit.GitOut(t, origin, "cat-file", "-t", "refs/tags/safe-remote") != "tag" {
		t.Error("origin must have both back")
	}
	if left := testkit.GitOut(t, main, "for-each-ref", refpins.SweptPrefix); left != "" {
		t.Errorf("a run restored whole leaves nothing behind: %s", left)
	}
}

// --run-id names the run; one that has refs already refuses, moving nothing.
func TestRefsSweepRunID(t *testing.T) {
	t.Parallel()
	ctx, main := testkit.BackupsRepo(t)
	id := "20260930T091500Z-00c0ffee"
	r := refstest.SweepAll(t, ctx, refs.Options{RunID: id, Only: []string{"refs/heads/backup/contained"}})
	if retire.Deref(r.RunID) != id {
		t.Fatalf("run = %v", r.RunID)
	}
	var m refpins.RunMeta
	if err := json.Unmarshal([]byte(testkit.GitOut(t, main, "cat-file", "blob", refpins.MetaRef(id))), &m); err != nil ||
		m.RunID != id || m.SweptAt == 0 || m.Endpoint != nil || m.Repo == "" {
		t.Errorf("meta = %+v, %v", m, err)
	}
	before := testkit.RepoState(t, main)
	r, _, err := refstest.RefsSweepJSON(t, ctx, refs.Options{NoFetch: true, RunID: id})
	if err == nil || r.Outcome != kit.OutcomeRefused || len(r.Problems) != 1 || r.Problems[0].Code != refs.ProblemRunIDTaken {
		t.Errorf("a reused run id: %v %+v", err, r)
	}
	if testkit.RepoState(t, main) != before {
		t.Error("a refused run changed the repository")
	}
	if _, _, err := refstest.RefsSweepJSON(t, ctx, refs.Options{NoFetch: true, RunID: "tomorrow"}); err == nil {
		t.Error("a run id not in wt's format must refuse")
	}
}

// A push origin rejects keeps the pin; once origin holds the ref, a restore
// only drops the pin.
func TestRefsRestoreRemoteKeepsThePinUntilOriginHasIt(t *testing.T) {
	t.Parallel()
	ctx, main, origin, run := refstest.RemoteBackups(t)
	hook := filepath.Join(origin, "hooks", "pre-receive")
	testkit.MustWrite(t, hook, "#!/bin/sh\necho no >&2\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	pin := refpins.PinOf(run, refpins.RefRemoteBranch, "backup/landed")
	id := "origin:refs/heads/backup/landed"
	res, err := refstest.RestoreJSON(t, ctx, run, refs.RestoreRefsOptions{Only: []string{id}})
	it := res.Items[slices.IndexFunc(res.Items, func(i *refs.RefRestoreItem) bool { return i.ID == id })]
	if err == nil || it.Result != refs.RefFailed || it.PinDeleted || it.Restored {
		t.Fatalf("rejected push: %v %+v", err, it)
	}
	if _, ok, _ := refpins.RefValue(main, pin); !ok {
		t.Fatal("a rejected push must keep the pin")
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, main, "push", "-q", "origin", pin+":refs/heads/backup/landed")
	p, _ := refstest.RestorePlanJSON(t, ctx, run, []string{id})
	if p.Items[slices.IndexFunc(p.Items, func(i refs.RefRestorePlanItem) bool { return i.ID == id })].Action != refs.RestoreNone {
		t.Fatalf("origin has it back: %+v", p.Items)
	}
	res, err = refstest.RestoreJSON(t, ctx, run, refs.RestoreRefsOptions{Only: []string{id}})
	if err != nil || res.Outcome != kit.OutcomeDone {
		t.Fatalf("%v %+v", err, res)
	}
	if _, ok, _ := refpins.RefValue(main, pin); ok {
		t.Error("the pin must go once origin has the ref")
	}
}

// A restore whose ref is already back drops the pin only while the ref is
// still there; a run id that is not one is still an object its schema takes.
func TestRefsRestoreNoneKeepsThePinOfARefThatWent(t *testing.T) {
	t.Parallel()
	ctx, main := testkit.BackupsRepo(t)
	r := refstest.SweepAll(t, ctx, refs.Options{Only: []string{"refs/heads/backup/old"}})
	run := *r.RunID
	obj := refs.RefResultOf(t, r, "refs/heads/backup/old").Object
	testkit.GitIn(t, main, "branch", "backup/old", obj)
	plan, err := refs.PlanRefsRestore(ctx, run)
	if err != nil || plan.Rows[0].Action != refs.RestoreNone {
		t.Fatalf("%v %+v", err, plan.Rows)
	}
	testkit.GitIn(t, main, "branch", "-D", "backup/old")
	st := refs.RestoreRef(main, "", plan.Rows[0])
	if st.Kept == "" || st.PinDeleted {
		t.Errorf("state = %+v", st)
	}
	if _, ok, _ := refpins.RefValue(main, plan.Rows[0].Pin); !ok {
		t.Error("the pin must stay while the ref is gone")
	}
	if _, err := refstest.RestorePlanJSON(t, ctx, "not-a-run", nil); err == nil {
		t.Error("a run id that is not one must be an error")
	}
	res, err := refstest.RestoreJSON(t, ctx, "not-a-run", refs.RestoreRefsOptions{})
	if err == nil || res.Outcome != kit.OutcomeRefused || res.RunID != "not-a-run" {
		t.Errorf("%v %+v", err, res)
	}
}
