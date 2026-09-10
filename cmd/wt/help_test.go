package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// reachable is every command a user can get help for: the whole tree minus the
// hidden compatibility surfaces and cobra's own generated commands.
func reachable(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
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

// Examples are the fastest way to learn what a command can do, so every
// command has some — between two and five, because a wall of them is a manual
// again.
func TestEveryCommandShowsExamples(t *testing.T) {
	for _, c := range reachable(newRootCmd()) {
		lines := exampleLines(c)
		// A command that takes something — an argument or a flag — has more
		// than one way to be used, and help has to show more than one.
		want := 1
		if strings.ContainsAny(c.Use, "<[") || c.Flags().HasAvailableFlags() {
			want = 2
		}
		switch {
		case len(lines) == 0:
			t.Errorf("%q has no Example block", c.CommandPath())
		case len(lines) < want:
			t.Errorf("%q shows %d example(s); show at least %d",
				c.CommandPath(), len(lines), want)
		case len(lines) > 5:
			t.Errorf("%q shows %d examples; keep it to 5", c.CommandPath(), len(lines))
		}
	}
}

// An example is a line you can paste into a shell, indented the way cobra
// indents everything else, and short enough not to wrap in an 80-column
// terminal.
func TestEveryExampleIsAPastableLine(t *testing.T) {
	for _, c := range reachable(newRootCmd()) {
		for _, line := range exampleLines(c) {
			if !strings.HasPrefix(line, "  wt ") {
				t.Errorf("%q: example is not a `wt` invocation: %q", c.CommandPath(), line)
			}
			if len(line) > 79 {
				t.Errorf("%q: example wraps at 80 columns (%d): %q",
					c.CommandPath(), len(line), line)
			}
		}
	}
}

// A flag nobody shows in use is a flag nobody finds. The flag list says what
// exists; the examples say what it is for.
func TestEveryFlagAppearsInAnExample(t *testing.T) {
	for _, c := range reachable(newRootCmd()) {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden || f.Name == "help" {
				return
			}
			if !strings.Contains(c.Example, "--"+f.Name) {
				t.Errorf("%q: no example uses --%s", c.CommandPath(), f.Name)
			}
		})
	}
}

// Help is read by people who have never seen this fleet. Names from it teach
// nothing and date badly.
func TestHelpUsesIllustrativeNames(t *testing.T) {
	fleet := []string{"webkey", "idiotthings", "local-gecko", "controller_stats",
		"axis_acc", "new_vapix", "statepush", "telcred", "dedd5f22", "server_wt",
		"accessmanager", "personal-v"}
	for _, c := range reachable(newRootCmd()) {
		text := strings.ToLower(c.Long + "\n" + c.Example + "\n" + c.Short)
		for _, name := range fleet {
			if strings.Contains(text, name) {
				t.Errorf("%q: help names %q from the real fleet", c.CommandPath(), name)
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
