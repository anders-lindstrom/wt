package schema

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

var semver = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// A schema that changes gets a new version, so a caller can tell: this test
// fails until versions.lock names the new content under a new version, and
// the version is the file's x-version, its major the file's schema number.
func TestEverySchemaChangeIsVersioned(t *testing.T) {
	lock := map[string][2]string{}
	f, err := os.Open("versions.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			t.Fatalf("versions.lock: %q is not <file> <version> <sha256>", line)
		}
		lock[fields[0]] = [2]string{fields[1], fields[2]}
	}
	for _, d := range All() {
		sum := sha256.Sum256(d.Bytes)
		hash := hex.EncodeToString(sum[:])
		m := semver.FindStringSubmatch(d.Version())
		if m == nil {
			t.Errorf("%s: x-version %q is not MAJOR.MINOR.PATCH", d.File, d.Version())
			continue
		}
		if want := fmt.Sprintf("%s.v%s.json", d.Name, m[1]); d.File != want {
			t.Errorf("%s: major version %s belongs in %s", d.File, m[1], want)
		}
		entry, ok := lock[d.File]
		switch {
		case !ok:
			t.Errorf("versions.lock has no line for %s; add: %s %s %s", d.File, d.File, d.Version(), hash)
		case entry[1] != hash && entry[0] == d.Version():
			t.Errorf("%s changed but its version did not: bump x-version (minor for an added field or "+
				"enumeration value, patch for wording, a new file for a breaking change), then write in "+
				"versions.lock: %s <new version> %s", d.File, d.File, hash)
		case entry[1] != hash || entry[0] != d.Version():
			t.Errorf("versions.lock is out of date for %s; write: %s %s %s", d.File, d.File, d.Version(), hash)
		}
	}
}
