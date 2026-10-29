package refstest

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/refs"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/jsonout"
)

// RefsSweepJSON runs wt refs sweep --json and decodes its checked output.
func RefsSweepJSON(t *testing.T, ctx *kit.Context, opts refs.Options) (refs.Result, string, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal, opts.Yes = refs.NewRefsJournal(&out), true
	err := refs.Sweep(ctx, opts, &human)
	testkit.ValidateJSON(t, "refs-sweep", out.Bytes())
	var r refs.Result
	dec := json.NewDecoder(&out)
	if jerr := dec.Decode(&r); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	if dec.More() {
		t.Error("a second object was written")
	}
	return r, human.String(), err
}

// SweepAll sweeps the default selection and returns the run.
func SweepAll(t *testing.T, ctx *kit.Context, opts refs.Options) refs.Result {
	t.Helper()
	opts.NoFetch = true
	r, human, err := RefsSweepJSON(t, ctx, opts)
	if err != nil || r.RunID == nil {
		t.Fatalf("sweep: %v %+v\n%s", err, r, human)
	}
	return r
}

// RestoreJSON runs wt refs restore --json and decodes its checked output.
func RestoreJSON(t *testing.T, ctx *kit.Context, runID string, opts refs.RestoreRefsOptions) (refs.RestoreRefsResult, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal, opts.Yes = refs.NewRefsRestoreJournal(&out, runID), true
	err := refs.RestoreRefs(ctx, runID, opts, &human)
	testkit.ValidateJSON(t, "refs-restore", out.Bytes())
	var r refs.RestoreRefsResult
	if jerr := json.Unmarshal(out.Bytes(), &r); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out.String())
	}
	return r, err
}

// RestorePlanJSON runs wt refs restore --plan --json and decodes its
// checked output.
func RestorePlanJSON(t *testing.T, ctx *kit.Context, runID string, only []string) (refs.RestoreRefsPlanOutput, error) {
	t.Helper()
	var out, progress bytes.Buffer
	err := refs.RestoreRefsPlanJSON(ctx, runID, only, &out, &progress)
	testkit.ValidateJSON(t, "refs-restore-plan", out.Bytes())
	var p refs.RestoreRefsPlanOutput
	if jerr := json.Unmarshal(out.Bytes(), &p); jerr != nil {
		t.Fatalf("%v\n%s", jerr, out.String())
	}
	return p, err
}

// RemoteBackups is a repository whose origin has one contained backup
// branch that is not here, and the run that swept it with --remote.
func RemoteBackups(t *testing.T) (ctx *kit.Context, main, origin, run string) {
	t.Helper()
	ctx, main, origin = testkit.SweepRepo(t)
	first := testkit.GitOut(t, main, "rev-parse", "main")
	testkit.GitIn(t, main, "commit", "-q", "--allow-empty", "-m", "second")
	testkit.GitIn(t, main, "push", "-q", "origin", "main", "main:refs/heads/backup/landed")
	testkit.GitIn(t, main, "branch", "backup/local", first)
	r, human, err := RefsSweepJSON(t, ctx, refs.Options{Remote: true, OpenPRs: testkit.NoPRs})
	if err != nil || r.Outcome != jsonout.OutcomeDone || r.RunID == nil {
		t.Fatalf("%v %+v\n%s", err, r, human)
	}
	return ctx, main, origin, *r.RunID
}
