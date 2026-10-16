package retire

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// A signal writes the one object: the item in flight interrupted, those not
// reached not run, and nothing written a second time.
func TestSweepJournalWritesOneObjectWhenInterrupted(t *testing.T) {
	ctx, main, _ := testkit.SweepRepo(t)
	testkit.BranchWithWork(t, main, "one", 0)
	testkit.BranchWithWork(t, main, "two", 0)
	plan := PlanOf(t, ctx)

	var out bytes.Buffer
	j := NewSweepJournal(&out)
	j.begin(ctx, plan, "1:abc")
	j.start(sweepKey(plan.Delete[0]))
	j.Interrupted("", "interrupted")
	j.Finish()
	testkit.ValidateJSON(t, "sweep", out.Bytes())
	dec := json.NewDecoder(&out)
	var r SweepResult
	if err := dec.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if dec.More() {
		t.Error("a second object was written")
	}
	if r.Outcome != kit.OutcomeInterrupted || r.Recovery == nil || r.Items[0].Result != SweepInterrupted || r.Items[1].Result != SweepNotRun {
		t.Errorf("%+v", r)
	}
}

// The outcome rules, as docs/json.md states them.
func TestSweepOutcomeRules(t *testing.T) {
	items := func(results ...string) []*SweepResultItem {
		var out []*SweepResultItem
		for _, r := range results {
			out = append(out, &SweepResultItem{Category: SweepDelete, Result: r})
		}
		out = append(out, &SweepResultItem{Category: SweepInUse, Result: SweepKept})
		return out
	}
	for _, tc := range []struct {
		items []*SweepResultItem
		early bool
		want  string
	}{
		{items(SweepDeleted, SweepRemoved), false, kit.OutcomeDone},
		{items(), false, kit.OutcomeDone},
		{items(), true, kit.OutcomeRefused},
		{items(SweepKept, SweepNotRun), false, kit.OutcomeRefused},
		{items(SweepDeleted, SweepKept), false, kit.OutcomePartial},
		{items(SweepFailed), false, kit.OutcomeRefused},
		{[]*SweepResultItem{{Category: SweepRemove, Result: SweepFailed, WorktreeRemoved: true}}, false, kit.OutcomePartial},
	} {
		if got := sweepOutcome(tc.items, tc.early); got != tc.want {
			t.Errorf("%+v early=%v: got %s, want %s", tc.items, tc.early, got, tc.want)
		}
	}
}
