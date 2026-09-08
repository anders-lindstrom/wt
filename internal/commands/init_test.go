package commands

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// bareRepo is a repository with no worktree configuration at all — the state
// `wt init` exists to get you out of, and the one fixtureRepo cannot produce.
func bareRepo(t *testing.T) *repo.Repo {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q", "-b", "main", "demo")
	cmd.Dir = parent
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	r, err := repo.Discover(filepath.Join(parent, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInitMakesAConfiglessRepoOpenable(t *testing.T) {
	r := bareRepo(t)
	if _, err := Open(r.Root); err == nil {
		t.Fatal("fixture already has a configuration; nothing to init")
	}

	var buf bytes.Buffer
	if err := Init(r, InitOptions{}, &buf); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ctx, err := Open(r.Root)
	if err != nil {
		t.Fatalf("Open after Init: %v", err)
	}
	if ctx.Config.MainBranch != "main" {
		t.Errorf("MAIN_BRANCH = %q, want detected %q", ctx.Config.MainBranch, "main")
	}
	if _, err := os.Stat(filepath.Join(r.Root, "bin", "worktree", "worktree.conf")); err != nil {
		t.Errorf("worktree.conf not written: %v", err)
	}
}

func TestInitWritesTheAnsweredValues(t *testing.T) {
	r := bareRepo(t)
	ask := func(Answers) (Answers, error) {
		return Answers{MainBranch: "trunk", BranchPrefix: "chore_wt", BuildCommand: "make build"}, nil
	}

	var buf bytes.Buffer
	if err := Init(r, InitOptions{Ask: ask}, &buf); err != nil {
		t.Fatalf("Init: %v", err)
	}

	ctx, err := Open(r.Root)
	if err != nil {
		t.Fatalf("Open after Init: %v", err)
	}
	if ctx.Config.MainBranch != "trunk" {
		t.Errorf("MainBranch = %q, want %q", ctx.Config.MainBranch, "trunk")
	}
	if ctx.Config.BranchPrefix != "chore_wt" {
		t.Errorf("BranchPrefix = %q, want %q", ctx.Config.BranchPrefix, "chore_wt")
	}
	if ctx.Config.DefaultType != "chore" {
		t.Errorf("DefaultType = %q, want %q", ctx.Config.DefaultType, "chore")
	}
	if ctx.Config.BuildInitCommand != "make build" {
		t.Errorf("BuildInitCommand = %q, want %q", ctx.Config.BuildInitCommand, "make build")
	}
	if !ctx.Config.BuildInitEnabled {
		t.Error("BuildInitEnabled = false, want true when a build command was given")
	}
}

func TestInitOffersDetectedValuesAsTheDefaultsToAnswer(t *testing.T) {
	r := bareRepo(t)
	var offered Answers
	ask := func(defaults Answers) (Answers, error) {
		offered = defaults
		return defaults, nil
	}

	var buf bytes.Buffer
	if err := Init(r, InitOptions{Ask: ask}, &buf); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if offered.MainBranch != "main" {
		t.Errorf("offered MainBranch = %q, want detected %q", offered.MainBranch, "main")
	}
	if offered.BranchPrefix != "feat_wt" {
		t.Errorf("offered BranchPrefix = %q, want %q", offered.BranchPrefix, "feat_wt")
	}
	if offered.BuildCommand != "" {
		t.Errorf("offered BuildCommand = %q, want empty", offered.BuildCommand)
	}
}

// confPath is where Init writes, and where the tests below read back from.
func confPath(r *repo.Repo) string {
	return filepath.Join(r.Root, "bin", "worktree", "worktree.conf")
}

func TestInitCommentsEveryRemainingKeyAtItsDefault(t *testing.T) {
	r := bareRepo(t)
	var buf bytes.Buffer
	if err := Init(r, InitOptions{}, &buf); err != nil {
		t.Fatalf("Init: %v", err)
	}
	body := string(mustReadFile(t, confPath(r)))

	for _, key := range []string{
		"WORKTREE_TYPE_SUFFIX", "WORKTREE_DEFAULT_TYPE", "WORKTREE_TYPES",
		"DEVELOPER_CONFIG_DIRS", "DEVELOPER_CONFIG_FILES", "BUILD_INIT_ENABLED",
		"BUILD_INIT_COMMAND", "REQUIRED_BINS", "TEST_COMMAND", "RUN_TESTS_BEFORE_REMOVE",
	} {
		if !strings.Contains(body, "# "+key+"=") {
			t.Errorf("key %s is not written as a commented default", key)
		}
	}
}

// The commented lines are the repository's reference for what it may set, so
// they have to be assignments that actually work — not prose that looks like
// one. Uncommenting the whole file must parse, and must not change a thing.
func TestInitCommentedDefaultsParseAndChangeNothingWhenUncommented(t *testing.T) {
	r := bareRepo(t)
	var buf bytes.Buffer
	if err := Init(r, InitOptions{}, &buf); err != nil {
		t.Fatalf("Init: %v", err)
	}
	before := resolvedConfig(t, r)

	assignment := regexp.MustCompile(`^# ([A-Z_]+=)`)
	var lines []string
	for _, line := range strings.Split(string(mustReadFile(t, confPath(r))), "\n") {
		lines = append(lines, assignment.ReplaceAllString(line, "$1"))
	}
	if err := os.WriteFile(confPath(r), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	if after := resolvedConfig(t, r); after != before {
		t.Errorf("uncommenting the defaults changed the configuration:\n before %s\n after  %s", before, after)
	}
}

// resolvedConfig is the configuration as the repo observes it, via the same
// rendering `wt config --shell` gives the compat layer. Comparing that rather
// than the struct keeps an explicitly empty array and an absent key equal,
// which they are in every way that matters here.
func resolvedConfig(t *testing.T, r *repo.Repo) string {
	t.Helper()
	ctx, err := Open(r.Root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var buf bytes.Buffer
	if err := Config(ctx, true, &buf); err != nil {
		t.Fatalf("Config: %v", err)
	}
	return buf.String()
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInitReAsksWhenAnAnswerWouldNotValidate(t *testing.T) {
	r := bareRepo(t)
	// "wip_wt" yields default type "wip", which is not in WORKTREE_TYPES.
	answers := []Answers{
		{MainBranch: "main", BranchPrefix: "wip_wt"},
		{MainBranch: "main", BranchPrefix: "chore_wt"},
	}
	asked := 0
	ask := func(Answers) (Answers, error) {
		a := answers[asked]
		asked++
		return a, nil
	}

	var buf bytes.Buffer
	if err := Init(r, InitOptions{Ask: ask}, &buf); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if asked != 2 {
		t.Errorf("asked %d times, want 2 — the bad answer should be re-asked", asked)
	}
	if !strings.Contains(buf.String(), "wip") || !strings.Contains(buf.String(), "WORKTREE_TYPES") {
		t.Errorf("the rejection does not say what was wrong:\n%s", buf.String())
	}
	ctx, err := Open(r.Root)
	if err != nil {
		t.Fatalf("Open after Init: %v", err)
	}
	if ctx.Config.BranchPrefix != "chore_wt" {
		t.Errorf("BranchPrefix = %q, want the corrected %q", ctx.Config.BranchPrefix, "chore_wt")
	}
}

// An Ask that cannot make progress must not spin: a piped or scripted answer
// repeats rather than corrects, and a hung `wt init` is worse than a refusal.
func TestInitGivesUpWhenReAskingChangesNothing(t *testing.T) {
	r := bareRepo(t)
	asked := 0
	ask := func(Answers) (Answers, error) {
		asked++
		return Answers{MainBranch: "main", BranchPrefix: "wip_wt"}, nil
	}

	var buf bytes.Buffer
	err := Init(r, InitOptions{Ask: ask}, &buf)
	if err == nil {
		t.Fatal("Init accepted a prefix whose default type is not a known type")
	}
	if !strings.Contains(err.Error(), "WORKTREE_TYPES") {
		t.Errorf("error does not explain the problem: %v", err)
	}
	if asked > 2 {
		t.Errorf("asked %d times without the answer changing; want it to stop at 2", asked)
	}
	if _, statErr := os.Stat(confPath(r)); !os.IsNotExist(statErr) {
		t.Error("a configuration was written despite the answers being invalid")
	}
}

// Ctrl-D at the prompt aborts. The half-answered state must not reach disk.
func TestInitWritesNothingWhenTheAnswersAreAbandoned(t *testing.T) {
	r := bareRepo(t)
	ask := func(Answers) (Answers, error) {
		return Answers{}, io.EOF
	}

	var buf bytes.Buffer
	if err := Init(r, InitOptions{Ask: ask}, &buf); !errors.Is(err, io.EOF) {
		t.Fatalf("Init error = %v, want io.EOF passed through", err)
	}
	if _, err := os.Stat(confPath(r)); !os.IsNotExist(err) {
		t.Error("a configuration was written despite the prompt being abandoned")
	}
}

func TestInitRefusesToOverwriteAnExistingConfig(t *testing.T) {
	for _, name := range []string{"worktree.conf", "worktree.toml"} {
		t.Run(name, func(t *testing.T) {
			r := bareRepo(t)
			existing := filepath.Join(r.Root, "bin", "worktree", name)
			if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(existing, []byte("MAIN_BRANCH=keep\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			var buf bytes.Buffer
			err := Init(r, InitOptions{}, &buf)
			if err == nil {
				t.Fatal("Init overwrote an existing configuration")
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error does not name the file in the way: %v", err)
			}
			if got := string(mustReadFile(t, existing)); got != "MAIN_BRANCH=keep\n" {
				t.Errorf("existing configuration was modified: %q", got)
			}
		})
	}
}

func TestInitForceReplacesAnExistingConfig(t *testing.T) {
	r := bareRepo(t)
	if err := os.MkdirAll(filepath.Dir(confPath(r)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(confPath(r), []byte("MAIN_BRANCH=stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Init(r, InitOptions{Force: true}, &buf); err != nil {
		t.Fatalf("Init --force: %v", err)
	}
	ctx, err := Open(r.Root)
	if err != nil {
		t.Fatalf("Open after Init: %v", err)
	}
	if ctx.Config.MainBranch != "main" {
		t.Errorf("MainBranch = %q, want the rewritten %q", ctx.Config.MainBranch, "main")
	}
}

// Init writes defaults for everything it did not ask about, and cannot tell
// whether the answered main branch exists or the build command runs. Doctor
// can, so the last thing init does is hand over to it.
func TestInitHandsOverToDoctor(t *testing.T) {
	r := bareRepo(t)
	var buf bytes.Buffer
	if err := Init(r, InitOptions{}, &buf); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !strings.Contains(buf.String(), "wt doctor") {
		t.Errorf("init does not say what to run next:\n%s", buf.String())
	}
}

// A configuration git will not track is worse than none: it works for whoever
// ran init and for nobody else, and the next clone is back where it started.
// wt's own repository ignores bin/ for its build output, which is how this was
// found.
func TestInitWarnsWhenTheConfigWouldBeGitIgnored(t *testing.T) {
	r := bareRepo(t)
	if err := os.WriteFile(filepath.Join(r.Root, ".gitignore"), []byte("bin/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Init(r, InitOptions{}, &buf); err != nil {
		t.Fatalf("Init: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "ignored") {
		t.Errorf("init does not warn that the configuration is git-ignored:\n%s", out)
	}
	if !strings.Contains(out, ".gitignore") {
		t.Errorf("the warning does not say where to fix it:\n%s", out)
	}
}

func TestInitStaysQuietWhenTheConfigIsTrackable(t *testing.T) {
	r := bareRepo(t)
	var buf bytes.Buffer
	if err := Init(r, InitOptions{}, &buf); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if strings.Contains(buf.String(), "ignored") {
		t.Errorf("init warns about ignoring on a repository that ignores nothing:\n%s", buf.String())
	}
}
