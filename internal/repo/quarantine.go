package repo

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
)

// AdminDir is a linked worktree's own git dir, .git/worktrees/<id>: what
// registers it, and where its index, HEAD, lock and submodule repositories
// live. The main checkout has none.
func AdminDir(wtPath string) (string, error) {
	dir, err := gitDirOf(wtPath)
	if err != nil {
		return "", err
	}
	dir = filepath.Clean(dir)
	if filepath.Base(filepath.Dir(dir)) != "worktrees" {
		return "", fmt.Errorf("%s is not a linked worktree: its git dir is %s", wtPath, dir)
	}
	return dir, nil
}

// LockWorktree takes git's worktree lock on path, leaving reason.
func (r *Repo) LockWorktree(path, reason string) error {
	_, err := git.Run(r.MainRoot, "worktree", "lock", "--reason", reason, path)
	return err
}

// ConfigEntry is one key and value of the local config, as git lists it.
type ConfigEntry struct{ Key, Value string }

// BranchConfig is the branch's section of the local config file: every
// value in order, multi-valued keys kept, includes not followed — what git
// itself removes with the branch. Empty when there is none.
func (r *Repo) BranchConfig(name string) ([]ConfigEntry, error) {
	out, err := git.Exec(git.Opts{Dir: r.MainRoot}, "config", "--local", "-z", "--get-regexp",
		`^branch\.`+regexp.QuoteMeta(name)+`\.[^.]+$`)
	if _, aerr := git.Answer(err, 1); aerr != nil {
		return nil, aerr
	}
	var list []ConfigEntry
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry == "" {
			continue
		}
		key, value, _ := strings.Cut(entry, "\n")
		list = append(list, ConfigEntry{Key: key, Value: value})
	}
	return list, nil
}

// BeforeConfigAdd runs, when set, before AddConfig writes, and an error from
// it is AddConfig's. Tests set it; nothing else does.
var BeforeConfigAdd func(key, value string) error

// AddConfig appends one value to the local config, as git config --add.
func (r *Repo) AddConfig(key, value string) error {
	if BeforeConfigAdd != nil {
		if err := BeforeConfigAdd(key, value); err != nil {
			return err
		}
	}
	_, err := git.Run(r.MainRoot, "config", "--local", "--add", key, value)
	return err
}
