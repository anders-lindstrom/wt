package synccmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/provision/provisiontest"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// failGit puts a git on the PATH that fails the one command whose arguments
// contain match and runs the real git for everything else, so a test can
// break a single call in the middle of a sequence that must otherwise work.
func failGit(t *testing.T, match string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	stub := t.TempDir()
	body := "#!/bin/sh\ncase \"$*\" in\n*'" + match + "'*) echo 'fatal: boom' >&2; exit 128 ;;\nesac\nexec " + gitPath + " \"$@\"\n"
	path := filepath.Join(stub, "git")
	gittest.WriteExecutable(t, path, body)
	// The first exec of a new file can be slow (macOS vets it); warm it so
	// no deadline under test is spent on that.
	if err := exec.Command(path, "--version").Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Reading the tip of a branch that was just pushed can fail. Returning there
// took the run's whole report with it: the worktrees that did not finish were
// never printed, and the push that did go through was never named either.
func TestOfferPushKeepsTheReportWhenANewTipCannotBeRead(t *testing.T) {
	// Not parallel: puts a fake git on the process-wide PATH.
	if testing.Short() {
		t.Skip("integration: real repositories with an origin to push to")
	}
	ctx, bump := provisiontest.RunFixture(t, false)
	bare := testkit.PushOrigin(t, ctx)
	// Something for the push to carry: the fixture's origin already holds the
	// branch as it is, so without this the tip never moves and the read under
	// test is not the read of a new tip.
	testkit.WriteFile(t, bump, "p.txt", "push me\n")
	testkit.GitOut(t, bump, "add", "-A")
	testkit.GitOut(t, bump, "commit", "-q", "-m", "ahead of origin")
	// Only the read of the new tip fails; the push before it is the real one.
	failGit(t, "rev-parse --verify feat_wt/bump")

	var out bytes.Buffer
	_, failed, err := OfferPush(ctx, &out, PushAlways, PushOptions{}, []PushTarget{{Work: "bump", Branch: "feat_wt/bump", Path: bump}})
	if err != nil {
		t.Fatalf("a tip that could not be read ended the run: %v\n%s", err, out.String())
	}
	if len(failed) != 1 || !strings.Contains(failed[0], "bump") {
		t.Fatalf("failed %v; the run's report must still name it", failed)
	}
	if !strings.Contains(out.String(), "its new tip could not be read") {
		t.Fatalf("out %s", out.String())
	}
	// Named as something that did not come off, but the push itself stands.
	if !testkit.Pushed(t, bare, bump) {
		t.Fatal("the push did not reach origin; the test never read a new tip")
	}
}
