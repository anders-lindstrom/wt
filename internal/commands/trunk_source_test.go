package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/repo"
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

// noTrunkRepo is an unconfigured repository whose only branch has no
// conventional name and no origin/HEAD, so nothing names its trunk.
func noTrunkRepo(t *testing.T) string {
	t.Helper()
	main := unconfiguredRepo(t)
	gitIn(t, main, "branch", "-m", "main", "production")
	return main
}

// Nothing names trunk: the checked-out branch is not taken for it. The error
// is ErrNoConfig too, which --json reports as noConfiguration.
func TestOpenFailsWhenNothingNamesTrunk(t *testing.T) {
	_, err := Open(noTrunkRepo(t))
	if !errors.Is(err, repo.ErrNoTrunk) || !errors.Is(err, config.ErrNoConfig) {
		t.Fatalf("Open = %v, want ErrNoTrunk and ErrNoConfig", err)
	}
	if !strings.Contains(err.Error(), "cannot tell which branch is trunk") {
		t.Errorf("error does not say what is missing: %v", err)
	}
}

// A config file without MAIN_BRANCH has nothing to fall back on either.
func TestConfigWithoutMainBranchFailsWhenNothingNamesTrunk(t *testing.T) {
	main := committedNoTrunkRepo(t)
	if _, err := Open(main); !errors.Is(err, repo.ErrNoTrunk) {
		t.Fatalf("Open = %v, want ErrNoTrunk", err)
	}
	ctx := OpenLenient(main, &bytes.Buffer{})
	if !errors.Is(ctx.ConfigError, repo.ErrNoTrunk) {
		t.Errorf("OpenLenient ConfigError = %v, want ErrNoTrunk", ctx.ConfigError)
	}
}

// wt doctor names the failed detection and the way out.
func TestDoctorSaysNothingNamesTrunk(t *testing.T) {
	ctx := OpenLenient(noTrunkRepo(t), &bytes.Buffer{})
	var buf bytes.Buffer
	problems, err := Doctor(ctx, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); problems != 1 || !strings.Contains(out, "  ! cannot tell which branch is trunk") ||
		strings.Contains(out, "guess") {
		t.Errorf("%d problem(s); want the failed detection alone:\n%s", problems, out)
	}
}

// wt init --yes has no trunk to write, and refuses rather than write a guess.
func TestInitWithoutAskingRefusesWhenNothingNamesTrunk(t *testing.T) {
	main := noTrunkRepo(t)
	r := mustDiscover(t, main)
	err := Init(r, InitOptions{}, &bytes.Buffer{})
	if !errors.Is(err, repo.ErrNoTrunk) {
		t.Fatalf("Init = %v, want ErrNoTrunk", err)
	}
	if _, statErr := os.Stat(filepath.Join(main, "bin")); !os.IsNotExist(statErr) {
		t.Errorf("init wrote something: %v", statErr)
	}
}

// Asked, wt init offers no trunk, and writes the one named.
func TestInitAsksForTrunkWithNoDefaultWhenNothingNamesIt(t *testing.T) {
	main := noTrunkRepo(t)
	var offered []Answers
	ask := func(a Answers) (Answers, error) {
		offered = append(offered, a)
		a.MainBranch = "production"
		return a, nil
	}
	var buf bytes.Buffer
	if err := Init(mustDiscover(t, main), InitOptions{Ask: ask}, &buf); err != nil {
		t.Fatal(err)
	}
	if len(offered) != 1 || offered[0].MainBranch != "" {
		t.Errorf("offered %+v, want one round with no trunk", offered)
	}
	conf, _ := os.ReadFile(filepath.Join(main, "bin", "worktree", "worktree.conf"))
	if !strings.Contains(string(conf), "MAIN_BRANCH=production") {
		t.Errorf("init did not write the named trunk:\n%s", conf)
	}
	if !strings.Contains(buf.String(), "no origin/HEAD and no development, main or master") {
		t.Errorf("init does not say why no trunk is offered:\n%s", buf.String())
	}
}

// Asked, a blank trunk is refused, not written.
func TestInitRefusesABlankTrunk(t *testing.T) {
	main := noTrunkRepo(t)
	ask := func(a Answers) (Answers, error) { return a, nil }
	if err := Init(mustDiscover(t, main), InitOptions{Ask: ask}, &bytes.Buffer{}); err == nil {
		t.Fatal("init wrote a configuration with no trunk")
	}
	if _, err := os.Stat(filepath.Join(main, "bin")); !os.IsNotExist(err) {
		t.Errorf("init wrote something: %v", err)
	}
}

func mustDiscover(t *testing.T, dir string) *repo.Repo {
	t.Helper()
	r, err := repo.Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
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

// Every JSON output gittree judges trunk by says how trunk was found, and a
// trunk no file names says so: config for runFixture's MAIN_BRANCH.
func TestJSONOutputsSayHowTrunkWasFound(t *testing.T) {
	ctx, _ := runFixture(t, false)
	gitIn(t, ctx.Repo.MainRoot, "branch", "release-2.1")
	for _, want := range []config.TrunkSource{config.TrunkFromConfig, config.TrunkConventional} {
		ctx.Config.TrunkSource = want
		got := map[string]*string{}
		got["status"] = planOfWork(t, ctx, "bump").TrunkSource
		up, err := upJSON(t, ctx, "bump", noAgents())
		if err != nil {
			t.Fatal(err)
		}
		got["up"] = up.TrunkSource
		got["sync"] = overviewJSON(t, ctx).TrunkSource
		sweep, _ := sweepPlanJSON(t, ctx, SweepOptions{NoFetch: true})
		got["sweep-plan"] = sweep.TrunkSource
		got["new-plan"] = newPlanOf(t, ctx, "fix/x", NewOptions{}).TrunkSource
		got["checkout-plan"] = checkoutPlanOf(t, ctx, "release-2.1", "").TrunkSource
		for name, src := range got {
			if src == nil || *src != string(want) {
				t.Errorf("%s: trunkSource = %q, want %s", name, deref(src), want)
			}
		}
	}
}

// wt sync on a repository nothing names trunk for says so, rather than fetch
// and compare against an "origin/" with no branch.
func TestSyncSaysNothingNamesTrunk(t *testing.T) {
	ctx := OpenLenient(noTrunkRepo(t), &bytes.Buffer{})
	for _, noFetch := range []bool{true, false} {
		o := overviewOf(ctx, SyncOptions{NoFetch: noFetch})
		if o.Error == nil || !strings.Contains(*o.Error, "cannot tell which branch is trunk") ||
			o.Trunk != nil || o.TrunkRef != nil || o.Fetched || o.FetchError != nil {
			t.Errorf("no-fetch %v: overview %+v, want the detection error and no trunk", noFetch, o)
		}
		var buf bytes.Buffer
		if err := Sync(ctx, SyncOptions{NoFetch: noFetch}, &buf); err == nil ||
			!strings.Contains(err.Error(), "cannot tell which branch is trunk") {
			t.Errorf("no-fetch %v: Sync = %v, want the detection error", noFetch, err)
		}
	}
}

// wt status --json names no trunk ref when there is no trunk.
func TestStatusPlanHasNoTrunkRefWhenNothingNamesTrunk(t *testing.T) {
	main := noTrunkRepo(t)
	for name, ctx := range map[string]*Context{
		"no file":        OpenLenient(main, &bytes.Buffer{}),
		"no MAIN_BRANCH": OpenLenient(committedNoTrunkRepo(t), &bytes.Buffer{}),
	} {
		var buf bytes.Buffer
		if err := UpPlanJSON(ctx, ".", &buf); err != nil {
			t.Fatal(err)
		}
		var p UpPlan
		if err := json.Unmarshal(buf.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if p.Trunk != nil || p.TrunkRef != nil || p.TrunkSource != nil || p.TrunkRefExists ||
			deref(p.UpIneligibleCode) != IneligibleNoConfig {
			t.Errorf("%s: trunk %q ref %q source %q exists %v code %q", name, deref(p.Trunk),
				deref(p.TrunkRef), deref(p.TrunkSource), p.TrunkRefExists, deref(p.UpIneligibleCode))
		}
	}
}

// committedNoTrunkRepo has a config file without MAIN_BRANCH, and nothing
// else names trunk.
func committedNoTrunkRepo(t *testing.T) string {
	t.Helper()
	main := committedRepo(t, "BUILD_INIT_ENABLED=false\n")
	gitIn(t, main, "branch", "-m", "main", "production")
	return main
}
