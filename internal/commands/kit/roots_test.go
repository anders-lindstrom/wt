package kit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
)

// WT_ROOTS wins over the file, which wins over the defaults, and a root from
// WT_ROOTS is named after its directory.
func TestRootsComeFromTheEnvironmentThenTheFileThenTheDefaults(t *testing.T) {
	u := &config.User{Roots: []config.NamedRoot{{Name: "work", Path: "~/src/work"}}}
	home, _ := os.UserHomeDir()

	t.Setenv("WT_ROOTS", "/a/one:/b/one")
	roots, origin := RootsFor(u)
	if origin != "WT_ROOTS" || len(roots) != 2 || roots[0] != (Root{"one", "/a/one"}) || roots[1].Name != "one-2" {
		t.Errorf("WT_ROOTS: %+v (%s)", roots, origin)
	}
	t.Setenv("WT_ROOTS", "")
	roots, origin = RootsFor(u)
	if origin != "user config" || len(roots) != 1 || roots[0] != (Root{"work", filepath.Join(home, "src/work")}) {
		t.Errorf("file: %+v (%s)", roots, origin)
	}
	if roots, origin = RootsFor(&config.User{}); origin != "default" || len(roots) == 0 {
		t.Errorf("defaults: %+v (%s)", roots, origin)
	}
}
