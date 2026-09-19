package superset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fake writes a shell script as `superset` in its own directory and returns
// the CLI pointing at it. Every call to the real thing goes through argv, so a
// fake that records argv is what proves wt asks Superset the right question.
func fake(t *testing.T, body string) (CLI, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	exe := filepath.Join(dir, "superset")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\n" + body + "\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return CLI{Exe: exe}, log
}

func argv(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// A CLI on the PATH wins; without one the desktop app's shim is used, and it
// is not on the PATH by design.
func TestFind(t *testing.T) {
	onPath, _ := fake(t, "")
	home := t.TempDir()
	shim := filepath.Join(home, ".superset", "bin", "superset")
	if err := os.MkdirAll(filepath.Dir(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	empty := t.TempDir()
	noHome := t.TempDir()
	for name, tc := range map[string]struct {
		path, home string
		want       string
	}{
		"PATH wins":     {filepath.Dir(onPath.Exe), home, onPath.Exe},
		"falls to shim": {empty, home, shim},
		"neither":       {empty, noHome, ""},
		"unexecutable":  {empty, unexecutableHome(t), ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PATH", tc.path)
			t.Setenv("HOME", tc.home)
			c, ok := Find()
			if (tc.want != "") != ok {
				t.Fatalf("ok = %v, want %v", ok, tc.want != "")
			}
			if c.Exe != tc.want {
				t.Errorf("Exe = %q, want %q", c.Exe, tc.want)
			}
		})
	}
}

// A shim that is there but not executable is not a CLI: running it would fail
// with a confusing error rather than "Superset is not installed".
func unexecutableHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	shim := filepath.Join(home, ".superset", "bin", "superset")
	if err := os.MkdirAll(filepath.Dir(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

// The three states Superset has. `status` is the only command a stopped host
// answers, which is why it and not an error string is the gate.
func TestProbeStates(t *testing.T) {
	for name, tc := range map[string]struct {
		body        string
		wantRunning bool
		wantErr     string
	}{
		"running": {`echo '{"running":true,"port":48286}'`, true, ""},
		"stopped": {`echo '{"running":false}'`, false, ""},
		"broken": {
			`echo "Error: Host service for this machine isn't running" >&2; exit 1`,
			false, "Host service for this machine isn't running",
		},
		"not json": {`echo 'wat'`, false, "superset status --json"},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := fake(t, tc.body)
			t.Setenv("PATH", filepath.Dir(c.Exe))
			s := Probe()
			if !s.Installed() {
				t.Fatal("Installed() = false, want the fake")
			}
			if s.Running != tc.wantRunning {
				t.Errorf("Running = %v, want %v", s.Running, tc.wantRunning)
			}
			switch {
			case tc.wantErr == "" && s.Err != nil:
				t.Errorf("Err = %v, want none", s.Err)
			case tc.wantErr != "" && (s.Err == nil || !strings.Contains(s.Err.Error(), tc.wantErr)):
				t.Errorf("Err = %v, want it to mention %q", s.Err, tc.wantErr)
			}
		})
	}
}

// Probe on a machine with no Superset says so rather than failing.
func TestProbeNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	s := Probe()
	if s.Installed() || s.Err != nil || s.Running {
		t.Errorf("Probe() = %+v, want the zero state", s)
	}
}

func TestProjects(t *testing.T) {
	c, log := fake(t, `echo '[{"id":"p1","name":"one","path":"/repos/one"},{"id":"p2","name":"two","path":"/repos/two"}]'`)
	ps, err := c.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[1].ID != "p2" || ps[1].Path != "/repos/two" {
		t.Fatalf("Projects() = %+v", ps)
	}
	// The ids in ~/.superset/local.db are different ones; --local --json is
	// what asks the host for the ones `ws create` takes.
	if got := argv(t, log); len(got) != 1 || got[0] != "projects list --local --json" {
		t.Errorf("argv = %q", got)
	}
}

// A project recorded through a symlink and a repository discovered through the
// real path are the same repository.
func TestProjectAt(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	ps := []Project{{ID: "p1", Name: "one", Path: link}}

	if p, ok := ProjectAt(ps, root); !ok || p.ID != "p1" {
		t.Errorf("ProjectAt(real) = %+v, %v; want p1", p, ok)
	}
	if p, ok := ProjectAt(ps, root+string(filepath.Separator)); !ok || p.ID != "p1" {
		t.Errorf("ProjectAt(trailing slash) = %+v, %v; want p1", p, ok)
	}
	if _, ok := ProjectAt(ps, t.TempDir()); ok {
		t.Error("ProjectAt matched an unrelated directory")
	}
	if _, ok := ProjectAt(nil, root); ok {
		t.Error("ProjectAt matched with no projects")
	}
}

// Register asks Superset to adopt the worktree git already has for the branch:
// --skip-branch-prefix keeps the branch wt's, and nothing here creates a
// checkout.
func TestRegisterArgvAndOutcome(t *testing.T) {
	c, log := fake(t, `echo '{"workspace":{"id":"w1","name":"fix_wt/x","branch":"fix_wt/x"},"alreadyExists":false}'`)
	reg, err := c.Register("p1", "fix_wt/x")
	if err != nil {
		t.Fatal(err)
	}
	if reg.AlreadyExists || reg.Workspace.ID != "w1" {
		t.Errorf("Register() = %+v", reg)
	}
	want := "ws create --local --project p1 --name fix_wt/x --branch fix_wt/x --skip-branch-prefix --json"
	if got := argv(t, log); len(got) != 1 || got[0] != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// Registering a branch twice is Superset saying it already has one, not an
// error: `wt checkout` of a branch that has been through this before must not
// produce a duplicate or a failure.
func TestRegisterAlreadyExists(t *testing.T) {
	c, _ := fake(t, `echo '{"workspace":{"id":"w1"},"alreadyExists":true}'`)
	reg, err := c.Register("p1", "fix_wt/x")
	if err != nil {
		t.Fatal(err)
	}
	if !reg.AlreadyExists {
		t.Error("AlreadyExists = false, want true")
	}
}

// The CLI's own complaint is what a person needs to see, not "exit status 1".
func TestRunQuotesSupersetsComplaint(t *testing.T) {
	c, _ := fake(t, `printf 'Error: No active organization\nHint: Run: superset auth login\n' >&2; exit 1`)
	_, err := c.Projects()
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "No active organization") || strings.Contains(err.Error(), "Hint") {
		t.Errorf("err = %v, want the Error: line alone", err)
	}
}

// A superset that never answers must not hold `wt new` open.
func TestRunDeadline(t *testing.T) {
	c, _ := fake(t, `sleep 30`)
	old := readDeadline
	readDeadline = 200 * time.Millisecond
	t.Cleanup(func() { readDeadline = old })

	start := time.Now()
	_, err := c.Projects()
	if err == nil || !strings.Contains(err.Error(), "did not answer within") {
		t.Fatalf("err = %v, want a deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %s, want the deadline to have cut it short", elapsed)
	}
}

func TestVersion(t *testing.T) {
	c, _ := fake(t, `echo 1.29.0`)
	if got := c.Version(); got != "1.29.0" {
		t.Errorf("Version() = %q", got)
	}
	broken, _ := fake(t, `exit 1`)
	if got := broken.Version(); got != "" {
		t.Errorf("Version() = %q, want empty", got)
	}
}
