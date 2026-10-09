package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wt status takes one worktree at most: the whole table, or one in full.
func TestStatusTakesAtMostOneWorktree(t *testing.T) {
	_, err := runCmd(t, "status", "login-crash", "api-tidy")
	if err == nil || !strings.Contains(err.Error(), "at most 1 arg") {
		t.Errorf("want an argument-count error for two worktrees, got %v", err)
	}
}

// claudeOnPath puts a claude first on the PATH that answers `agents --json`
// with listing, and counts how many times it was asked in the file returned.
// Nothing here reaches the machine's own claude.
func claudeOnPath(t *testing.T, listing string) (calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	answer := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(answer, []byte(listing), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho asked >> '" + calls + "'\ncat '" + answer + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func timesAsked(t *testing.T, calls string) int {
	t.Helper()
	data, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "asked")
}

// sessionIn is one background session, as claude lists it, in the
// login-crash worktree of the repository the test stands in.
func sessionIn(t *testing.T, id string) string {
	t.Helper()
	main, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	main, err = filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(filepath.Dir(main), "demo_wt", "fix_wt", "login-crash")
	return `[{"id":"` + id + `","sessionId":"` + id + `-1111-4222-8333-444455556666","name":"fix the crash",` +
		`"kind":"background","state":"blocked","cwd":"` + cwd + `"}]`
}

// --all, --roots and --profile ask claude once for every repository, and
// with --no-sessions not at all.
func TestListAcrossRepositoriesAsksClaudeOnceOrNotAtAll(t *testing.T) {
	repoWithWorktrees(t)
	main, _ := os.Getwd()
	t.Setenv("WT_ROOTS", filepath.Dir(main))
	calls := claudeOnPath(t, sessionIn(t, "dddd4444"))

	out, err := runCmd(t, "list", "--all", "--no-sessions")
	if err != nil {
		t.Fatalf("list --all --no-sessions: %v\n%s", err, out)
	}
	if strings.Contains(out, "SESSION") || timesAsked(t, calls) != 0 {
		t.Errorf("--no-sessions asked claude %d time(s):\n%s", timesAsked(t, calls), out)
	}

	out, err = runCmd(t, "list", "--all")
	if err != nil {
		t.Fatalf("list --all: %v\n%s", err, out)
	}
	if !strings.Contains(out, "== demo") || !strings.Contains(out, "fix the crash · needs input") {
		t.Errorf("want the repository's section with its session:\n%s", out)
	}
	if n := timesAsked(t, calls); n != 1 {
		t.Errorf("claude was asked %d times for one listing", n)
	}
}

// A claude that cannot be read is said once, under every section.
func TestListAcrossRepositoriesSaysOnceWhenSessionsCouldNotBeRead(t *testing.T) {
	repoWithWorktrees(t)
	main, _ := os.Getwd()
	t.Setenv("WT_ROOTS", filepath.Dir(main))
	claudeOnPath(t, `{"not":"a list"}`)

	out, err := runCmd(t, "list", "--all")
	if err != nil {
		t.Fatalf("list --all: %v\n%s", err, out)
	}
	if strings.Count(out, "sessions not shown") != 1 || !strings.HasSuffix(out,
		"\n   sessions not shown: claude agents --json printed something wt cannot read as a list of sessions\n") {
		t.Errorf("want one line, in words, at the end:\n%s", out)
	}
}
