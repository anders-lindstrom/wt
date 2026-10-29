package refs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/retire"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/jsonout"
	"github.com/anders-lindstrom/wt/internal/refpins"
)

func RefsPlanJSON(t *testing.T, ctx *kit.Context, opts Options) (PlanOutput, error) {
	t.Helper()
	var out, progress bytes.Buffer
	err := SweepPlanJSON(ctx, opts, &out, &progress)
	testkit.ValidateJSON(t, "refs-sweep-plan", out.Bytes())
	var p PlanOutput
	if jerr := json.Unmarshal(out.Bytes(), &p); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	return p, err
}

func RefPlanItem(t *testing.T, p PlanOutput, id string) RefItem {
	t.Helper()
	for _, it := range p.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s in %+v", id, p.Items)
	return RefItem{}
}

func RefResultOf(t *testing.T, r Result, id string) *RefResultItem {
	t.Helper()
	for _, it := range r.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s in %+v", id, r.Items)
	return nil
}

// The plan names every backup and what it is, measures age from when the
// branch was made rather than from its commits, and writes nothing.
func TestRefsPlanSortsTheBackups(t *testing.T) {
	t.Parallel()
	ctx, main := testkit.BackupsRepo(t)
	before := testkit.RepoState(t, main)
	p, err := RefsPlanJSON(t, ctx, Options{NoFetch: true})
	if err != nil {
		t.Fatal(err)
	}
	if after := testkit.RepoState(t, main); after != before {
		t.Errorf("the plan wrote to the repository:\nbefore %s\nafter %s", before, after)
	}
	if p.Command != "refs sweep" || p.Token == nil || !strings.HasPrefix(*p.Token, "rs1-") || p.RunID != nil ||
		jsonout.Deref(p.Trunk) != "main" || jsonout.Deref(p.Age) != "14d" || len(p.Patterns) != 9 {
		t.Fatalf("plan = %+v", p)
	}
	it := RefPlanItem(t, p, "refs/heads/backup/contained")
	if it.Category != RefBackupContained || !it.Selected || jsonout.Deref(it.ContainedIn) != "refs/heads/main" ||
		it.UniqueCommits != nil || it.Pattern != "backup/*" || jsonout.Deref(it.DateSource) != DateReflog || it.Annotated != nil {
		t.Errorf("contained = %+v", it)
	}
	it = RefPlanItem(t, p, "refs/heads/backup/old")
	if it.Category != RefBackupOld || !it.Selected || it.ContainedIn != nil || it.UniqueCommits == nil ||
		*it.UniqueCommits != 1 || jsonout.Deref(it.Subject) != "old work" {
		t.Errorf("old = %+v", it)
	}
	it = RefPlanItem(t, p, "refs/heads/backup/rebased")
	if it.Category != RefBackupYoung || it.Selected || jsonout.Deref(it.DateSource) != DateReflog {
		t.Errorf("a backup made today of an old branch must be young: %+v", it)
	}
	it = RefPlanItem(t, p, "refs/tags/safe-tag")
	if it.Category != RefBackupContained || it.Annotated == nil || !*it.Annotated || jsonout.Deref(it.DateSource) != DateTagger ||
		it.Object == jsonout.Deref(it.Tip) || jsonout.Deref(it.ContainedIn) != "refs/heads/main" {
		t.Errorf("tag = %+v", it)
	}
	if it := RefPlanItem(t, p, "refs/heads/backup/checked"); it.Category != RefKept || it.Selected ||
		!slices.Equal(it.Kept, []string{KeptWorktree}) {
		t.Errorf("checked out = %+v", it)
	}
	if it := RefPlanItem(t, p, "refs/heads/release-backup"); it.Category != RefKept ||
		!slices.Equal(it.Kept, []string{KeptProtected}) {
		t.Errorf("protected = %+v", it)
	}
	for _, it := range p.Items {
		if it.Name == "feature" || it.Name == "main" {
			t.Errorf("%s matches no pattern and must not be listed", it.Name)
		}
	}
}

// A ref that moves between the plan and its move is kept, and the pin is
// not made.
func TestRefsSweepKeepsARefThatMoved(t *testing.T) {
	t.Parallel()
	ctx, main := testkit.BackupsRepo(t)
	plan, err := planRefs(ctx, Options{NoFetch: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	testkit.CommitOn(t, main, "backup/old", "more", time.Now())
	var out bytes.Buffer
	j := NewRefsJournal(&out)
	j.planned(plan, refsToken(plan))
	run := refpins.NewRunID(time.Now())
	j.begin(run)
	if err := plan.apply(ctx, run, j, &bytes.Buffer{}); err == nil {
		t.Error("a run that kept a row must say so")
	}
	j.Finish()
	var r Result
	_ = json.Unmarshal(out.Bytes(), &r)
	it := RefResultOf(t, r, "refs/heads/backup/old")
	if it.Result != RefResultKept || it.Pinned || it.Deleted || !ctx.Repo.BranchExists("backup/old") {
		t.Errorf("moved = %+v", it)
	}
	if r.Outcome != jsonout.OutcomePartial {
		t.Errorf("outcome = %s", r.Outcome)
	}
}

// A branch a live quarantine names is kept: restoring the quarantine would
// need it.
func TestRefsSweepKeepsWhatAQuarantineNames(t *testing.T) {
	t.Parallel()
	ctx, path := provisiontest.SafetyWorktree(t, "fix/trashed")
	tip := strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	var buf bytes.Buffer
	if err := retire.RemoveAt(ctx, path, retire.RemoveOptions{Quarantine: testkit.TrashFor(t, ctx)}, &buf); err != nil {
		t.Fatalf("RemoveAt: %v\n%s", err, buf.String())
	}
	testkit.GitIn(t, ctx.Repo.MainRoot, "branch", "fix_wt/trashed", tip)
	ctx.User.RefSweepPatterns = []string{"fix_wt/*"}
	p, err := RefsPlanJSON(t, ctx, Options{NoFetch: true})
	if err != nil {
		t.Fatal(err)
	}
	if it := RefPlanItem(t, p, "refs/heads/fix_wt/trashed"); it.Category != RefKept ||
		!slices.Contains(it.Kept, KeptQuarantine) {
		t.Errorf("quarantined = %+v", it)
	}
	// The code is the JSON's; the plan a person reads says it in words.
	var out, human bytes.Buffer
	if err := SweepPlanJSON(ctx, Options{NoFetch: true}, &out, &human); err != nil {
		t.Fatal(err)
	}
	if printed := human.String(); !strings.Contains(printed, "a worktree moved aside") ||
		strings.Contains(printed, "quarantine") {
		t.Errorf("the printed plan:\n%s", printed)
	}
}

// The journal a signal finishes writes one object: the row in flight read
// back and interrupted.
func TestRefsJournalWritesOneObjectWhenInterrupted(t *testing.T) {
	t.Parallel()
	ctx, _ := testkit.BackupsRepo(t)
	plan, err := planRefs(ctx, Options{NoFetch: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	j := NewRefsJournal(&out)
	j.planned(plan, refsToken(plan))
	j.begin(refpins.NewRunID(time.Now()))
	j.start("refs/heads/backup/old")
	j.Interrupted("", "interrupted")
	j.Finish()
	testkit.ValidateJSON(t, "refs-sweep", out.Bytes())
	var r Result
	dec := json.NewDecoder(&out)
	if err := dec.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if dec.More() {
		t.Error("a second object was written")
	}
	if r.Outcome != jsonout.OutcomeInterrupted || RefResultOf(t, r, "refs/heads/backup/old").Result != RefInterrupted ||
		jsonout.Deref(r.Recovery) != "interrupted" {
		t.Errorf("%+v", r)
	}
}

// The plan stays fast on a repository shaped like the ones it was built
// for: about a hundred backups, half of them rebased away, and fifty other
// branches. The time is logged (go test -v -run TestRefsPlanTime); the
// bound is only there to catch a pass per backup per container.
func TestRefsPlanTime(t *testing.T) {
	// Not parallel: times the plan against a wall-clock bound.
	if testing.Short() {
		t.Skip("performance: builds 300 commits and 150 refs")
	}
	ctx, main, _ := testkit.SweepRepo(t)
	var refs strings.Builder
	tree := testkit.GitOut(t, main, "rev-parse", "main^{tree}")
	parent := testkit.GitOut(t, main, "rev-parse", "main")
	for i := range 200 {
		parent = testkit.GitOut(t, main, "commit-tree", "-p", parent, "-m", fmt.Sprintf("trunk %d", i), tree)
	}
	testkit.GitIn(t, main, "update-ref", "refs/heads/main", parent)
	history := strings.Split(testkit.GitOut(t, main, "rev-list", "main"), "\n")
	for i := range 50 {
		c := testkit.GitOut(t, main, "commit-tree", "-p", history[i*3], "-m", fmt.Sprintf("feature %d", i), tree)
		fmt.Fprintf(&refs, "create refs/heads/feature/%d %s\n", i, c)
	}
	for i := range 100 {
		at := history[i]
		if i%2 == 1 {
			at = testkit.GitOut(t, main, "commit-tree", "-p", history[i], "-m", fmt.Sprintf("backup %d", i), tree)
		}
		fmt.Fprintf(&refs, "create refs/heads/backup/b%d %s\n", i, at)
	}
	cmd := exec.Command("git", "-C", main, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(refs.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	start := time.Now()
	p, err := planRefs(ctx, Options{NoFetch: true}, &bytes.Buffer{})
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
	if err := p.apply(ctx, refpins.NewRunID(time.Now()), nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	t.Logf("swept %d of them in %v", len(p.Selected()), time.Since(start))
}

// --remote needs origin to fetch from and push to one and the same URL, with
// no secret in it; the plan names that URL, redacted, and the token covers it.
func TestRefsRemoteEndpoint(t *testing.T) {
	t.Parallel()
	ctx, main, origin := testkit.SweepRepo(t)
	p, err := RefsPlanJSON(t, ctx, Options{Remote: true, NoFetch: true, OpenPRs: testkit.NoPRs})
	if err != nil || p.Endpoint == nil || p.Endpoint.URL != origin || !strings.HasPrefix(p.Endpoint.Digest, "sha256:") {
		t.Fatalf("%v endpoint = %+v", err, p.Endpoint)
	}
	for name, setup := range map[string][]string{
		"a push URL of its own": {"remote", "set-url", "--push", "origin", t.TempDir()},
		"two push URLs":         {"remote", "set-url", "--add", "--push", "origin", t.TempDir()},
	} {
		testkit.GitIn(t, main, "remote", "set-url", "origin", origin)
		_, _ = exec.Command("git", "-C", main, "config", "--unset-all", "remote.origin.pushurl").CombinedOutput()
		if name == "two push URLs" {
			testkit.GitIn(t, main, "remote", "set-url", "--add", "--push", "origin", origin)
		}
		testkit.GitIn(t, main, setup...)
		p, err := RefsPlanJSON(t, ctx, Options{Remote: true, NoFetch: true, OpenPRs: testkit.NoPRs})
		if err == nil || p.Token != nil || len(p.Problems) != 1 || p.Problems[0].Code != ProblemRemoteAmbiguous {
			t.Errorf("%s: %v %+v", name, err, p.Problems)
		}
	}
	_, _ = exec.Command("git", "-C", main, "config", "--unset-all", "remote.origin.pushurl").CombinedOutput()
	for _, url := range []string{"https://token@example.com/o/r.git", "https://u:p@example.com/o/r.git",
		"ssh://git:secret@example.com/o/r.git", "user:secret@example.com:o/r.git"} {
		testkit.GitIn(t, main, "remote", "set-url", "origin", url)
		p, err := RefsPlanJSON(t, ctx, Options{Remote: true, NoFetch: true, OpenPRs: testkit.NoPRs})
		if err == nil || len(p.Problems) != 1 || p.Problems[0].Code != ProblemRemoteCredentials ||
			strings.Contains(jsonout.Deref(p.Error), "example.com") {
			t.Errorf("%s: %v %+v", url, err, p.Problems)
		}
	}
	for url, want := range map[string]string{"git@example.com:o/r.git": "example.com:o/r.git",
		"ssh://git@example.com/o/r.git": "ssh://example.com/o/r.git"} {
		testkit.GitIn(t, main, "remote", "set-url", "origin", url)
		ep, err := refpins.ResolveEndpoint(main)
		if err != nil || ep.URL != want {
			t.Errorf("%s: %v %+v", url, err, ep)
		}
	}
	testkit.GitIn(t, main, "remote", "set-url", "origin", origin)
	if p, err := RefsPlanJSON(t, ctx, Options{NoFetch: true}); err != nil || p.Endpoint != nil {
		t.Errorf("without --remote origin's URLs do not matter: %v", err)
	}
}

// A remote row the push cannot delete drops its pin and leaves origin as it
// was; one origin moved after the plan is kept and pinned nowhere.
func TestRefsSweepRemoteGuards(t *testing.T) {
	t.Parallel()
	ctx, main, origin := testkit.SweepRepo(t)
	testkit.GitIn(t, main, "commit", "-q", "--allow-empty", "-m", "second")
	testkit.GitIn(t, main, "push", "-q", "origin", "main", "main:refs/heads/backup/rejected", "main~1:refs/heads/backup/moved")
	testkit.GitIn(t, main, "fetch", "-q", "origin")
	plan, err := planRefs(ctx, Options{Remote: true, NoFetch: true, OpenPRs: testkit.NoPRs}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, main, "push", "-q", "--force", "origin", "main:refs/heads/backup/moved")
	hook := filepath.Join(origin, "hooks", "pre-receive")
	gittest.WriteExecutable(t, hook, "#!/bin/sh\nexit 1\n")
	var out bytes.Buffer
	j := NewRefsJournal(&out)
	j.planned(plan, refsToken(plan))
	run := refpins.NewRunID(time.Now())
	j.begin(run)
	_ = plan.apply(ctx, run, j, &bytes.Buffer{})
	j.Finish()
	testkit.ValidateJSON(t, "refs-sweep", out.Bytes())
	var r Result
	_ = json.Unmarshal(out.Bytes(), &r)
	if it := RefResultOf(t, r, "origin:refs/heads/backup/rejected"); it.Result != RefFailed || it.Pinned || it.Deleted {
		t.Errorf("rejected = %+v", it)
	}
	if it := RefResultOf(t, r, "origin:refs/heads/backup/moved"); it.Result != RefResultKept || it.Pinned {
		t.Errorf("moved = %+v", it)
	}
	if testkit.GitOut(t, origin, "for-each-ref", "--format=%(refname)", "refs/heads/backup/") == "" {
		t.Error("origin must keep both")
	}
	if r.Outcome != jsonout.OutcomeRefused {
		t.Errorf("outcome = %s", r.Outcome)
	}
	if left := testkit.GitOut(t, main, "for-each-ref", refpins.SweptPrefix); left != "" {
		t.Errorf("a run that moved nothing leaves nothing behind: %s", left)
	}
}

// A branch checked out after the plan is kept at apply time.
func TestRefsSweepKeepsABranchCheckedOutSinceThePlan(t *testing.T) {
	t.Parallel()
	ctx, main := testkit.BackupsRepo(t)
	plan, err := planRefs(ctx, Options{NoFetch: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, main, "worktree", "add", "-q", filepath.Join(t.TempDir(), "late"), "backup/old")
	var out bytes.Buffer
	j := NewRefsJournal(&out)
	j.planned(plan, refsToken(plan))
	run := refpins.NewRunID(time.Now())
	j.begin(run)
	_ = plan.apply(ctx, run, j, &bytes.Buffer{})
	j.Finish()
	var r Result
	_ = json.Unmarshal(out.Bytes(), &r)
	if it := RefResultOf(t, r, "refs/heads/backup/old"); it.Result != RefResultKept || it.Pinned ||
		!ctx.Repo.BranchExists("backup/old") {
		t.Errorf("checked out since = %+v", it)
	}
}

// A remote backup whose object is not here is kept: containment cannot be
// read.
func TestRefsRemoteObjectMissing(t *testing.T) {
	t.Parallel()
	ctx, main, origin := testkit.SweepRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	testkit.GitIn(t, main, "clone", "-q", origin, other)
	testkit.GitIn(t, other, "config", "user.email", "t@example.com")
	testkit.GitIn(t, other, "config", "user.name", "T")
	testkit.GitIn(t, other, "commit", "-q", "--allow-empty", "-m", "elsewhere")
	testkit.GitIn(t, other, "push", "-q", "origin", "HEAD:refs/heads/backup/elsewhere")
	p, err := RefsPlanJSON(t, ctx, Options{Remote: true, NoFetch: true, OpenPRs: testkit.NoPRs})
	if err != nil {
		t.Fatal(err)
	}
	if it := RefPlanItem(t, p, "origin:refs/heads/backup/elsewhere"); it.Category != RefKept || it.Tip != nil ||
		!slices.Contains(it.Kept, KeptObjectMissing) {
		t.Errorf("missing = %+v", it)
	}
}
