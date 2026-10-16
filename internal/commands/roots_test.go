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
