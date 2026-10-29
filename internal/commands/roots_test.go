package commands

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/reposel"
)

// AcrossRepos prints each repository's output under its name, in order, and
// names the ones it could not run in; --profile takes several names.
func TestAcrossReposPrintsEachRepositoryUnderItsName(t *testing.T) {
	t.Parallel()
	root := testkit.RealTempDir(t)
	a, b := testkit.ManagedRepo(t, root, "alpha"), testkit.ManagedRepo(t, root, "beta")
	u := &config.User{Profiles: []config.Profile{
		{Name: "one", Repos: []string{a}},
		{Name: "two", Repos: []string{b, filepath.Join(root, "gone")}},
	}}
	var buf bytes.Buffer
	err := kit.AcrossRepos(u, reposel.Selection{Profiles: []string{"one", "two"}}, &buf, func(ctx *kit.Context, w io.Writer) error {
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
