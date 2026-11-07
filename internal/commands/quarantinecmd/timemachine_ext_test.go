package quarantinecmd_test

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/quarantinecmd"
	"github.com/anders-lindstrom/wt/internal/commands/quarantinecmd/quarantinecmdtest"
	"github.com/anders-lindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// wt restore puts a worktree back where it was, and excludes its folder the
// way wt new did when it first made it.
func TestRestoreExcludesTheWorktreeFolderFromTimeMachine(t *testing.T) {
	ctx, path, _, _, dir := retiretest.Quarantined(t, "fix/back", false)
	tm := testkit.FakeTmutil(t, ctx, false)
	quarantinecmdtest.Restore(t, ctx, dir)
	if want := filepath.Join(ctx.Repo.Parent, "demo_wt"); !slices.Equal(tm.Excluded(t), []string{want}) {
		t.Errorf("restored %s; excluded %q, want %s", path, tm.Excluded(t), want)
	}
}

// A tmutil that fails during a restore is the one warning line on the
// context's own warning stream, not lost with the repository it opened.
func TestRestoreWarnsAboutAFailingTmutil(t *testing.T) {
	ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/back", false)
	testkit.FakeTmutil(t, ctx, true)
	var warned bytes.Buffer
	ctx.WarnTo(&warned)
	quarantinecmdtest.Restore(t, ctx, dir)
	if !strings.Contains(warned.String(), "not excluded from Time Machine") {
		t.Errorf("warned %q", warned.String())
	}
}

// Run outside any repository, wt restore still has somewhere to say that
// tmutil failed: the Warn it was given.
func TestRestoreOutsideARepositoryWarnsAboutAFailingTmutil(t *testing.T) {
	ctx, _, _, _, dir := retiretest.Quarantined(t, "fix/back", false)
	testkit.FakeTmutil(t, ctx, true)
	var warned, out bytes.Buffer
	opts := quarantinecmd.RestoreOptions{Warn: &warned, Machine: kit.Machine{GOOS: "darwin"}}
	if err := quarantinecmd.Restore(nil, dir, opts, &out); err != nil {
		t.Fatalf("Restore: %v\n%s", err, out.String())
	}
	if !strings.Contains(warned.String(), "not excluded from Time Machine") {
		t.Errorf("warned %q", warned.String())
	}
}
