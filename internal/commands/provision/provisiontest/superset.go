package provisiontest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// FakeSuperset writes a `superset` that answers projects/ws create for one
// project rooted at repoRoot, and points probeSuperset at it for this test.
// It returns the file every invocation's argv is appended to.
func FakeSuperset(t *testing.T, repoRoot string, running bool) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	exe := filepath.Join(dir, "superset")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + log + "\n" +
		"case \"$1 $2\" in\n" +
		"  'projects list') echo '[{\"id\":\"p1\",\"name\":\"demo\",\"path\":\"" + repoRoot + "\"}]' ;;\n" +
		"  'ws create') echo '{\"workspace\":{\"id\":\"w1\"},\"alreadyExists\":false}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	Stub(t, superset.Status{Exe: exe, Running: running})
	return log
}

// Stub replaces the Superset state for one test.
func Stub(t *testing.T, s superset.Status) {
	t.Helper()
	old := provision.ProbeSuperset
	provision.ProbeSuperset = func() superset.Status { return s }
	t.Cleanup(func() { provision.ProbeSuperset = old })
}

// StaleSuperset is a `superset` the way 1.36.0 is beside the desktop app: its
// status calls the manifest stale while the host answers everything else.
// WORKAROUND(superset-stale-status), with every test that uses it.
func StaleSuperset(t *testing.T, repoRoot string) string {
	t.Helper()
	return SupersetSaying(t, repoRoot, `{"running":false,"stale":true,"pid":96249}`)
}

// SupersetSaying is a 1.36.0 `superset` whose `status --json` prints status
// and whose host answers everything else. The real Probe is put in front of
// it, and it is first on the PATH. WORKAROUND(superset-stale-status)
func SupersetSaying(t *testing.T, repoRoot, status string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + log + "\n" +
		"case \"$1 $2\" in\n" +
		"  'status --json') echo '" + status + "' ;;\n" +
		"  '--version ') echo 1.36.0 ;;\n" +
		"  'projects list') echo '[{\"id\":\"p1\",\"name\":\"demo\",\"path\":\"" + repoRoot + "\"}]' ;;\n" +
		"  'ws create') echo '{\"workspace\":{\"id\":\"w1\"},\"alreadyExists\":false}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "superset"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := provision.ProbeSuperset
	provision.ProbeSuperset = superset.Probe
	t.Cleanup(func() { provision.ProbeSuperset = old })
	return log
}

// HostCommand answers for `ps` in this test, and returns the pids it was
// asked about. WORKAROUND(superset-stale-status)
func HostCommand(t *testing.T, line string, err error) *[]int {
	t.Helper()
	var asked []int
	old := provision.SupersetHostCommand
	provision.SupersetHostCommand = func(pid int) (string, error) {
		asked = append(asked, pid)
		return line, err
	}
	t.Cleanup(func() { provision.SupersetHostCommand = old })
	return &asked
}

// SupersetWithoutProject is a `superset` that knows some other repository, so
// the one under test is not one of its projects.
func SupersetWithoutProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	exe := filepath.Join(dir, "superset")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + log + "\n" +
		"case \"$1 $2\" in\n" +
		"  'projects list') echo '[{\"id\":\"p9\",\"name\":\"somewhere-else\",\"path\":\"/somewhere/else\"}]' ;;\n" +
		"  '--version ') echo 1.29.0 ;;\n" +
		"esac\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	Stub(t, superset.Status{Exe: exe, Running: true})
	return log
}
