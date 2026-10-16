package synccmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// doctorFixture is a main checkout that is its own origin, declaring v.txt
// owned-line max-plus-patch, with core.hooksPath pinned to its own hooks dir
// (so an ambient global hooks path cannot make the hooks check fail) and
// rerere.enabled explicitly set, local, to rerereOn: rerereCheck reads the
// effective config value, so an ambient global rerere.enabled=true must not
// be able to leak into a fixture that means to be testing "off".
func doctorFixture(t *testing.T, rerereOn bool) *kit.Context {
	t.Helper()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n"
	if err := os.WriteFile(filepath.Join(main, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, "v.txt"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, main, "add", "-A")
	testkit.GitIn(t, main, "commit", "-q", "-m", "declare")
	testkit.GitIn(t, main, "config", "core.hooksPath", filepath.Join(main, ".git", "hooks"))
	testkit.GitIn(t, main, "config", "rerere.enabled", strconv.FormatBool(rerereOn))
	testkit.GitIn(t, main, "remote", "add", "origin", main)
	testkit.GitIn(t, main, "fetch", "-q", "origin")
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

// DoctorRows splits SyncDoctor's tabwriter output into its data rows,
// failing the test if the header is missing.
func DoctorRows(t *testing.T, out string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "CHECK") {
		t.Fatalf("missing header:\n%s", out)
	}
	return lines[1:]
}

func TestSyncDoctorPrintsTwelveOKRowsForAHealthyFixture(t *testing.T) {
	// Not parallel: sets HOME for the process.
	ctx := doctorFixture(t, true)
	// No keeper is installed in the fixture's home, whatever this machine has.
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	if err := SyncDoctor(ctx, DoctorOptions{}, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	rows := DoctorRows(t, out.String())
	if len(rows) != 12 {
		t.Fatalf("got %d rows, want 12:\n%s", len(rows), out.String())
	}
	for _, row := range rows {
		fields := strings.Fields(row)
		if len(fields) < 2 || fields[1] != "ok" {
			t.Errorf("row not ok: %q", row)
		}
	}
}

func TestSyncDoctorFixesRerereOffWithFixFlag(t *testing.T) {
	t.Parallel()
	ctx := doctorFixture(t, false)
	var out bytes.Buffer
	if err := SyncDoctor(ctx, DoctorOptions{Fix: true}, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "fixed rerere") {
		t.Fatalf("out does not report the fix:\n%s", out.String())
	}
	if got := testkit.GitOut(t, ctx.Repo.MainRoot, "config", "--get", "rerere.enabled"); got != "true" {
		t.Fatalf("rerere.enabled = %q", got)
	}
}

func TestSyncDoctorReportsAWorktreeWaitingOnAPerson(t *testing.T) {
	t.Parallel()
	ctx := doctorFixture(t, true)
	var buf bytes.Buffer
	bump, err := provision.New(ctx, "feat/bump", provision.NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	gitDir, err := wtsync.GitDir(bump)
	if err != nil {
		t.Fatal(err)
	}
	if err := wtsync.WriteState(gitDir, wtsync.State{Branch: "feat_wt/bump", Work: "bump"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := SyncDoctor(ctx, DoctorOptions{}, &out); err != nil {
		t.Fatalf("a worktree waiting on a person does not block a run elsewhere: %v\n%s", err, out.String())
	}
	var plan string
	for _, row := range DoctorRows(t, out.String()) {
		if strings.HasPrefix(row, "plan ") {
			plan = row
		}
	}
	// A sidecar with no rebase in progress, no old tip and no onto is a
	// handover nothing can certify as finished: the branch reads as moved,
	// and the forced undo is named, as the row and the run's refusal do.
	if fields := strings.Fields(plan); len(fields) < 2 || fields[1] != "warn" || !strings.Contains(plan, "bump: "+wtsync.WayOut(wtsync.Way{Work: "bump", Path: bump, Plan: true, Moved: true})) {
		t.Fatalf("plan row %q:\n%s", plan, out.String())
	}
}
