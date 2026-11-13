package synccmd_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/anlindstrom/wt/internal/commands/kit"
	"github.com/anlindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anlindstrom/wt/internal/commands/synccmd"
	"github.com/anlindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anlindstrom/wt/internal/commands/syncrun"
	"github.com/anlindstrom/wt/internal/commands/testkit"
)

// object is the one JSON object out holds, checked against schema.
func object(t *testing.T, schema string, out []byte) map[string]any {
	t.Helper()
	testkit.ValidateJSON(t, schema, out)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return m
}

func targetOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	got, ok := m["target"].(map[string]any)
	if !ok {
		t.Fatalf("no target object in %v", m)
	}
	return got
}

func overviewOnto(t *testing.T, ctx *kit.Context, opts synccmd.SyncOptions) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if err := synccmd.SyncJSON(ctx, opts, &buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	return object(t, "sync", buf.Bytes())
}

// Every result names what it is against: trunk, or the ref --onto names,
// and onto is that commit.
func TestOntoJSONNamesTheTarget(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, _ := provisiontest.RunFixture(t, false)
	integ := synccmdtest.Integration(t, ctx)
	main := ctx.Repo.MainRoot
	trunk := testkit.GitOut(t, main, "rev-parse", "origin/main")

	o := overviewOnto(t, ctx, synccmd.SyncOptions{NoFetch: true})
	if want := map[string]any{"ref": "origin/main", "trunk": true, "upstream": nil}; !reflect.DeepEqual(targetOf(t, o), want) || o["onto"] != trunk {
		t.Errorf("overview: target %v onto %v, want %v onto %s", o["target"], o["onto"], want, trunk)
	}
	o = overviewOnto(t, ctx, synccmd.SyncOptions{NoFetch: true, Onto: "integ", Work: "bump"})
	if want := map[string]any{"ref": "integ", "trunk": false, "upstream": nil}; !reflect.DeepEqual(targetOf(t, o), want) || o["onto"] != integ {
		t.Errorf("overview --onto: target %v onto %v, want %v onto %s", o["target"], o["onto"], want, integ)
	}
	if rows, _ := o["worktrees"].([]any); len(rows) != 1 {
		t.Errorf("overview --onto holds %d worktrees, want bump alone", len(rows))
	}

	var out, human bytes.Buffer
	opts := synccmdtest.Onto("integ", "feat_wt/bump~1")
	opts.Journal = syncrun.NewSyncRunJournal(&out, "sync rebase")
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &human); err != nil {
		t.Fatalf("%v\n%s", err, human.String())
	}
	r := object(t, "sync-run", out.Bytes())
	base := testkit.GitOut(t, main, "rev-parse", "feat_wt/bump@{1}~1")
	if want := map[string]any{"ref": "integ", "trunk": false, "upstream": base}; !reflect.DeepEqual(targetOf(t, r), want) || r["onto"] != integ {
		t.Errorf("sync-run: target %v onto %v, want %v onto %s", r["target"], r["onto"], want, integ)
	}
	if r["trunk"] != "main" || r["trunkRef"] != "origin/main" {
		t.Errorf("trunk %v %v: trunk stays trunk", r["trunk"], r["trunkRef"])
	}
}

func TestUpOntoJSONNamesTheTarget(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, _ := provisiontest.RunFixture(t, false)
	integ := synccmdtest.Integration(t, ctx)
	var out, human bytes.Buffer
	opts := synccmdtest.Onto("integ", "")
	opts.Journal = syncrun.NewRunJournal(&out)
	if err := synccmd.Up(ctx, "bump", opts, &human); err != nil {
		t.Fatalf("%v\n%s", err, human.String())
	}
	r := object(t, "up", out.Bytes())
	if want := map[string]any{"ref": "integ", "trunk": false, "upstream": nil}; !reflect.DeepEqual(targetOf(t, r), want) || r["onto"] != integ {
		t.Errorf("up: target %v onto %v, want %v onto %s", r["target"], r["onto"], want, integ)
	}
}

// The token of a look with --onto covers the target: a run told to expect
// it refuses once the target has moved.
func TestOntoExpectRefusesATargetThatMoved(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, bump := provisiontest.RunFixture(t, false)
	synccmdtest.Integration(t, ctx)
	look := synccmd.SyncOptions{NoFetch: true, Onto: "integ", Work: "bump"}
	tok, _ := overviewOnto(t, ctx, look)["token"].(string)
	if tok == "" {
		t.Fatal("no token")
	}
	plain, _ := overviewOnto(t, ctx, synccmd.SyncOptions{NoFetch: true})["token"].(string)
	if tok == plain {
		t.Error("the token with --onto is trunk's")
	}
	moved := synccmdtest.Branch(t, ctx, "integ2", "integ", map[string]string{"j.txt": "j\n"})
	testkit.GitOut(t, ctx.Repo.MainRoot, "branch", "-f", "integ", moved)
	old := testkit.GitOut(t, bump, "rev-parse", "HEAD")
	opts := synccmdtest.Onto("integ", "")
	opts.Expect = tok
	var out bytes.Buffer
	err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out)
	if err == nil || !strings.Contains(err.Error(), "the look changed since it was read") {
		t.Fatalf("err %v, want the look changed\n%s", err, out.String())
	}
	if testkit.GitOut(t, bump, "rev-parse", "HEAD") != old {
		t.Fatal("bump moved")
	}
	// The token of the look as it is now goes.
	opts.Expect, _ = overviewOnto(t, ctx, look)["token"].(string)
	out.Reset()
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &out); err != nil {
		t.Fatalf("with the fresh token: %v\n%s", err, out.String())
	}
}

// Without --onto the token is the one wt has always given: pinned for this
// fixture, whose every input is fixed.
func TestOverviewTokenWithoutOntoIsUnchanged(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, _ := provisiontest.RunFixture(t, false)
	o := synccmdtest.OverviewJSON(t, ctx)
	if o.Token == nil || *o.Token != pinnedToken {
		t.Errorf("token %v, want %s", jsonStr(o.Token), pinnedToken)
	}
}

// pinnedToken is what wt at f5e5e97, before --onto, gave this fixture.
const pinnedToken = "1:ff4c11b7b0e6fdd4f353091f6967ad4a" //nolint:gosec // a plan token, not a credential

func jsonStr(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

// A target that does not resolve leaves onto and target null, in the look
// and in the run: nothing was assessed against trunk instead.
func TestOntoJSONLeavesAnUnresolvedTargetNull(t *testing.T) {
	t.Parallel()
	skipShort(t)
	ctx, _ := provisiontest.RunFixture(t, false)
	var buf bytes.Buffer
	if err := synccmd.SyncJSON(ctx, synccmd.SyncOptions{NoFetch: true, Onto: "nope", Work: "bump"}, &buf); err == nil {
		t.Fatalf("no error for a target that does not resolve\n%s", buf.String())
	}
	o := object(t, "sync", buf.Bytes())
	if o["onto"] != nil || o["target"] != nil || o["error"] == nil {
		t.Errorf("look: onto %v target %v error %v, want null, null and the error", o["onto"], o["target"], o["error"])
	}
	var out, human bytes.Buffer
	opts := synccmdtest.Onto("nope", "")
	opts.Journal = syncrun.NewSyncRunJournal(&out, "sync rebase")
	if err := synccmd.SyncRebase(ctx, []string{"bump"}, opts, &human); err == nil {
		t.Fatalf("no error\n%s", human.String())
	}
	r := object(t, "sync-run", out.Bytes())
	if r["onto"] != nil || r["target"] != nil {
		t.Errorf("run: onto %v target %v, want both null", r["onto"], r["target"])
	}
}
