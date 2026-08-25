// Package commands implements the wt subcommands.
package commands

import (
	"os"
	"path/filepath"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// Context is the repository and configuration a subcommand operates on.
type Context struct {
	Repo   *repo.Repo
	Config *config.Config
}

// Open discovers the repository containing cwd and loads its configuration.
func Open(cwd string) (*Context, error) {
	r, err := repo.Discover(cwd)
	if err != nil {
		return nil, err
	}
	c, err := config.Load(r.MainRoot, r.DetectMainBranch())
	if err != nil {
		return nil, err
	}
	return &Context{Repo: r, Config: c}, nil
}

// HasProvisionScript reports whether the repo declares its own setup step.
func (c *Context) HasProvisionScript() bool {
	info, err := os.Stat(filepath.Join(c.Repo.MainRoot, "bin", "worktree", "provision.sh"))
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// loadFor loads another repository's configuration without building a Context.
func loadFor(r *repo.Repo) (*config.Config, error) {
	return config.Load(r.MainRoot, r.DetectMainBranch())
}
