package testkit

import (
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// WriteFile writes content to rel under dir.
func WriteFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	gittest.WriteFile(t, filepath.Join(dir, rel), content)
}

// GitTry runs git where a non-zero exit is part of the scenario — a person's
// `rebase --continue` that stops at the next conflict — and never opens an
// editor. The caller asserts the state it was after.
func GitTry(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, _ := gittest.Try(t, dir, args...)
	return out
}
