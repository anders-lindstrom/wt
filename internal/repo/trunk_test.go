package repo

import (
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// trunkOf detects the trunk of the repository at dir.
func trunkOf(t *testing.T, dir string) (string, config.TrunkSource) {
	t.Helper()
	r, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r.DetectTrunk()
}

func wantTrunk(t *testing.T, dir, want string, source config.TrunkSource) {
	t.Helper()
	if got, src := trunkOf(t, dir); got != want || src != source {
		t.Errorf("DetectTrunk = %q, %s; want %q, %s", got, src, want, source)
	}
}

// origin/HEAD, when set, names trunk over every branch the repository has.
func TestDetectTrunkPrefersOriginHead(t *testing.T) {
	main := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	run(t, main, "branch", "development")
	withOriginNoHead(t, main)
	run(t, main, "remote", "set-head", "origin", "development")
	run(t, main, "checkout", "-q", "-b", "feat/x")
	wantTrunk(t, main, "development", config.TrunkFromOriginHead)
}

// A clone that never recorded origin/HEAD, its main checkout on a feature
// branch: trunk is main, which exists here and on origin, not the feature.
func TestDetectTrunkIgnoresTheCheckedOutFeatureBranch(t *testing.T) {
	main := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	withOriginNoHead(t, main)
	run(t, main, "checkout", "-q", "-b", "feat/stage-6")
	if _, ok := mustDiscover(t, main).OriginHead(); ok {
		t.Fatal("fixture has origin/HEAD set")
	}
	wantTrunk(t, main, "main", config.TrunkConventional)
}

// A conventional name both here and on origin beats one earlier in the list
// that is only local.
func TestDetectTrunkPrefersAConventionalBranchOriginHas(t *testing.T) {
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	run(t, dir, "branch", "-m", "main", "master")
	withOriginNoHead(t, dir)
	run(t, dir, "branch", "main")
	run(t, dir, "checkout", "-q", "-b", "feat/x")
	wantTrunk(t, dir, "master", config.TrunkConventional)
}

// Several conventional names on origin: the first in the list wins.
func TestDetectTrunkTakesTheFirstConventionalName(t *testing.T) {
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	run(t, dir, "branch", "develop")
	run(t, dir, "branch", "trunk")
	withOriginNoHead(t, dir)
	run(t, dir, "checkout", "-q", "-b", "feat/x")
	wantTrunk(t, dir, "main", config.TrunkConventional)
}

// The tiers outrank the list's order: a name both here and on origin beats
// one earlier in the list that only origin has, which beats one earlier still
// that is only local.
func TestDetectTrunkRanksTiersBeforeListOrder(t *testing.T) {
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	run(t, dir, "branch", "-m", "main", "trunk")
	run(t, dir, "branch", "master")
	withOriginNoHead(t, dir)
	run(t, dir, "branch", "-D", "master")
	run(t, dir, "branch", "main")
	run(t, dir, "checkout", "-q", "-b", "feat/x")
	wantTrunk(t, dir, "trunk", config.TrunkConventional)

	run(t, dir, "branch", "-D", "trunk")
	run(t, dir, "update-ref", "-d", "refs/remotes/origin/trunk")
	wantTrunk(t, dir, "master", config.TrunkConventional)
}

// A conventional name only origin has still beats the checked-out branch.
func TestDetectTrunkTakesAConventionalNameOnlyOriginHas(t *testing.T) {
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	run(t, dir, "branch", "-m", "main", "development")
	withOriginNoHead(t, dir)
	run(t, dir, "checkout", "-q", "-b", "feat/x")
	run(t, dir, "branch", "-D", "development")
	wantTrunk(t, dir, "development", config.TrunkConventional)
}

// A local-only conventional name, with no remote at all, is still trunk.
func TestDetectTrunkTakesALocalConventionalName(t *testing.T) {
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	run(t, dir, "branch", "-m", "main", "develop")
	run(t, dir, "checkout", "-q", "-b", "feat/x")
	wantTrunk(t, dir, "develop", config.TrunkConventional)
}

// Nothing else to go on: the checked-out branch, said to be a guess.
func TestDetectTrunkGuessesTheCheckedOutBranchLast(t *testing.T) {
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	run(t, dir, "branch", "-m", "main", "production")
	wantTrunk(t, dir, "production", config.TrunkCurrentBranchGuess)
}

// An unborn branch has no ref, so no conventional name exists yet.
func TestDetectTrunkOfAnUnbornRepoIsAGuess(t *testing.T) {
	parent := resolved(t, t.TempDir())
	run(t, parent, "init", "-q", "-b", "trunk", "fresh")
	wantTrunk(t, filepath.Join(parent, "fresh"), "trunk", config.TrunkCurrentBranchGuess)
}

// withOriginNoHead is gittest.WithOrigin without refs/remotes/origin/HEAD:
// a clone made before git recorded it, or a remote added and fetched by hand
// with a git older than 2.48.
func withOriginNoHead(t *testing.T, dir string) {
	t.Helper()
	gittest.WithOrigin(t, dir)
	run(t, dir, "remote", "set-head", "origin", "--delete")
}

func mustDiscover(t *testing.T, dir string) *Repo {
	t.Helper()
	r, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
