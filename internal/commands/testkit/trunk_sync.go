package testkit

import (
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// WriteIn writes content to rel under dir.
func WriteIn(t *testing.T, dir, rel, content string) {
	t.Helper()
	gittest.WriteFile(t, filepath.Join(dir, rel), content)
}
