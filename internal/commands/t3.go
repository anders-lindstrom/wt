package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// t3WorktreesDir is the directory T3 Code creates worktrees under, read the
// way T3 reads it: its data directory is $T3CODE_HOME or ~/.t3, and the
// worktreesDirectory in <data>/userdata/settings.json moves the worktrees out
// of <data>/worktrees, with a leading ~ for the home directory. T3 keeps no
// setting per project, so one directory holds every repository's, each in a
// folder named after its main checkout. Empty when no home directory is
// known, or the setting is not an absolute path, which T3 refuses too.
func t3WorktreesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	base := os.Getenv("T3CODE_HOME")
	if strings.TrimSpace(base) == "" {
		base = filepath.Join(home, ".t3")
	}
	dir := filepath.Join(base, "worktrees")
	raw, err := os.ReadFile(filepath.Join(base, "userdata", "settings.json")) //nolint:gosec // T3's own data directory, named as T3 names it
	if err != nil {
		return dir
	}
	var settings struct {
		WorktreesDirectory string `json:"worktreesDirectory"`
	}
	if json.Unmarshal(raw, &settings) != nil {
		return dir
	}
	setting := strings.TrimSpace(settings.WorktreesDirectory)
	switch {
	case setting == "":
		return dir
	case setting == "~":
		setting = home
	case strings.HasPrefix(setting, "~/"):
		setting = filepath.Join(home, setting[2:])
	}
	if !filepath.IsAbs(setting) {
		return ""
	}
	setting = filepath.Clean(setting)
	if filepath.Dir(setting) == setting {
		return ""
	}
	return setting
}

// t3Root is where T3 Code puts the worktrees of the repository whose main
// checkout is named repoName, or empty where that is not known. Symlinks in
// it are resolved, because git lists a worktree by its real path.
func t3Root(repoName string) string {
	dir := t3WorktreesDir()
	if dir == "" || repoName == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return filepath.Join(dir, repoName)
}
