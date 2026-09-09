package wtsync

import (
	"testing"
	"time"
)

func TestWriteSafetyPinsTheTipUnderTheBranchAndEpoch(t *testing.T) {
	dir := linearRepo(t, nil, []map[string]string{{"a.txt": "a2\n"}})
	tip := gitIn(t, dir, "rev-parse", "feature")
	s, err := WriteSafety(dir, "feature", tip, 1700000000)
	if err != nil {
		t.Fatal(err)
	}
	if s.Ref != "refs/wt-sync/feature/1700000000" || s.Tip != tip || s.Branch != "feature" || s.Epoch != 1700000000 {
		t.Fatalf("got %+v", s)
	}
	if got := gitIn(t, dir, "rev-parse", s.Ref); got != tip {
		t.Fatalf("ref points at %s, want %s", got, tip)
	}
}

func TestListSafetyIsNewestFirstAndLatestPicksPerBranch(t *testing.T) {
	dir := linearRepo(t, nil, []map[string]string{{"a.txt": "a2\n"}})
	tip := gitIn(t, dir, "rev-parse", "feature")
	for _, e := range []int64{100, 300, 200} {
		if _, err := WriteSafety(dir, "feature", tip, e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := WriteSafety(dir, "team/other", tip, 250); err != nil {
		t.Fatal(err)
	}
	all, err := ListSafety(dir)
	if err != nil {
		t.Fatal(err)
	}
	var epochs []int64
	for _, s := range all {
		epochs = append(epochs, s.Epoch)
	}
	if len(epochs) != 4 || epochs[0] != 300 || epochs[1] != 250 || epochs[2] != 200 || epochs[3] != 100 {
		t.Fatalf("epochs %v", epochs)
	}
	latest, ok, err := LatestSafety(dir, "team/other")
	if err != nil || !ok || latest.Epoch != 250 || latest.Branch != "team/other" {
		t.Fatalf("latest %+v ok=%v err=%v", latest, ok, err)
	}
	if _, ok, _ := LatestSafety(dir, "nobody"); ok {
		t.Fatal("a branch with no safety ref reported one")
	}
}

func TestPrunableKeepsEveryNewestRunGroupAndAnythingYoung(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	ago := func(days int) int64 { return now.Add(-time.Duration(days) * 24 * time.Hour).UnixNano() }
	all := []Safety{
		{Branch: "a", Epoch: ago(1)},  // newest for a, young
		{Branch: "a", Epoch: ago(40)}, // old: prunable
		{Branch: "b", Epoch: ago(50)}, // old but newest for b: kept
		{Branch: "b", Epoch: ago(60)}, // prunable
		{Branch: "c", Epoch: ago(2)},  // young
		{Branch: "c", Epoch: ago(3)},  // young, not newest: kept because young
		{Branch: "p", Epoch: ago(45)}, // old, not p's newest ...
		{Branch: "p", Epoch: ago(10)}, // p's newest
		{Branch: "q", Epoch: ago(45)}, // ... but ago(45) is q's newest: the whole group is kept
	}
	got := Prunable(all, now, 30*24*time.Hour)
	if len(got) != 2 || got[0].Epoch != ago(40) || got[1].Epoch != ago(60) {
		t.Fatalf("prunable %+v", got)
	}
}

func TestDeleteSafetyRemovesTheRef(t *testing.T) {
	dir := linearRepo(t, nil, []map[string]string{{"a.txt": "a2\n"}})
	tip := gitIn(t, dir, "rev-parse", "feature")
	s, err := WriteSafety(dir, "feature", tip, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteSafety(dir, s); err != nil {
		t.Fatal(err)
	}
	all, _ := ListSafety(dir)
	if len(all) != 0 {
		t.Fatalf("still listed: %+v", all)
	}
}
