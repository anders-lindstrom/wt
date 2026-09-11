package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/BurntSushi/toml"
)

// ErrNoConfig means the repository declares no worktree configuration. The
// message names the way out: this is the one error a repository that has never
// used wt is guaranteed to hit, and every other command refuses to run until
// it is gone.
var ErrNoConfig = errors.New(
	"no bin/worktree/worktree.conf or worktree.toml — run `wt init` to create one")

// Load reads a repository's worktree configuration, preferring worktree.toml.
func Load(repoRoot, mainBranchFallback string) (*Config, error) {
	dir := filepath.Join(repoRoot, "bin", "worktree")

	tomlPath := filepath.Join(dir, "worktree.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		return loadTOML(tomlPath, mainBranchFallback)
	}

	confPath := filepath.Join(dir, "worktree.conf")
	f, err := os.Open(confPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoConfig
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	raw, err := ParseBash(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", confPath, err)
	}
	return FromRaw(raw, mainBranchFallback)
}

// loadTOML reads the typed format, which a repo can opt into instead of the
// bash-subset one. The keys table says which TOML key carries which setting
// and how to read it, so the two formats cannot drift apart. Values are
// decoded one at a time, in the file's own order, which is the order a typed
// decode reported its problems in. A key wt does not read is ignored, as it
// always has been here.
func loadTOML(path, mainBranchFallback string) (*Config, error) {
	var doc map[string]toml.Primitive
	md, err := toml.DecodeFile(path, &doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	byTOML := make(map[string]key, len(keys))
	for _, k := range keys {
		byTOML[k.TOML] = k
	}
	raw := map[string]Value{}
	for _, name := range md.Keys() {
		k, ok := byTOML[name.String()]
		if !ok {
			continue
		}
		v, set, err := decodeTOML(md, doc[k.TOML], k.Kind)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if set {
			raw[k.Name] = v
		}
	}
	return FromRaw(raw, mainBranchFallback)
}

// decodeTOML reads one value as its kind says it is written. set is false for a
// value that says nothing — an empty string, or a list the file left out —
// which is how the bash format spells an absent key too.
func decodeTOML(md toml.MetaData, prim toml.Primitive, k kind) (v Value, set bool, err error) {
	switch k {
	case kindList:
		var l []string
		if err := md.PrimitiveDecode(prim, &l); err != nil {
			return Value{}, false, err
		}
		return Value{List: l, IsList: true}, l != nil, nil
	case kindBool:
		var b bool
		if err := md.PrimitiveDecode(prim, &b); err != nil {
			return Value{}, false, err
		}
		return Value{Scalar: strconv.FormatBool(b)}, true, nil
	default:
		var s string
		if err := md.PrimitiveDecode(prim, &s); err != nil {
			return Value{}, false, err
		}
		return Value{Scalar: s}, s != "", nil
	}
}
