package repo

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// trunkOf detects the trunk of the repository at dir.
func trunkOf(t *testing.T, dir string) (string, config.TrunkSource, error) {
	t.Helper()
	r, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r.DetectTrunk()
}

func wantTrunk(t *testing.T, dir, want string, source config.TrunkSource) {
	t.Helper()
	got, src, err := trunkOf(t, dir)
	if err != nil || got != want || src != source {
		t.Errorf("DetectTrunk = %q, %s, %v; want %q, %s", got, src, err, want, source)
	}
}

func wantNoTrunk(t *testing.T, dir string) {
	t.Helper()
	got, src, err := trunkOf(t, dir)
	if !errors.Is(err, ErrNoTrunk) || got != "" || src != "" {
		t.Errorf("DetectTrunk = %q, %q, %v; want ErrNoTrunk", got, src, err)
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

// origin/HEAD outranks the conventional names, even a name no list holds.
func TestDetectTrunkTakesOriginHeadOverMain(t *testing.T) {
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	run(t, dir, "branch", "residential_development")
	withOriginNoHead(t, dir)
	run(t, dir, "remote", "set-head", "origin", "residential_development")
	wantTrunk(t, dir, "residential_development", config.TrunkFromOriginHead)
}

// A clone that never recorded origin/HEAD, its main checkout on a feature
// branch: trunk is main, not the feature.
func TestDetectTrunkIgnoresTheCheckedOutFeatureBranch(t *testing.T) {
	main := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	withOriginNoHead(t, main)
	run(t, main, "checkout", "-q", "-b", "feat/stage-6")
	if _, ok := mustDiscover(t, main).OriginHead(); ok {
		t.Fatal("fixture has origin/HEAD set")
	}
	wantTrunk(t, main, "main", config.TrunkConventional)
}

// development, main and master all there: development, first in the list.
func TestDetectTrunkPrefersDevelopmentThenMainThenMaster(t *testing.T) {
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	run(t, dir, "branch", "development")
	run(t, dir, "branch", "master")
	withOriginNoHead(t, dir)
	run(t, dir, "checkout", "-q", "-b", "feat/x")
	wantTrunk(t, dir, "development", config.TrunkConventional)

	run(t, dir, "branch", "-D", "development")
	run(t, dir, "update-ref", "-d", "refs/remotes/origin/development")
	wantTrunk(t, dir, "main", config.TrunkConventional)

	run(t, dir, "branch", "-D", "main")
	run(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
	wantTrunk(t, dir, "master", config.TrunkConventional)
}

// The list's order decides, not where a name exists: development only here
// beats main here and on origin, and development only on origin does too.
func TestDetectTrunkOrderOutranksWhereANameExists(t *testing.T) {
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	withOriginNoHead(t, dir)
	run(t, dir, "checkout", "-q", "-b", "feat/x")
	run(t, dir, "branch", "development")
	wantTrunk(t, dir, "development", config.TrunkConventional)

	run(t, dir, "branch", "-D", "development")
	run(t, dir, "update-ref", "refs/remotes/origin/development", "main")
	wantTrunk(t, dir, "development", config.TrunkConventional)
}

// trunk and develop are no trunk names wt knows.
func TestDetectTrunkIgnoresTrunkAndDevelop(t *testing.T) {
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	run(t, dir, "branch", "-m", "main", "trunk")
	run(t, dir, "branch", "develop")
	wantNoTrunk(t, dir)
}

// Nothing names trunk: detection fails rather than take the checked-out
// branch, which may be a feature branch.
func TestDetectTrunkFailsRatherThanGuess(t *testing.T) {
	dir := gittest.NewRepo(t, resolved(t, t.TempDir()), "demo")
	run(t, dir, "branch", "-m", "main", "production")
	wantNoTrunk(t, dir)
	if !strings.Contains(ErrNoTrunk.Error(), "wt init") || !strings.Contains(ErrNoTrunk.Error(), "MAIN_BRANCH") {
		t.Errorf("ErrNoTrunk names no way out: %v", ErrNoTrunk)
	}
}

// An unborn branch has no ref, so no conventional name exists yet.
func TestDetectTrunkOfAnUnbornRepoFails(t *testing.T) {
	parent := resolved(t, t.TempDir())
	run(t, parent, "init", "-q", "-b", "main", "fresh")
	wantNoTrunk(t, filepath.Join(parent, "fresh"))
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
