package wtsync

import (
	"strings"
	"testing"
)

func TestResolvedTreeReplacesABlob(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n", "sub/b.txt": "b\n"}, nil, nil)
	tree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")

	out, err := resolvedTree(dir, tree, map[string][]byte{"sub/b.txt": []byte("resolved\n")})
	if err != nil {
		t.Fatal(err)
	}
	if out == tree {
		t.Fatal("expected a new tree")
	}
	if got := gitIn(t, dir, "cat-file", "-p", out+":sub/b.txt"); got != "resolved" {
		t.Fatalf("sub/b.txt = %q, want %q", got, "resolved")
	}
	if got := gitIn(t, dir, "cat-file", "-p", out+":a.txt"); got != "a" {
		t.Fatalf("a.txt = %q, want %q", got, "a")
	}
}

func TestResolvedTreeKeepsTheExecutableBit(t *testing.T) {
	dir := repoWith(t, map[string]string{"s.sh": "old\n"}, nil, nil)
	gitIn(t, dir, "update-index", "--chmod=+x", "s.sh")
	gitIn(t, dir, "commit", "-q", "-m", "exec")
	tree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")

	out, err := resolvedTree(dir, tree, map[string][]byte{"s.sh": []byte("new\n")})
	if err != nil {
		t.Fatal(err)
	}
	if mode := strings.Fields(gitIn(t, dir, "ls-tree", out, "--", "s.sh"))[0]; mode != "100755" {
		t.Fatalf("mode = %s, want 100755", mode)
	}
}

func TestResolvedTreeRefusesAPathThatIsNotThere(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	tree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")

	_, err := resolvedTree(dir, tree, map[string][]byte{"missing.txt": []byte("x\n")})
	if err == nil || !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("err = %v, want it to name missing.txt", err)
	}
}

func TestResolvedTreeIsANoOpForNoPaths(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	tree := gitIn(t, dir, "rev-parse", "HEAD^{tree}")

	out, err := resolvedTree(dir, tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != tree {
		t.Fatalf("tree = %s, want it unchanged (%s)", out, tree)
	}
}
