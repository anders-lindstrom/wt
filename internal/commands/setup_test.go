package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupCopiesConfigDirsAndFiles(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\n"+
		"DEVELOPER_CONFIG_DIRS=(.vscode)\nDEVELOPER_CONFIG_FILES=(\"nested/app.env\")\n")
	mustMkdir(t, filepath.Join(main, ".vscode"))
	mustWrite(t, filepath.Join(main, ".vscode", "settings.json"), "{}")
	mustWrite(t, filepath.Join(main, "nested", "app.env"), "K=V")

	target := t.TempDir()
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main}, &buf); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".vscode", "settings.json")); err != nil {
		t.Errorf("config dir not copied: %v", err)
	}
	if got := mustRead(t, filepath.Join(target, "nested", "app.env")); got != "K=V" {
		t.Errorf("config file not copied: %q", got)
	}
}

func TestSetupNeverOverwritesExistingFiles(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\n"+
		"DEVELOPER_CONFIG_FILES=(\"app.env\")\n")
	mustWrite(t, filepath.Join(main, "app.env"), "FROM=source")
	target := t.TempDir()
	mustWrite(t, filepath.Join(target, "app.env"), "MINE=keep")

	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main}, &buf); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(target, "app.env")); got != "MINE=keep" {
		t.Errorf("existing file was overwritten: %q", got)
	}
}

func TestSetupRunsProvisionScript(t *testing.T) {
	main := fixtureRepo(t, minimalConf)
	p := filepath.Join(main, "bin", "worktree", "provision.sh")
	mustWrite(t, p, "#!/bin/sh\necho provisioned > provisioned.txt\n")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main}, &buf); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if got := mustRead(t, filepath.Join(target, "provisioned.txt")); !strings.Contains(got, "provisioned") {
		t.Errorf("provision.sh did not run in the target: %q", got)
	}
}

// A failing build is a warning in the bash implementation. Setup must not
// return an error for it, or every agent that provisions would start failing.
func TestSetupBuildFailureIsAWarningNotAnError(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_COMMAND=\"exit 3\"\n")
	target := t.TempDir()
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main}, &buf); err != nil {
		t.Fatalf("build failure should not be fatal: %v", err)
	}
	if !strings.Contains(buf.String(), "Warning") {
		t.Errorf("want a warning in the output:\n%s", buf.String())
	}
}

func TestSetupSkipBuild(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_COMMAND=\"touch built.txt\"\n")
	target := t.TempDir()
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main, SkipBuild: true}, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "built.txt")); err == nil {
		t.Error("build ran despite --skip-build")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// A config entry that escapes the worktree must be refused, not silently
// written outside it.
func TestSetupRefusesConfigEntriesThatEscapeTheWorktree(t *testing.T) {
	main := fixtureRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_ENABLED=false\n"+
		"DEVELOPER_CONFIG_FILES=(\"../escaped.env\")\n")
	mustWrite(t, filepath.Join(filepath.Dir(main), "escaped.env"), "SECRET=1")

	target := filepath.Join(t.TempDir(), "wt")
	mustMkdir(t, target)
	ctx, _ := Open(main)
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main}, &buf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), "escaped.env")); err == nil {
		t.Error("Setup wrote outside the worktree")
	}
	if !strings.Contains(buf.String(), "escapes the worktree") {
		t.Errorf("want a refusal message:\n%s", buf.String())
	}
}
