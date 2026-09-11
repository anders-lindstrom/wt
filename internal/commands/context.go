// Package commands implements the wt subcommands.
package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// Context is the repository and configuration a subcommand operates on.
type Context struct {
	Repo   *repo.Repo
	Config *config.Config
	// ConfigError records why Config fell back to defaults, when it did.
	// Only OpenLenient sets it; Open fails outright instead.
	ConfigError error
	// Cwd is the directory the context was opened from: where the caller is
	// standing.
	Cwd string
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
	return &Context{Repo: r, Config: c, Cwd: cwd}, nil
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
	c, trunk, err := loadFor(r)
	if err != nil {
		// Keep whatever did parse: a single retired key should not hide the
		// repository's REQUIRED_BINS, branch prefix and the rest.
		if c == nil {
			c, _ = config.FromRaw(nil, trunk)
		}
		fmt.Fprintf(w, "wt: using partial configuration for %s: %v\n", r.Name, err)
		return &Context{Repo: r, Config: c, ConfigError: err, Cwd: cwd}
	}
	return &Context{Repo: r, Config: c, Cwd: cwd}
}
