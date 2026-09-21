package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points XDG_CONFIG_HOME at a directory of this run's own, so no
// test here reads or writes the user config of whoever runs them.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wt-config-test")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// A missing file means the built-in defaults.
func TestUserDefaultsWithNoFile(t *testing.T) {
	u, err := loadUser(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatalf("loadUser: %v", err)
	}
	if u.Exists {
		t.Error("a file that is not there was reported as existing")
	}
	if u.Superset {
		t.Error("superset defaults to true; it must be opt-in")
	}
	if !u.GitHub {
		t.Error("github defaults to false; it must be opt-out")
	}
	for _, k := range UserKeyNames() {
		if got := u.Origin(k); got != "default" {
			t.Errorf("Origin(%q) = %q, want default", k, got)
		}
	}
}

func writeUser(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUserFileOverridesTheDefaults(t *testing.T) {
	u, err := loadUser(writeUser(t, "superset = true\ngithub = false\nbranch_suffix = \"\"\n"))
	if err != nil {
		t.Fatalf("loadUser: %v", err)
	}
	if !u.Superset || u.GitHub || u.BranchSuffix != "" {
		t.Errorf("superset = %v, github = %v, branch_suffix = %q; want true, false, \"\"",
			u.Superset, u.GitHub, u.BranchSuffix)
	}
	for _, k := range UserKeyNames() {
		if got := u.Origin(k); got != "user file" {
			t.Errorf("Origin(%q) = %q, want user file", k, got)
		}
	}
}

// `wt config` prints where each value came from, so the two must be
// distinguishable per key.
func TestUserOriginIsPerKey(t *testing.T) {
	u, err := loadUser(writeUser(t, "superset = true\n"))
	if err != nil {
		t.Fatalf("loadUser: %v", err)
	}
	if u.Origin(UserKeySuperset) != "user file" {
		t.Errorf("superset origin = %q", u.Origin(UserKeySuperset))
	}
	if u.Origin(UserKeyGitHub) != "default" {
		t.Errorf("github origin = %q", u.Origin(UserKeyGitHub))
	}
}

// A misspelled key is an error naming the key, as worktree.toml treats one.
func TestUserUnknownKeyIsAnError(t *testing.T) {
	u, err := loadUser(writeUser(t, "superst = true\n"))
	if err == nil {
		t.Fatal("a misspelled key was accepted")
	}
	if !strings.Contains(err.Error(), `unknown key "superst"`) {
		t.Errorf("error does not name the key: %v", err)
	}
	if !strings.Contains(err.Error(), "superset") {
		t.Errorf("error does not say what the keys are: %v", err)
	}
	if u == nil || u.GitHub != true {
		t.Error("the defaults did not come back with the error")
	}
}

func TestUserNonBooleanValueIsAnError(t *testing.T) {
	_, err := loadUser(writeUser(t, `superset = "yes"`+"\n"))
	if err == nil || !strings.Contains(err.Error(), "not a boolean") {
		t.Errorf("err = %v, want a complaint about the type", err)
	}
}

func TestUserPathFollowsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	got, err := UserPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("/xdg", "wt", "config.toml") {
		t.Errorf("UserPath = %q", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory here")
	}
	got, err = UserPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "wt", "config.toml"); got != want {
		t.Errorf("UserPath = %q, want %q", got, want)
	}
}

// setIn points the user path at a fresh directory and returns it.
func setIn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "wt", "config.toml")
}

func TestSetUserCreatesTheFile(t *testing.T) {
	want := setIn(t)
	path, set, err := SetUser(UserKeySuperset, "true")
	if err != nil {
		t.Fatalf("SetUser: %v", err)
	}
	if path != want {
		t.Errorf("wrote %q, want %q", path, want)
	}
	if set != "true" {
		t.Errorf("SetUser reported %q for true", set)
	}
	u, err := loadUser(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if !u.Superset || !u.Exists {
		t.Errorf("read back superset = %v, exists = %v", u.Superset, u.Exists)
	}
}

// An existing file is edited, not regenerated.
func TestSetUserKeepsCommentsAndOrder(t *testing.T) {
	path := setIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# my notes\ngithub = false  # the CLI is not installed here\nsuperset = false\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SetUser(UserKeySuperset, "true"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# my notes\ngithub = false  # the CLI is not installed here\nsuperset = true\n"
	if string(got) != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}
}

func TestSetUserRejectsBadInputWithoutWriting(t *testing.T) {
	path := setIn(t)
	for name, tc := range map[string]struct{ key, value, want string }{
		"unknown key": {"superst", "true", `unknown key "superst"`},
		"not a bool":  {UserKeySuperset, "maybe", "is not a boolean"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := SetUser(tc.key, tc.value)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("a rejected set created the file anyway")
			}
		})
	}
}

func TestUnsetUserRemovesOnlyThatKey(t *testing.T) {
	setIn(t)
	if _, _, err := SetUser(UserKeySuperset, "true"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SetUser(UserKeyGitHub, "false"); err != nil {
		t.Fatal(err)
	}
	path, removed, err := UnsetUser(UserKeySuperset)
	if err != nil || !removed {
		t.Fatalf("UnsetUser: removed = %v, err = %v", removed, err)
	}
	u, err := loadUser(path)
	if err != nil {
		t.Fatal(err)
	}
	if u.Superset {
		t.Error("superset survived the unset")
	}
	if u.Origin(UserKeySuperset) != "default" {
		t.Error("superset is still reported as coming from the file")
	}
	if u.GitHub {
		t.Error("unsetting superset took github with it")
	}
}

// Removing what was never written is a no-op.
func TestUnsetUserOnAnAbsentKeyIsNotAnError(t *testing.T) {
	setIn(t)
	_, removed, err := UnsetUser(UserKeyGitHub)
	if err != nil {
		t.Fatalf("UnsetUser: %v", err)
	}
	if removed {
		t.Error("reported removing a key from a file that does not exist")
	}
}

func TestUnsetUserRejectsAnUnknownKey(t *testing.T) {
	setIn(t)
	if _, _, err := UnsetUser("superst"); err == nil {
		t.Fatal("accepted an unknown key")
	}
}

// A file wt cannot parse at all turns every integration off, whatever each
// one's own default is: the person wrote something in that file, and wt does
// not know what. The alternative would hand someone who wrote `github = false`
// a GitHub integration back because of a typo two lines below it.
func TestUserFileThatDoesNotParseTurnsEverythingOff(t *testing.T) {
	path := writeUser(t, "github = false\nsuperset = \n")
	u, err := loadUser(path)
	if err == nil {
		t.Fatal("a file that is not TOML was accepted")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want it to name the file", err)
	}
	if !u.Exists || !u.Unusable {
		t.Errorf("exists = %v, unusable = %v; want both true", u.Exists, u.Unusable)
	}
	if u.GitHub || u.Superset {
		t.Errorf("superset = %v, github = %v; want every integration off", u.Superset, u.GitHub)
	}
	for _, k := range UserKeyNames() {
		if got := u.Origin(k); got != "file unreadable" {
			t.Errorf("Origin(%q) = %q, want file unreadable", k, got)
		}
	}
}

// A file that parses with one bad key keeps its good keys: the line that says
// what it means is still the person's instruction.
func TestUserFileWithOneBadKeyKeepsTheRest(t *testing.T) {
	u, err := loadUser(writeUser(t, "superset = true\ngithbu = false\n"))
	if err == nil {
		t.Fatal("the unknown key was not reported")
	}
	if u.Unusable {
		t.Error("a file that parsed was treated as unreadable")
	}
	if !u.Superset {
		t.Error("the good key was dropped with the bad one")
	}
	if !u.GitHub {
		t.Error("a key the file never set lost its default")
	}
	if !strings.Contains(err.Error(), `unknown key "githbu"`) {
		t.Errorf("err = %v, want it to name the key", err)
	}
}

// The writer edits the top-level section and nothing else: a key of the same
// name inside a [table] belongs to that table.
func TestSetUserLeavesATablesKeysAlone(t *testing.T) {
	path := setIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "github = false\n\n[somebody-elses-tool]\ngithub = false\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SetUser(UserKeyGitHub, "true"); err != nil {
		t.Fatal(err)
	}
	got := readBack(t, path)
	if want := "github = true\n\n[somebody-elses-tool]\ngithub = false\n"; got != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}
}

// A new key goes at the end of the top-level section, not after a [table]
// header, where it would become that table's key.
func TestSetUserWritesANewKeyAboveTheFirstTable(t *testing.T) {
	path := setIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("superset = true\n\n[tool]\nx = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SetUser(UserKeyGitHub, "false"); err != nil {
		t.Fatal(err)
	}
	got := readBack(t, path)
	if want := "superset = true\ngithub = false\n\n[tool]\nx = 1\n"; got != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}
	u, err := loadUser(path)
	if err == nil {
		t.Fatal("the [tool] table is an unknown key and should be reported")
	}
	if u.GitHub {
		t.Errorf("github = true; the new line did not land at the top level:\n%s", got)
	}
}

// TOML lets a key be quoted, and `"github" = false` is the same assignment.
// Missing it appended a second one and left a file that no longer parses.
func TestSetUserFindsAQuotedKey(t *testing.T) {
	for name, body := range map[string]string{
		"double quoted": "\"github\" = false\n",
		"single quoted": "'github' = false\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := setIn(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := SetUser(UserKeyGitHub, "true"); err != nil {
				t.Fatal(err)
			}
			got := readBack(t, path)
			if strings.Count(got, "github") != 1 {
				t.Errorf("the key was written twice:\n%q", got)
			}
			u, err := loadUser(path)
			if err != nil {
				t.Fatalf("the file no longer parses: %v\n%s", err, got)
			}
			if !u.GitHub {
				t.Errorf("github = false after setting it true:\n%q", got)
			}
		})
	}
}

// The file's own shape survives a write: CRLF stays CRLF, and a file that
// ended without a newline still does.
func TestWriterKeepsTheFilesShape(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"CRLF":              {"superset = true\r\ngithub = true\r\n", "superset = true\r\ngithub = false\r\n"},
		"no final newline":  {"superset = true", "superset = true\ngithub = false"},
		"CRLF, no final nl": {"superset = true\r\ngithub = true", "superset = true\r\ngithub = false"},
	} {
		t.Run(name, func(t *testing.T) {
			path := setIn(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := SetUser(UserKeyGitHub, "false"); err != nil {
				t.Fatal(err)
			}
			if got := readBack(t, path); got != tc.want {
				t.Errorf("file = %q, want %q", got, tc.want)
			}
		})
	}
}

// A file wt cannot parse is not edited: the writer works line by line, and a
// line put into a file wt has not understood can only make it worse.
func TestWriterRefusesAFileThatDoesNotParse(t *testing.T) {
	path := setIn(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "github = \n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, write := range map[string]func() error{
		"set":   func() error { _, _, err := SetUser(UserKeyGitHub, "true"); return err },
		"unset": func() error { _, _, err := UnsetUser(UserKeyGitHub); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := write()
			if err == nil {
				t.Fatal("a file that does not parse was edited anyway")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("err = %v, want it to name the file", err)
			}
			if got := readBack(t, path); got != body {
				t.Errorf("the file was changed: %q", got)
			}
		})
	}
}

// true and false, and nothing else. ParseBool would also take 1, t, T and
// TRUE, none of which the help, the file or `wt config` ever writes.
func TestSetUserTakesOnlyTrueOrFalse(t *testing.T) {
	for _, value := range []string{"1", "0", "t", "f", "TRUE", "False", "yes"} {
		t.Run(value, func(t *testing.T) {
			path := setIn(t)
			if _, _, err := SetUser(UserKeyGitHub, value); err == nil {
				t.Errorf("%q was accepted as a boolean", value)
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("a rejected set wrote the file anyway")
			}
		})
	}
}

// A directory nothing can be written into is the error, not a silent no-op.
func TestSetUserReportsAnUnwritableDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wt"), []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
	if _, _, err := SetUser(UserKeyGitHub, "false"); err == nil {
		t.Fatal("SetUser reported success with nowhere to write")
	}
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The branch suffix is a string, and the empty one is a value: the file must
// carry it as written, and say that the person chose it.
func TestBranchSuffixIsWrittenAndReadBackAsAString(t *testing.T) {
	for _, value := range []string{"", "-wt", "_wt"} {
		t.Run("suffix "+value, func(t *testing.T) {
			path := setIn(t)
			_, set, err := SetUser(UserKeyBranchSuffix, value)
			if err != nil {
				t.Fatalf("SetUser: %v", err)
			}
			if want := `"` + value + `"`; set != want {
				t.Errorf("SetUser reported %s, want %s", set, want)
			}
			u, err := loadUser(path)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}
			if u.BranchSuffix != value || !u.IsSet(UserKeyBranchSuffix) {
				t.Errorf("read back %q, set = %v; want %q true", u.BranchSuffix, u.IsSet(UserKeyBranchSuffix), value)
			}
		})
	}
}

// A file that never names it is not a choice, so the repository decides.
func TestAnUnwrittenBranchSuffixIsNotSet(t *testing.T) {
	u, err := loadUser(writeUser(t, "github = true\n"))
	if err != nil {
		t.Fatalf("loadUser: %v", err)
	}
	if u.IsSet(UserKeyBranchSuffix) {
		t.Error("a key the file never wrote reads as chosen")
	}
	if u.BranchSuffix != DefaultTypeSuffix {
		t.Errorf("BranchSuffix = %q, want %q", u.BranchSuffix, DefaultTypeSuffix)
	}
}

// A branch suffix cannot carry what a branch name cannot.
func TestSetUserRefusesABranchSuffixWithASlashOrASpace(t *testing.T) {
	for _, value := range []string{"wt/", "_wt x"} {
		t.Run(value, func(t *testing.T) {
			path := setIn(t)
			if _, _, err := SetUser(UserKeyBranchSuffix, value); err == nil {
				t.Errorf("%q was accepted", value)
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("a rejected set wrote the file anyway")
			}
		})
	}
}

// A file wt cannot read costs the integrations, because switching one on
// under a misunderstanding is the harm. A branch has to be called something
// either way, so that setting stays out of it and the repository answers.
func TestAnUnreadableFileLeavesTheBranchSuffixToTheRepository(t *testing.T) {
	u, _ := loadUser(writeUser(t, "branch_suffix = \nsuperset = true\n"))
	if !u.Unusable {
		t.Fatal("the file was not treated as unreadable")
	}
	if u.BranchSuffix != DefaultTypeSuffix || u.IsSet(UserKeyBranchSuffix) {
		t.Errorf("BranchSuffix = %q, set = %v; want the default, unchosen",
			u.BranchSuffix, u.IsSet(UserKeyBranchSuffix))
	}
}

// The value the file wanted is named in the problem, not just the key.
func TestUserBranchSuffixMustBeAString(t *testing.T) {
	_, err := loadUser(writeUser(t, "branch_suffix = true\n"))
	if err == nil || !strings.Contains(err.Error(), "is not a string") {
		t.Fatalf("want a not-a-string error, got %v", err)
	}
}
