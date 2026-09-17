package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/gittest"
)

// repoWithWorktrees is a repository with two worktrees, and the test standing
// in its main checkout, so completion has something to offer.
func repoWithWorktrees(t *testing.T) {
	t.Helper()
	parent := t.TempDir()
	main := gittest.NewRepo(t, parent, "demo")
	gittest.WriteFile(t, filepath.Join(main, "bin", "worktree", "worktree.conf"),
		"MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\n")
	gittest.Git(t, main, "add", "-A")
	gittest.Git(t, main, "commit", "-qm", "conf")
	for _, w := range []string{"fix_wt/login-crash", "feat_wt/api-tidy"} {
		gittest.Git(t, main, "worktree", "add", "-q", "-b", w,
			filepath.Join(parent, "demo_wt", w))
	}
	t.Chdir(main)
}

// complete drives cobra's __complete protocol the way the generated shell
// scripts do, and returns the offered words and the directive line.
func complete(t *testing.T, args ...string) (words []string, directive string) {
	t.Helper()
	out, err := runCmd(t, append([]string{"__complete"}, args...)...)
	if err != nil {
		t.Fatalf("__complete %v: %v", args, err)
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, ":"):
			directive = line
		case line == "" || strings.HasPrefix(line, "Completion ended") ||
			strings.HasPrefix(line, "[Debug]"):
		default:
			words = append(words, line)
		}
	}
	return words, directive
}

const (
	noFileComp   = ":4" // cobra.ShellCompDirectiveNoFileComp
	shellDefault = ":0" // cobra.ShellCompDirectiveDefault
)

// cd and exec run in the shell layer, but completion runs in the binary, so
// the placeholder commands must offer the worktrees like any other verb.
func TestCdAndExecCompleteTheWorktrees(t *testing.T) {
	repoWithWorktrees(t)
	for _, verb := range []string{"cd", "exec"} {
		words, directive := complete(t, verb, "")
		if directive != noFileComp {
			t.Errorf("%s: directive %q, want %s", verb, directive, noFileComp)
		}
		got := strings.Join(words, " ")
		for _, want := range []string{"fix/login-crash", "feat/api-tidy"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q missing from %q", verb, want, got)
			}
		}
		// One key each; quicker to type than to pick.
		for _, w := range words {
			if w == "." || w == "/" {
				t.Errorf("%s: offers %q", verb, w)
			}
		}
	}
}

// The word after the worktree is the command; after that it is the command's
// business, and both go to the shell's own completion.
func TestExecHandsTheCommandToTheShell(t *testing.T) {
	repoWithWorktrees(t)
	for _, args := range [][]string{
		{"exec", "login-crash", ""},
		{"exec", "login-crash", "gi"},
		{"exec", "login-crash", "ls", ""},
		{"exec", "login-crash", "ls", "-la", ""},
		{"exec", ".", ""},
	} {
		words, directive := complete(t, args...)
		if directive != shellDefault {
			t.Errorf("%v: directive %q, want %s", args, directive, shellDefault)
		}
		if len(words) != 0 {
			t.Errorf("%v: offers %q, want nothing so the shell completes", args, words)
		}
	}
}

// cd takes one pattern; a second word has nothing to complete to.
func TestCdCompletesNothingAfterTheWorktree(t *testing.T) {
	repoWithWorktrees(t)
	words, directive := complete(t, "cd", "login-crash", "")
	if directive != noFileComp || len(words) != 0 {
		t.Errorf("got %q %q, want nothing and %s", words, directive, noFileComp)
	}
}
