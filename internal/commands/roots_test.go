package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

func targetNames(ts []kit.RepoTarget) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

// A folder root is searched one level down: its managed repositories, not
// the <repo>_wt folders beside them, not a linked worktree at that depth, and
// not a checkout wt does not manage, which is counted instead. A root that is
// a repository is that one repository and nothing under it.
func TestDiscoverReposFindsManagedCheckoutsAndSingleRepoRoots(t *testing.T) {
	folder := testkit.RealTempDir(t)
	api := testkit.ManagedRepo(t, folder, "api")
	gittest.NewRepo(t, folder, "loose")
	gittest.Git(t, api, "worktree", "add", "-q", "-b", "feat_wt/x", filepath.Join(folder, "api_wt", "feat_wt", "x"))
	gittest.Git(t, api, "worktree", "add", "-q", "-b", "legacy", filepath.Join(folder, "api-legacy"))
	if err := os.Mkdir(filepath.Join(folder, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	dotfiles := testkit.ManagedRepo(t, testkit.RealTempDir(t), "dotfiles")
	// Something that looks like a repository under the single-repo root must
	// not be found: that root is searched no deeper.
	testkit.ManagedRepo(t, dotfiles, "vendored")

	set := kit.DiscoverRepos([]kit.Root{{Name: "work", Path: folder}, {Name: "dotfiles", Path: dotfiles}})
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
	a, b := testkit.RealTempDir(t), testkit.RealTempDir(t)
	testkit.ManagedRepo(t, a, "one")
	two := testkit.ManagedRepo(t, b, "two")
	loose := gittest.NewRepo(t, b, "loose")
	gittest.Git(t, two, "worktree", "add", "-q", "-b", "wt", filepath.Join(b, "two-wt"))
	u := &config.User{
		Roots: []config.NamedRoot{{Name: "a", Path: a}, {Name: "b", Path: b}},
		Profiles: []config.Profile{{Name: "p", Repos: []string{
			two, filepath.Join(b, "gone"), loose, filepath.Join(b, "two-wt"),
		}}},
	}
	t.Setenv("WT_ROOTS", "")

	set, err := kit.SelectRepos(u, kit.Selection{Roots: []string{"b"}})
	if err != nil || !slices.Equal(targetNames(set.Repos), []string{"two"}) {
		t.Fatalf("--roots b: %v, %v", targetNames(set.Repos), err)
	}
	if _, err := kit.SelectRepos(u, kit.Selection{Roots: []string{"c"}}); err == nil || !strings.Contains(err.Error(), "the roots are: a b") {
		t.Errorf("unknown root: %v", err)
	}
	if _, err := kit.SelectRepos(u, kit.Selection{Profiles: []string{"q"}}); err == nil || !strings.Contains(err.Error(), "the profiles are: p") {
		t.Errorf("unknown profile: %v", err)
	}
	set, err = kit.SelectRepos(u, kit.Selection{Profiles: []string{"p"}})
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
	a := testkit.RealTempDir(t)
	inside := filepath.Join(a, "inner")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	testkit.ManagedRepo(t, a, "one")
	loose := gittest.NewRepo(t, testkit.RealTempDir(t), "loose")
	outside := testkit.ManagedRepo(t, testkit.RealTempDir(t), "elsewhere")
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

// The listing says per repository whether trunk declares wt sync, and names a
// wt configuration that does not parse; wt doctor --all runs doctor in each,
// and wt sync doctor where trunk declares wt sync.
func TestReposShowsSyncAndABrokenConfiguration(t *testing.T) {
	root := testkit.RealTempDir(t)
	synced := testkit.ManagedRepo(t, root, "synced")
	if err := os.WriteFile(filepath.Join(synced, ".wt-sync.yaml"), []byte("conflicts: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, synced, "add", "-A")
	gittest.Git(t, synced, "commit", "-q", "-m", "declare")
	gittest.WithOrigin(t, synced)
	gittest.WithOrigin(t, testkit.ManagedRepo(t, root, "plain"))
	testkit.ManagedRepo(t, root, "unfetched")
	broken := testkit.ManagedRepo(t, root, "broken")
	if err := os.WriteFile(filepath.Join(broken, "bin", "worktree", "worktree.conf"), []byte("NOT_A_KEY=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u := &config.User{Roots: []config.NamedRoot{{Name: "r", Path: root}}}
	t.Setenv("WT_ROOTS", "")

	var buf bytes.Buffer
	if err := kit.Repos(u, kit.Selection{}, kit.ReposOptions{}, &buf); err != nil {
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
	err := DoctorAll(u, kit.Selection{All: true}, &buf)
	if err == nil || !strings.Contains(buf.String(), "broken     1 problem(s)") || !strings.Contains(buf.String(), "plain      ✓") ||
		!strings.Contains(buf.String(), "~ sync ") {
		t.Errorf("want doctor per repository and a failure: %v\n%s", err, buf.String())
	}
}

// A config file wt cannot read may name roots it cannot see: a run across
// repositories refuses rather than fall back to the defaults. The same
// repository named twice in a profile is one repository.
func TestSelectReposRefusesAnUnreadableFileAndMergesDuplicates(t *testing.T) {
	t.Setenv("WT_ROOTS", "")
	if _, err := kit.SelectRepos(&config.User{Path: "/x/config.toml", TablesUnreadable: true}, kit.Selection{All: true}); err == nil ||
		!strings.Contains(err.Error(), "wt does not know your roots") {
		t.Errorf("want a refusal, got %v", err)
	}
	dir := testkit.ManagedRepo(t, testkit.RealTempDir(t), "api")
	u := &config.User{Profiles: []config.Profile{{Name: "p", Repos: []string{dir, dir + "/", filepath.Join(dir, ".")}}}}
	set, err := kit.SelectRepos(u, kit.Selection{Profiles: []string{"p"}})
	if err != nil || len(set.Repos) != 1 {
		t.Errorf("want one repository, got %v, %v", targetNames(set.Repos), err)
	}
}

// AcrossRepos prints each repository's output under its name, in order, and
// names the ones it could not run in; --profile takes several names.
func TestAcrossReposPrintsEachRepositoryUnderItsName(t *testing.T) {
	root := testkit.RealTempDir(t)
	a, b := testkit.ManagedRepo(t, root, "alpha"), testkit.ManagedRepo(t, root, "beta")
	u := &config.User{Profiles: []config.Profile{
		{Name: "one", Repos: []string{a}},
		{Name: "two", Repos: []string{b, filepath.Join(root, "gone")}},
	}}
	var buf bytes.Buffer
	err := kit.AcrossRepos(u, kit.Selection{Profiles: []string{"one", "two"}}, &buf, func(ctx *kit.Context, w io.Writer) error {
		_, err := fmt.Fprintf(w, "in %s\n", ctx.Repo.Name)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "1 of 3 repositories failed: gone") {
		t.Errorf("err = %v", err)
	}
	out := buf.String()
	ia, ib := strings.Index(out, "== alpha"), strings.Index(out, "== beta")
	if ia < 0 || ib < ia || !strings.Contains(out, "in alpha") || !strings.Contains(out, "in beta") {
		t.Errorf("sections:\n%s", out)
	}
}
