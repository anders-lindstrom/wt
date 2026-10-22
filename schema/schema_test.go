package schema

import (
	"fmt"
	"regexp"
	"testing"
)

var semver = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// A schema's x-version is MAJOR.MINOR.PATCH, and its major is the schema
// number in its file name: a breaking change is a new file, never an edit.
func TestEverySchemaVersionMatchesItsFileName(t *testing.T) {
	for _, d := range All() {
		m := semver.FindStringSubmatch(d.Version())
		if m == nil {
			t.Errorf("%s: x-version %q is not MAJOR.MINOR.PATCH", d.File, d.Version())
			continue
		}
		if want := fmt.Sprintf("%s.v%s.json", d.Name, m[1]); d.File != want {
			t.Errorf("%s: major version %s belongs in %s", d.File, m[1], want)
		}
	}
}
