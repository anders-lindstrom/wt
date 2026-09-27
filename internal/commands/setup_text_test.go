package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// provisionFixture is a repository whose provisioning can be made to fail
// step by step: a provision.sh exiting with provisionExit, a build command,
// and, with badSubmodule, a submodule whose URL does not exist.
func provisionFixture(t *testing.T, provisionExit int, build string, badSubmodule bool) *Context {
	t.Helper()
	conf := "MAIN_BRANCH=\"main\"\nDEVELOPER_CONFIG_DIRS=\"\"\nDEVELOPER_CONFIG_FILES=\".env\"\n"
	if build == "" {
		conf += "BUILD_INIT_ENABLED=false\n"
	} else {
		conf += "BUILD_INIT_COMMAND=\"" + build + "\"\n"
	}
	main := committedRepo(t, conf)
	if provisionExit >= 0 {
		p := filepath.Join(main, "bin", "worktree", "provision.sh")
		mustWrite(t, p, "#!/bin/sh\necho 'provisioning'\nexit "+strconv.Itoa(provisionExit)+"\n")
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if badSubmodule {
		mustWrite(t, filepath.Join(main, ".gitmodules"),
			"[submodule \"sub\"]\n\tpath = sub\n\turl = "+filepath.Join(t.TempDir(), "missing.git")+"\n")
		head := gitOut(t, main, "rev-parse", "HEAD")
		gitIn(t, main, "update-index", "--add", "--cacheinfo", "160000,"+strings.TrimSpace(head)+",sub")
	}
	mustWrite(t, filepath.Join(main, ".env"), "SECRET=1\n")
	gitIn(t, main, "add", "bin")
	if badSubmodule {
		gitIn(t, main, "add", ".gitmodules")
	}
	gitIn(t, main, "commit", "-qm", "provisioning")
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

// git's own error is several lines, and names temporary paths.
var submoduleErr = regexp.MustCompile(`(?s)(failed to initialise submodules: ).*?\n(Running: )`)

// Setup's text is what a person reads from wt setup, wt adopt, the create
// hook and wt new: reporting the steps for --json must not change a byte of
// it, whichever steps fail.
func TestSetupTextIsUnchangedByStepReporting(t *testing.T) {
	for name, tc := range map[string]struct {
		provisionExit int
		build         string
		badSubmodule  bool
		opts          SetupOptions
		want          string
	}{
		"everything fails": {1, "exit 3", true, SetupOptions{}, "" +
			" ✓ copied .env\n" +
			"Running bin/worktree/provision.sh...\n" +
			"provisioning\n" +
			"Initializing git submodules...\n" +
			" ! Warning: failed to initialise submodules: <err>\n" +
			"Running: exit 3\n" +
			" ! Warning: build initialisation failed: exit status 3\n" +
			"   Try running it by hand: exit 3\n" +
			"\n" +
			"! provision.sh failed: exit status 1\n" +
			"  The worktree is otherwise set up. Fix the cause and finish it with:\n" +
			"      cd <path> && wt setup\n"},
		"everything works": {0, "true", false, SetupOptions{Source: "superset"}, "" +
			"Setup run by superset\n" +
			" ✓ copied .env\n" +
			"Running bin/worktree/provision.sh...\n" +
			"provisioning\n" +
			"✓ provision.sh complete\n" +
			"Running: true\n" +
			"✓ build dependencies downloaded\n" +
			"✓ Worktree setup complete\n" +
			"  <path>\n"},
		"build skipped": {-1, "true", false, SetupOptions{SkipBuild: true}, "" +
			" ✓ copied .env\n" +
			"⏭ build initialisation skipped (--no-build)\n" +
			"✓ Worktree setup complete\n" +
			"  <path>\n"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := provisionFixture(t, tc.provisionExit, tc.build, tc.badSubmodule)
			path, err := New(ctx, "fix/golden", NewOptions{NoSetup: true}, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			_ = Setup(ctx, path, tc.opts, &buf)
			got := strings.ReplaceAll(buf.String(), path, "<path>")
			got = submoduleErr.ReplaceAllString(got, "${1}<err>\n${2}")
			if got != tc.want {
				t.Errorf("Setup printed\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}
