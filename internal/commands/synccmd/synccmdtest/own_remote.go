package synccmdtest

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/syncrun"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// PushedWork is TrunkFixture.WithWork with worktree x pushed to origin under
// its own name and tracking it: x is the checkout, opts the run options.
func (f *TrunkFixture) PushedWork(t *testing.T) (x string, opts synccmd.RunOptions) {
	t.Helper()
	opts = f.WithWork(t)
	wt, err := kit.Locate(f.Ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, wt.Path, "push", "-q", "-u", "origin", "feat_wt/x")
	return wt.Path, opts
}

// Theirs commits file on branch in the second clone and pushes it: a commit
// origin has and the main checkout has not fetched.
func (f *TrunkFixture) Theirs(t *testing.T, branch, file, content string) string {
	t.Helper()
	testkit.GitIn(t, f.Other, "fetch", "-q", "origin")
	testkit.GitIn(t, f.Other, "checkout", "-q", "-B", branch, "origin/"+branch)
	testkit.WriteIn(t, f.Other, file, content)
	testkit.GitIn(t, f.Other, "add", "-A")
	testkit.GitIn(t, f.Other, "commit", "-q", "-m", "theirs "+file)
	testkit.GitIn(t, f.Other, "push", "-q", "origin", branch)
	sha := testkit.GitOut(t, f.Other, "rev-parse", "HEAD")
	// Back on main, where AdvanceOrigin commits.
	testkit.GitIn(t, f.Other, "checkout", "-q", "-B", "main", "origin/main")
	return sha
}

// At is run options for a later run of the same test: every run pins its
// safety ref under its own clock.
func At(opts synccmd.RunOptions, nanos int64) synccmd.RunOptions {
	opts.Now = func() time.Time { return time.Unix(0, nanos) }
	return opts
}

// UpOut is UpJSON with what a person would have read.
func UpOut(t *testing.T, ctx *kit.Context, work string, opts synccmd.RunOptions) (syncrun.UpResult, string, error) {
	t.Helper()
	var out, human bytes.Buffer
	opts.Journal = syncrun.NewRunJournal(&out)
	err := synccmd.Up(ctx, work, opts, &human)
	testkit.ValidateJSON(t, "up", out.Bytes())
	var r syncrun.UpResult
	if jerr := json.Unmarshal(out.Bytes(), &r); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out.String())
	}
	return r, human.String(), err
}
