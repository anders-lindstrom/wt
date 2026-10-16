package provision

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

func TestNewCreatesCanonicalWorktreeAndBranch(t *testing.T) {
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	path, err := New(ctx, "fix/login-crash", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "login-crash")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("worktree not created: %v", err)
	}
	if got := ctx.Repo.BranchAt(path); got != "fix_wt/login-crash" {
		t.Errorf("branch = %q", got)
	}
}

func TestNewBareWorkNameTakesDefaultType(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	var buf bytes.Buffer
	path, err := New(ctx, "thing", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := ctx.Repo.BranchAt(path); got != "feat_wt/thing" {
		t.Errorf("branch = %q, want feat_wt/thing", got)
	}
}

func TestNewRefusesDuplicateWork(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	var buf bytes.Buffer
	if _, err := New(ctx, "fix/dup", NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := New(ctx, "fix/dup", NewOptions{NoSetup: true}, &buf); err == nil {
		t.Error("want an error creating the same work twice")
	}
}

func TestNewRejectsUnknownType(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	var buf bytes.Buffer
	if _, err := New(ctx, "wibble/thing", NewOptions{NoSetup: true}, &buf); err == nil {
		t.Error("want an error for an unknown type")
	}
}

// "." and "/" name the worktree you are in and the main checkout, as
// everywhere in wt; read as a work name they would fail on a branch called
// feat_wt/. or a path that already exists, with an error about the wrong
// thing.
func TestNewRefusesDotAndRootAsAWorkName(t *testing.T) {
	ctx, _ := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	var buf bytes.Buffer
	for spec, want := range map[string]string{
		".":  ". names the worktree you are in; wt new takes a new <type>/<work>",
		"./": ". names the worktree you are in; wt new takes a new <type>/<work>",
		"/":  "/ names the main checkout; wt new takes a new <type>/<work>",
	} {
		_, err := New(ctx, spec, NewOptions{NoSetup: true}, &buf)
		if err == nil || err.Error() != want {
			t.Errorf("New(%q): err %v, want %q", spec, err, want)
		}
	}
	if buf.Len() != 0 {
		t.Errorf("nothing is created before the refusal:\n%s", buf.String())
	}
}

// A repository that names its branches without a suffix gets exactly that,
// and its worktrees do not move for it: the folders are wt's layout.
func TestNewWithoutABranchSuffixLeavesTheFoldersAlone(t *testing.T) {
	conf := testkit.MinimalConf + "WORKTREE_BRANCH_SUFFIX=\"\"\n"
	ctx, err := kit.Open(testkit.CommittedRepo(t, conf))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	path, err := New(ctx, "login-crash", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "login-crash")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if got := ctx.Repo.BranchAt(path); got != "feat/login-crash" {
		t.Errorf("branch = %q, want feat/login-crash", got)
	}
	// The worktree must still be found by the name it was created under, and
	// by the branch it is on.
	for _, spec := range []string{"login-crash", "feat/login-crash"} {
		if got, err := kit.Path(ctx, spec); err != nil || got != want {
			t.Errorf("Path(%q) = %q, %v; want %q", spec, got, err, want)
		}
	}
}

// The person's own setting names their branches wherever the repository has
// not made the choice for them.
func TestTheUserBranchSuffixAppliesWhereTheRepositoryIsSilent(t *testing.T) {
	path := testkit.UserConfigIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("branch_suffix = \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	wtPath, err := New(ctx, "fix/login-crash", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := ctx.Repo.BranchAt(wtPath); got != "fix/login-crash" {
		t.Errorf("branch = %q, want fix/login-crash", got)
	}
	if want := filepath.Join(ctx.Repo.Parent, "demo_wt", "fix_wt", "login-crash"); wtPath != want {
		t.Errorf("path = %q, want %q", wtPath, want)
	}
	if suffix, origin := ctx.BranchSuffix(); suffix != "" || origin != "user file" {
		t.Errorf("BranchSuffix = %q (%s), want \"\" (user file)", suffix, origin)
	}
}

// A repository that does name its branches outranks the person: a committed
// answer is the one everybody working on it shares.
func TestTheRepositoryOutranksTheUserBranchSuffix(t *testing.T) {
	path := testkit.UserConfigIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("branch_suffix = \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf+"WORKTREE_BRANCH_SUFFIX=_wt\n"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	wtPath, err := New(ctx, "fix/login-crash", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := ctx.Repo.BranchAt(wtPath); got != "fix_wt/login-crash" {
		t.Errorf("branch = %q, want fix_wt/login-crash", got)
	}
	if suffix, origin := ctx.BranchSuffix(); suffix != "_wt" || origin != "repo file" {
		t.Errorf("BranchSuffix = %q (%s), want _wt (repo file)", suffix, origin)
	}
}

// What a type is called is a naming, not a move: the branch reads the word,
// the worktree sits under the type, and either spelling names the same work.
func TestNewNamesTheBranchByTheTypesWord(t *testing.T) {
	conf := testkit.MinimalConf + "WORKTREE_TYPE_NAMES=(feat=feature)\nWORKTREE_BRANCH_SUFFIX=\"\"\n"
	ctx, err := kit.Open(testkit.CommittedRepo(t, conf))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	path, err := New(ctx, "feature/login-crash", NewOptions{NoSetup: true}, &buf)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := filepath.Join(ctx.Repo.Parent, "demo_wt", "feat_wt", "login-crash")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if got := ctx.Repo.BranchAt(path); got != "feature/login-crash" {
		t.Errorf("branch = %q, want feature/login-crash", got)
	}
	// The same piece of work, named three ways.
	for _, spec := range []string{"login-crash", "feature/login-crash", "feat/login-crash"} {
		if got, err := kit.Path(ctx, spec); err != nil || got != want {
			t.Errorf("Path(%q) = %q, %v; want %q", spec, got, err, want)
		}
	}
	// And a type it was never given a word for keeps its own.
	if got, err := kit.Branch(ctx, "fix/flake"); err != nil || got != "fix/flake" {
		t.Errorf("Branch = %q, %v; want fix/flake", got, err)
	}
}

// One person's naming travels with them: it applies in every repository that
// has not named its types itself, and only to the types that repository has.
func TestTheUserTypeNamesApplyWhereTheRepositoryIsSilent(t *testing.T) {
	path := testkit.UserConfigIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "type_names = [\"feat=feature\", \"wibble=w\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf))
	if err != nil {
		t.Fatal(err)
	}
	names, origin := ctx.TypeNames()
	if names["feat"] != "feature" || origin != "user file" {
		t.Errorf("TypeNames = %v (%s), want feat=feature (user file)", names, origin)
	}
	// A pair for a type this repository does not have is a name for
	// somewhere else, and drops out quietly.
	if _, ok := names["wibble"]; ok {
		t.Error("a pair for an unknown type was kept")
	}
	if got, err := kit.Branch(ctx, "feature/login"); err != nil || got != "feature_wt/login" {
		t.Errorf("Branch = %q, %v; want feature_wt/login", got, err)
	}
}

// A repository that names its types has named them for everybody who clones
// it, whatever they call them elsewhere.
func TestTheRepositoryOutranksTheUserTypeNames(t *testing.T) {
	path := testkit.UserConfigIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("type_names = [\"feat=feature\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, err := kit.Open(testkit.CommittedRepo(t, testkit.MinimalConf+"WORKTREE_TYPE_NAMES=(feat=feat)\n"))
	if err != nil {
		t.Fatal(err)
	}
	if names, origin := ctx.TypeNames(); names["feat"] != "feat" || origin != "repo file" {
		t.Errorf("TypeNames = %v (%s), want feat=feat (repo file)", names, origin)
	}
	if got, err := kit.Branch(ctx, "feat/login"); err != nil || got != "feat_wt/login" {
		t.Errorf("Branch = %q, %v; want feat_wt/login", got, err)
	}
}
