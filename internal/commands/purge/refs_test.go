package purge

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/refpins"
	"github.com/anders-lindstrom/wt/internal/commands/refs"
	"github.com/anders-lindstrom/wt/internal/commands/refs/refstest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// wt refs swept lists the runs; purge deletes a run's pins, counts what
// becomes unreachable, and --older-than picks runs by the time in their id.
func TestRefsSweptAndPurge(t *testing.T) {
	ctx, main := testkit.BackupsRepo(t)
	r := refstest.SweepAll(t, ctx, refs.Options{})
	run := *r.RunID
	var out bytes.Buffer
	if err := RefsSwept(ctx, true, &out); err != nil {
		t.Fatal(err)
	}
	testkit.ValidateJSON(t, "refs-swept", out.Bytes())
	var sw RefsSweptOutput
	_ = json.Unmarshal(out.Bytes(), &sw)
	if len(sw.Runs) != 1 || sw.Runs[0].RunID != run || len(sw.Runs[0].Refs) != 3 || sw.Runs[0].SweptAt == 0 {
		t.Fatalf("swept = %+v", sw)
	}

	var plan bytes.Buffer
	if err := RefsPurgePlanJSON(ctx, RefsPurgeOptions{OlderThan: "1d"}, &plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	testkit.ValidateJSON(t, "refs-purge-plan", plan.Bytes())
	var pp RefsPurgePlanOutput
	_ = json.Unmarshal(plan.Bytes(), &pp)
	if len(pp.Runs) != 0 || pp.Token != nil {
		t.Errorf("a run from today is not older than a day: %+v", pp)
	}
	plan.Reset()
	if err := RefsPurgePlanJSON(ctx, RefsPurgeOptions{RunIDs: []string{run}}, &plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	testkit.ValidateJSON(t, "refs-purge-plan", plan.Bytes())
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
	testkit.ValidateJSON(t, "refs-purge", res.Bytes())
	var pr RefsPurgeResult
	_ = json.Unmarshal(res.Bytes(), &pr)
	if err != nil || pr.Outcome != OutcomePurged || pr.Runs[0].Refs[0].Result != PinDeleted {
		t.Fatalf("%v %+v", err, pr)
	}
	if left := testkit.GitOut(t, main, "for-each-ref", refpins.SweptPrefix); left != "" {
		t.Errorf("pins left: %s", left)
	}
	if err := RefsPurge(ctx, RefsPurgeOptions{RunIDs: []string{run}, Yes: true}, &bytes.Buffer{}); err == nil {
		t.Error("purging a run that is gone must be an error")
	}
	if err := RefsPurge(ctx, RefsPurgeOptions{OlderThan: "1d"}, &bytes.Buffer{}); err != nil {
		t.Errorf("nothing older than a day is not an error: %v", err)
	}
}

// A remote ref goes back only to the origin it came from: a run whose
// origin now resolves elsewhere keeps it, and so does a run with no meta.
// Local refs are not affected.
func TestRefsRestoreRemoteNeedsTheSameOrigin(t *testing.T) {
	ctx, main, origin, run := refstest.RemoteBackups(t)
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
	testkit.GitIn(t, main, "clone", "-q", "--bare", origin, moved)
	testkit.GitIn(t, main, "remote", "set-url", "origin", moved)
	p, err := refstest.RestorePlanJSON(t, ctx, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range p.Items {
		want := refs.RestoreCreate
		if it.Kind == refpins.RefRemoteBranch {
			want = refs.RestoreEndpointChanged
		}
		if it.Action != want || it.Selected != (want == refs.RestoreCreate) {
			t.Errorf("%s: action %s, selected %v", it.ID, it.Action, it.Selected)
		}
	}
	testkit.GitIn(t, main, "remote", "set-url", "origin", origin)
	testkit.GitIn(t, main, "update-ref", "-d", refpins.MetaRef(run))
	p, _ = refstest.RestorePlanJSON(t, ctx, run, nil)
	for _, it := range p.Items {
		if it.Kind == refpins.RefRemoteBranch && it.Action != refs.RestoreEndpointUnknown {
			t.Errorf("no meta: %s is %s", it.ID, it.Action)
		}
	}
}

// A run that wrote its meta and moved nothing is still listed, and a purge
// deletes the meta last.
func TestRefsPurgeDeletesTheMetaLast(t *testing.T) {
	ctx, main := testkit.BackupsRepo(t)
	id := "20260101T000000Z-dead"
	if err := refpins.WriteMeta(main, refpins.RunMeta{RunID: id, Repo: main, SweptAt: 1767225600}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_ = RefsSwept(ctx, true, &out)
	var sw RefsSweptOutput
	_ = json.Unmarshal(out.Bytes(), &sw)
	if len(sw.Runs) != 1 || sw.Runs[0].RunID != id || len(sw.Runs[0].Refs) != 0 || sw.Runs[0].SweptAt != 1767225600 {
		t.Fatalf("swept = %+v", sw)
	}
	r := refstest.SweepAll(t, ctx, refs.Options{})
	var res bytes.Buffer
	err := RefsPurge(ctx, RefsPurgeOptions{OlderThan: "1d", Yes: true, Journal: NewRefsPurgeJournal(&res)}, &bytes.Buffer{})
	testkit.ValidateJSON(t, "refs-purge", res.Bytes())
	var pr RefsPurgeResult
	_ = json.Unmarshal(res.Bytes(), &pr)
	if err != nil || pr.Outcome != OutcomePurged || len(pr.Runs) != 1 || !pr.Runs[0].MetaDeleted {
		t.Fatalf("%v %+v", err, pr)
	}
	if left := testkit.GitOut(t, main, "for-each-ref", "--format=%(refname)", refpins.SweptPrefix); !strings.HasPrefix(left, refpins.SweptPrefix+*r.RunID) {
		t.Errorf("only today's run is left, got %q", left)
	}
}
