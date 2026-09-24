package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/gittest"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// managedRepo is a repository at parent/name with the configuration wt reads.
func managedRepo(t *testing.T, parent, name string) string {
	t.Helper()
	dir := gittest.NewRepo(t, parent, name)
	conf := filepath.Join(dir, "bin", "worktree")
	if err := os.MkdirAll(conf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conf, "worktree.conf"), []byte(minimalConf), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, dir, "add", "-A")
	gittest.Git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func targetNames(ts []RepoTarget) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

// WT_ROOTS wins over the file, which wins over the defaults, and a root from
// WT_ROOTS is named after its directory.
func TestRootsComeFromTheEnvironmentThenTheFileThenTheDefaults(t *testing.T) {
	u := &config.User{Roots: []config.NamedRoot{{Name: "work", Path: "~/src/work"}}}
	home, _ := os.UserHomeDir()

	t.Setenv("WT_ROOTS", "/a/one:/b/one")
	roots, origin := RootsFor(u)
	if origin != "WT_ROOTS" || len(roots) != 2 || roots[0] != (Root{"one", "/a/one"}) || roots[1].Name != "one-2" {
		t.Errorf("WT_ROOTS: %+v (%s)", roots, origin)
	}
	t.Setenv("WT_ROOTS", "")
	roots, origin = RootsFor(u)
	if origin != "user config" || len(roots) != 1 || roots[0] != (Root{"work", filepath.Join(home, "src/work")}) {
		t.Errorf("file: %+v (%s)", roots, origin)
	}
	if roots, origin = RootsFor(&config.User{}); origin != "default" || len(roots) == 0 {
		t.Errorf("defaults: %+v (%s)", roots, origin)
	}
}

// A folder root is searched one level down: its managed repositories, not
// the <repo>_wt folders beside them, not a linked worktree at that depth, and
// not a checkout wt does not manage, which is counted instead. A root that is
// a repository is that one repository and nothing under it.
func TestDiscoverReposFindsManagedCheckoutsAndSingleRepoRoots(t *testing.T) {
	folder := realTempDir(t)
	api := managedRepo(t, folder, "api")
	gittest.NewRepo(t, folder, "loose")
	gittest.Git(t, api, "worktree", "add", "-q", "-b", "feat_wt/x", filepath.Join(folder, "api_wt", "feat_wt", "x"))
	gittest.Git(t, api, "worktree", "add", "-q", "-b", "legacy", filepath.Join(folder, "api-legacy"))
	if err := os.Mkdir(filepath.Join(folder, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	dotfiles := managedRepo(t, realTempDir(t), "dotfiles")
	// Something that looks like a repository under the single-repo root must
	// not be found: that root is searched no deeper.
	managedRepo(t, dotfiles, "vendored")

	set := discoverRepos([]Root{{"work", folder}, {"dotfiles", dotfiles}})
	if got := targetNames(set.Repos); !slices.Equal(got, []string{"api", "dotfiles"}) {
		t.Errorf("repos = %v", got)
	}
	if set.Repos[1].Root != "dotfiles" || set.Repos[1].Path != dotfiles {
		t.Errorf("single-repo root: %+v", set.Repos[1])
	}
	if len(set.Unmanaged) != 1 || filepath.Base(set.Unmanaged[0]) != "loose" {
		t.Errorf("unmanaged = %v", set.Unmanaged)
	}
}

// --roots narrows to the named roots, and a name there is not is an error
// that says which there are; a profile entry that is not a managed main
// checkout comes back with what is wrong, not as an error.
func TestSelectReposByRootAndByProfile(t *testing.T) {
	a, b := realTempDir(t), realTempDir(t)
	managedRepo(t, a, "one")
	two := managedRepo(t, b, "two")
	loose := gittest.NewRepo(t, b, "loose")
	gittest.Git(t, two, "worktree", "add", "-q", "-b", "wt", filepath.Join(b, "two-wt"))
	u := &config.User{
		Roots: []config.NamedRoot{{Name: "a", Path: a}, {Name: "b", Path: b}},
		Profiles: []config.Profile{{Name: "p", Repos: []string{
			two, filepath.Join(b, "gone"), loose, filepath.Join(b, "two-wt"),
		}}},
	}
	t.Setenv("WT_ROOTS", "")

	set, err := SelectRepos(u, Selection{Roots: []string{"b"}})
	if err != nil || !slices.Equal(targetNames(set.Repos), []string{"two"}) {
		t.Fatalf("--roots b: %v, %v", targetNames(set.Repos), err)
	}
	if _, err := SelectRepos(u, Selection{Roots: []string{"c"}}); err == nil || !strings.Contains(err.Error(), "the roots are: a b") {
		t.Errorf("unknown root: %v", err)
	}
	if _, err := SelectRepos(u, Selection{Profile: "q"}); err == nil || !strings.Contains(err.Error(), "the profiles are: p") {
		t.Errorf("unknown profile: %v", err)
	}
	set, err = SelectRepos(u, Selection{Profile: "p"})
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	for _, r := range set.Repos {
		problems = append(problems, r.Problem)
	}
	if problems[0] != "" || problems[1] != "not there any more" ||
		!strings.HasPrefix(problems[2], "not managed by wt") || !strings.Contains(problems[3], "not its main checkout") {
		t.Errorf("problems = %q", problems)
	}
	if set.Repos[0].Root != "b" {
		t.Errorf("a profile entry names the root it sits in: %+v", set.Repos[0])
	}
}

// Doctor checks what every --all run reads: a root that is not there, a root
// inside another, a single-repo root wt does not manage, and profile entries
// that are gone or sit outside every root.
func TestDoctorChecksRootsAndProfiles(t *testing.T) {
	a := realTempDir(t)
	inside := filepath.Join(a, "inner")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	managedRepo(t, a, "one")
	loose := gittest.NewRepo(t, realTempDir(t), "loose")
	outside := managedRepo(t, realTempDir(t), "elsewhere")
	u := &config.User{
		Roots: []config.NamedRoot{{Name: "a", Path: a}, {Name: "in", Path: inside},
			{Name: "gone", Path: filepath.Join(a, "nope")}, {Name: "loose", Path: loose}},
		Profiles: []config.Profile{{Name: "p", Repos: []string{filepath.Join(a, "one"), outside, filepath.Join(a, "missing")}}},
	}
	t.Setenv("WT_ROOTS", "")
	var buf bytes.Buffer
	if n := DoctorRepositories(u, nil, &buf); n != 5 {
		t.Errorf("problems = %d, want 5:\n%s", n, buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"✓ root a: ", "(1 repository)",
		"! root in: ", "is inside root a",
		"! root gone: ", "is not there",
		"! root loose: ", "is one repository, and wt does not manage it",
		"✓ profile p: ", "(in a)",
		"is outside every root",
		"missing is not there any more",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

// One question covers every repository, each is swept as it would be alone,
// and one that cannot be swept is reported while the rest go ahead.
func TestSweepAllAsksOnceAndSweepsEveryRepository(t *testing.T) {
	ctxA, _, _ := sweepRepo(t)
	ctxB, _, _ := sweepRepo(t)
	pathA := mergedWorktree(t, ctxA, "fix/one")
	pathB := mergedWorktree(t, ctxB, "fix/two")
	u := &config.User{Profiles: []config.Profile{{Name: "p", Repos: []string{
		ctxA.Repo.MainRoot, ctxB.Repo.MainRoot, filepath.Join(realTempDir(t), "gone"),
	}}}}

	var asked []string
	opts := SweepAllOptions{SweepOptions: SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}},
		Ask: func(q string) (bool, error) { asked = append(asked, q); return true, nil }}
	var buf bytes.Buffer
	err := SweepAll(u, Selection{Profile: "p"}, opts, &buf)
	if err == nil || !strings.Contains(err.Error(), "1 of 3 repositories could not be swept in full: gone") {
		t.Fatalf("want the missing entry reported: %v\n%s", err, buf.String())
	}
	if !slices.Equal(asked, []string{"Sweep 2 worktrees across 2 repositories?"}) {
		t.Errorf("asked %q", asked)
	}
	if exists(pathA) || exists(pathB) {
		t.Errorf("both merged worktrees should be gone:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "== demo") || !strings.Contains(buf.String(), "not there any more") {
		t.Errorf("each repository gets a section:\n%s", buf.String())
	}
}

func TestSweepAllChangesNothingWhenTheAnswerIsNo(t *testing.T) {
	ctx, _, _ := sweepRepo(t)
	path := mergedWorktree(t, ctx, "fix/one")
	u := &config.User{Profiles: []config.Profile{{Name: "p", Repos: []string{ctx.Repo.MainRoot}}}}
	opts := SweepAllOptions{SweepOptions: SweepOptions{NoFetch: true, Agents: []wtsync.Agent{}, PRs: map[string]github.PR{}},
		Ask: func(string) (bool, error) { return false, nil }}
	var buf bytes.Buffer
	if err := SweepAll(u, Selection{Profile: "p"}, opts, &buf); err != nil {
		t.Fatal(err)
	}
	if !exists(path) || !strings.Contains(buf.String(), "Nothing was swept.") {
		t.Errorf("no means nothing goes:\n%s", buf.String())
	}
}

// The listing says per repository whether trunk declares wt sync, and names a
// wt configuration that does not parse; --doctor runs doctor in each.
func TestReposShowsSyncAndABrokenConfiguration(t *testing.T) {
	root := realTempDir(t)
	synced := managedRepo(t, root, "synced")
	if err := os.WriteFile(filepath.Join(synced, ".wt-sync.yaml"), []byte("conflicts: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, synced, "add", "-A")
	gittest.Git(t, synced, "commit", "-q", "-m", "declare")
	gittest.WithOrigin(t, synced)
	gittest.WithOrigin(t, managedRepo(t, root, "plain"))
	managedRepo(t, root, "unfetched")
	broken := managedRepo(t, root, "broken")
	if err := os.WriteFile(filepath.Join(broken, "bin", "worktree", "worktree.conf"), []byte("NOT_A_KEY=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u := &config.User{Roots: []config.NamedRoot{{Name: "r", Path: root}}}
	t.Setenv("WT_ROOTS", "")

	var buf bytes.Buffer
	if err := Repos(u, Selection{}, ReposOptions{}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"synced     no worktrees  sync set up", "plain      no worktrees  no sync",
		"unfetched  no worktrees  sync unknown: origin/main never fetched", "! broken", "/broken  wt configuration: "} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	buf.Reset()
	err := Repos(u, Selection{}, ReposOptions{Doctor: true}, &buf)
	if err == nil || !strings.Contains(buf.String(), "broken     1 problem(s)") || !strings.Contains(buf.String(), "plain      ✓") {
		t.Errorf("want doctor per repository and a failure: %v\n%s", err, buf.String())
	}
}

// A config file wt cannot read may name roots it cannot see: a run across
// repositories refuses rather than fall back to the defaults. The same
// repository named twice in a profile is one repository.
func TestSelectReposRefusesAnUnreadableFileAndMergesDuplicates(t *testing.T) {
	t.Setenv("WT_ROOTS", "")
	if _, err := SelectRepos(&config.User{Path: "/x/config.toml", TablesUnreadable: true}, Selection{All: true}); err == nil ||
		!strings.Contains(err.Error(), "wt does not know your roots") {
		t.Errorf("want a refusal, got %v", err)
	}
	dir := managedRepo(t, realTempDir(t), "api")
	u := &config.User{Profiles: []config.Profile{{Name: "p", Repos: []string{dir, dir + "/", filepath.Join(dir, ".")}}}}
	set, err := SelectRepos(u, Selection{Profile: "p"})
	if err != nil || len(set.Repos) != 1 {
		t.Errorf("want one repository, got %v, %v", targetNames(set.Repos), err)
	}
}
