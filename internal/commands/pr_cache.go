package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/anders-lindstrom/wt/internal/github"
)

// prCacheName is where the cached answers live: the main checkout's git dir,
// beside the keeper's state, so git throws them away with the repository.
const prCacheName = "wt-pr-cache.json"

// PRCacheTTL is how long an answer stands before it is asked again;
// `wt list --refresh` is the way past it. It does not govern a merged pull
// request: see merged.
var PRCacheTTL = 5 * time.Minute

// prCacheRetention drops an entry nothing has asked about for a month, so a
// repository's dead branches cannot grow the file without bound.
const prCacheRetention = 30 * 24 * time.Hour

// prCacheVersion is what the entries were written to mean. Bumping it is how
// a wt that asks GitHub a different question discards these answers rather
// than reading them as answers to its own.
const prCacheVersion = 1

// prCache is what GitHub last said about this repository's branches, one entry
// each. It carries the repository it was read from (the one gh resolves as the
// base, which on a fork is the parent), so a checkout whose remotes change
// never shows the other repository's pull requests.
type prCache struct {
	Version  int                     `json:"version"`
	Host     string                  `json:"host"`
	Slug     string                  `json:"slug"`
	Branches map[string]prCacheEntry `json:"branches"`
}

// prCacheEntry is one branch's answer and when it was read. PR is nil when
// GitHub had no pull request on that branch, which is worth remembering:
// otherwise every listing asks again for every branch that has none.
type prCacheEntry struct {
	Fetched time.Time  `json:"fetched"`
	PR      *github.PR `json:"pr,omitempty"`
}

// fresh is what the cache says about branch, read less than PRCacheTTL ago,
// and when it was read. found false means "ask GitHub"; found true with a zero
// PR is "there is none".
func (c prCache) fresh(branch string, now time.Time) (pr github.PR, fetched time.Time, found bool) {
	e, ok := c.Branches[branch]
	if !ok {
		return github.PR{}, time.Time{}, false
	}
	age := now.Sub(e.Fetched)
	if age < 0 || age > PRCacheTTL {
		return github.PR{}, time.Time{}, false
	}
	if e.PR == nil {
		return github.PR{}, e.Fetched, true
	}
	return *e.PR, e.Fetched, true
}

// merged is the pull request the cache holds for branch, at any age, when
// GitHub had already merged it.
//
// The TTL does not apply: it is about how fresh an open pull request's state
// is, and a merged one has none left to change. The base it merged into and
// the commit it carried are fixed, so however old the entry is it still
// answers whether the branch's work landed — which is what lets `wt remove`
// act on a squash merge with no process and no network.
func (c prCache) merged(branch string) (github.PR, bool) {
	e, ok := c.Branches[branch]
	if !ok || e.PR == nil || !e.PR.Merged() {
		return github.PR{}, false
	}
	return *e.PR, true
}

// prCachePath is the cache file, or "" when the git dir cannot be resolved.
func prCachePath(ctx *Context) string {
	dir, err := ctx.Repo.GitDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, prCacheName)
}

// readPRCache is what the file holds for this remote. No file, a corrupt one
// or a different remote is an empty cache: it never makes a command fail.
func readPRCache(ctx *Context, r github.Remote) prCache {
	path := prCachePath(ctx)
	if path == "" {
		return prCache{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return prCache{}
	}
	var c prCache
	if err := json.Unmarshal(data, &c); err != nil {
		return prCache{}
	}
	if c.Version != prCacheVersion || c.Host != r.Host || c.Slug != r.Slug {
		return prCache{}
	}
	return c
}

// writePRCache records what GitHub just said about the branches in answers, a
// nil value being "no pull request on this one", and keeps what the file held
// about the others. The write goes through a rename so a reader never sees
// half of it; an unwritable git dir costs the cache, not the command.
func writePRCache(ctx *Context, r github.Remote, answers map[string]*github.PR, now time.Time) {
	path := prCachePath(ctx)
	if path == "" {
		return
	}
	c := readPRCache(ctx, r)
	c.Version, c.Host, c.Slug = prCacheVersion, r.Host, r.Slug
	if c.Branches == nil {
		c.Branches = map[string]prCacheEntry{}
	}
	for branch, e := range c.Branches {
		if now.Sub(e.Fetched) > prCacheRetention {
			delete(c.Branches, branch)
		}
	}
	for branch, pr := range answers {
		c.Branches[branch] = prCacheEntry{Fetched: now, PR: pr}
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = writeThroughRename(path, data)
}

// writeThroughRename puts data at path through a temporary file beside it, so
// a reader sees the whole of one version or the whole of the other. A write
// that fails at either step takes its temporary file with it.
func writeThroughRename(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.%d", path, os.Getpid())
	// #nosec G703 -- path is prCachePath's: the git dir git itself reported,
	// plus a fixed file name. Nothing a caller passes reaches it.
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// prCacheWritable reports what stops the cache being written, or nil when it
// can be: `wt doctor` is where a listing that silently asks GitHub every time
// becomes visible. It writes what the file already holds, so a cache in use
// is left exactly as it was.
func prCacheWritable(ctx *Context) error {
	path := prCachePath(ctx)
	if path == "" {
		return errors.New("this repository's git dir cannot be resolved")
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Nothing cached yet: write one and take it away again, so asking the
		// question leaves the repository as it was.
		if err := writeThroughRename(path, []byte("{}")); err != nil {
			return err
		}
		return os.Remove(path)
	case err != nil:
		return err
	}
	return writeThroughRename(path, data)
}
