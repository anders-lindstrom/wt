package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
)

// A configuration file that names MAIN_BRANCH is the trunk's source.
func TestTrunkSourceIsTheConfigWhenItNamesTrunk(t *testing.T) {
	ctx, err := Open(committedRepo(t, minimalConf))
	if err != nil {
		t.Fatal(err)
	}
	if got := ctx.Config.TrunkSource; got != config.TrunkFromConfig {
		t.Errorf("TrunkSource = %q, want config", got)
	}
}

// The shape FINDING 5 was seen in: no config, no origin/HEAD, main here and
// on origin, the main checkout on a feature branch. Trunk is main.
func TestDetectedTrunkIsNotTheCheckedOutFeatureBranch(t *testing.T) {
	main := unconfiguredRepo(t)
	withOriginNoHead(t, main)
	gitIn(t, main, "checkout", "-q", "-b", "feat/stage-6")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	if c := ctx.Config; c.MainBranch != "main" || c.TrunkSource != config.TrunkConventional {
		t.Errorf("trunk %q from %q, want main from conventional", c.MainBranch, c.TrunkSource)
	}
}

// A config file without MAIN_BRANCH detects it the same way.
func TestConfigWithoutMainBranchDetectsTrunk(t *testing.T) {
	main := committedRepo(t, "BUILD_INIT_ENABLED=false\n")
	gitIn(t, main, "checkout", "-q", "-b", "feat/x")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	if c := ctx.Config; c.MainBranch != "main" || c.TrunkSource != config.TrunkConventional {
		t.Errorf("trunk %q from %q, want main from conventional", c.MainBranch, c.TrunkSource)
	}
}

// guessedRepo is an unconfigured repository whose only branch has no
// conventional name, so its trunk can only be guessed.
func guessedRepo(t *testing.T) string {
	t.Helper()
	main := unconfiguredRepo(t)
	gitIn(t, main, "branch", "-m", "main", "production")
	return main
}

func TestATrunkGuessedFromTheCheckoutSaysSo(t *testing.T) {
	ctx, err := Open(guessedRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	if c := ctx.Config; c.MainBranch != "production" || c.TrunkSource != config.TrunkCurrentBranchGuess {
		t.Errorf("trunk %q from %q, want production guessed", c.MainBranch, c.TrunkSource)
	}
	var buf bytes.Buffer
	if err := Config(ctx, false, &buf); err != nil {
		t.Fatal(err)
	}
	want := "trunk:         production (guessed from the checked-out branch; `wt init` pins it)\n"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("wt config does not say the trunk is a guess:\n%s", buf.String())
	}
}

// wt doctor warns about a guessed trunk, naming the --json field.
func TestDoctorWarnsAboutAGuessedTrunk(t *testing.T) {
	ctx := OpenLenient(guessedRepo(t), &bytes.Buffer{})
	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if problems != 1 || !strings.Contains(out, "  ! trunk production is guessed") ||
		!strings.Contains(out, "currentBranchGuess") || !strings.Contains(out, "wt init") {
		t.Errorf("%d problem(s); want one warning about the guessed trunk:\n%s", problems, out)
	}
}

// wt init --yes writes a guessed trunk down, and says it was a guess.
func TestInitSaysItPinsAGuessedTrunk(t *testing.T) {
	ctx, err := Open(guessedRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Init(ctx.Repo, InitOptions{}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "  ! MAIN_BRANCH production is the checked-out branch") {
		t.Errorf("init does not say its trunk was a guess:\n%s", buf.String())
	}
}

// withOriginNoHead gives dir a fetched origin but no refs/remotes/origin/HEAD,
// as a clone made before git recorded it has.
func withOriginNoHead(t *testing.T, dir string) {
	t.Helper()
	origin := t.TempDir() + "/origin.git"
	gitIn(t, dir, "clone", "-q", "--bare", dir, origin)
	gitIn(t, dir, "remote", "add", "origin", origin)
	gitIn(t, dir, "fetch", "-q", "origin")
	gitIn(t, dir, "remote", "set-head", "origin", "--delete")
}
