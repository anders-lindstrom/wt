package provision_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// The happy path: a repository Superset knows, a running host, and a worktree
// wt has just made. Superset is asked to adopt the branch — never to create a
// checkout, which it already has.
func TestNewRegistersTheWorktreeWithSuperset(t *testing.T) {
	t.Parallel()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.OptIn(ctx)
	log := provisiontest.FakeSuperset(t, ctx, ctx.Repo.MainRoot, true)

	var out, errs bytes.Buffer
	path, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs)
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
	if got := testkit.ArgvOf(t, log); strings.Join(got, "|") != strings.Join(want, "|") {
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
	t.Parallel()
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
			config.SupersetAuto, superset.Status{Exe: "/nope/superset", Err: testkit.ErrBoom},
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
			ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
			if err != nil {
				t.Fatal(err)
			}
			testkit.OptIn(ctx)
			ctx.Config.SupersetRegister = tc.mode
			provisiontest.Stub(ctx, tc.status)

			var errs bytes.Buffer
			path, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs)
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

// A branch that already has a workspace is Superset's answer, not wt's
// problem: nothing is duplicated and nothing fails.
func TestRegisterSupersetIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.OptIn(ctx)
	dir := t.TempDir()
	exe := filepath.Join(dir, "superset")
	script := "#!/bin/sh\n" +
		"case \"$1 $2\" in\n" +
		"  'projects list') echo '[{\"id\":\"p1\",\"name\":\"demo\",\"path\":\"" + ctx.Repo.MainRoot + "\"}]' ;;\n" +
		"  'ws create') echo '{\"workspace\":{\"id\":\"w1\"},\"alreadyExists\":true}' ;;\n" +
		"esac\n"
	gittest.WriteExecutable(t, exe, script)
	provisiontest.Stub(ctx, superset.Status{Exe: exe, Running: true})

	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
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
	t.Parallel()
	for name, opts := range map[string]provision.NewOptions{
		"--no-superset": {NoSuperset: true},
		"--no-setup":    {NoSetup: true},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
			if err != nil {
				t.Fatal(err)
			}
			testkit.OptIn(ctx)
			log := provisiontest.FakeSuperset(t, ctx, ctx.Repo.MainRoot, true)
			var errs bytes.Buffer
			if _, err := provision.New(ctx, "fix/login-crash", opts, &errs); err != nil {
				t.Fatal(err)
			}
			if got := testkit.ArgvOf(t, log); got != nil {
				t.Errorf("superset was run: %q", got)
			}
		})
	}
}

// Checkout goes through the same tail as New, so a branch someone else pushed
// reaches Superset the same way.
func TestCheckoutRegistersToo(t *testing.T) {
	t.Parallel()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	testkit.OptIn(ctx)
	testkit.GitIn(t, main, "branch", "fix_wt/from-elsewhere")
	log := provisiontest.FakeSuperset(t, ctx, ctx.Repo.MainRoot, true)

	var errs bytes.Buffer
	if _, err := provision.Checkout(ctx, "fix_wt/from-elsewhere", "", provision.NewOptions{}, &errs); err != nil {
		t.Fatal(err)
	}
	got := testkit.ArgvOf(t, log)
	if len(got) != 2 || !strings.Contains(got[1], "--branch fix_wt/from-elsewhere") {
		t.Errorf("argv = %q", got)
	}
}

// Superset's status calling a serving host stale does not stop a registration:
// the host is asked, answers, and the worktree is registered with no remark.
func TestNewRegistersThoughStatusCallsTheHostStale(t *testing.T) {
	// Not parallel: swaps ProbeSuperset, SupersetHostCommand; sets environment variables for the whole test binary.
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	testkit.OptIn(ctx)
	log := provisiontest.StaleSuperset(t, ctx.Repo.MainRoot)
	asked := provisiontest.HostCommand(t, "", nil)

	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.Contains(errs.String(), "✓ registered with Superset as a workspace of project demo") {
		t.Errorf("stderr = %q", errs.String())
	}
	if strings.Contains(errs.String(), "not running") || strings.Contains(errs.String(), "stale") {
		t.Errorf("stderr remarks on Superset's state: %q", errs.String())
	}
	got := testkit.ArgvOf(t, log)
	if len(got) != 4 || got[0] != "status --json" || !strings.HasPrefix(got[3], "ws create --local --project p1 ") {
		t.Errorf("argv = %q, want status, the probe's read, the projects and ws create", got)
	}
	if len(*asked) != 0 {
		t.Errorf("ps was asked about %v; only doctor inspects a process", *asked)
	}
}

// Most repositories are not Superset projects, and an integration that does
// not apply here says nothing. Under auto that is silence; a repository that
// asked for registration with SUPERSET_REGISTER=on is told.
func TestNewSaysNothingAboutARepositorySupersetDoesNotTrack(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		mode config.SupersetMode
		want string
	}{
		"auto": {config.SupersetAuto, ""},
		"on":   {config.SupersetOn, "! Superset has no project for "},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
			if err != nil {
				t.Fatal(err)
			}
			testkit.OptIn(ctx)
			ctx.Config.SupersetRegister = tc.mode
			log := provisiontest.SupersetWithoutProject(t, ctx)

			var errs bytes.Buffer
			if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if line := supersetLine(errs.String()); line != "" {
					t.Errorf("stderr mentions Superset: %q", line)
				}
			} else if !strings.Contains(errs.String(), tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", errs.String(), tc.want)
			}
			// Either way the projects were read: the silence is about what
			// was found, not about wt declining to look.
			if got := testkit.ArgvOf(t, log); len(got) != 1 || !strings.HasPrefix(got[0], "projects list") {
				t.Errorf("argv = %q, want the project listing alone", got)
			}
		})
	}
}

// A worktree with no branch has nothing Superset can adopt: Superset takes the
// checkout git already has for a named branch.
func TestRegisterSupersetNeedsABranch(t *testing.T) {
	t.Parallel()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	testkit.OptIn(ctx)
	log := provisiontest.FakeSuperset(t, ctx, ctx.Repo.MainRoot, true)
	path := ctx.Scheme().Dir("fix", "detached")
	testkit.GitIn(t, main, "worktree", "add", "--detach", path, "HEAD")

	var errs bytes.Buffer
	provision.RegisterSuperset(ctx, path, &errs)
	if !strings.Contains(errs.String(), path+" is not on a branch; not registered with Superset") {
		t.Errorf("stderr = %q", errs.String())
	}
	if got := testkit.ArgvOf(t, log); got != nil {
		t.Errorf("superset was asked to adopt a worktree with no branch: %q", got)
	}
}

// Provisioning that failed does not cost the registration: the worktree is on
// disk and on its branch, which is all Superset is told.
func TestRegisterSupersetRunsEvenWhenSetupFailed(t *testing.T) {
	t.Parallel()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	// A provision.sh that fails is the one thing Setup reports and carries on
	// from; the worktree is made either way.
	dir := filepath.Join(main, "bin", "worktree")
	gittest.WriteExecutable(t, filepath.Join(dir, "provision.sh"), "#!/bin/sh\necho 'provisioning broke' >&2\nexit 1\n")
	testkit.GitIn(t, main, "add", "-A")
	testkit.GitIn(t, main, "commit", "-qm", "provision")
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	testkit.OptIn(ctx)
	log := provisiontest.FakeSuperset(t, ctx, ctx.Repo.MainRoot, true)

	var errs bytes.Buffer
	if _, err := provision.New(ctx, "fix/login-crash", provision.NewOptions{}, &errs); err == nil {
		t.Fatal("a provision.sh that failed was not reported")
	}
	if !strings.Contains(errs.String(), "✓ registered with Superset") {
		t.Errorf("stderr = %q, want the registration to have gone ahead", errs.String())
	}
	if got := testkit.ArgvOf(t, log); len(got) != 2 {
		t.Errorf("argv = %q, want the listing and the create", got)
	}
}
