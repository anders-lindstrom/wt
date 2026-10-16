package provisiontest

import (
	"bytes"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/provision"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// LaterStopFixture is ContestedFixture with a second branch commit that bumps
// v.txt again, so a rebase continued past the handed-over stop meets v.txt —
// owned-line's — in conflict at 2/2. withUnclaimed also has that commit add
// b.txt, which trunk adds too and nobody claims.
func LaterStopFixture(t *testing.T, withUnclaimed bool) (ctx *kit.Context, bump string) {
	t.Helper()
	ctx, bump = ContestedFixture(t)
	main := ctx.Repo.MainRoot
	if withUnclaimed {
		testkit.WriteFile(t, main, "b.txt", "trunk\n")
		testkit.GitOut(t, main, "add", "-A")
		testkit.GitOut(t, main, "commit", "-q", "-m", "b on trunk")
		testkit.GitOut(t, main, "fetch", "-q", "origin")
		testkit.WriteFile(t, bump, "b.txt", "branch\n")
	}
	testkit.WriteFile(t, bump, "v.txt", "1.0.2\n")
	testkit.GitOut(t, bump, "add", "-A")
	testkit.GitOut(t, bump, "commit", "-q", "-m", "bump again")
	return ctx, bump
}

// GlobFixture declares owned-line for v*.txt over two tracked files it
// matches, v1.txt and v[1].txt. Read as a pathspec, v[1].txt also names
// v1.txt. Only v[1].txt conflicts; a.txt, which nobody claims, makes the run
// hand the stop over.
func GlobFixture(t *testing.T) (ctx *kit.Context, bump string) {
	t.Helper()
	main := testkit.CommittedRepo(t, testkit.MinimalConf)
	testkit.WriteFile(t, main, ".wt-sync.yaml", "conflicts:\n  - paths: ['v*.txt']\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n")
	testkit.WriteFile(t, main, "v1.txt", "0.0.1\n")
	testkit.WriteFile(t, main, "v[1].txt", "1.0.0\n")
	testkit.WriteFile(t, main, "a.txt", "a\n")
	testkit.GitOut(t, main, "add", "-A")
	testkit.GitOut(t, main, "commit", "-q", "-m", "declare")
	ctx, err := kit.Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if bump, err = provision.New(ctx, "feat/bump", provision.NewOptions{NoSetup: true}, &buf); err != nil {
		t.Fatal(err)
	}
	testkit.WriteFile(t, bump, "v[1].txt", "1.0.1\n")
	testkit.WriteFile(t, bump, "a.txt", "branch\n")
	testkit.GitOut(t, bump, "commit", "-q", "-am", "bump")
	testkit.WriteFile(t, main, "v[1].txt", "1.0.5\n")
	testkit.WriteFile(t, main, "a.txt", "trunk\n")
	testkit.GitOut(t, main, "commit", "-q", "-am", "trunk bump")
	testkit.GitOut(t, main, "remote", "add", "origin", main)
	testkit.GitOut(t, main, "fetch", "-q", "origin")
	return ctx, bump
}
