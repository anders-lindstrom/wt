package synccmdtest

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synccmd"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// Branch makes branch name in the main checkout at from, with one commit
// writing files, and returns its tip. The main checkout is left on the
// branch it was on; nothing is fetched.
func Branch(t *testing.T, ctx *kit.Context, name, from string, files map[string]string) string {
	t.Helper()
	main := ctx.Repo.MainRoot
	was := testkit.GitOut(t, main, "symbolic-ref", "--short", "HEAD")
	testkit.GitOut(t, main, "checkout", "-q", "-b", name, from)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := os.WriteFile(filepath.Join(main, p), []byte(files[p]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testkit.GitOut(t, main, "add", "-A")
	testkit.GitOut(t, main, "commit", "-q", "-m", name+": "+paths[0])
	tip := testkit.GitOut(t, main, "rev-parse", "HEAD")
	testkit.GitOut(t, main, "checkout", "-q", was)
	return tip
}

// Integration is Branch integ off trunk, bumping v.txt to 1.0.7 above
// trunk's 1.0.5 and adding i.txt: the integration branch a feature of
// RunFixture's is rebased onto before it lands.
func Integration(t *testing.T, ctx *kit.Context) string {
	t.Helper()
	return Branch(t, ctx, "integ", "main", map[string]string{"v.txt": "1.0.7\n", "i.txt": "i\n"})
}

// Onto is NoAgents rebasing onto ref, above upstream when it is not empty.
func Onto(ref, upstream string) synccmd.RunOptions {
	opts := NoAgents()
	opts.Onto, opts.Upstream = ref, upstream
	return opts
}
