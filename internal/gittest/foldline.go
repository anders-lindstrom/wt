package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// FoldGen is the generator a FoldLine carries: it writes gen/spec.json from
// the version line and every file under src/, in name order, and nothing
// else. Deterministic, so running it again at a tip that holds its output
// changes nothing.
const FoldGen = `#!/bin/sh
set -e
mkdir -p gen
{
  printf '{\n  "version": "%s",\n  "sources": [\n' "$(head -n 1 v.txt)"
  for f in src/*; do printf '    "%s: %s",\n' "$f" "$(cat "$f")"; done
  printf '    "end"\n  ]\n}\n'
} > gen/spec.json
`

// FoldYAML is the declaration of the case: the version line under rule, the
// generator as a step at every layer tip folded into the commit that owns
// its output, and a check of every commit that fails on a marker file.
func FoldYAML(rule string) string {
	return "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: " + rule + "\n" +
		"defer:\n  - run: ./gen.sh\n    paths: [gen/**]\n    each: layer\n    fold: owner\n    needs: [docker]\n" +
		"    commit: \"chore: regenerate the spec\"\n" +
		"each_commit:\n  - run: test ! -e broken.marker\n"
}

// FoldLine is a stack held on one line shaped like the case a fold is for:
// a generated file every layer's PR is checked against, owned by a commit in
// some layers, drifting in others when trunk or a bump below changes what
// the generator reads.
//
//	f1  feat(f1): a (src/a.txt and its gen/spec.json, the owner), then a minor bump to 1.1.0; in a clean worktree
//	f2  two docs commits; a fixup! for the first sits at the top of the line
//	f3  feat(f3): c (src/c.txt and its spec, the owner), then a patch bump to 1.1.1; in a worktree with tracked changes
//	f4  a docs commit
//	f5  a patch bump to 1.1.2, with no spec: no owner, so the step commits
//	tmp/fold  f5 plus the fixup!, in its own worktree
//
// Trunk moved after the base: the same 1.1.0 bump as f1's, src/t.txt with its
// spec regenerated, and a migration. origin is the repository itself.
type FoldLine struct {
	Dir    string
	Base   string
	Layers []string          // bottom first
	Tips   map[string]string // each layer's tip, and tmp/fold's
	Paths  map[string]string // the worktree of a branch that has one
	// Owners is the subject of the commit that owns the spec in each layer
	// that has one.
	Owners map[string]string
}

// FoldTmp is the branch at the top of a FoldLine.
const FoldTmp = "tmp/fold"

// NewFoldLine builds a FoldLine in dir, a repository on main with at least
// one commit. yaml is committed on trunk after the base.
func NewFoldLine(t testing.TB, dir, wtDir, yaml string) *FoldLine {
	t.Helper()
	s := &FoldLine{Dir: dir, Layers: []string{"f1", "f2", "f3", "f4", "f5"}, Tips: map[string]string{},
		Paths: map[string]string{}, Owners: map[string]string{"f1": "feat(f1): a", "f3": "feat(f3): c"}}
	write := func(rel, content string) { WriteFile(t, filepath.Join(dir, rel), content) }
	gen := func() { RunGen(t, dir) }
	commit := func(msg string) string {
		Git(t, dir, "add", "-A")
		Git(t, dir, "commit", "-q", "--no-verify", "-m", msg)
		return Git(t, dir, "rev-parse", "HEAD")
	}
	write("v.txt", "1.0.0\n")
	write("src/core.txt", "core\n")
	write("gen.sh", FoldGen)
	if err := os.Chmod(filepath.Join(dir, "gen.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	gen()
	s.Base = commit("base")
	Git(t, dir, "checkout", "-q", "-b", FoldTmp)
	branch := func(name string) {
		s.Tips[name] = Git(t, dir, "rev-parse", "HEAD")
		Git(t, dir, "branch", name)
	}
	write("src/a.txt", "a\n")
	gen()
	commit("feat(f1): a")
	write("v.txt", "1.1.0\n")
	commit("chore(f1): bump to 1.1.0")
	branch("f1")
	write("docs/n.txt", "notes\n")
	commit("feat(f2): notes")
	write("docs/m.txt", "more\n")
	commit("feat(f2): more")
	branch("f2")
	write("src/c.txt", "c\n")
	gen()
	commit("feat(f3): c")
	write("v.txt", "1.1.1\n")
	commit("chore(f3): bump to 1.1.1")
	branch("f3")
	write("docs/d.txt", "d\n")
	commit("feat(f4): docs")
	branch("f4")
	write("v.txt", "1.1.2\n")
	commit("chore(f5): bump to 1.1.2")
	branch("f5")
	write("docs/n.txt", "notes, fixed\n")
	commit("fixup! feat(f2): notes")
	s.Tips[FoldTmp] = Git(t, dir, "rev-parse", "HEAD")
	Git(t, dir, "checkout", "-q", "main")
	if yaml != "" {
		write(".wt-sync.yaml", yaml)
		commit("chore: declare")
	}
	write("v.txt", "1.1.0\n")
	commit("chore: bump to 1.1.0")
	write("src/t.txt", "trunk\n")
	gen()
	commit("feat: trunk's own source")
	write("db/V2__trunk.sql", "create table t (id int);\n")
	commit("feat: trunk's migration")
	Git(t, dir, "remote", "add", "origin", dir)
	Git(t, dir, "fetch", "-q", "origin")
	add := func(branch string) string {
		path := filepath.Join(wtDir, strings.ReplaceAll(branch, "/", "_"))
		Git(t, dir, "worktree", "add", "-q", path, branch)
		s.Paths[branch] = path
		return path
	}
	add("f1")
	f3 := add("f3")
	WriteFile(t, filepath.Join(f3, "docs/m.txt"), "work in progress\n")
	add(FoldTmp)
	return s
}

// RunGen runs the generator in dir.
func RunGen(t testing.TB, dir string) {
	t.Helper()
	cmd := exec.Command("sh", "./gen.sh")
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gen.sh in %s: %v\n%s", dir, err, b)
	}
}
