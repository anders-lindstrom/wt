package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A directory holding a claude stays on the PATH for everything else in it.
func TestHideClaudeKeepsTheRestOfItsDirectory(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"claude", "tool"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	hideClaude(t.TempDir())
	if p, err := exec.LookPath("claude"); err == nil {
		t.Errorf("claude is still found, at %s", p)
	}
	p, err := exec.LookPath("tool")
	if err != nil || realPath(p) != realPath(filepath.Join(bin, "tool")) {
		t.Errorf("tool is %q, %v; want the one in %s", p, err, bin)
	}
}
