package commands

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// cacheFixture is a repository declaring two cache dirs, both present in the
// source checkout, and an empty worktree to provision.
func cacheFixture(t *testing.T) (ctx *kit.Context, main, target string) {
	t.Helper()
	main = testkit.FixtureRepo(t, testkit.MinimalConf+"WORKTREE_CACHE_DIRS=(node_modules web/.gradle)\n")
	testkit.MustWrite(t, filepath.Join(main, "node_modules", "left-pad", "index.js"), "module.exports = 1")
	testkit.MustWrite(t, filepath.Join(main, "web", ".gradle", "cache.bin"), "gradle")
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, main, t.TempDir()
}

// withCloner stands in for the filesystem's clone for one test.
func withCloner(t *testing.T, f func(from, to string) error) {
	t.Helper()
	saved := cloneDir
	cloneDir = f
	t.Cleanup(func() { cloneDir = saved })
}

// The real clone, on the temp directory's filesystem: APFS on a Mac, where
// clonefile shares the blocks. A filesystem that cannot clone skips the test
// rather than failing it; the skip path has its own test below.
func TestSetupClonesCacheDirsCopyOnWrite(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	var buf bytes.Buffer
	result, reason := cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf)
	if strings.Contains(buf.String(), "not cloned") {
		if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
			t.Errorf("a clone that could not be made left %v (%v)", entries, err)
		}
		t.Skipf("this filesystem cannot clone: %s", buf.String())
	}
	if result != kit.StepDone || reason != "" {
		t.Fatalf("result %s %q\n%s", result, reason, buf.String())
	}
	if got := testkit.MustRead(t, filepath.Join(target, "node_modules", "left-pad", "index.js")); got != "module.exports = 1" {
		t.Errorf("node_modules not cloned: %q", got)
	}
	if got := testkit.MustRead(t, filepath.Join(target, "web", ".gradle", "cache.bin")); got != "gradle" {
		t.Errorf("a nested cache dir not cloned: %q", got)
	}
	// A clone, not a link: writing the worktree's copy leaves the source's.
	testkit.MustWrite(t, filepath.Join(target, "node_modules", "left-pad", "index.js"), "changed")
	if got := testkit.MustRead(t, filepath.Join(main, "node_modules", "left-pad", "index.js")); got != "module.exports = 1" {
		t.Errorf("the source changed with the clone: %q", got)
	}
	for _, d := range []string{"node_modules", "web/.gradle"} {
		if !strings.Contains(buf.String(), "✓ cloned "+d) {
			t.Errorf("no line for %s:\n%s", d, buf.String())
		}
	}
}

// Where the filesystem cannot clone, the cache is skipped with a line saying
// why, and nothing is copied in its place.
func TestSetupSkipsCachesTheFilesystemCannotClone(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	withCloner(t, func(_, _ string) error { return errCloneUnsupported{"the source is on another volume"} })
	var buf bytes.Buffer
	result, reason := cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf)
	if result != kit.StepSkipped || !strings.Contains(reason, "node_modules: the source is on another volume") {
		t.Errorf("result %s %q", result, reason)
	}
	if !strings.Contains(buf.String(), " - node_modules not cloned: the source is on another volume; skipped rather than copied\n") {
		t.Errorf("no line saying why:\n%s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(target, "node_modules")); !os.IsNotExist(err) {
		t.Errorf("node_modules is in the worktree although it could not be cloned: %v", err)
	}
}

// One cache cloned and one not is done, with the one not cloned named.
func TestSetupCacheStepNamesWhatItCouldNotClone(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	withCloner(t, func(from, to string) error {
		if strings.HasSuffix(from, ".gradle") {
			return errCloneUnsupported{"this filesystem cannot clone"}
		}
		return os.Mkdir(to, 0o755)
	})
	var buf bytes.Buffer
	result, reason := cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf)
	if result != kit.StepDone || reason != "web/.gradle: this filesystem cannot clone" {
		t.Errorf("result %s %q", result, reason)
	}
}

// A clone that fails for any other reason is a failed step, not a skip.
func TestSetupCacheCloneErrorFails(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	withCloner(t, func(_, _ string) error { return errors.New("permission denied") })
	var buf bytes.Buffer
	result, reason := cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf)
	if result != kit.StepFailed || !strings.Contains(reason, "node_modules: permission denied") {
		t.Errorf("result %s %q", result, reason)
	}
	if !strings.Contains(buf.String(), " ! failed to clone node_modules: permission denied") {
		t.Errorf("no failure line:\n%s", buf.String())
	}
}

// A cache already in the worktree is the worktree's own, and is kept.
func TestSetupKeepsAnExistingCacheDir(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	testkit.MustWrite(t, filepath.Join(target, "node_modules", "mine.txt"), "keep")
	cloned := map[string]bool{}
	withCloner(t, func(from, to string) error {
		cloned[filepath.Base(from)] = true
		return os.Mkdir(to, 0o755)
	})
	var buf bytes.Buffer
	if result, reason := cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf); result != kit.StepDone {
		t.Errorf("result %s %q", result, reason)
	}
	if cloned["node_modules"] || !cloned[".gradle"] {
		t.Errorf("cloned %v; want .gradle alone", cloned)
	}
	if got := testkit.MustRead(t, filepath.Join(target, "node_modules", "mine.txt")); got != "keep" {
		t.Errorf("the existing cache was touched: %q", got)
	}
	if _, err := os.Stat(filepath.Join(target, "node_modules", "left-pad")); !os.IsNotExist(err) {
		t.Error("the source's cache was merged into the existing one")
	}
	if !strings.Contains(buf.String(), " - node_modules already exists, skipping") {
		t.Errorf("no line for the kept cache:\n%s", buf.String())
	}
}

func TestSetupNoCacheClonesNothing(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	withCloner(t, func(_, _ string) error {
		t.Error("cloned under --no-cache")
		return nil
	})
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{SourceDir: main, NoCache: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "build caches skipped (--no-cache)") {
		t.Errorf("no line for --no-cache:\n%s", buf.String())
	}
}

// A repository that declares no caches hears nothing about them.
func TestSetupWithoutCacheDirsSaysNothing(t *testing.T) {
	main := testkit.FixtureRepo(t, testkit.MinimalConf)
	ctx, _ := kit.Open(main)
	var buf bytes.Buffer
	result, reason := cloneCacheDirs(ctx, main, t.TempDir(), SetupOptions{NoCache: true}, &buf)
	if result != kit.StepSkipped || reason != "no WORKTREE_CACHE_DIRS" || buf.Len() != 0 {
		t.Errorf("result %s %q, printed %q", result, reason, buf.String())
	}
}

func TestSetupRefusesCacheDirsThatEscapeTheWorktree(t *testing.T) {
	main := testkit.FixtureRepo(t, testkit.MinimalConf+"WORKTREE_CACHE_DIRS=(../outside)\n")
	testkit.MustMkdir(t, filepath.Join(filepath.Dir(main), "outside"))
	ctx, _ := kit.Open(main)
	withCloner(t, func(_, _ string) error {
		t.Error("cloned an entry outside the worktree")
		return nil
	})
	var buf bytes.Buffer
	result, _ := cloneCacheDirs(ctx, main, t.TempDir(), SetupOptions{}, &buf)
	if result != kit.StepFailed || !strings.Contains(buf.String(), "escapes the worktree") {
		t.Errorf("result %s\n%s", result, buf.String())
	}
}

// The caches are there before provision.sh runs, so it finds them warm.
func TestSetupClonesCachesBeforeProvision(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	withCloner(t, func(_, to string) error { return os.Mkdir(to, 0o755) })
	p := filepath.Join(main, "bin", "worktree", "provision.sh")
	testkit.MustWrite(t, p, "#!/bin/sh\ntest -d node_modules && touch warm.txt\n")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{SourceDir: main}, &buf); err != nil {
		t.Fatalf("Setup: %v\n%s", err, buf.String())
	}
	if _, err := os.Stat(filepath.Join(target, "warm.txt")); err != nil {
		t.Errorf("provision.sh did not find node_modules:\n%s", buf.String())
	}
}

// An entry under a symlinked directory that leaves the worktree is refused
// before anything is made or cloned there.
func TestSetupRefusesCacheDirsBehindAnEscapingSymlink(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(target, "web")); err != nil {
		t.Fatal(err)
	}
	cloned := map[string]bool{}
	withCloner(t, func(from, to string) error {
		cloned[filepath.Base(from)] = true
		return os.Mkdir(to, 0o755)
	})
	var buf bytes.Buffer
	result, reason := cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf)
	if result != kit.StepFailed || !strings.Contains(reason, "web/.gradle escapes the worktree") {
		t.Errorf("result %s %q\n%s", result, reason, buf.String())
	}
	if cloned[".gradle"] || !cloned["node_modules"] {
		t.Errorf("cloned %v; want node_modules alone", cloned)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("something was made outside the worktree: %v", entries)
	}
}

// A symlinked parent that stays inside the worktree is fine.
func TestSetupClonesBehindASymlinkWithinTheWorktree(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	testkit.MustMkdir(t, filepath.Join(target, "real"))
	if err := os.Symlink("real", filepath.Join(target, "web")); err != nil {
		t.Fatal(err)
	}
	withCloner(t, func(_, to string) error { return os.Mkdir(to, 0o755) })
	var buf bytes.Buffer
	if result, reason := cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf); result != kit.StepDone {
		t.Errorf("result %s %q\n%s", result, reason, buf.String())
	}
	if _, err := os.Stat(filepath.Join(target, "real", ".gradle")); err != nil {
		t.Error(err)
	}
}

func TestSetupCacheWithNoSourceCheckoutIsSkipped(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	gone := filepath.Join(main, "no-such-checkout")
	var buf bytes.Buffer
	result, reason := cloneCacheDirs(ctx, gone, target, SetupOptions{}, &buf)
	if result != kit.StepSkipped || reason != "source "+gone+" not found" {
		t.Errorf("result %s %q", result, reason)
	}
}

// A source entry that is a symlink, to a directory or not, is never followed
// or cloned, and says it is a symlink. A file is not a directory.
func TestSetupCacheSourceEntryThatIsNotADirectory(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	shared := t.TempDir()
	testkit.MustWrite(t, filepath.Join(shared, "cache.bin"), "shared")
	if err := os.RemoveAll(filepath.Join(main, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(main, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(main, "web", ".gradle")); err != nil {
		t.Fatal(err)
	}
	testkit.MustWrite(t, filepath.Join(main, "web", ".gradle"), "a file")
	withCloner(t, func(_, _ string) error {
		t.Error("cloned something that is not a directory")
		return nil
	})
	var buf bytes.Buffer
	result, reason := cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf)
	if result != kit.StepDone || reason != "" {
		t.Errorf("result %s %q", result, reason)
	}
	for _, want := range []string{
		" - node_modules is a symlink in the source, not followed, skipping\n",
		" - web/.gradle is not a directory in the source, skipping\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("no line %q:\n%s", want, buf.String())
		}
	}
}

// A lookup that fails for a reason other than absence fails the step.
func TestSetupCacheSourceLookupErrorFails(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	web := filepath.Join(main, "web")
	if err := os.Chmod(web, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(web, 0o755) })
	if _, err := os.Lstat(filepath.Join(web, ".gradle")); err == nil {
		t.Skip("directory permissions are not enforced here")
	}
	withCloner(t, func(_, to string) error { return os.Mkdir(to, 0o755) })
	var buf bytes.Buffer
	result, reason := cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf)
	if result != kit.StepFailed || !strings.Contains(reason, "web/.gradle") {
		t.Errorf("result %s %q\n%s", result, reason, buf.String())
	}
	if strings.Contains(buf.String(), "not in source") {
		t.Errorf("an unreadable source reported as absent:\n%s", buf.String())
	}
}

// A failure and a cache the filesystem cannot clone in one run: failed, with
// both named.
func TestSetupCacheFailureAndUnclonableTogether(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	withCloner(t, func(from, _ string) error {
		if strings.HasSuffix(from, ".gradle") {
			return errCloneUnsupported{"this filesystem cannot clone"}
		}
		return errors.New("disk on fire")
	})
	var buf bytes.Buffer
	result, reason := cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf)
	want := "node_modules: disk on fire; web/.gradle: this filesystem cannot clone"
	if result != kit.StepFailed || reason != want {
		t.Errorf("result %s %q; want %q", result, reason, want)
	}
}

// Every entry already in the worktree, or none in the source, is done with
// nothing to say.
func TestSetupCacheNothingToCloneIsDone(t *testing.T) {
	ctx, main, target := cacheFixture(t)
	withCloner(t, func(_, _ string) error {
		t.Error("cloned with nothing to clone")
		return nil
	})
	testkit.MustMkdir(t, filepath.Join(target, "node_modules"))
	testkit.MustMkdir(t, filepath.Join(target, "web", ".gradle"))
	var buf bytes.Buffer
	if result, reason := cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf); result != kit.StepDone || reason != "" {
		t.Errorf("all present: %s %q", result, reason)
	}
	if err := os.RemoveAll(filepath.Join(main, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(main, "web")); err != nil {
		t.Fatal(err)
	}
	if result, reason := cloneCacheDirs(ctx, main, t.TempDir(), SetupOptions{}, &buf); result != kit.StepDone || reason != "" {
		t.Errorf("all missing: %s %q", result, reason)
	}
}

// Parent directories made for a cache that is not cloned do not stay behind.
func TestSetupCacheLeavesNoParentsWhenNotCloned(t *testing.T) {
	for name, err := range map[string]error{
		"unsupported": errCloneUnsupported{"this filesystem cannot clone"},
		"failed":      errors.New("disk on fire"),
	} {
		t.Run(name, func(t *testing.T) {
			ctx, main, target := cacheFixture(t)
			withCloner(t, func(_, _ string) error { return err })
			var buf bytes.Buffer
			cloneCacheDirs(ctx, main, target, SetupOptions{}, &buf)
			if _, statErr := os.Lstat(filepath.Join(target, "web")); !os.IsNotExist(statErr) {
				t.Errorf("web/ was left behind: %v", statErr)
			}
		})
	}
}
