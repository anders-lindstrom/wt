package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func TestRunReturnsTrimmedOutput(t *testing.T) {
	dir := newRepo(t)
	got, err := Run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "main" {
		t.Errorf("got %q, want %q", got, "main")
	}
}

func TestRunOutsideRepoReturnsErrNotRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, "rev-parse", "--show-toplevel"); err != ErrNotRepo {
		t.Errorf("got %v, want ErrNotRepo", err)
	}
}

func TestLinesDropsTrailingBlank(t *testing.T) {
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := Lines(dir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("Lines: %v", err)
	}
	if len(lines) != 1 {
		t.Errorf("got %d lines %q, want 1", len(lines), lines)
	}
}
