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
}

// Open discovers the repository containing cwd and loads its configuration.
func Open(cwd string) (*Context, error) {
	r, err := repo.Discover(cwd)
	if err != nil {
		return nil, err
	}
	c, err := loadFor(r)
	if err != nil {
		return nil, err
	}
	return &Context{Repo: r, Config: c}, nil
}

// HasProvisionScript reports whether the repo declares its own setup step.
func (c *Context) HasProvisionScript() bool {
	info, err := os.Stat(filepath.Join(c.Repo.Root, "bin", "worktree", "provision.sh"))
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// loadFor loads a repository's configuration, preferring the worktree the
// caller is standing in and falling back to the main checkout when that
// worktree carries none.
func loadFor(r *repo.Repo) (*config.Config, error) {
	c, err := config.Load(r.Root, r.DetectMainBranch())
	if errors.Is(err, config.ErrNoConfig) && r.Root != r.MainRoot {
		return config.Load(r.MainRoot, r.DetectMainBranch())
	}
	return c, err
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
	c, err := loadFor(r)
	if err != nil {
		// Keep whatever did parse: a single retired key should not hide the
		// repository's REQUIRED_BINS, branch prefix and the rest.
		if c == nil {
			c, _ = config.FromRaw(nil, r.DetectMainBranch())
		}
		fmt.Fprintf(w, "wt: using partial configuration for %s: %v\n", r.Name, err)
		return &Context{Repo: r, Config: c, ConfigError: err}
	}
	return &Context{Repo: r, Config: c}
}
