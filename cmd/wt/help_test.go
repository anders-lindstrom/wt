package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// reachable is every command a user can get help for: the whole tree minus
// cobra's own generated commands. Hidden ones are in it: someone who types
// `wt branch-strip --help` reads that help too.
func reachable(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Name() == "help" || c.Name() == "completion" {
			return
		}
		out = append(out, c)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return out
}

func exampleLines(c *cobra.Command) []string {
	var out []string
	for _, line := range strings.Split(c.Example, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// An example is a line you can paste into a shell, and its command and flags
// still exist: help that shows a flag the command no longer has is worse than
// no example. This reads each example back and parses it; it runs nothing.
func TestEveryExampleStillParses(t *testing.T) {
	root := newRootCmd()
	for _, c := range reachable(root) {
		for _, line := range exampleLines(c) {
			words := exampleWords(strings.TrimSpace(line))
			if len(words) < 2 || words[0] != "wt" {
				t.Errorf("%q: example is not a `wt` invocation: %q", c.CommandPath(), line)
				continue
			}
			found, rest, err := root.Find(words[1:])
			if err != nil {
				t.Errorf("%q: example %q: %v", c.CommandPath(), line, err)
				continue
			}
			if found.Name() == "exec" {
				continue // everything after the work is the command to run, flags and all
			}
			found.InitDefaultHelpFlag()
			found.InitDefaultVersionFlag()
			if err := found.ParseFlags(rest); err != nil {
				t.Errorf("%q: example %q: %v", c.CommandPath(), line, err)
			}
		}
	}
}

// exampleWords splits an example line into its words, honouring quotes, and
// stops at a comment, a pipe, a redirection or a `;`.
func exampleWords(line string) []string {
	var words []string
	var word strings.Builder
	var quote rune
	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ':
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
		case r == '#' && (i == 0 || line[i-1] == ' '), r == '>', r == '|', r == ';':
			if word.Len() > 0 {
				words = append(words, word.String())
			}
			return words
		default:
			word.WriteRune(r)
		}
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	return words
}

// fleet is names from the real fleet: worktrees, repositories, a Superset id.
var fleet = []string{"webkey", "idiotthings", "local-gecko", "controller_stats",
	"axis_acc", "new_vapix", "statepush", "telcred", "dedd5f22", "server_wt",
	"accessmanager", "personal-v"}

// Help is read by people who have never seen this fleet. Names from it teach
// nothing and date badly.
func TestHelpUsesIllustrativeNames(t *testing.T) {
	for _, c := range reachable(newRootCmd()) {
		text := strings.ToLower(c.Long + "\n" + c.Example + "\n" + c.Short)
		for _, name := range fleet {
			if strings.Contains(text, name) {
				t.Errorf("%q: help names %q from the real fleet", c.CommandPath(), name)
			}
		}
	}
}

// A worktree moved to a folder is "a removed worktree" in "its folder" to a
// person; quarantine is what the JSON, the refs and the code call it. Only
// wt schema's help says the word, in the names of two schemas.
func TestHelpNeverSaysQuarantine(t *testing.T) {
	for _, c := range reachable(newRootCmd()) {
		text := c.Short + "\n" + c.Example + "\n" + c.Flags().FlagUsages()
		if c.Name() != "schema" {
			text += "\n" + c.Long
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(strings.ToLower(line), "quarantin") {
				t.Errorf("%q: help says %q", c.CommandPath(), strings.TrimSpace(line))
			}
		}
	}
}

// --move-to moves a worktree out of the repository; wt move, the alias of wt
// migrate, moves one within it. Completion keeps the flag and the command
// apart.
func TestMoveToIsNotTheMoveCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	if words, _ := complete(t, "remove", "--move"); len(words) != 1 || !strings.HasPrefix(words[0], "--move-to") {
		t.Errorf("wt remove --move completes to %q", words)
	}
	if words, _ := complete(t, "migrate", "--move"); len(words) != 0 {
		t.Errorf("wt migrate --move completes to %q", words)
	}
}

// The README and the docs beside it are read by the same people, and their
// examples date the same way.
func TestDocsUseIllustrativeNames(t *testing.T) {
	root := filepath.Join("..", "..")
	docs, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range append([]string{filepath.Join(root, "README.md")}, docs...) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(strings.ToLower(string(data)), "\n") {
			for _, name := range fleet {
				if strings.Contains(line, name) {
					t.Errorf("%s:%d names %q from the real fleet", rel, i+1, name)
				}
			}
		}
	}
}

// Twenty commands in one flat list is a list nobody reads. Cobra groups them
// if every command says which group it is in.
func TestEveryCommandIsInAGroup(t *testing.T) {
	root := newRootCmd()
	groups := map[string]bool{}
	for _, g := range root.Groups() {
		groups[g.ID] = true
	}
	if len(groups) == 0 {
		t.Fatal("the root command declares no groups")
	}
	for _, c := range root.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		if c.GroupID == "" {
			t.Errorf("%q is in no group", c.CommandPath())
			continue
		}
		if !groups[c.GroupID] {
			t.Errorf("%q is in group %q, which the root does not declare",
				c.CommandPath(), c.GroupID)
		}
	}
}
