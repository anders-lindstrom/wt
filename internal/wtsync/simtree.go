package wtsync

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// resolvedTree writes each path's resolved bytes into tree and returns the
// tree that results. It is how the simulation carries a resolved stop
// forward: merge-tree writes a tree with conflict markers in it, and this
// replaces those blobs with what the strategies answered, without an index,
// a working tree or a ref being touched.
//
// The mode is whatever the merged tree already carries for the path, so an
// executable stays executable. A path the merged tree does not carry is an
// error rather than an addition: the simulation only ever replaces a file
// git itself put there.
//
// One divergence, stated because it cannot be closed here: a real rebase
// stages a strategy's answer with `git add`, which runs the repository's
// clean filters and end-of-line normalisation; this hashes the bytes as
// they are. A repository with a filter that rewrites resolver output would
// feed the next commit something the simulation did not model.
func resolvedTree(mainRoot, tree string, resolved map[string][]byte) (string, error) {
	if len(resolved) == 0 {
		return tree, nil
	}
	index, cleanup, err := scratchIndex()
	if err != nil {
		return "", err
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := gitEnv(mainRoot, env, nil, "read-tree", tree); err != nil {
		return "", fmt.Errorf("read-tree %s: %w", tree, err)
	}
	paths := make([]string, 0, len(resolved))
	for p := range resolved {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	modes, err := indexModes(mainRoot, env, paths)
	if err != nil {
		return "", err
	}
	var info strings.Builder
	for _, p := range paths {
		mode, ok := modes[p]
		if !ok {
			return "", fmt.Errorf("%s is not in the merged tree %s", p, tree)
		}
		oid, err := hashObject(mainRoot, resolved[p])
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&info, "%s %s\t%s\x00", mode, oid, p)
	}
	if _, err := gitEnv(mainRoot, env, strings.NewReader(info.String()), "update-index", "-z", "--index-info"); err != nil {
		return "", fmt.Errorf("update-index: %w", err)
	}
	out, err := gitEnv(mainRoot, env, nil, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write-tree: %w", err)
	}
	return out, nil
}

// indexModes reads the mode each path carries in the scratch index. Paths go
// after "--" as literal pathspecs and come back NUL-separated, so a name with
// a space, a quote or a glob character in it survives.
func indexModes(mainRoot string, env, paths []string) (map[string]string, error) {
	args := append([]string{"--literal-pathspecs", "ls-files", "--stage", "-z", "--"}, paths...)
	out, err := gitEnv(mainRoot, env, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("ls-files --stage: %w", err)
	}
	modes := map[string]string{}
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 {
			continue
		}
		modes[path] = f[0]
	}
	return modes, nil
}

// scratchIndex is a private index file git creates for itself. The file must
// not exist when git first writes it: an empty file is not a valid index.
func scratchIndex() (string, func(), error) {
	dir, err := os.MkdirTemp("", "wtsync-simindex-")
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(dir, "index"), func() { _ = os.RemoveAll(dir) }, nil
}
