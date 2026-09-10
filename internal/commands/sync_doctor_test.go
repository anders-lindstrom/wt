package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// doctorFixture is a main checkout that is its own origin, declaring v.txt
// owned-line max-plus-patch, with core.hooksPath pinned to its own hooks dir
// (so an ambient global hooks path cannot make the hooks check fail) and
// rerere.enabled explicitly set, local, to rerereOn: rerereCheck reads the
// effective config value, so an ambient global rerere.enabled=true must not
// be able to leak into a fixture that means to be testing "off".
func doctorFixture(t *testing.T, rerereOn bool) *Context {
	t.Helper()
	main := committedRepo(t, minimalConf)
	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n"
	if err := os.WriteFile(filepath.Join(main, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, "v.txt"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-q", "-m", "declare")
	gitIn(t, main, "config", "core.hooksPath", filepath.Join(main, ".git", "hooks"))
	gitIn(t, main, "config", "rerere.enabled", strconv.FormatBool(rerereOn))
	gitIn(t, main, "remote", "add", "origin", main)
	gitIn(t, main, "fetch", "-q", "origin")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

// doctorRows splits SyncDoctor's tabwriter output into its data rows,
// failing the test if the header is missing.
func doctorRows(t *testing.T, out string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "CHECK") {
		t.Fatalf("missing header:\n%s", out)
	}
	return lines[1:]
}

func TestSyncDoctorPrintsElevenOKRowsForAHealthyFixture(t *testing.T) {
	ctx := doctorFixture(t, true)
	var out bytes.Buffer
	if err := SyncDoctor(ctx, DoctorOptions{}, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	rows := doctorRows(t, out.String())
	if len(rows) != 11 {
		t.Fatalf("got %d rows, want 11:\n%s", len(rows), out.String())
	}
	for _, row := range rows {
		fields := strings.Fields(row)
		if len(fields) < 2 || fields[1] != "ok" {
			t.Errorf("row not ok: %q", row)
		}
	}
}

func TestSyncDoctorFixesRerereOffWithFixFlag(t *testing.T) {
	ctx := doctorFixture(t, false)
	var out bytes.Buffer
	if err := SyncDoctor(ctx, DoctorOptions{Fix: true}, &out); err != nil {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "fixed rerere") {
		t.Fatalf("out does not report the fix:\n%s", out.String())
	}
	if got := gitOut(t, ctx.Repo.MainRoot, "config", "--get", "rerere.enabled"); got != "true" {
		t.Fatalf("rerere.enabled = %q", got)
	}
}

func TestSyncDoctorReportsAWorktreeWaitingOnAPerson(t *testing.T) {
	ctx := doctorFixture(t, true)
	var buf bytes.Buffer
	bump, err := New(ctx, "feat/bump", NewOptions{NoSetup: true}, &buf)
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
	for _, row := range doctorRows(t, out.String()) {
		if strings.HasPrefix(row, "plan ") {
			plan = row
		}
	}
	if fields := strings.Fields(plan); len(fields) < 2 || fields[1] != "warn" || !strings.Contains(plan, "bump: wt sync resume bump, or wt sync undo bump") {
		t.Fatalf("plan row %q:\n%s", plan, out.String())
	}
}
