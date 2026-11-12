package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A deferred step whose need did not answer, in a run told to go on, is not
// run and is owed; the steps after it still run.
func TestRunDeferredOwesAStepWhoseNeedDidNotAnswer(t *testing.T) {
	t.Parallel()
	wt, old, cur := deferRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	steps := []Deferred{{Run: "touch '" + marker + "'", Needs: []string{NeedDocker}}, {Run: "true"}}
	rs, err := RunDeferredWith(wt, steps, old, cur, nil, map[string]string{NeedDocker: "exit 1"})
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Ran || rs[0].Err == nil || !strings.Contains(rs[0].Why, "docker did not answer") {
		t.Fatalf("the step that needs docker: %+v", rs[0])
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("the step ran")
	}
	if !rs[1].Ran || rs[1].Err != nil {
		t.Fatalf("the step after it: %+v", rs[1])
	}
}

// A check whose need did not answer is not run at any commit and is owed
// once for the branch.
func TestRebaseOwesACheckWhoseNeedDidNotAnswer(t *testing.T) {
	t.Parallel()
	dir, wt, cfg, logPath := checkRepo(t,
		[]map[string]string{{"a.txt": "a2\n"}},
		[]map[string]string{{"src/x.txt": "x2\n"}, {"src/y.txt": "y\n"}}, "    needs: [docker]\n")
	req := trunkReq(wt, 13)
	req.Unavailable = map[string]string{NeedDocker: "exit 1"}
	res, err := Rebase(dir, cfg, req, nil)
	if err != nil || res.Left != nil {
		t.Fatalf("res %+v err %v", res, err)
	}
	if res.Checked != 0 || len(res.Unchecked) != 1 || res.Unchecked[0].Step != cfg.EachCommit[0].Run {
		t.Fatalf("checked %d unchecked %+v", res.Checked, res.Unchecked)
	}
	if got := checkedSubjects(t, logPath); len(got) != 0 {
		t.Fatalf("the check ran at %v", got)
	}
}

func TestProbeNeedsAsksEachNeedOnce(t *testing.T) {
	t.Parallel()
	calls := map[string]int{}
	probes := map[string]func() error{NeedDocker: func() error { calls[NeedDocker]++; return os.ErrNotExist }}
	got := ProbeNeedsWith(probes, []string{NeedDocker, NeedDocker})
	if calls[NeedDocker] != 1 {
		t.Fatalf("probed %d times", calls[NeedDocker])
	}
	if got[NeedDocker] == nil {
		t.Fatal("the failed probe is not reported")
	}
}
