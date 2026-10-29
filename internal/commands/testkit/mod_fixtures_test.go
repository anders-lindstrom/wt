package testkit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modFixtures is where the wt mod's tests keep the wt --json they answer
// with: one file per schema, named after it, holding `export default ` and
// the JSON, because a mod can import only code.
const modFixtures = "../../../mods/wt/tests/fixtures"

// The mod's tests answer it with wt output; each fixture must be output the
// current schema accepts, so a change to wt's JSON that breaks the mod's
// input fails here and not only in the mod's own suite.
func TestTheModsFixturesMatchTheirSchemas(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join(modFixtures, "*.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no fixtures in %s", modFixtures)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			out, ok := bytes.CutPrefix(src, []byte("export default "))
			if !ok {
				t.Fatalf("%s does not start with `export default `", f)
			}
			ValidateJSON(t, strings.TrimSuffix(filepath.Base(f), ".ts"), out)
		})
	}
}
