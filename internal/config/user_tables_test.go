package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Both tables are read in the file's own order, which is the order wt repos
// and every --all run go through the roots.
func TestRootsAndProfilesAreReadInTheFilesOrder(t *testing.T) {
	u, err := loadUser(writeUser(t, "github = false\n\n[roots]\nzeta = \"~/z\"\nalpha = \"/a\"\n\n"+
		"[profiles]\nbackend = [\"~/z/server\", \"/a/api\"]\n"))
	if err != nil {
		t.Fatalf("loadUser: %v", err)
	}
	if u.GitHub {
		t.Error("the top-level key above the tables was lost")
	}
	if len(u.Roots) != 2 || u.Roots[0] != (NamedRoot{"zeta", "~/z"}) || u.Roots[1] != (NamedRoot{"alpha", "/a"}) {
		t.Errorf("roots = %+v", u.Roots)
	}
	p, ok := u.Profile("backend")
	if !ok || !slices.Equal(p.Repos, []string{"~/z/server", "/a/api"}) {
		t.Errorf("profile = %+v, %v", p, ok)
	}
	if v, err := u.Value("root.alpha"); err != nil || v != "/a" {
		t.Errorf("Value(root.alpha) = %q, %v", v, err)
	}
	if _, err := u.Value("root.nope"); err == nil {
		t.Error("a root the file does not name is an error")
	}
}

// A table of the wrong shape is reported and the rest of the file kept.
func TestABadTableKeepsTheRestOfTheFile(t *testing.T) {
	u, err := loadUser(writeUser(t, "superset = true\n[roots]\nx = 3\n"))
	if err == nil || !strings.Contains(err.Error(), "[roots]") {
		t.Fatalf("want the bad table named, got %v", err)
	}
	if !u.Superset || len(u.Roots) != 0 {
		t.Errorf("want superset kept and no roots: %+v", u)
	}
}

// Set and unset write inside the table, keep the file's comments and its
// top-level keys, and add a missing table at the end.
func TestSetAndUnsetATableEntryKeepTheFilesShape(t *testing.T) {
	path := setIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# mine\nsuperset = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{
		{"root.telcred", "~/src/telcred"},
		{"root.dotfiles", "~/dotfiles"},
		{"profile.backend", "~/src/telcred/server ~/src/telcred/api"},
		{"root.telcred", "~/work/telcred"},
		{"github", "false"},
	} {
		if _, _, err := SetUser(kv[0], kv[1]); err != nil {
			t.Fatalf("SetUser(%s): %v", kv[0], err)
		}
	}
	want := "# mine\nsuperset = true\ngithub = false\n\n[roots]\ntelcred = \"~/work/telcred\"\n" +
		"dotfiles = \"~/dotfiles\"\n\n[profiles]\nbackend = [\"~/src/telcred/server\", \"~/src/telcred/api\"]\n"
	if got := readBack(t, path); got != want {
		t.Fatalf("file:\n%s\nwant:\n%s", got, want)
	}
	if _, removed, err := UnsetUser("root.dotfiles"); err != nil || !removed {
		t.Fatalf("UnsetUser: %v, %v", removed, err)
	}
	if _, removed, _ := UnsetUser("root.dotfiles"); removed {
		t.Error("a second unset found something to remove")
	}
	if got := readBack(t, path); strings.Contains(got, "dotfiles") || !strings.Contains(got, "telcred = ") {
		t.Errorf("unset took the wrong line:\n%s", got)
	}
}

func TestSetRefusesABadTableEntryWithoutWriting(t *testing.T) {
	path := setIn(t)
	for _, kv := range [][2]string{{"root.has space", "/x"}, {"root.ok", "  "}, {"profile.p", ""}} {
		if _, _, err := SetUser(kv[0], kv[1]); err == nil {
			t.Errorf("SetUser(%q, %q) wrote it", kv[0], kv[1])
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a refused value created the file")
	}
}

// A table written in a shape the line writer cannot edit — a value over
// several lines, a dotted table — is refused with the file untouched, rather
// than left half-edited and unreadable.
func TestSetRefusesATableItCannotEditLineByLine(t *testing.T) {
	for name, body := range map[string]string{
		"multi-line array": "[profiles]\nbackend = [\n  \"~/a\",\n]\n",
		"dotted table":     "roots.work = \"~/w\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := setIn(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			key := "profile.backend"
			if name == "dotted table" {
				key = "root.work"
			}
			if _, _, err := SetUser(key, "~/b"); err == nil || !strings.Contains(err.Error(), "edit it by hand") {
				t.Errorf("want a refusal, got %v", err)
			}
			if got := readBack(t, path); got != body {
				t.Errorf("file changed:\n%s", got)
			}
		})
	}
}

// A header written with spaces inside the brackets is the same table.
func TestSetFindsATableHeaderWithSpaces(t *testing.T) {
	path := setIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[ roots ]\na = \"/a\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SetUser("root.b", "/b"); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, path); got != "[ roots ]\na = \"/a\"\nb = \"/b\"\n" {
		t.Errorf("file:\n%s", got)
	}
}
