package admin

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anlindstrom/wt/internal/agents"
	"github.com/anlindstrom/wt/internal/commands/kit"
	"github.com/anlindstrom/wt/internal/commands/provision"
	"github.com/anlindstrom/wt/internal/commands/testkit"
)

func tmRepo(t *testing.T) (*kit.Context, *testkit.Tmutil) {
	t.Helper()
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, testkit.FakeTmutil(t, ctx, false)
}

// wt adopt excludes what it adopts: <repo>_wt/ for a worktree inside it, the
// worktree's own folder for one elsewhere, and after --relocate the folder
// it now lives in, not the one it left.
func TestAdoptExcludesTheWorktreeFromTimeMachine(t *testing.T) {
	cases := map[string]struct {
		at       func(parent string) string
		relocate bool
		want     func(parent, at string) string
	}{
		"in <repo>_wt": {
			at:   func(p string) string { return filepath.Join(p, "demo_wt", "demo", "feat_wt", "x") },
			want: func(p, _ string) string { return filepath.Join(p, "demo_wt") },
		},
		"elsewhere": {
			at:   func(p string) string { return filepath.Join(p, "agent", "x") },
			want: func(_, at string) string { return at },
		},
		"elsewhere, relocated": {
			at:       func(p string) string { return filepath.Join(p, "agent", "x") },
			relocate: true,
			want:     func(p, _ string) string { return filepath.Join(p, "demo_wt") },
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, tm := tmRepo(t)
			at := c.at(ctx.Repo.Parent)
			testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "-b", "feat_wt/x", at)
			if _, err := Adopt(ctx, at, c.relocate, provision.SetupOptions{}, io.Discard); err != nil {
				t.Fatalf("Adopt: %v", err)
			}
			if got, want := tm.Excluded(t), c.want(ctx.Repo.Parent, at); !slices.Equal(got, []string{want}) {
				t.Errorf("excluded %q, want %s", got, want)
			}
		})
	}
}

// wt migrate excludes the folder the worktree moved into.
func TestMigrateExcludesWhereTheWorktreeLands(t *testing.T) {
	ctx, tm := tmRepo(t)
	at := filepath.Join(ctx.Repo.Parent, "demo-legacy")
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "-b", "feat_wt/legacy", at)
	to, err := Migrate(ctx, at, "", MigrateOptions{Agents: []agents.Agent{}}, io.Discard)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if want := filepath.Join(ctx.Repo.Parent, "demo_wt"); filepath.Dir(filepath.Dir(to)) != want ||
		!slices.Equal(tm.Excluded(t), []string{want}) {
		t.Errorf("moved to %s; excluded %q, want %s", to, tm.Excluded(t), want)
	}
}

// wt migrate --dry-run moves nothing, so it excludes nothing.
func TestMigrateDryRunRunsNoTmutil(t *testing.T) {
	ctx, tm := tmRepo(t)
	at := filepath.Join(ctx.Repo.Parent, "demo-legacy")
	testkit.GitIn(t, ctx.Repo.MainRoot, "worktree", "add", "-q", "-b", "feat_wt/legacy", at)
	if _, err := Migrate(ctx, at, "", MigrateOptions{DryRun: true, Agents: []agents.Agent{}}, io.Discard); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := tm.Calls(t); len(got) != 0 {
		t.Errorf("tmutil was called %q", got)
	}
}

// wt pr checkout puts its worktree under <repo>_wt/.
func TestPRCheckoutExcludesTheWorktreeFolderFromTimeMachine(t *testing.T) {
	ctx, tm := tmRepo(t)
	testkit.FakeGitHub(t, ctx, testkit.OpenPR(12, "residential_fixes", "Residents no longer rewrite a door"))
	if _, err := PRCheckout(ctx, 12, PROptions{}, io.Discard); err != nil {
		t.Fatalf("PRCheckout: %v", err)
	}
	if got, want := tm.Excluded(t), filepath.Join(ctx.Repo.Parent, "demo_wt"); !slices.Equal(got, []string{want}) {
		t.Errorf("excluded %q, want %s", got, want)
	}
}

// doctorTimeMachine is the Time Machine section of wt doctor, and the
// problems the whole run counted.
func doctorTimeMachine(t *testing.T, ctx *kit.Context) (section string, problems int) {
	t.Helper()
	var out bytes.Buffer
	problems, err := Doctor(ctx, &out)
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(out.String(), "Time Machine:\n")
	if !ok {
		t.Fatalf("no Time Machine section in\n%s", out.String())
	}
	var lines []string
	for _, l := range strings.Split(rest, "\n") {
		if !strings.HasPrefix(l, "  ") {
			break
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n"), problems
}

// wt doctor names which of the four states the machine is in.
func TestDoctorNamesTheTimeMachineState(t *testing.T) {
	t.Run("not macOS", func(t *testing.T) {
		ctx, _ := tmRepo(t)
		ctx.Machine.GOOS = "linux"
		if got, _ := doctorTimeMachine(t, ctx); !strings.Contains(got, "not macOS") {
			t.Errorf("section %q", got)
		}
	})
	t.Run("off", func(t *testing.T) {
		ctx, tm := tmRepo(t)
		testkit.TimeMachineSetting(t, ctx, "false")
		if got, _ := doctorTimeMachine(t, ctx); !strings.Contains(got, "off in your wt config") {
			t.Errorf("section %q", got)
		}
		if calls := tm.Calls(t); len(calls) != 0 {
			t.Errorf("tmutil was called %q", calls)
		}
	})
	t.Run("no tmutil, by default", func(t *testing.T) {
		ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		testkit.OnMac(ctx)
		got, problems := doctorTimeMachine(t, ctx)
		if !strings.Contains(got, "  - no tmutil on the PATH") || problems != 0 {
			t.Errorf("section %q, %d problems", got, problems)
		}
	})
	t.Run("no tmutil, switched on in the file", func(t *testing.T) {
		ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf), io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		testkit.OnMac(ctx)
		testkit.TimeMachineSetting(t, ctx, "true")
		got, problems := doctorTimeMachine(t, ctx)
		if !strings.Contains(got, "  ! no tmutil on the PATH") || problems != 1 {
			t.Errorf("section %q, %d problems", got, problems)
		}
	})
	t.Run("on", func(t *testing.T) {
		ctx, _ := tmRepo(t)
		if got, _ := doctorTimeMachine(t, ctx); !strings.Contains(got, "worktree folders wt makes are excluded") {
			t.Errorf("section %q", got)
		}
	})
}

// wt doctor says whether this repository's <repo>_wt folder is excluded,
// and changes nothing either way.
func TestDoctorSaysWhetherTheWorktreeFolderIsExcluded(t *testing.T) {
	ctx, tm := tmRepo(t)
	container := filepath.Join(ctx.Repo.Parent, "demo_wt")
	if err := os.MkdirAll(container, 0o755); err != nil {
		t.Fatal(err)
	}
	got, problems := doctorTimeMachine(t, ctx)
	if !strings.Contains(got, container+" is not excluded") || problems != 0 {
		t.Errorf("section %q, %d problems", got, problems)
	}
	if excluded := tm.Excluded(t); len(excluded) != 0 {
		t.Errorf("doctor excluded %q", excluded)
	}
	tm.AlreadyExcluded(t, container)
	if got, _ := doctorTimeMachine(t, ctx); !strings.Contains(got, "✓ "+container+" is excluded") {
		t.Errorf("section %q", got)
	}
}
