// Package wtsync keeps worktrees rebased on trunk: it reads a repository's
// declaration of its conflict shapes, resolves those shapes deterministically,
// and simulates every rebase before anything is touched.
package wtsync

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/anders-lindstrom/wt/internal/git"
)

// ConfigFile is the declaration at the top of a repository.
const ConfigFile = ".wt-sync.yaml"

// ErrNoConfig means trunk carries no declaration: the repository is reported
// by wt sync and never rebased.
var ErrNoConfig = errors.New("no " + ConfigFile + " on trunk")

// Rule declares that files matching Paths have one conflict shape.
type Rule struct {
	Paths    []string `yaml:"paths"`
	Strategy string   `yaml:"strategy"`
	// Line is the regex of the owned line (owned-line) or the list line
	// (list-union).
	Line string `yaml:"line"`
	// Rule decides the value of an owned line or an OpenAPI version:
	// max-plus-patch, keep-branch or keep-trunk.
	Rule string `yaml:"rule"`
	// Delimiter separates list-union items; "," when omitted.
	Delimiter string `yaml:"delimiter"`
	// Run is the executable of a script strategy, relative to the root.
	Run string `yaml:"run"`

	// line is Line compiled and value is Rule resolved, both filled in by
	// Parse: a declaration is checked once, and nothing downstream compiles
	// or re-resolves them. Both are nil on a Rule built by hand.
	line  *regexp.Regexp
	value ValueRule
}

// Deferred is work that runs once after the last commit is replayed.
type Deferred struct {
	Run string `yaml:"run"`
	// Paths gate the step on what the rebase changed; empty means always.
	Paths []string `yaml:"paths"`
	// Commit is the message for the step's output, when it changes tracked
	// files; empty means the output is never committed.
	Commit string `yaml:"commit"`
}

// Config is one repository's .wt-sync.yaml.
type Config struct {
	Conflicts       []Rule     `yaml:"conflicts"`
	Defer           []Deferred `yaml:"defer"`
	DependencyGraph []string   `yaml:"dependency_graph"`
}

// LoadFromTrunk reads the declaration from origin/<trunk>, never from a
// working tree: the file names executables, and a feature branch must not be
// able to change what runs unattended.
func LoadFromTrunk(mainRoot, trunk string) (*Config, error) {
	return LoadFromRef(mainRoot, "origin/"+trunk)
}

// LoadFromRef reads the declaration from any ref. A run passes the SHA it
// pinned, so the declaration comes from the same trunk as every rebase
// target even when someone else fetches underneath it mid-run.
func LoadFromRef(mainRoot, ref string) (*Config, error) {
	out, err := git.Run(mainRoot, "show", ref+":"+ConfigFile)
	if err != nil {
		var gerr *git.Error
		if errors.As(err, &gerr) && !gerr.TimedOut {
			switch said := gerr.Stderr; {
			case strings.Contains(said, "does not exist") || strings.Contains(said, "exists on disk, but not in"):
				return nil, ErrNoConfig
			case strings.Contains(said, "invalid object name") || strings.Contains(said, "unknown revision"):
				return nil, fmt.Errorf("%s is not known here; run git fetch origin", ref)
			}
		}
		return nil, err
	}
	return Parse([]byte(out))
}

// Parse decodes and validates a declaration, filling in defaults.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// An empty file decodes to an empty declaration; io.EOF is not an error.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	for i := range cfg.Conflicts {
		r := &cfg.Conflicts[i]
		if len(r.Paths) == 0 {
			return nil, fmt.Errorf("%s: conflicts[%d] has no paths", ConfigFile, i)
		}
		k, ok := kinds[r.Strategy]
		if !ok {
			return nil, fmt.Errorf("%s: conflicts[%d]: unknown strategy %q", ConfigFile, i, r.Strategy)
		}
		if err := k.prepare(r); err != nil {
			return nil, fmt.Errorf("%s: conflicts[%d]: %w", ConfigFile, i, err)
		}
		if r.Rule != "" {
			v, err := RuleNamed(r.Rule)
			if err != nil {
				return nil, fmt.Errorf("%s: conflicts[%d]: %w", ConfigFile, i, err)
			}
			r.value = v
		}
	}
	for i, d := range cfg.Defer {
		if d.Run == "" {
			return nil, fmt.Errorf("%s: defer[%d] has no run", ConfigFile, i)
		}
	}
	return &cfg, nil
}

// escapesRoot reports whether p contains a ".." segment, which would let a
// script strategy reach outside the repository root.
func escapesRoot(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// RuleFor returns the first rule claiming path.
func (c *Config) RuleFor(path string) (Rule, bool) {
	for _, r := range c.Conflicts {
		if matchesAny(r.Paths, path) {
			return r, true
		}
	}
	return Rule{}, false
}

// MatchGlob matches a root-relative path against a pattern where `*` spans
// one path segment and `**` spans any number, including none. Patterns are
// matched, never expanded against the disk.
func MatchGlob(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

// matchesAny reports whether path matches any of patterns.
func matchesAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if MatchGlob(p, path) {
			return true
		}
	}
	return false
}

func matchSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegments(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if !matchSegment(pat[0], segs[0]) {
		return false
	}
	return matchSegments(pat[1:], segs[1:])
}

// matchSegment matches one segment where `*` spans any characters.
func matchSegment(pat, seg string) bool {
	parts := strings.Split(pat, "*")
	if len(parts) == 1 {
		return pat == seg
	}
	if !strings.HasPrefix(seg, parts[0]) {
		return false
	}
	seg = seg[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(seg, part)
		if i < 0 {
			return false
		}
		seg = seg[i+len(part):]
	}
	return strings.HasSuffix(seg, parts[len(parts)-1])
}
