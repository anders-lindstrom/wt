package wtsync

import (
	"strings"
	"testing"
)

// Every situation a sync verb can leave a worktree in has one sentence, and
// each names the verbs that get a person out of it and none that would
// refuse or discard. This is where the wording is pinned; the callers only
// prefix it with what they know.
func TestWayOutCoversEverySituation(t *testing.T) {
	const (
		resume = "wt sync resume"
		undo   = "wt sync undo"
		force  = "wt sync undo --force"
		abort  = "rebase --abort"
		add    = "git add"
		run    = "wt sync run"
	)
	named := func(w Way) Way { w.Work, w.Path = "bump", "/w/bump"; return w }
	for _, tc := range []struct {
		name      string
		w         Way
		want, not []string
	}{
		{"A: handed over, resume or undo", Way{Plan: true, Rebasing: true},
			[]string{resume + " bump", undo + " bump"}, []string{force, abort, add}},
		{"A without a name", Way{Plan: true, Rebasing: true, Work: "", Path: ""},
			[]string{resume + ", or " + undo}, []string{force, abort, add, "bump"}},
		{"B: git add first", Way{Plan: true, Rebasing: true, OwesAdd: true},
			[]string{add, resume + " bump", undo + " bump"}, []string{force, abort}},
		{"C: a rebase with no plan", Way{Rebasing: true},
			[]string{"git -C /w/bump " + abort, "by hand"}, []string{undo, resume}},
		{"C with the old tip", Way{Rebasing: true, Safety: "refs/wt-sync/feat_wt/bump/99"},
			[]string{"git -C /w/bump " + abort, "refs/wt-sync/feat_wt/bump/99"}, []string{undo}},
		{"D: finished by hand", Way{Plan: true, Finished: true},
			[]string{"finished by hand", resume + " bump", force + " bump", "safety ref"}, []string{abort, add}},
		{"D without a name", Way{Plan: true, Finished: true, Work: "", Path: ""},
			[]string{"finished by hand", resume + " runs", force + " puts"}, []string{"bump"}},
		{"E: aborted by hand", Way{Plan: true, Aborted: true},
			[]string{undo + " bump", run + " bump"}, []string{resume, force, abort}},
		{"F: moved, undo needs force", Way{Plan: true, Moved: true},
			[]string{force + " bump", "safety ref"}, []string{resume, abort, add}},
		{"G: interrupted after the rebase", Way{Moved: true},
			[]string{"the rebase stands", force + " bump"}, []string{resume, abort}},
		{"H: a completed run", Way{Result: true, Safety: "a1d198b"},
			[]string{undo + " bump puts it back (was a1d198b)"}, []string{force, resume, abort}},
		{"I: one commit inside", Way{Plan: true, Rebasing: true, Inside: 1},
			[]string{resume + " bump keeps it", force + " bump aborts and keeps it"}, []string{add, abort}},
		{"I: two commits inside", Way{Plan: true, Rebasing: true, Inside: 2},
			[]string{"keeps them", "keeps them under a safety ref"}, []string{add, abort, "keeps it"}},
		{"J: a rebase that is not the run's", Way{Plan: true, Rebasing: true, Foreign: true},
			[]string{"git -C /w/bump " + abort, "yourself", undo + " bump"}, []string{resume, force, add, "lose"}},
		{"J: the run's own, with commits undo cannot pin", Way{Plan: true, Rebasing: true, Foreign: true, Inside: 1},
			[]string{"git -C /w/bump " + abort, "yourself", "lose those commits", undo + " bump"}, []string{resume, force, add}},
		{"J: the same, on a branch that also moved", Way{Plan: true, Rebasing: true, Foreign: true, Moved: true, Inside: 1},
			[]string{"git -C /w/bump " + abort, "lose those commits", force + " bump"}, []string{resume, add}},
		{"I: an old sidecar that cannot prove anything", Way{Plan: true, Rebasing: true, Unproven: true},
			[]string{resume + " bump carries on", force + " bump aborts and keeps what is at HEAD"}, []string{add, abort, "keeps it"}},
	} {
		w := tc.w
		if !strings.Contains(tc.name, "without a name") {
			w = named(w)
		}
		got := WayOut(w)
		for _, s := range tc.want {
			if !strings.Contains(got, s) {
				t.Errorf("%s: %q lacks %q", tc.name, got, s)
			}
		}
		for _, s := range tc.not {
			if strings.Contains(got, s) {
				t.Errorf("%s: %q must not say %q", tc.name, got, s)
			}
		}
		if strings.Contains(got, "\n") || strings.HasSuffix(got, ".") {
			t.Errorf("%s: %q is not one sentence without a full stop", tc.name, got)
		}
	}
}

// A Way nothing is true of is an unknown state: the sentence still names
// undo rather than saying nothing, since a person reading it has to do
// something.
func TestWayOutOfNothingStillNamesUndo(t *testing.T) {
	if got := WayOut(Way{Work: "bump"}); !strings.Contains(got, "wt sync undo bump") {
		t.Errorf("got %q", got)
	}
}
