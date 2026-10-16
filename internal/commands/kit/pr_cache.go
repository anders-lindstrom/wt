package kit

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

// PRCacheRetention drops an entry nothing has asked about for a month, so a
// repository's dead branches cannot grow the file without bound.
const PRCacheRetention = 30 * 24 * time.Hour

// prCacheVersion is what the entries were written to mean. Bumping it is how
// a wt that asks GitHub a different question discards these answers rather
// than reading them as answers to its own.
const prCacheVersion = 1

// PRCache is what GitHub last said about this repository's branches, one entry
// each. It carries the repository it was read from (the one gh resolves as the
// base, which on a fork is the parent), so a checkout whose remotes change
// never shows the other repository's pull requests.
type PRCache struct {
	Version  int                     `json:"version"`
	Host     string                  `json:"host"`
	Slug     string                  `json:"slug"`
	Branches map[string]prCacheEntry `json:"branches"`
}

// prCacheEntry is one branch's answer and when it was read. PR is nil when
// GitHub had no pull request on that branch, which is worth remembering:
// otherwise every listing asks again for every branch that has none. Checks
// says the checks were asked for with it, whether or not GitHub showed them.
type prCacheEntry struct {
	Fetched time.Time  `json:"fetched"`
	PR      *github.PR `json:"pr,omitempty"`
	Checks  bool       `json:"checks,omitempty"`
}

// Fresh is what the cache says about branch, read less than PRCacheTTL ago,
// and when it was read. found false means "ask GitHub"; found true with a zero
// PR is "there is none". A pull request read without its checks does not
// answer a lookup that wants them.
func (c PRCache) Fresh(branch string, now time.Time, checks bool) (pr github.PR, fetched time.Time, found bool) {
	e, ok := c.Branches[branch]
	if !ok {
		return github.PR{}, time.Time{}, false
	}
	age := now.Sub(e.Fetched)
	if age < 0 || age > PRCacheTTL {
		return github.PR{}, time.Time{}, false
	}
	if checks && e.PR != nil && !e.Checks {
		return github.PR{}, time.Time{}, false
	}
	if e.PR == nil {
		return github.PR{}, e.Fetched, true
	}
	return *e.PR, e.Fetched, true
}

// Merged is the pull request the cache holds for branch, at any age, when
// GitHub had already merged it.
//
// The TTL does not apply: it is about how fresh an open pull request's state
// is, and a merged one has none left to change. The base it merged into and
// the commit it carried are fixed, so however old the entry is it still
// answers whether the branch's work landed — which is what lets `wt remove`
// act on a squash merge with no process and no network.
func (c PRCache) Merged(branch string) (github.PR, bool) {
	e, ok := c.Branches[branch]
	if !ok || e.PR == nil || !e.PR.Merged() {
		return github.PR{}, false
	}
	return *e.PR, true
}

// PRCachePath is the cache file, or "" when the git dir cannot be resolved.
func PRCachePath(ctx *Context) string {
	dir, err := ctx.Repo.GitDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, prCacheName)
}

// ReadPRCache is what the file holds for this remote. No file, a corrupt one
// or a different remote is an empty cache: it never makes a command fail.
func ReadPRCache(ctx *Context, r github.Remote) PRCache {
	path := PRCachePath(ctx)
	if path == "" {
		return PRCache{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return PRCache{}
	}
	var c PRCache
	if err := json.Unmarshal(data, &c); err != nil {
		return PRCache{}
	}
	if c.Version != prCacheVersion || c.Host != r.Host || c.Slug != r.Slug {
		return PRCache{}
	}
	return c
}

// WritePRCache records what GitHub just said about the branches in answers, a
// nil value being "no pull request on this one", and keeps what the file held
// about the others. The write goes through a rename so a reader never sees
// half of it; an unwritable git dir costs the cache, not the command.
func WritePRCache(ctx *Context, r github.Remote, answers map[string]*github.PR, now time.Time) {
	WritePRCacheChecks(ctx, r, answers, now, false)
}

// WritePRCacheChecks is writePRCache for answers read with the checks asked
// for, when checks is true.
func WritePRCacheChecks(ctx *Context, r github.Remote, answers map[string]*github.PR, now time.Time, checks bool) {
	path := PRCachePath(ctx)
	if path == "" {
		return
	}
	c := ReadPRCache(ctx, r)
	c.Version, c.Host, c.Slug = prCacheVersion, r.Host, r.Slug
	if c.Branches == nil {
		c.Branches = map[string]prCacheEntry{}
	}
	for branch, e := range c.Branches {
		if now.Sub(e.Fetched) > PRCacheRetention {
			delete(c.Branches, branch)
		}
	}
	for branch, pr := range answers {
		c.Branches[branch] = prCacheEntry{Fetched: now, PR: pr, Checks: checks}
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
	path := PRCachePath(ctx)
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
