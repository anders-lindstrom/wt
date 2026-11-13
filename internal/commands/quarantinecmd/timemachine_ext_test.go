package quarantinecmd_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anlindstrom/wt/internal/commands/kit"
	"github.com/anlindstrom/wt/internal/commands/quarantinecmd"
	"github.com/anlindstrom/wt/internal/commands/quarantinecmd/quarantinecmdtest"
	"github.com/anlindstrom/wt/internal/commands/retire/retiretest"
	"github.com/anlindstrom/wt/internal/commands/testkit"
	"github.com/anlindstrom/wt/internal/quarantine"
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

func TestRestoreWarnsWhenRestoredConfigCannotOpen(t *testing.T) {
	t.Parallel()
	for _, outside := range []bool{false, true} {
		t.Run(fmt.Sprint(outside), func(t *testing.T) {
			ctx, path, _, _, dir := retiretest.Quarantined(t, "fix/back", false)
			record, err := quarantine.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(record.Checkout.Quarantined, "bin", "worktree", "worktree.toml")
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, []byte("main_branch = ["), 0o644); err != nil {
				t.Fatal(err)
			}
			var warned, out bytes.Buffer
			ctx.WarnTo(&warned)
			if outside {
				ctx = nil
			}
			if err := quarantinecmd.Restore(ctx, dir, quarantinecmd.RestoreOptions{Warn: &warned}, &out); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("restore did not put back checkout: %v", err)
			}
			if strings.Count(warned.String(), "\n") != 1 || !strings.Contains(warned.String(), "did not exclude it from Time Machine or register it with Superset") || !strings.Contains(warned.String(), "worktree.toml") {
				t.Errorf("warning = %q; want one integration warning with the config error", warned.String())
			}
			if strings.Contains(out.String(), "did not exclude") {
				t.Errorf("warning leaked to stdout: %q", out.String())
			}
		})
	}
}
