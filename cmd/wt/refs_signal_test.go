package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// sigtermRepo is a repository with three backups, backup/a to backup/c,
// and a reference-transaction hook that holds the second transaction
// touching a branch pin open until killed: the move a signal is sent in.
func sigtermRepo(t *testing.T) (main, mark string) {
	t.Helper()
	main = gittest.NewRepo(t, t.TempDir(), "r")
	for _, b := range []string{"backup/a", "backup/b", "backup/c"} {
		gittest.Git(t, main, "branch", b)
	}
	dir := t.TempDir()
	count, mark := filepath.Join(dir, "count"), filepath.Join(dir, "mark")
	hook := "#!/bin/sh\n" +
		"[ \"$1\" = prepared ] || exit 0\n" +
		"[ -e '" + filepath.Join(dir, "armed") + "' ] || exit 0\n" +
		"grep -q 'refs/wt-swept/.*/heads/' || exit 0\n" +
		"n=$(cat '" + count + "' 2>/dev/null || echo 0); n=$((n+1)); echo $n > '" + count + "'\n" +
		"[ $n -ge 2 ] || exit 0\n" +
		"touch '" + mark + "'\n" +
		"sleep 30\n"
	path := filepath.Join(main, ".git", "hooks", "reference-transaction")
	gittest.WriteFile(t, path, hook)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return main, mark
}

// arm makes the hook hold the second pin transaction from now on.
func arm(t *testing.T, mark string) {
	t.Helper()
	gittest.WriteFile(t, filepath.Join(filepath.Dir(mark), "armed"), "")
}

// sigtermRun runs wt with args in main as a child — this test binary, which
// runs Execute when WT_REFS_SIGNAL_ARGS is set — sends it SIGTERM once the
// hook holds a move open, and returns stdout after checking exit 130.
func sigtermRun(t *testing.T, main, mark string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1") //nolint:gosec // G702: re-runs this test binary with its own test name
	cmd.Dir = main
	cmd.Env = append(os.Environ(), "WT_REFS_SIGNAL_ARGS="+strings.Join(args, "\x1f"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(mark); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("the second move never started\nstderr: %s", stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 130 {
		t.Fatalf("exit = %v, want 130\nstderr: %s", err, stderr.String())
	}
	return stdout.Bytes()
}

// childMain runs wt in a child sigtermRun started, and never returns there.
func childMain() {
	if args := os.Getenv("WT_REFS_SIGNAL_ARGS"); args != "" {
		os.Args = append([]string{"wt"}, strings.Split(args, "\x1f")...)
		os.Exit(Execute())
	}
}

// oneObject decodes stdout as exactly one object.
func oneObject(t *testing.T, out []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(out))
	if err := dec.Decode(v); err != nil {
		t.Fatalf("stdout is not one object: %v\n%s", err, out)
	}
	if dec.More() {
		t.Error("a second object was written")
	}
}

// sweptRun sweeps the three backups under run in process, before the hook
// is armed.
func sweptRun(t *testing.T, main, run string) {
	t.Helper()
	ctx, err := commands.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	if err := commands.RefsSweep(ctx, commands.RefsOptions{NoFetch: true, Yes: true, RunID: run}, io.Discard); err != nil {
		t.Fatal(err)
	}
}

// SIGTERM in the middle of wt refs sweep --yes --json still gets its one
// object, and exit 130: the row done is swept, the row the signal caught is
// interrupted with what is true of it read back, the rest not run.
func TestRefsSweepSIGTERMWritesTheObject(t *testing.T) {
	childMain()
	main, mark := sigtermRepo(t)
	arm(t, mark)
	var r commands.RefsResult
	oneObject(t, sigtermRun(t, main, mark, "refs", "sweep", "--yes", "--json", "--no-fetch"), &r)
	if r.Outcome != commands.OutcomeInterrupted || r.RunID == nil || r.Recovery == nil {
		t.Fatalf("result = %+v", r)
	}
	want := map[string]string{"refs/heads/backup/a": commands.RefSwept, "refs/heads/backup/b": commands.RefInterrupted,
		"refs/heads/backup/c": commands.RefNotRun}
	for _, it := range r.Items {
		if it.Result != want[it.ID] {
			t.Errorf("%s is %s, want %s", it.ID, it.Result, want[it.ID])
		}
		if it.ID == "refs/heads/backup/b" && (it.Pinned || it.Deleted) {
			t.Errorf("the move the signal caught was never committed: %+v", it)
		}
	}
}

// The same for wt refs restore --yes --json.
func TestRefsRestoreSIGTERMWritesTheObject(t *testing.T) {
	childMain()
	main, mark := sigtermRepo(t)
	run := "20260930T091500Z-5157"
	sweptRun(t, main, run)
	arm(t, mark)
	var r commands.RefsRestoreResult
	oneObject(t, sigtermRun(t, main, mark, "refs", "restore", run, "--yes", "--json"), &r)
	if r.Outcome != commands.OutcomeInterrupted || r.Recovery == nil {
		t.Fatalf("result = %+v", r)
	}
	want := map[string]string{"refs/heads/backup/a": commands.RefRestored, "refs/heads/backup/b": commands.RefInterrupted,
		"refs/heads/backup/c": commands.RefNotRun}
	for _, it := range r.Items {
		if it.Result != want[it.ID] {
			t.Errorf("%s is %s, want %s", it.ID, it.Result, want[it.ID])
		}
		if it.ID == "refs/heads/backup/b" && (it.Restored || it.PinDeleted) {
			t.Errorf("the restore the signal caught was never committed: %+v", it)
		}
	}
}

// The same for wt refs purge --yes --json.
func TestRefsPurgeSIGTERMWritesTheObject(t *testing.T) {
	childMain()
	main, mark := sigtermRepo(t)
	run := "20260930T091500Z-9e7a"
	sweptRun(t, main, run)
	arm(t, mark)
	var r commands.RefsPurgeResult
	oneObject(t, sigtermRun(t, main, mark, "refs", "purge", run, "--yes", "--json"), &r)
	if r.Outcome != commands.OutcomeInterrupted || r.Recovery == nil || len(r.Runs) != 1 {
		t.Fatalf("result = %+v", r)
	}
	want := map[string]string{"refs/heads/backup/a": commands.PinDeleted, "refs/heads/backup/b": commands.RefInterrupted,
		"refs/heads/backup/c": commands.RefNotRun}
	for _, it := range r.Runs[0].Refs {
		if it.Result != want[it.ID] {
			t.Errorf("%s is %s, want %s", it.ID, it.Result, want[it.ID])
		}
	}
	if r.Runs[0].MetaDeleted {
		t.Error("the meta goes last, and the purge never got there")
	}
}
