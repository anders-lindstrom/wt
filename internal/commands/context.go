// Package commands implements the wt subcommands.
package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// Context is the repository and configuration a subcommand operates on.
type Context struct {
	Repo   *repo.Repo
	Config *config.Config
	// ConfigError records why Config fell back to defaults, when it did.
	// Only OpenLenient sets it; Open fails outright instead.
	ConfigError error
	// User is this person's own configuration, which decides which
	// integrations wt uses at all. Never nil.
	User *config.User
	// UserError is what was wrong with the user file, when something was. It
	// never stops a command: see Open.
	UserError error
	// Cwd is the directory the context was opened from: where the caller is
	// standing.
	Cwd string
	// warn is where an integration that was supposed to work says it did
	// not: stderr, never the command's own output and never its exit code.
	// Nil discards.
	warn io.Writer
	// warned is the topics already spoken about.
	warned map[string]bool
}

// The warning topics. One line per topic per invocation, so a command that
// asks GitHub twice complains once.
const (
	WarnUserConfig = "user config"
	WarnGitHub     = "github"
)

// WarnTo gives the context somewhere to put its warnings, and says at once
// what it already knows went wrong with the user file: that wt could not read
// it at all, and every integration is off for this run, or which part of it
// wt is ignoring.
func (c *Context) WarnTo(w io.Writer) {
	c.warn = w
	switch {
	case c.UserError == nil:
	case c.UserConfig().Unusable:
		c.Warnf(WarnUserConfig, "every integration is off for this run: %v", c.UserError)
	default:
		c.Warnf(WarnUserConfig, "ignoring %v", c.UserError)
	}
}

// Warnf writes one warning line about topic, the first time that topic comes
// up. A context nobody gave a writer to says nothing.
func (c *Context) Warnf(topic, format string, args ...any) {
	if c.warn == nil || c.warned[topic] {
		return
	}
	if c.warned == nil {
		c.warned = map[string]bool{}
	}
	c.warned[topic] = true
	fmt.Fprintf(c.warn, "wt: "+format+"\n", args...)
}

// Open discovers the repository containing cwd and loads its configuration.
func Open(cwd string) (*Context, error) {
	r, err := repo.Discover(cwd)
	if err != nil {
		return nil, err
	}
	c, _, err := loadFor(r)
	if err != nil {
		return nil, err
	}
	// A user file wt cannot read costs the integrations, not the command:
	// every one of them is off for the run, because a file the person wrote
	// and wt cannot parse is no licence to fall back to a default that
	// switches one on. A file that parses with one bad key keeps its good
	// keys. WarnTo says which it was, once, on stderr, and `wt doctor` counts
	// it as a problem.
	u, userErr := config.LoadUser()
	return &Context{Repo: r, Config: c, User: u, UserError: userErr, Cwd: cwd}, nil
}

// Scheme is how this repository spells its worktrees: the directory they sit
// under, the repository's name and its type suffix. Every conversion between a
// piece of work, its branch and its path is made through it.
func (c *Context) Scheme() naming.Scheme {
	return naming.Scheme{Parent: c.Repo.Parent, Repo: c.Repo.Name, Suffix: c.Config.TypeSuffix}
}

// UserConfig is the person's own settings, defaulted for a Context assembled
// by hand.
func (c *Context) UserConfig() *config.User {
	if c.User == nil {
		return config.DefaultUser()
	}
	return c.User
}

// HasProvisionScript reports whether the repo declares its own setup step.
func (c *Context) HasProvisionScript() bool {
	info, err := os.Stat(filepath.Join(c.Repo.Root, "bin", "worktree", "provision.sh"))
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// loadFor loads a repository's configuration, preferring the worktree the
// caller is standing in and falling back to the main checkout when that
// worktree carries none. The trunk it detected comes back with it: reading it
// costs a git process, and everything that needs a fallback branch here needs
// the same answer.
func loadFor(r *repo.Repo) (*config.Config, string, error) {
	trunk := r.DetectMainBranch()
	c, err := config.Load(r.Root, trunk)
	if errors.Is(err, config.ErrNoConfig) && r.Root != r.MainRoot {
		c, err = config.Load(r.MainRoot, trunk)
	}
	return c, trunk, err
}

// OpenLenient builds a Context for read-only lookups, falling back to default
// configuration when the repository's own does not load — and saying so on w
// rather than swallowing it.
//
// This exists for `wt find`: a repository with a stale or invalid worktree.conf
// still has worktrees worth searching, and silently dropping to a repo-less
// search makes it return a match from somewhere else with no hint why. Returns
// nil only when cwd is not in a repository at all.
func OpenLenient(cwd string, w io.Writer) *Context {
	r, err := repo.Discover(cwd)
	if err != nil {
		return nil
	}
	u, userErr := config.LoadUser()
	c, trunk, err := loadFor(r)
	if err != nil {
		// Keep whatever did parse: a single retired key should not hide the
		// repository's REQUIRED_BINS, branch prefix and the rest.
		if c == nil {
			c, _ = config.FromRaw(nil, trunk)
		}
		fmt.Fprintf(w, "wt: using partial configuration for %s: %v\n", r.Name, err)
		return &Context{Repo: r, Config: c, ConfigError: err, User: u, UserError: userErr, Cwd: cwd}
	}
	return &Context{Repo: r, Config: c, User: u, UserError: userErr, Cwd: cwd}
}
