package testkit

import (
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// GitOut runs git and returns its stdout; GitIn returns nothing.
func GitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gittest.Git(t, dir, args...)
}

// FetchHead is the fixture's FETCH_HEAD, whose mtime is the age --no-fetch
// reports.
func FetchHead(t *testing.T, main string) string {
	t.Helper()
	path := GitOut(t, main, "rev-parse", "--git-path", "FETCH_HEAD")
	if !filepath.IsAbs(path) {
		path = filepath.Join(main, path)
	}
	return path
}
