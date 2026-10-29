package admin

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// Every Superset workspace sits one segment off the canonical path, by that
// tool's design. Reporting each of them as a problem made doctor exit non-zero
// on a healthy repository and prescribe a migrate that breaks the workspace.
func TestDoctorAcceptsSupersetsLayout(t *testing.T) {
	t.Parallel()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, _ := kit.Open(main, io.Discard)
	superset := filepath.Join(ctx.Repo.Parent, "demo_wt", "demo", "feat_wt", "thing")
	testkit.GitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/thing", superset)

	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if problems != 0 {
		t.Errorf("Superset's layout reported as %d problem(s):\n%s", problems, buf.String())
	}
	if !strings.Contains(buf.String(), "Superset") {
		t.Errorf("want the layout named so it is not mistaken for canonical:\n%s", buf.String())
	}
}

// T3 Code puts its worktrees in a directory of its own, in a folder named
// after the temporary branch it renames later. Neither shape is wt's, and
// neither is a problem: the thread holds the path.
func TestDoctorAcceptsT3sLayout(t *testing.T) {
	// Not parallel: sets environment variables for the whole test binary.
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, _ := kit.Open(main, io.Discard)
	data := t.TempDir()
	t.Setenv("T3CODE_HOME", data)
	renamed := filepath.Join(data, "worktrees", "demo", "t3-0aedd242")
	testkit.GitIn(t, main, "worktree", "add", "-q", "-b", "t3_wt/fix-login", renamed)
	temporary := filepath.Join(data, "worktrees", "demo", "t3-5f2c8e10")
	testkit.GitIn(t, main, "worktree", "add", "-q", "-b", "t3/5f2c8e10", temporary)

	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if problems != 0 {
		t.Errorf("T3 Code's layout reported as %d problem(s):\n%s", problems, buf.String())
	}
	if n := strings.Count(buf.String(), "T3 Code's layout"); n != 2 {
		t.Errorf("want both worktrees named T3 Code's layout, got %d:\n%s", n, buf.String())
	}
}

// A pre-migration checkout is the case the migrate advice was written for:
// nothing else holds the path, so moving it is safe and still recommended.
func TestDoctorStillReportsAForeignLayout(t *testing.T) {
	t.Parallel()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	ctx, _ := kit.Open(main, io.Discard)
	testkit.GitIn(t, main, "worktree", "add", "-q", "-b", "feat_wt/thing",
		filepath.Join(ctx.Repo.Parent, "demo-thing"))

	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if problems == 0 {
		t.Errorf("want the pre-migration layout reported:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "wt migrate feat/thing") {
		t.Errorf("want the migrate prescription kept:\n%s", buf.String())
	}
}

// A repository with no configuration and no trunk to detect is the one case
// where doctor's closing advice had nothing behind it: there are no migrate
// commands above to need fixing first, and no way out was named.
func TestDoctorTellsAConfiglessRepoToRunInit(t *testing.T) {
	t.Parallel()
	ctx := kit.OpenLenient(testkit.NoTrunkRepo(t), io.Discard)
	if ctx == nil {
		t.Fatal("OpenLenient returned nil for a real repository")
	}

	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if problems == 0 {
		t.Fatal("Doctor found no problems in a repository with no configuration")
	}
	out := buf.String()
	if !strings.Contains(out, "wt init") {
		t.Errorf("doctor does not name the command that fixes it:\n%s", out)
	}
	if strings.Contains(out, "the migrate commands above") {
		t.Errorf("doctor advises fixing config for migrate commands it never printed:\n%s", out)
	}
}

// A configuration that exists but does not parse keeps the original advice:
// there the mutating commands really are what the fix unblocks.
func TestDoctorStillSaysToFixAConfigurationThatIsPresentButBroken(t *testing.T) {
	t.Parallel()
	main := testkit.FixtureRepo(t, "MAIN_BRANCH=\"main\"\nNO_SUCH_KEY=1\n")
	ctx := kit.OpenLenient(main, io.Discard)

	var buf bytes.Buffer
	if _, err := Doctor(ctx, &buf); err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Fix the configuration first") {
		t.Errorf("doctor dropped the advice for a broken configuration:\n%s", out)
	}
	if strings.Contains(out, "wt init") {
		t.Errorf("doctor tells a configured repository to run wt init:\n%s", out)
	}
}
