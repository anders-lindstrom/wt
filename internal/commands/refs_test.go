package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// gitAt runs git in dir as if at when: commits and reflog entries carry that
// time, which is how a test makes a backup old.
func gitAt(t *testing.T, dir string, when time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	stamp := fmt.Sprintf("%d +0000", when.Unix())
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+stamp, "GIT_AUTHOR_DATE="+stamp,
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitOn puts one empty commit on branch, made at when, and returns it.
func commitOn(t *testing.T, main, branch, msg string, when time.Time) string {
	t.Helper()
	tree := gitOut(t, main, "rev-parse", branch+"^{tree}")
	c := gitAt(t, main, when, "commit-tree", "-p", branch, "-m", msg, tree)
	gitAt(t, main, when, "update-ref", "refs/heads/"+branch, c)
	return c
}

var noPRs = func([]string) (map[string]bool, error) { return map[string]bool{}, nil }

func refsPlanJSON(t *testing.T, ctx *Context, opts RefsOptions) (RefsPlanOutput, error) {
	t.Helper()
	var out, progress bytes.Buffer
	err := RefsSweepPlanJSON(ctx, opts, &out, &progress)
	validateJSON(t, "refs-sweep-plan", out.Bytes())
	var p RefsPlanOutput
	if jerr := json.Unmarshal(out.Bytes(), &p); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	return p, err
}

func refsSweepJSON(t *testing.T, ctx *Context, opts RefsOptions) (RefsResult, string, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal, opts.Yes = NewRefsJournal(&out), true
	err := RefsSweep(ctx, opts, &human)
	validateJSON(t, "refs-sweep", out.Bytes())
	var r RefsResult
	dec := json.NewDecoder(&out)
	if jerr := dec.Decode(&r); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	if dec.More() {
		t.Error("a second object was written")
	}
	return r, human.String(), err
}

func refPlanItem(t *testing.T, p RefsPlanOutput, id string) RefItem {
	t.Helper()
	for _, it := range p.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s in %+v", id, p.Items)
	return RefItem{}
}

func refResultItem(t *testing.T, r RefsResult, id string) *RefResultItem {
	t.Helper()
	for _, it := range r.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s in %+v", id, r.Items)
	return nil
}

// backupsRepo is a repository with origin and one of each kind of backup:
//
//	backup/contained  a branch at a commit main has
//	backup/old        a branch with a commit of its own, made and branched 30 days ago
//	backup/rebased    a branch cut today from a feature branch last committed 30 days ago
//	safe-tag          an annotated tag on main's first commit, tagged 30 days ago
//	backup/checked    a contained branch checked out in a worktree
//	release-backup    a contained branch with a protected name
//	feature           a branch no pattern matches
func backupsRepo(t *testing.T) (*Context, string) {
	t.Helper()
	ctx, main, _ := sweepRepo(t)
	old := time.Now().Add(-30 * 24 * time.Hour)
	first := gitOut(t, main, "rev-parse", "main")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "second")
	gitIn(t, main, "push", "-q", "origin", "main")
	gitIn(t, main, "branch", "backup/contained", first)
	gitAt(t, main, old, "branch", "backup/old", "main")
	commitOn(t, main, "backup/old", "old work", old)
	gitAt(t, main, old, "branch", "feature", "main")
	commitOn(t, main, "feature", "feature work", old)
	gitIn(t, main, "branch", "backup/rebased", "feature")
	commitOn(t, main, "feature", "rebased away", time.Now())
	gitIn(t, main, "update-ref", "refs/heads/feature", gitOut(t, main, "rev-parse", "main"))
	gitAt(t, main, old, "tag", "-a", "-m", "safety", "safe-tag", first)
	gitIn(t, main, "branch", "backup/checked", first)
	gitIn(t, main, "worktree", "add", "-q", filepath.Join(t.TempDir(), "checked"), "backup/checked")
	gitIn(t, main, "branch", "release-backup", first)
	gitIn(t, main, "config", "branch.backup/contained.description", "mine")
	return ctx, main
}

// The plan names every backup and what it is, measures age from when the
// branch was made rather than from its commits, and writes nothing.
func TestRefsPlanSortsTheBackups(t *testing.T) {
	ctx, main := backupsRepo(t)
	before := repoState(t, main)
	p, err := refsPlanJSON(t, ctx, RefsOptions{NoFetch: true})
	if err != nil {
		t.Fatal(err)
	}
	if after := repoState(t, main); after != before {
		t.Errorf("the plan wrote to the repository:\nbefore %s\nafter %s", before, after)
	}
	if p.Command != "refs sweep" || p.Token == nil || !strings.HasPrefix(*p.Token, "rs1-") || p.RunID != nil ||
		deref(p.Trunk) != "main" || deref(p.Age) != "14d" || len(p.Patterns) != 9 {
		t.Fatalf("plan = %+v", p)
	}
	it := refPlanItem(t, p, "refs/heads/backup/contained")
	if it.Category != RefBackupContained || !it.Selected || deref(it.ContainedIn) != "refs/heads/main" ||
		it.UniqueCommits != nil || it.Pattern != "backup/*" || deref(it.DateSource) != DateReflog || it.Annotated != nil {
		t.Errorf("contained = %+v", it)
	}
	it = refPlanItem(t, p, "refs/heads/backup/old")
	if it.Category != RefBackupOld || !it.Selected || it.ContainedIn != nil || it.UniqueCommits == nil ||
		*it.UniqueCommits != 1 || deref(it.Subject) != "old work" {
		t.Errorf("old = %+v", it)
	}
	it = refPlanItem(t, p, "refs/heads/backup/rebased")
	if it.Category != RefBackupYoung || it.Selected || deref(it.DateSource) != DateReflog {
		t.Errorf("a backup made today of an old branch must be young: %+v", it)
	}
	it = refPlanItem(t, p, "refs/tags/safe-tag")
	if it.Category != RefBackupContained || it.Annotated == nil || !*it.Annotated || deref(it.DateSource) != DateTagger ||
		it.Object == deref(it.Tip) || deref(it.ContainedIn) != "refs/heads/main" {
		t.Errorf("tag = %+v", it)
	}
	if it := refPlanItem(t, p, "refs/heads/backup/checked"); it.Category != RefKept || it.Selected ||
		!slices.Equal(it.Kept, []string{KeptWorktree}) {
		t.Errorf("checked out = %+v", it)
	}
	if it := refPlanItem(t, p, "refs/heads/release-backup"); it.Category != RefKept ||
		!slices.Equal(it.Kept, []string{KeptProtected}) {
		t.Errorf("protected = %+v", it)
	}
	for _, it := range p.Items {
		if it.Name == "feature" || it.Name == "main" {
			t.Errorf("%s matches no pattern and must not be listed", it.Name)
		}
	}
}

// A sweep pins each selected ref and deletes it in one step, drops a
// branch's config, and says how to put each back.
func TestRefsSweepPinsAndDeletes(t *testing.T) {
	ctx, main := backupsRepo(t)
	plan, err := refsPlanJSON(t, ctx, RefsOptions{NoFetch: true})
	if err != nil {
		t.Fatal(err)
	}
	r, human, err := refsSweepJSON(t, ctx, RefsOptions{NoFetch: true, Expect: *plan.Token})
	if err != nil {
		t.Fatalf("%v\n%s", err, human)
	}
	if r.Outcome != OutcomeDone || r.RunID == nil || !runIDPattern.MatchString(*r.RunID) || r.Error != nil {
		t.Fatalf("result = %+v\n%s", r, human)
	}
	run := *r.RunID
	for _, id := range []string{"refs/heads/backup/contained", "refs/heads/backup/old", "refs/tags/safe-tag"} {
		it := refResultItem(t, r, id)
		if it.Result != RefSwept || !it.Pinned || !it.Deleted || it.Pin == nil || len(it.RestoreCommand) != 7 ||
			it.RestoreCommand[3] != run {
			t.Errorf("%s = %+v", id, it)
			continue
		}
		if got := gitOut(t, main, "rev-parse", *it.Pin); got != it.Object {
			t.Errorf("%s: pin at %s, want %s", id, got, it.Object)
		}
	}
	if pin := deref(refResultItem(t, r, "refs/tags/safe-tag").Pin); pin != SweptPrefix+run+"/tags/safe-tag" {
		t.Errorf("tag pin = %s", pin)
	}
	if it := refResultItem(t, r, "refs/heads/backup/rebased"); it.Result != RefNotSelected || it.Pinned {
		t.Errorf("young = %+v", it)
	}
	if it := refResultItem(t, r, "refs/heads/backup/checked"); it.Result != RefResultKept || deref(it.Reason) != KeptWorktree {
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
	ctx, main := backupsRepo(t)
	plan, _ := refsPlanJSON(t, ctx, RefsOptions{NoFetch: true})
	gitIn(t, main, "branch", "backup/new", "main")
	before := repoState(t, main)
	r, _, err := refsSweepJSON(t, ctx, RefsOptions{NoFetch: true, Expect: *plan.Token})
	if !errors.Is(err, errRefsPlanChanged) || r.Outcome != OutcomeRefused || r.Error == nil || r.RunID != nil {
		t.Fatalf("err = %v, result = %+v", err, r)
	}
	if repoState(t, main) != before {
		t.Error("a refused sweep changed the repository")
	}
}

// --only replaces the selection, under the same token; it takes a young
// backup, and refuses the lot when it names one that is kept or unknown.
func TestRefsSweepOnly(t *testing.T) {
	ctx, _ := backupsRepo(t)
	full, _ := refsPlanJSON(t, ctx, RefsOptions{NoFetch: true})
	only := []string{"refs/heads/backup/rebased"}
	p, err := refsPlanJSON(t, ctx, RefsOptions{NoFetch: true, Only: only})
	if err != nil || deref(p.Token) != deref(full.Token) {
		t.Fatalf("--only changed the token or failed: %v", err)
	}
	for _, it := range p.Items {
		if it.Selected != (it.ID == only[0]) {
			t.Errorf("%s selected = %v", it.ID, it.Selected)
		}
	}
	for _, bad := range []string{"refs/heads/backup/checked", "refs/heads/nope"} {
		p, err := refsPlanJSON(t, ctx, RefsOptions{NoFetch: true, Only: []string{only[0], bad}})
		if err == nil || p.Token != nil || !strings.Contains(deref(p.Error), bad) {
			t.Errorf("--only %s: err %v, plan %+v", bad, err, p.Error)
		}
		r, _, err := refsSweepJSON(t, ctx, RefsOptions{NoFetch: true, Only: []string{only[0], bad}})
		if err == nil || r.Outcome != OutcomeRefused || !ctx.Repo.BranchExists("backup/rebased") {
			t.Errorf("--only %s swept: %v %+v", bad, err, r)
		}
	}
	r, _, err := refsSweepJSON(t, ctx, RefsOptions{NoFetch: true, Only: only, Expect: *full.Token})
	if err != nil || r.Outcome != OutcomeDone || refResultItem(t, r, only[0]).Result != RefSwept ||
		refResultItem(t, r, "refs/heads/backup/old").Result != RefNotSelected {
		t.Fatalf("%v %+v", err, r)
	}
}

// A ref that moves between the plan and its move is kept, and the pin is
// not made.
func TestRefsSweepKeepsARefThatMoved(t *testing.T) {
	ctx, main := backupsRepo(t)
	plan, err := planRefs(ctx, RefsOptions{NoFetch: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	commitOn(t, main, "backup/old", "more", time.Now())
	var out bytes.Buffer
	j := NewRefsJournal(&out)
	j.planned(plan, refsToken(plan))
	run := newRunID(time.Now())
	j.begin(run)
	if err := plan.apply(ctx, run, j, &bytes.Buffer{}); err == nil {
		t.Error("a run that kept a row must say so")
	}
	j.Finish()
	var r RefsResult
	_ = json.Unmarshal(out.Bytes(), &r)
	it := refResultItem(t, r, "refs/heads/backup/old")
	if it.Result != RefResultKept || it.Pinned || it.Deleted || !ctx.Repo.BranchExists("backup/old") {
		t.Errorf("moved = %+v", it)
	}
	if r.Outcome != OutcomePartial {
		t.Errorf("outcome = %s", r.Outcome)
	}
}

// A branch a live quarantine names is kept: restoring the quarantine would
// need it.
func TestRefsSweepKeepsWhatAQuarantineNames(t *testing.T) {
	ctx, path := safetyWorktree(t, "fix/trashed")
	tip := strings.TrimSpace(gitOut(t, path, "rev-parse", "HEAD"))
	var buf bytes.Buffer
	if err := RemoveAt(ctx, path, RemoveOptions{Quarantine: trashFor(t, ctx)}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	gitIn(t, ctx.Repo.MainRoot, "branch", "fix_wt/trashed", tip)
	ctx.User.RefSweepPatterns = []string{"fix_wt/*"}
	p, err := refsPlanJSON(t, ctx, RefsOptions{NoFetch: true})
	if err != nil {
		t.Fatal(err)
	}
	if it := refPlanItem(t, p, "refs/heads/fix_wt/trashed"); it.Category != RefKept ||
		!slices.Contains(it.Kept, KeptQuarantine) {
		t.Errorf("quarantined = %+v", it)
	}
}

// The journal a signal finishes writes one object: the row in flight read
// back and interrupted.
func TestRefsJournalWritesOneObjectWhenInterrupted(t *testing.T) {
	ctx, _ := backupsRepo(t)
	plan, err := planRefs(ctx, RefsOptions{NoFetch: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	j := NewRefsJournal(&out)
	j.planned(plan, refsToken(plan))
	j.begin(newRunID(time.Now()))
	j.start("refs/heads/backup/old")
	j.interrupted("", "interrupted")
	j.Finish()
	validateJSON(t, "refs-sweep", out.Bytes())
	var r RefsResult
	dec := json.NewDecoder(&out)
	if err := dec.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if dec.More() {
		t.Error("a second object was written")
	}
	if r.Outcome != OutcomeInterrupted || refResultItem(t, r, "refs/heads/backup/old").Result != RefInterrupted ||
		deref(r.Recovery) != "interrupted" {
		t.Errorf("%+v", r)
	}
}

// sweepAll sweeps the default selection and returns the run.
func sweepAll(t *testing.T, ctx *Context, opts RefsOptions) RefsResult {
	t.Helper()
	opts.NoFetch = true
	r, human, err := refsSweepJSON(t, ctx, opts)
	if err != nil || r.RunID == nil {
		t.Fatalf("sweep: %v %+v\n%s", err, r, human)
	}
	return r
}

func restoreJSON(t *testing.T, ctx *Context, runID string, opts RefsRestoreOptions) (RefsRestoreResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal, opts.Yes = NewRefsRestoreJournal(&out, runID), true
	err := RefsRestore(ctx, runID, opts, &human)
	validateJSON(t, "refs-restore", out.Bytes())
	var r RefsRestoreResult
	if jerr := json.Unmarshal(out.Bytes(), &r); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out.String())
	}
	return r, err
}

func restorePlanJSON(t *testing.T, ctx *Context, runID string, only []string) (RefsRestorePlanOutput, error) {
	t.Helper()
	var out, progress bytes.Buffer
	err := RefsRestorePlanJSON(ctx, runID, only, &out, &progress)
	validateJSON(t, "refs-restore-plan", out.Bytes())
	var p RefsRestorePlanOutput
	if jerr := json.Unmarshal(out.Bytes(), &p); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out.String())
	}
	return p, err
}

// A restore puts each ref back at its object where the name is free, keeps
// the one whose name was taken, and leaves no pin of what went back.
func TestRefsRestorePutsBack(t *testing.T) {
	ctx, main := backupsRepo(t)
	r := sweepAll(t, ctx, RefsOptions{})
	run := *r.RunID
	old := refResultItem(t, r, "refs/heads/backup/old")
	gitIn(t, main, "branch", "backup/contained", "main")
	p, err := restorePlanJSON(t, ctx, run, nil)
	if err != nil || p.Token == nil || !strings.HasPrefix(*p.Token, "rr1-") {
		t.Fatalf("%v %+v", err, p)
	}
	actions := map[string]string{}
	for _, it := range p.Items {
		actions[it.ID] = it.Action
	}
	if actions["refs/heads/backup/old"] != RestoreCreate || actions["refs/heads/backup/contained"] != RestoreOccupied ||
		actions["refs/tags/safe-tag"] != RestoreCreate {
		t.Fatalf("actions = %v", actions)
	}
	res, err := restoreJSON(t, ctx, run, RefsRestoreOptions{Expect: *p.Token})
	if err != nil || res.Outcome != OutcomeDone {
		t.Fatalf("%v %+v", err, res)
	}
	if got := gitOut(t, main, "rev-parse", "refs/heads/backup/old"); got != old.Object {
		t.Errorf("backup/old back at %s, want %s", got, old.Object)
	}
	if gitOut(t, main, "cat-file", "-t", "refs/tags/safe-tag") != "tag" {
		t.Error("the annotated tag must come back as the tag object")
	}
	pins := gitOut(t, main, "for-each-ref", "--format=%(refname)", SweptPrefix+run+"/")
	if pins != SweptPrefix+run+"/heads/backup/contained\n"+SweptPrefix+run+"/meta" {
		t.Errorf("pins left: %q", pins)
	}
	if _, err := restorePlanJSON(t, ctx, run, []string{"refs/heads/backup/contained"}); err == nil {
		t.Error("--only on a name that is taken must refuse")
	}
	if _, err := restorePlanJSON(t, ctx, "20200101T000000Z-0000", nil); err == nil {
		t.Error("a run that is not there must be an error")
	}
}

// --only takes one id per flag, and a ref name may carry a comma.
func TestRefsOnlyTakesAnIDWithAComma(t *testing.T) {
	ctx, main := backupsRepo(t)
	gitIn(t, main, "branch", "backup/a,b", "main")
	id := "refs/heads/backup/a,b"
	r := sweepAll(t, ctx, RefsOptions{Only: []string{id}})
	it := refResultItem(t, r, id)
	if it.Result != RefSwept || !slices.Equal(it.RestoreCommand, []string{"wt", "refs", "restore", *r.RunID, "--only", id, "--yes"}) {
		t.Fatalf("%+v", it)
	}
	res, err := restoreJSON(t, ctx, *r.RunID, RefsRestoreOptions{Only: []string{id}})
	if err != nil || res.Outcome != OutcomeDone || !ctx.Repo.BranchExists("backup/a,b") {
		t.Fatalf("%v %+v", err, res)
	}
}

// wt refs swept lists the runs; purge deletes a run's pins, counts what
// becomes unreachable, and --older-than picks runs by the time in their id.
func TestRefsSweptAndPurge(t *testing.T) {
	ctx, main := backupsRepo(t)
	r := sweepAll(t, ctx, RefsOptions{})
	run := *r.RunID
	var out bytes.Buffer
	if err := RefsSwept(ctx, true, &out); err != nil {
		t.Fatal(err)
	}
	validateJSON(t, "refs-swept", out.Bytes())
	var sw RefsSweptOutput
	_ = json.Unmarshal(out.Bytes(), &sw)
	if len(sw.Runs) != 1 || sw.Runs[0].RunID != run || len(sw.Runs[0].Refs) != 3 || sw.Runs[0].SweptAt == 0 {
		t.Fatalf("swept = %+v", sw)
	}

	var plan bytes.Buffer
	if err := RefsPurgePlanJSON(ctx, RefsPurgeOptions{OlderThan: "1d"}, &plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	validateJSON(t, "refs-purge-plan", plan.Bytes())
	var pp RefsPurgePlanOutput
	_ = json.Unmarshal(plan.Bytes(), &pp)
	if len(pp.Runs) != 0 || pp.Token != nil {
		t.Errorf("a run from today is not older than a day: %+v", pp)
	}
	plan.Reset()
	if err := RefsPurgePlanJSON(ctx, RefsPurgeOptions{RunIDs: []string{run}}, &plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	validateJSON(t, "refs-purge-plan", plan.Bytes())
	pp = RefsPurgePlanOutput{}
	_ = json.Unmarshal(plan.Bytes(), &pp)
	if len(pp.Runs) != 1 || len(pp.Runs[0].Refs) != 3 || pp.Token == nil ||
		!slices.Equal(pp.Runs[0].Unreachable, []RefLost{{ID: "refs/heads/backup/old", Count: 1}}) {
		t.Fatalf("purge plan = %+v", pp)
	}

	var res bytes.Buffer
	j := NewRefsPurgeJournal(&res)
	err := RefsPurge(ctx, RefsPurgeOptions{RunIDs: []string{run}, Yes: true, Expect: *pp.Token, Journal: j},
		&bytes.Buffer{})
	validateJSON(t, "refs-purge", res.Bytes())
	var pr RefsPurgeResult
	_ = json.Unmarshal(res.Bytes(), &pr)
	if err != nil || pr.Outcome != OutcomePurged || pr.Runs[0].Refs[0].Result != PinDeleted {
		t.Fatalf("%v %+v", err, pr)
	}
	if left := gitOut(t, main, "for-each-ref", SweptPrefix); left != "" {
		t.Errorf("pins left: %s", left)
	}
	if err := RefsPurge(ctx, RefsPurgeOptions{RunIDs: []string{run}, Yes: true}, &bytes.Buffer{}); err == nil {
		t.Error("purging a run that is gone must be an error")
	}
	if err := RefsPurge(ctx, RefsPurgeOptions{OlderThan: "1d"}, &bytes.Buffer{}); err != nil {
		t.Errorf("nothing older than a day is not an error: %v", err)
	}
}

// With --remote, origin's backups are swept only when origin's own refs
// contain them, never with an open pull request or when GitHub cannot say;
// a swept one is pinned here, gone from origin, and restore pushes it back.
func TestRefsSweepRemote(t *testing.T) {
	ctx, main, origin := sweepRepo(t)
	first := gitOut(t, main, "rev-parse", "main")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "second")
	gitIn(t, main, "branch", "backup/landed", first)
	gitIn(t, main, "branch", "backup/pr", first)
	gitIn(t, main, "branch", "backup/unique", "main")
	commitOn(t, main, "backup/unique", "only here and there", time.Now())
	gitIn(t, main, "tag", "-a", "-m", "safety", "safe-remote", first)
	gitIn(t, main, "push", "-q", "origin", "main", "backup/landed", "backup/pr", "backup/unique", "safe-remote")
	gitIn(t, main, "fetch", "-q", "origin")
	for _, b := range []string{"backup/landed", "backup/pr", "backup/unique"} {
		gitIn(t, main, "branch", "-D", b)
	}
	gitIn(t, main, "tag", "-d", "safe-remote")
	gitIn(t, main, "fetch", "-q", "origin", "refs/tags/safe-remote:refs/tags/elsewhere")

	prs := func([]string) (map[string]bool, error) { return map[string]bool{"backup/pr": true}, nil }
	p, err := refsPlanJSON(t, ctx, RefsOptions{Remote: true, OpenPRs: prs})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Remote || !p.Fetched {
		t.Errorf("plan = %+v", p)
	}
	if it := refPlanItem(t, p, "origin:refs/heads/backup/landed"); it.Kind != RefRemoteBranch ||
		it.Category != RefBackupContained || !it.Selected || deref(it.ContainedIn) != "origin:refs/heads/main" {
		t.Errorf("landed = %+v", it)
	}
	if it := refPlanItem(t, p, "origin:refs/heads/backup/pr"); it.Category != RefKept ||
		!slices.Equal(it.Kept, []string{KeptPullRequestOpen}) {
		t.Errorf("pr = %+v", it)
	}
	if it := refPlanItem(t, p, "origin:refs/heads/backup/unique"); it.Category != RefKept ||
		!slices.Equal(it.Kept, []string{KeptNotContained}) {
		t.Errorf("unique = %+v", it)
	}
	if it := refPlanItem(t, p, "origin:refs/tags/safe-remote"); it.Kind != RefRemoteTag || it.Category != RefBackupContained ||
		it.Annotated == nil || !*it.Annotated {
		t.Errorf("tag = %+v", it)
	}
	unknown := func([]string) (map[string]bool, error) { return nil, errors.New("gh is not on your PATH") }
	if p, _ := refsPlanJSON(t, ctx, RefsOptions{Remote: true, NoFetch: true, OpenPRs: unknown}); !slices.Contains(
		refPlanItem(t, p, "origin:refs/heads/backup/landed").Kept, KeptPullRequestUnknown) {
		t.Error("without GitHub every remote branch is kept")
	}

	r, human, err := refsSweepJSON(t, ctx, RefsOptions{Remote: true, OpenPRs: prs, Expect: *p.Token})
	if err != nil || r.Outcome != OutcomeDone {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	run := *r.RunID
	for _, ref := range []string{"refs/heads/backup/landed", "refs/tags/safe-remote"} {
		if out := gitOut(t, origin, "for-each-ref", ref); out != "" {
			t.Errorf("origin still has %s", ref)
		}
		it := refResultItem(t, r, "origin:"+ref)
		if it.Result != RefSwept || !it.Pinned || !it.Deleted {
			t.Errorf("%s = %+v", ref, it)
		}
	}
	if pin := pinOf(run, RefRemoteBranch, "backup/landed"); gitOut(t, main, "rev-parse", pin) != first {
		t.Errorf("pin %s is not at %s", pin, first)
	}
	res, err := restoreJSON(t, ctx, run, RefsRestoreOptions{})
	if err != nil || res.Outcome != OutcomeDone {
		t.Fatalf("%v %+v", err, res)
	}
	if gitOut(t, origin, "rev-parse", "refs/heads/backup/landed") != first ||
		gitOut(t, origin, "cat-file", "-t", "refs/tags/safe-remote") != "tag" {
		t.Error("origin must have both back")
	}
	if left := gitOut(t, main, "for-each-ref", SweptPrefix); left != "" {
		t.Errorf("a run restored whole leaves nothing behind: %s", left)
	}
}

// The plan stays fast on a repository shaped like the ones it was built
// for: about a hundred backups, half of them rebased away, and fifty other
// branches. The time is logged (go test -v -run TestRefsPlanTime); the
// bound is only there to catch a pass per backup per container.
func TestRefsPlanTime(t *testing.T) {
	ctx, main, _ := sweepRepo(t)
	var refs strings.Builder
	tree := gitOut(t, main, "rev-parse", "main^{tree}")
	parent := gitOut(t, main, "rev-parse", "main")
	for i := range 200 {
		parent = gitOut(t, main, "commit-tree", "-p", parent, "-m", fmt.Sprintf("trunk %d", i), tree)
	}
	gitIn(t, main, "update-ref", "refs/heads/main", parent)
	history := strings.Split(gitOut(t, main, "rev-list", "main"), "\n")
	for i := range 50 {
		c := gitOut(t, main, "commit-tree", "-p", history[i*3], "-m", fmt.Sprintf("feature %d", i), tree)
		fmt.Fprintf(&refs, "create refs/heads/feature/%d %s\n", i, c)
	}
	for i := range 100 {
		at := history[i]
		if i%2 == 1 {
			at = gitOut(t, main, "commit-tree", "-p", history[i], "-m", fmt.Sprintf("backup %d", i), tree)
		}
		fmt.Fprintf(&refs, "create refs/heads/backup/b%d %s\n", i, at)
	}
	cmd := exec.Command("git", "-C", main, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(refs.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	start := time.Now()
	p, err := planRefs(ctx, RefsOptions{NoFetch: true}, &bytes.Buffer{})
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	contained := 0
	for _, r := range p.Rows {
		if r.Category == RefBackupContained {
			contained++
		}
	}
	if len(p.Rows) != 100 || contained != 50 {
		t.Fatalf("%d rows, %d contained", len(p.Rows), contained)
	}
	t.Logf("planned 100 backups against 51 containers in %v", took)
	if took > 20*time.Second {
		t.Errorf("the plan took %v", took)
	}
	start = time.Now()
	if err := p.apply(ctx, newRunID(time.Now()), nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	t.Logf("swept %d of them in %v", len(p.Selected()), time.Since(start))
}

// remoteBackups is a repository whose origin has one contained backup
// branch that is not here, and the run that swept it with --remote.
func remoteBackups(t *testing.T) (ctx *Context, main, origin, run string) {
	t.Helper()
	ctx, main, origin = sweepRepo(t)
	first := gitOut(t, main, "rev-parse", "main")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "second")
	gitIn(t, main, "push", "-q", "origin", "main", "main:refs/heads/backup/landed")
	gitIn(t, main, "branch", "backup/local", first)
	r, human, err := refsSweepJSON(t, ctx, RefsOptions{Remote: true, OpenPRs: noPRs})
	if err != nil || r.Outcome != OutcomeDone || r.RunID == nil {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	return ctx, main, origin, *r.RunID
}

// --remote needs origin to fetch from and push to one and the same URL, with
// no secret in it; the plan names that URL, redacted, and the token covers it.
func TestRefsRemoteEndpoint(t *testing.T) {
	ctx, main, origin := sweepRepo(t)
	p, err := refsPlanJSON(t, ctx, RefsOptions{Remote: true, NoFetch: true, OpenPRs: noPRs})
	if err != nil || p.Endpoint == nil || p.Endpoint.URL != origin || !strings.HasPrefix(p.Endpoint.Digest, "sha256:") {
		t.Fatalf("%v endpoint = %+v", err, p.Endpoint)
	}
	for name, setup := range map[string][]string{
		"a push URL of its own": {"remote", "set-url", "--push", "origin", t.TempDir()},
		"two push URLs":         {"remote", "set-url", "--add", "--push", "origin", t.TempDir()},
	} {
		gitIn(t, main, "remote", "set-url", "origin", origin)
		_, _ = exec.Command("git", "-C", main, "config", "--unset-all", "remote.origin.pushurl").CombinedOutput()
		if name == "two push URLs" {
			gitIn(t, main, "remote", "set-url", "--add", "--push", "origin", origin)
		}
		gitIn(t, main, setup...)
		p, err := refsPlanJSON(t, ctx, RefsOptions{Remote: true, NoFetch: true, OpenPRs: noPRs})
		if err == nil || p.Token != nil || len(p.Problems) != 1 || p.Problems[0].Code != ProblemRemoteAmbiguous {
			t.Errorf("%s: %v %+v", name, err, p.Problems)
		}
	}
	_, _ = exec.Command("git", "-C", main, "config", "--unset-all", "remote.origin.pushurl").CombinedOutput()
	for _, url := range []string{"https://token@example.com/o/r.git", "https://u:p@example.com/o/r.git",
		"ssh://git:secret@example.com/o/r.git", "user:secret@example.com:o/r.git"} {
		gitIn(t, main, "remote", "set-url", "origin", url)
		p, err := refsPlanJSON(t, ctx, RefsOptions{Remote: true, NoFetch: true, OpenPRs: noPRs})
		if err == nil || len(p.Problems) != 1 || p.Problems[0].Code != ProblemRemoteCredentials ||
			strings.Contains(deref(p.Error), "example.com") {
			t.Errorf("%s: %v %+v", url, err, p.Problems)
		}
	}
	for url, want := range map[string]string{"git@example.com:o/r.git": "example.com:o/r.git",
		"ssh://git@example.com/o/r.git": "ssh://example.com/o/r.git"} {
		gitIn(t, main, "remote", "set-url", "origin", url)
		ep, err := resolveEndpoint(main)
		if err != nil || ep.URL != want {
			t.Errorf("%s: %v %+v", url, err, ep)
		}
	}
	gitIn(t, main, "remote", "set-url", "origin", origin)
	if p, err := refsPlanJSON(t, ctx, RefsOptions{NoFetch: true}); err != nil || p.Endpoint != nil {
		t.Errorf("without --remote origin's URLs do not matter: %v", err)
	}
}

// A remote ref goes back only to the origin it came from: a run whose
// origin now resolves elsewhere keeps it, and so does a run with no meta.
// Local refs are not affected.
func TestRefsRestoreRemoteNeedsTheSameOrigin(t *testing.T) {
	ctx, main, origin, run := remoteBackups(t)
	var out bytes.Buffer
	if err := RefsSwept(ctx, true, &out); err != nil {
		t.Fatal(err)
	}
	var sw RefsSweptOutput
	_ = json.Unmarshal(out.Bytes(), &sw)
	if len(sw.Runs) != 1 || sw.Runs[0].Endpoint == nil || sw.Runs[0].Endpoint.URL != origin {
		t.Fatalf("swept = %+v", sw)
	}
	moved := filepath.Join(t.TempDir(), "moved.git")
	gitIn(t, main, "clone", "-q", "--bare", origin, moved)
	gitIn(t, main, "remote", "set-url", "origin", moved)
	p, err := restorePlanJSON(t, ctx, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range p.Items {
		want := RestoreCreate
		if it.Kind == RefRemoteBranch {
			want = RestoreEndpointChanged
		}
		if it.Action != want || it.Selected != (want == RestoreCreate) {
			t.Errorf("%s: action %s, selected %v", it.ID, it.Action, it.Selected)
		}
	}
	gitIn(t, main, "remote", "set-url", "origin", origin)
	gitIn(t, main, "update-ref", "-d", metaRef(run))
	p, _ = restorePlanJSON(t, ctx, run, nil)
	for _, it := range p.Items {
		if it.Kind == RefRemoteBranch && it.Action != RestoreEndpointUnknown {
			t.Errorf("no meta: %s is %s", it.ID, it.Action)
		}
	}
}

// --run-id names the run; one that has refs already refuses, moving nothing.
func TestRefsSweepRunID(t *testing.T) {
	ctx, main := backupsRepo(t)
	id := "20260930T091500Z-00c0ffee"
	r := sweepAll(t, ctx, RefsOptions{RunID: id, Only: []string{"refs/heads/backup/contained"}})
	if deref(r.RunID) != id {
		t.Fatalf("run = %v", r.RunID)
	}
	var m runMeta
	if err := json.Unmarshal([]byte(gitOut(t, main, "cat-file", "blob", metaRef(id))), &m); err != nil ||
		m.RunID != id || m.SweptAt == 0 || m.Endpoint != nil || m.Repo == "" {
		t.Errorf("meta = %+v, %v", m, err)
	}
	before := repoState(t, main)
	r, _, err := refsSweepJSON(t, ctx, RefsOptions{NoFetch: true, RunID: id})
	if err == nil || r.Outcome != OutcomeRefused || len(r.Problems) != 1 || r.Problems[0].Code != ProblemRunIDTaken {
		t.Errorf("a reused run id: %v %+v", err, r)
	}
	if repoState(t, main) != before {
		t.Error("a refused run changed the repository")
	}
	if _, _, err := refsSweepJSON(t, ctx, RefsOptions{NoFetch: true, RunID: "tomorrow"}); err == nil {
		t.Error("a run id not in wt's format must refuse")
	}
}

// A push origin rejects keeps the pin; once origin holds the ref, a restore
// only drops the pin.
func TestRefsRestoreRemoteKeepsThePinUntilOriginHasIt(t *testing.T) {
	ctx, main, origin, run := remoteBackups(t)
	hook := filepath.Join(origin, "hooks", "pre-receive")
	mustWrite(t, hook, "#!/bin/sh\necho no >&2\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	pin := pinOf(run, RefRemoteBranch, "backup/landed")
	id := "origin:refs/heads/backup/landed"
	res, err := restoreJSON(t, ctx, run, RefsRestoreOptions{Only: []string{id}})
	it := res.Items[slices.IndexFunc(res.Items, func(i *RefRestoreItem) bool { return i.ID == id })]
	if err == nil || it.Result != RefFailed || it.PinDeleted || it.Restored {
		t.Fatalf("rejected push: %v %+v", err, it)
	}
	if _, ok, _ := refValue(main, pin); !ok {
		t.Fatal("a rejected push must keep the pin")
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "push", "-q", "origin", pin+":refs/heads/backup/landed")
	p, _ := restorePlanJSON(t, ctx, run, []string{id})
	if p.Items[slices.IndexFunc(p.Items, func(i RefRestorePlanItem) bool { return i.ID == id })].Action != RestoreNone {
		t.Fatalf("origin has it back: %+v", p.Items)
	}
	res, err = restoreJSON(t, ctx, run, RefsRestoreOptions{Only: []string{id}})
	if err != nil || res.Outcome != OutcomeDone {
		t.Fatalf("%v %+v", err, res)
	}
	if _, ok, _ := refValue(main, pin); ok {
		t.Error("the pin must go once origin has the ref")
	}
}

// A run that wrote its meta and moved nothing is still listed, and a purge
// deletes the meta last.
func TestRefsPurgeDeletesTheMetaLast(t *testing.T) {
	ctx, main := backupsRepo(t)
	id := "20260101T000000Z-dead"
	if err := writeMeta(main, runMeta{RunID: id, Repo: main, SweptAt: 1767225600}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_ = RefsSwept(ctx, true, &out)
	var sw RefsSweptOutput
	_ = json.Unmarshal(out.Bytes(), &sw)
	if len(sw.Runs) != 1 || sw.Runs[0].RunID != id || len(sw.Runs[0].Refs) != 0 || sw.Runs[0].SweptAt != 1767225600 {
		t.Fatalf("swept = %+v", sw)
	}
	r := sweepAll(t, ctx, RefsOptions{})
	var res bytes.Buffer
	err := RefsPurge(ctx, RefsPurgeOptions{OlderThan: "1d", Yes: true, Journal: NewRefsPurgeJournal(&res)}, &bytes.Buffer{})
	validateJSON(t, "refs-purge", res.Bytes())
	var pr RefsPurgeResult
	_ = json.Unmarshal(res.Bytes(), &pr)
	if err != nil || pr.Outcome != OutcomePurged || len(pr.Runs) != 1 || !pr.Runs[0].MetaDeleted {
		t.Fatalf("%v %+v", err, pr)
	}
	if left := gitOut(t, main, "for-each-ref", "--format=%(refname)", SweptPrefix); !strings.HasPrefix(left, SweptPrefix+*r.RunID) {
		t.Errorf("only today's run is left, got %q", left)
	}
}

// A remote row the push cannot delete drops its pin and leaves origin as it
// was; one origin moved after the plan is kept and pinned nowhere.
func TestRefsSweepRemoteGuards(t *testing.T) {
	ctx, main, origin := sweepRepo(t)
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "second")
	gitIn(t, main, "push", "-q", "origin", "main", "main:refs/heads/backup/rejected", "main~1:refs/heads/backup/moved")
	gitIn(t, main, "fetch", "-q", "origin")
	plan, err := planRefs(ctx, RefsOptions{Remote: true, NoFetch: true, OpenPRs: noPRs}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "push", "-q", "--force", "origin", "main:refs/heads/backup/moved")
	hook := filepath.Join(origin, "hooks", "pre-receive")
	mustWrite(t, hook, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	j := NewRefsJournal(&out)
	j.planned(plan, refsToken(plan))
	run := newRunID(time.Now())
	j.begin(run)
	_ = plan.apply(ctx, run, j, &bytes.Buffer{})
	j.Finish()
	validateJSON(t, "refs-sweep", out.Bytes())
	var r RefsResult
	_ = json.Unmarshal(out.Bytes(), &r)
	if it := refResultItem(t, r, "origin:refs/heads/backup/rejected"); it.Result != RefFailed || it.Pinned || it.Deleted {
		t.Errorf("rejected = %+v", it)
	}
	if it := refResultItem(t, r, "origin:refs/heads/backup/moved"); it.Result != RefResultKept || it.Pinned {
		t.Errorf("moved = %+v", it)
	}
	if gitOut(t, origin, "for-each-ref", "--format=%(refname)", "refs/heads/backup/") == "" {
		t.Error("origin must keep both")
	}
	if r.Outcome != OutcomeRefused {
		t.Errorf("outcome = %s", r.Outcome)
	}
	if left := gitOut(t, main, "for-each-ref", SweptPrefix); left != "" {
		t.Errorf("a run that moved nothing leaves nothing behind: %s", left)
	}
}

// A branch checked out after the plan is kept at apply time.
func TestRefsSweepKeepsABranchCheckedOutSinceThePlan(t *testing.T) {
	ctx, main := backupsRepo(t)
	plan, err := planRefs(ctx, RefsOptions{NoFetch: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "worktree", "add", "-q", filepath.Join(t.TempDir(), "late"), "backup/old")
	var out bytes.Buffer
	j := NewRefsJournal(&out)
	j.planned(plan, refsToken(plan))
	run := newRunID(time.Now())
	j.begin(run)
	_ = plan.apply(ctx, run, j, &bytes.Buffer{})
	j.Finish()
	var r RefsResult
	_ = json.Unmarshal(out.Bytes(), &r)
	if it := refResultItem(t, r, "refs/heads/backup/old"); it.Result != RefResultKept || it.Pinned ||
		!ctx.Repo.BranchExists("backup/old") {
		t.Errorf("checked out since = %+v", it)
	}
}

// A remote backup whose object is not here is kept: containment cannot be
// read.
func TestRefsRemoteObjectMissing(t *testing.T) {
	ctx, main, origin := sweepRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, main, "clone", "-q", origin, other)
	gitIn(t, other, "config", "user.email", "t@example.com")
	gitIn(t, other, "config", "user.name", "T")
	gitIn(t, other, "commit", "-q", "--allow-empty", "-m", "elsewhere")
	gitIn(t, other, "push", "-q", "origin", "HEAD:refs/heads/backup/elsewhere")
	p, err := refsPlanJSON(t, ctx, RefsOptions{Remote: true, NoFetch: true, OpenPRs: noPRs})
	if err != nil {
		t.Fatal(err)
	}
	if it := refPlanItem(t, p, "origin:refs/heads/backup/elsewhere"); it.Category != RefKept || it.Tip != nil ||
		!slices.Contains(it.Kept, KeptObjectMissing) {
		t.Errorf("missing = %+v", it)
	}
}

// A restore whose ref is already back drops the pin only while the ref is
// still there; a run id that is not one is still an object its schema takes.
func TestRefsRestoreNoneKeepsThePinOfARefThatWent(t *testing.T) {
	ctx, main := backupsRepo(t)
	r := sweepAll(t, ctx, RefsOptions{Only: []string{"refs/heads/backup/old"}})
	run := *r.RunID
	obj := refResultItem(t, r, "refs/heads/backup/old").Object
	gitIn(t, main, "branch", "backup/old", obj)
	plan, err := planRefsRestore(ctx, run)
	if err != nil || plan.Rows[0].Action != RestoreNone {
		t.Fatalf("%v %+v", err, plan.Rows)
	}
	gitIn(t, main, "branch", "-D", "backup/old")
	st := restoreRef(main, "", plan.Rows[0])
	if st.kept == "" || st.pinDeleted {
		t.Errorf("state = %+v", st)
	}
	if _, ok, _ := refValue(main, plan.Rows[0].Pin); !ok {
		t.Error("the pin must stay while the ref is gone")
	}
	if _, err := restorePlanJSON(t, ctx, "not-a-run", nil); err == nil {
		t.Error("a run id that is not one must be an error")
	}
	res, err := restoreJSON(t, ctx, "not-a-run", RefsRestoreOptions{})
	if err == nil || res.Outcome != OutcomeRefused || res.RunID != "not-a-run" {
		t.Errorf("%v %+v", err, res)
	}
}
