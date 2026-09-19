package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points XDG_CONFIG_HOME at a directory of this run's own: `wt
// config set` writes a real file. The settings there turn both integrations
// off, so a test running `wt list` cannot reach the gh or the Superset of
// whoever is running it. A test that wants one back writes its own file
// through userConfigIn.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wt-cli-test")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		panic(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "wt"), 0o755); err != nil {
		panic(err)
	}
	settings := filepath.Join(dir, "wt", "config.toml")
	if err := os.WriteFile(settings, []byte("github = false\nsuperset = false\n"), 0o644); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// userConfigIn gives one test its own XDG_CONFIG_HOME and returns the path
// the user file would have.
func userConfigIn(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "wt", "config.toml")
}

// The settings are per machine, not per repository, so every one of these has
// to answer from a directory that is not a checkout at all.
func TestUserConfigSubcommandsWorkOutsideARepository(t *testing.T) {
	want := userConfigIn(t)
	t.Chdir(t.TempDir())

	out, err := runCmd(t, "config", "path")
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	if strings.TrimSpace(out) != want {
		t.Errorf("config path = %q, want %q", strings.TrimSpace(out), want)
	}

	if _, err := os.Stat(want); err == nil {
		t.Error("printing the path created the file")
	}

	if out, err = runCmd(t, "config", "get", "github"); err != nil {
		t.Fatalf("config get: %v", err)
	}
	if strings.TrimSpace(out) != "true" {
		t.Errorf("config get github = %q, want the default true", out)
	}

	if _, err = runCmd(t, "config", "set", "superset", "true"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	if out, err = runCmd(t, "config", "get", "superset"); err != nil {
		t.Fatalf("config get: %v", err)
	}
	if strings.TrimSpace(out) != "true" {
		t.Errorf("config get superset = %q after setting it", out)
	}

	if _, err = runCmd(t, "config", "unset", "superset"); err != nil {
		t.Fatalf("config unset: %v", err)
	}
	if out, err = runCmd(t, "config", "get", "superset"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "false" {
		t.Errorf("config get superset = %q after unsetting it", out)
	}
}

func TestConfigSetRejectsAnUnknownKey(t *testing.T) {
	path := userConfigIn(t)
	t.Chdir(t.TempDir())

	out, err := runCmd(t, "config", "set", "superst", "true")
	if err == nil {
		t.Fatalf("accepted; it said %q", out)
	}
	if !strings.Contains(err.Error(), `unknown key "superst"`) {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a rejected set wrote the file")
	}
}

func TestConfigSetRejectsANonBooleanValue(t *testing.T) {
	userConfigIn(t)
	t.Chdir(t.TempDir())

	if _, err := runCmd(t, "config", "set", "superset", "sometimes"); err == nil {
		t.Fatal("accepted a value that is not a boolean")
	} else if !strings.Contains(err.Error(), "true or false") {
		t.Errorf("err = %v, want it to say what a value looks like", err)
	}
}
