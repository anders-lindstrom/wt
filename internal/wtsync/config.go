// Package wtsync keeps worktrees rebased on trunk: it reads a repository's
// declaration of its conflict shapes, resolves those shapes deterministically,
// and simulates every rebase before anything is touched.
package wtsync

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
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

// Strategies and value rules a declaration may name.
var (
	strategies = map[string]bool{"owned-line": true, "openapi": true, "list-union": true, "take-trunk": true, "script": true}
	valueRules = map[string]bool{"max-plus-patch": true, "keep-branch": true, "keep-trunk": true}
)

// LoadFromTrunk reads the declaration from origin/<trunk>, never from a
// working tree: the file names executables, and a feature branch must not be
// able to change what runs unattended.
func LoadFromTrunk(mainRoot, trunk string) (*Config, error) {
	out, err := git.Run(mainRoot, "show", "origin/"+trunk+":"+ConfigFile)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk, but not in") {
			return nil, ErrNoConfig
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
		if !strategies[r.Strategy] {
			return nil, fmt.Errorf("%s: conflicts[%d]: unknown strategy %q", ConfigFile, i, r.Strategy)
		}
		switch r.Strategy {
		case "owned-line":
			if r.Line == "" || r.Rule == "" {
				return nil, fmt.Errorf("%s: conflicts[%d]: owned-line needs line and rule", ConfigFile, i)
			}
			if _, err := regexp.Compile(r.Line); err != nil {
				return nil, fmt.Errorf("%s: conflicts[%d]: bad line regex %q: %w", ConfigFile, i, r.Line, err)
			}
		case "openapi":
			if r.Rule == "" {
				r.Rule = "max-plus-patch"
			}
		case "list-union":
			if r.Line == "" {
				return nil, fmt.Errorf("%s: conflicts[%d]: list-union needs line", ConfigFile, i)
			}
			if _, err := regexp.Compile(r.Line); err != nil {
				return nil, fmt.Errorf("%s: conflicts[%d]: bad line regex %q: %w", ConfigFile, i, r.Line, err)
			}
			if r.Delimiter == "" {
				r.Delimiter = ","
			}
		case "script":
			if r.Run == "" {
				return nil, fmt.Errorf("%s: conflicts[%d]: script needs run", ConfigFile, i)
			}
			if path.IsAbs(r.Run) || escapesRoot(r.Run) {
				return nil, fmt.Errorf("%s: conflicts[%d]: script run %q must be relative to the root, with no parent-directory segments", ConfigFile, i, r.Run)
			}
		}
		if r.Rule != "" && !valueRules[r.Rule] {
			return nil, fmt.Errorf("%s: conflicts[%d]: unknown rule %q", ConfigFile, i, r.Rule)
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
