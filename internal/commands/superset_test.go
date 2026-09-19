package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// TestMain puts "no Superset on this machine" in front of every test that does
// not ask for another state, so these tests never reach the Superset of
// whoever runs them.
func TestMain(m *testing.M) {
	probeSuperset = func() superset.Status { return superset.Status{} }
	os.Exit(m.Run())
}

// fakeSuperset writes a `superset` that answers projects/ws create for one
// project rooted at repoRoot, and points probeSuperset at it for this test.
// It returns the file every invocation's argv is appended to.
func fakeSuperset(t *testing.T, repoRoot string, running bool) string {
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
	stub(t, superset.Status{Exe: exe, Running: running})
	return log
}

// stub replaces the Superset state for one test.
func stub(t *testing.T, s superset.Status) {
	t.Helper()
	old := probeSuperset
	probeSuperset = func() superset.Status { return s }
	t.Cleanup(func() { probeSuperset = old })
}

func argvOf(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// The happy path: a repository Superset knows, a running host, and a worktree
// wt has just made. Superset is asked to adopt the branch — never to create a
// checkout, which it already has.
func TestNewRegistersTheWorktreeWithSuperset(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	log := fakeSuperset(t, ctx.Repo.MainRoot, true)

	var out, errs bytes.Buffer
	path, err := New(ctx, "fix/login-crash", NewOptions{}, &errs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout written by the command itself: %q", out.String())
	}
	if !strings.Contains(errs.String(), "✓ registered with Superset as a workspace of project demo") {
		t.Errorf("stderr = %q", errs.String())
	}
	want := []string{
		"projects list --local --json",
		"ws create --local --project p1 --name fix_wt/login-crash --branch fix_wt/login-crash --skip-branch-prefix --json",
	}
	if got := argvOf(t, log); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("argv =\n  %q\nwant\n  %q", got, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("worktree not created: %v", err)
	}
}

// Every way Superset can be unusable leaves `wt new` doing exactly what it did
// before, saying at most one line about it — and saying nothing at all on a
// machine that has no Superset.
func TestNewSurvivesEverySupersetState(t *testing.T) {
	for name, tc := range map[string]struct {
		mode   config.SupersetMode
		status superset.Status
		want   string
	}{
		"not installed": {config.SupersetAuto, superset.Status{}, ""},
		"host stopped": {
			config.SupersetAuto, superset.Status{Exe: "/nope/superset"},
			"- Superset's host service is not running; not registered",
		},
		"status unreadable": {
			config.SupersetAuto, superset.Status{Exe: "/nope/superset", Err: errBoom},
			"- boom; not registered",
		},
		"running but no such binary": {
			config.SupersetAuto, superset.Status{Exe: "/nope/superset", Running: true},
			"- superset projects list --local --json failed",
		},
		"off": {config.SupersetOff, superset.Status{Exe: "/nope/superset"}, ""},
		"on and absent": {
			config.SupersetOn, superset.Status{},
			"! SUPERSET_REGISTER=on but no superset",
		},
		"on and stopped": {
			config.SupersetOn, superset.Status{Exe: "/nope/superset"},
			"! Superset's host service is not running",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := Open(committedRepo(t, minimalConf))
			if err != nil {
				t.Fatal(err)
			}
			ctx.Config.SupersetRegister = tc.mode
			stub(t, tc.status)

			var errs bytes.Buffer
			path, err := New(ctx, "fix/login-crash", NewOptions{}, &errs)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("worktree not created: %v", err)
			}
			if tc.want == "" {
				if line := supersetLine(errs.String()); line != "" {
					t.Errorf("stderr mentions Superset: %q", line)
				}
				return
			}
			if !strings.Contains(errs.String(), tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", errs.String(), tc.want)
			}
		})
	}
}

// supersetLine is the first output line about Superset, ignoring worktree
// paths, which carry the test's own name and so the word.
func supersetLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		for _, marker := range []string{"- Superset", "! Superset", "! SUPERSET_REGISTER", "- SUPERSET_REGISTER", "registered with Superset"} {
			if strings.Contains(line, marker) {
				return line
			}
		}
	}
	return ""
}

var errBoom = boom{}

type boom struct{}

func (boom) Error() string { return "boom" }

// A branch that already has a workspace is Superset's answer, not wt's
// problem: nothing is duplicated and nothing fails.
func TestRegisterSupersetIsIdempotent(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "superset")
	script := "#!/bin/sh\n" +
		"case \"$1 $2\" in\n" +
		"  'projects list') echo '[{\"id\":\"p1\",\"name\":\"demo\",\"path\":\"" + ctx.Repo.MainRoot + "\"}]' ;;\n" +
		"  'ws create') echo '{\"workspace\":{\"id\":\"w1\"},\"alreadyExists\":true}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	stub(t, superset.Status{Exe: exe, Running: true})

	var errs bytes.Buffer
	if _, err := New(ctx, "fix/login-crash", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errs.String(), "- Superset already had a workspace for fix_wt/login-crash") {
		t.Errorf("stderr = %q", errs.String())
	}
}

// --no-superset and --no-setup both keep Superset out of it: the second means
// "the checkout, nothing else", and a workspace is one of the else — Superset
// answers a new one by running the project's setup script.
func TestNewSkipsSupersetWhenAsked(t *testing.T) {
	for name, opts := range map[string]NewOptions{
		"--no-superset": {NoSuperset: true},
		"--no-setup":    {NoSetup: true},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := Open(committedRepo(t, minimalConf))
			if err != nil {
				t.Fatal(err)
			}
			log := fakeSuperset(t, ctx.Repo.MainRoot, true)
			var errs bytes.Buffer
			if _, err := New(ctx, "fix/login-crash", opts, &errs); err != nil {
				t.Fatal(err)
			}
			if got := argvOf(t, log); got != nil {
				t.Errorf("superset was run: %q", got)
			}
		})
	}
}

// Checkout goes through the same tail as New, so a branch someone else pushed
// reaches Superset the same way.
func TestCheckoutRegistersToo(t *testing.T) {
	main := committedRepo(t, minimalConf)
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "branch", "fix_wt/from-elsewhere")
	log := fakeSuperset(t, ctx.Repo.MainRoot, true)

	var errs bytes.Buffer
	if _, err := Checkout(ctx, "fix_wt/from-elsewhere", "", NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	got := argvOf(t, log)
	if len(got) != 2 || !strings.Contains(got[1], "--branch fix_wt/from-elsewhere") {
		t.Errorf("argv = %q", got)
	}
}

// Doctor's Superset section: what state it found, and whether that state is a
// problem — which only SUPERSET_REGISTER=on makes it.
func TestDoctorReportsSupersetState(t *testing.T) {
	for name, tc := range map[string]struct {
		mode        config.SupersetMode
		status      superset.Status
		want        string
		wantProblem bool
	}{
		"absent under auto": {
			config.SupersetAuto, superset.Status{},
			"  - no superset on the PATH or at ~/.superset/bin", false,
		},
		"absent under on": {
			config.SupersetOn, superset.Status{},
			"  ! no superset on the PATH or at ~/.superset/bin", true,
		},
		"off": {
			config.SupersetOff, superset.Status{Exe: "/nope/superset", Running: true},
			"  - SUPERSET_REGISTER=off", false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := Open(committedRepo(t, minimalConf))
			if err != nil {
				t.Fatal(err)
			}
			ctx.Config.SupersetRegister = tc.mode
			stub(t, tc.status)

			var out bytes.Buffer
			problems, err := Doctor(ctx, &out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("doctor said\n%s\nwant a line %q", out.String(), tc.want)
			}
			if (problems > 0) != tc.wantProblem {
				t.Errorf("problems = %d, want problem = %v", problems, tc.wantProblem)
			}
		})
	}
}

// The full report, when Superset is running and knows this repository.
func TestDoctorReportsAUsableSuperset(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "superset")
	script := "#!/bin/sh\n" +
		"case \"$1 $2\" in\n" +
		"  '--version ') echo 1.29.0 ;;\n" +
		"  'projects list') echo '[{\"id\":\"p1\",\"name\":\"demo\",\"path\":\"" + ctx.Repo.MainRoot + "\"}]' ;;\n" +
		"esac\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	stub(t, superset.Status{Exe: exe, Running: true})

	var out bytes.Buffer
	problems, err := Doctor(ctx, &out)
	if err != nil {
		t.Fatal(err)
	}
	if problems != 0 {
		t.Errorf("problems = %d, want 0:\n%s", problems, out.String())
	}
	for _, want := range []string{"(1.29.0), host service running", `✓ project "demo"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("doctor said\n%s\nwant %q", out.String(), want)
		}
	}
}
