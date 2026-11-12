package wtsync

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// checkRepo builds a repository whose trunk declares one each_commit check
// over src/**: it appends the subject of the commit it runs at to a log
// outside the repository, and fails when a file under src/ says BROKEN. It
// returns the main checkout, the feature worktree, the declaration and the
// log.
func checkRepo(t *testing.T, trunkEdits, branchEdits []map[string]string, extra string) (dir, wt string, cfg *Config, logPath string) {
	t.Helper()
	dir = repoWith(t, map[string]string{"a.txt": "a\n", "src/x.txt": "x\n", "doc.md": "d\n", "v.txt": "1.0.0\n"}, trunkEdits, branchEdits)
	logPath = filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-checks.log")
	yaml := "each_commit:\n  - run: git log -1 --format=%s >> '" + logPath + "' && ! grep -rq BROKEN src\n    paths: [\"src/**\"]\n" + extra
	if err := os.WriteFile(filepath.Join(dir, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "declare")
	gitIn(t, dir, "remote", "add", "origin", dir)
	gitIn(t, dir, "fetch", "-q", "origin")
	gitIn(t, dir, "config", "core.hooksPath", filepath.Join(dir, ".git", "hooks"))
	cfg, err := LoadFromTrunk(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	return dir, featureWorktree(t, dir).Path, cfg, logPath
}

// checkedSubjects is the log the fixture's check writes, one subject a line.
func checkedSubjects(t *testing.T, logPath string) []string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(b)), "branch ", "branch_"))
}

func TestRebaseChecksEachReplayedCommitTouchingItsPaths(t *testing.T) {
	t.Parallel()
	dir, wt, cfg, logPath := checkRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"src/x.txt": "x2\n"}, {"doc.md": "d2\n"}, {"src/y.txt": "y\n"}}, "")
	var log bytes.Buffer
	res, err := Rebase(dir, cfg, trunkReq(wt, 7), &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res.Left != nil || res.Restored || res.Replayed != 3 {
		t.Fatalf("result %+v\n%s", res, log.String())
	}
	if res.Checked != 2 {
		t.Errorf("checked %d, want 2", res.Checked)
	}
	if got, want := checkedSubjects(t, logPath), []string{"branch_1", "branch_3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("checked at %v, want %v", got, want)
	}
	for _, want := range []string{"▸ check 1/2 ", "branch 1", "▸ check 2/2 ", "branch 3"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}
}

func TestRebaseStopsAtTheCommitWhoseCheckFailsAndResumeRechecksIt(t *testing.T) {
	t.Parallel()
	dir, wt, cfg, logPath := checkRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"src/x.txt": "x2\n"}, {"src/y.txt": "BROKEN\n"}, {"doc.md": "d2\n"}, {"src/z.txt": "z\n"}}, "")
	old := gitIn(t, wt, "rev-parse", "HEAD")
	var log bytes.Buffer
	res, err := Rebase(dir, cfg, trunkReq(wt, 8), &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res.Left == nil || res.Left.Check == nil {
		t.Fatalf("no check handover: %+v\n%s", res, log.String())
	}
	c := res.Left.Check
	if c.Subject != "branch 2" || c.Step != cfg.EachCommit[0].Run || c.Err == "" {
		t.Fatalf("check failure %+v", c)
	}
	if res.Left.Index != 2 || res.Left.Total != 4 {
		t.Errorf("stop %d/%d, want 2/4", res.Left.Index, res.Left.Total)
	}
	if busy, _ := RebaseInProgress(wt); !busy {
		t.Fatal("the rebase is not left in progress")
	}
	if gitIn(t, wt, "log", "-1", "--format=%s") != "branch 2" {
		t.Fatalf("HEAD is %q, not the failing commit", gitIn(t, wt, "log", "-1", "--format=%s"))
	}
	if got, want := checkedSubjects(t, logPath), []string{"branch_1", "branch_2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("checked at %v, want %v", got, want)
	}
	// The person amends the fix into the failing commit and resumes.
	gittestWrite(t, filepath.Join(wt, "src/y.txt"), "fixed\n")
	gitIn(t, wt, "commit", "-q", "-a", "--amend", "--no-edit")
	safety := res.Safety
	req := trunkReq(wt, 8)
	var rlog bytes.Buffer
	res, err = Resume(dir, cfg, req, old, safety, &rlog)
	if err != nil {
		t.Fatalf("resume: %v\n%s", err, rlog.String())
	}
	if res.Left != nil || res.Restored {
		t.Fatalf("resume result %+v\n%s", res, rlog.String())
	}
	if busy, _ := RebaseInProgress(wt); busy {
		t.Fatal("the rebase is still in progress")
	}
	// branch 2 again (the recheck), then branch 4; branch 3 touches no src.
	if got, want := checkedSubjects(t, logPath), []string{"branch_1", "branch_2", "branch_2", "branch_4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("checked at %v, want %v", got, want)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "src/y.txt")); string(got) != "fixed\n" {
		t.Fatalf("src/y.txt %q", got)
	}
}

func TestResumeStopsAgainWhenTheRecheckStillFails(t *testing.T) {
	t.Parallel()
	dir, wt, cfg, _ := checkRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"src/y.txt": "BROKEN\n"}, {"src/z.txt": "z\n"}}, "")
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, trunkReq(wt, 9), nil)
	if err != nil || res.Left == nil || res.Left.Check == nil {
		t.Fatalf("res %+v err %v", res, err)
	}
	res, err = Resume(dir, cfg, trunkReq(wt, 9), old, res.Safety, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Left == nil || res.Left.Check == nil || res.Left.Check.Subject != "branch 1" {
		t.Fatalf("resume did not stop again at the failing check: %+v", res)
	}
	if busy, _ := RebaseInProgress(wt); !busy {
		t.Fatal("the rebase is not left in progress")
	}
}

func TestRebaseRestoresAStackParentWhoseCheckFails(t *testing.T) {
	t.Parallel()
	dir, wt, cfg, _ := checkRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"src/y.txt": "BROKEN\n"}, {"src/z.txt": "z\n"}}, "")
	old := gitIn(t, wt, "rev-parse", "HEAD")
	req := trunkReq(wt, 10)
	req.Stacked = true
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Restored || res.Left != nil || res.CheckFailed == nil || res.CheckFailed.Subject != "branch 1" {
		t.Fatalf("result %+v", res)
	}
	if gitIn(t, wt, "rev-parse", "HEAD") != old {
		t.Fatal("not back at the old tip")
	}
	if busy, _ := RebaseInProgress(wt); busy {
		t.Fatal("rebase still in progress")
	}
}

func TestRebaseFailsACheckThatChangesTrackedFiles(t *testing.T) {
	t.Parallel()
	dir := repoWith(t, map[string]string{"a.txt": "a\n", "src/x.txt": "x\n"},
		[]map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"src/x.txt": "x2\n"}})
	yaml := "each_commit:\n  - run: echo touched > a.txt\n"
	gittestWrite(t, filepath.Join(dir, ".wt-sync.yaml"), yaml)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "declare")
	gitIn(t, dir, "remote", "add", "origin", dir)
	gitIn(t, dir, "fetch", "-q", "origin")
	cfg, err := LoadFromTrunk(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	wt := featureWorktree(t, dir).Path
	res, err := Rebase(dir, cfg, trunkReq(wt, 11), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Left == nil || res.Left.Check == nil || !strings.Contains(res.Left.Check.Err, "changed tracked files") {
		t.Fatalf("result %+v", res.Left)
	}
}

// A pick that stops is checked once the strategy's answer is committed: the
// check sees the resolved version, not the branch's or trunk's.
func TestRebaseChecksACommitAfterItsStopIsResolved(t *testing.T) {
	t.Parallel()
	dir := repoWith(t, map[string]string{"a.txt": "a\n", "v.txt": "1.0.0\n"},
		[]map[string]string{{"v.txt": "1.0.5\n", "a.txt": "a2\n"}},
		[]map[string]string{{"v.txt": "1.0.1\n", "b.txt": "b\n"}})
	logPath := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-lift.log")
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n" +
		"each_commit:\n  - run: cat v.txt >> '" + logPath + "' && git diff --quiet HEAD\n"
	gittestWrite(t, filepath.Join(dir, ".wt-sync.yaml"), yaml)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "declare")
	gitIn(t, dir, "remote", "add", "origin", dir)
	gitIn(t, dir, "fetch", "-q", "origin")
	cfg, err := LoadFromTrunk(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	wt := featureWorktree(t, dir).Path
	var log bytes.Buffer
	res, err := Rebase(dir, cfg, trunkReq(wt, 12), &log)
	if err != nil || res.Left != nil || res.Checked != 1 {
		t.Fatalf("res %+v err %v\n%s", res, err, log.String())
	}
	b, _ := os.ReadFile(logPath)
	if string(b) != "1.0.6\n" {
		t.Fatalf("the check saw v.txt %q, want the resolved 1.0.6", b)
	}
}

func TestPlanChecksListsTheCommitsARunWouldCheck(t *testing.T) {
	t.Parallel()
	dir, wt, cfg, logPath := checkRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"src/x.txt": "x2\n"}, {"doc.md": "d2\n"}, {"src/y.txt": "y\n"}}, "")
	commits, err := PlanChecks(wt, cfg, "origin/main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for _, c := range commits {
		subjects = append(subjects, c.Subject)
	}
	if want := []string{"branch 1", "branch 3"}; !reflect.DeepEqual(subjects, want) {
		t.Fatalf("would check %v, want %v", subjects, want)
	}
	if got := checkedSubjects(t, logPath); len(got) != 0 {
		t.Fatalf("planning ran the check: %v", got)
	}
	_ = dir
}

func gittestWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A fix a person adds as a commit of its own, on top of the commit whose
// check failed, is checked on resume even when it touches no listed path:
// the checks are the failing commit's.
func TestResumeRechecksAfterAnAddedCommitOutsideThePaths(t *testing.T) {
	t.Parallel()
	dir := repoWith(t, map[string]string{"a.txt": "a\n", "src/x.txt": "x\n"},
		[]map[string]string{{"a.txt": "a2\n"}}, []map[string]string{{"src/broken": "b\n"}})
	logPath := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-added.log")
	yaml := "each_commit:\n  - run: git log -1 --format=%s >> '" + logPath + "' && { test ! -f src/broken || test -f fixed.flag; }\n    paths: [\"src/**\"]\n"
	gittestWrite(t, filepath.Join(dir, ".wt-sync.yaml"), yaml)
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "declare")
	gitIn(t, dir, "remote", "add", "origin", dir)
	gitIn(t, dir, "fetch", "-q", "origin")
	cfg, err := LoadFromTrunk(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	wt := featureWorktree(t, dir).Path
	old := gitIn(t, wt, "rev-parse", "HEAD")
	res, err := Rebase(dir, cfg, trunkReq(wt, 14), nil)
	if err != nil || res.Left == nil || res.Left.Check == nil {
		t.Fatalf("res %+v err %v", res, err)
	}
	gittestWrite(t, filepath.Join(wt, "fixed.flag"), "ok\n")
	gitIn(t, wt, "add", "fixed.flag")
	gitIn(t, wt, "commit", "-q", "-m", "fix")
	res, err = Resume(dir, cfg, trunkReq(wt, 14), old, res.Safety, nil)
	if err != nil || res.Left != nil {
		t.Fatalf("resume %+v err %v", res, err)
	}
	if got, want := checkedSubjects(t, logPath), []string{"branch_1", "fix"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("checked at %v, want %v", got, want)
	}
}
