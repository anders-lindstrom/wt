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

// Modelled on server's real configuration: nested paths, and declared files
// that do not exist in the source. Everything present must arrive; everything
// absent must be skipped without failing the setup.
func TestSetupCopiesServerShapedConfig(t *testing.T) {
	main := committedRepo(t, `MAIN_BRANCH="main"
BUILD_INIT_ENABLED=false
DEVELOPER_CONFIG_DIRS=(.cursor .claude .run .vscode .idea)
DEVELOPER_CONFIG_FILES=(
    "override.properties"
    "accessmanagement/override.properties"
    "webapp/override.properties"
    "etc/ai-tooling/local-overrides/config.yaml"
    "accessmanagement/src/main/resources/application-local-secret.yaml"
)
`)
	// Present in the source — must be copied, including four levels deep.
	present := []string{
		"accessmanagement/override.properties",
		"webapp/override.properties",
		"etc/ai-tooling/local-overrides/config.yaml",
		"accessmanagement/src/main/resources/application-local-secret.yaml",
	}
	for _, f := range present {
		mustWrite(t, filepath.Join(main, f), "value-of-"+f)
	}
	mustWrite(t, filepath.Join(main, ".claude", "settings.json"), "{}")
	// "override.properties" is deliberately absent, as it is in the real repo.

	target := t.TempDir()
	ctx, err := Open(main)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Setup(ctx, target, SetupOptions{Source: main}, &buf); err != nil {
		t.Fatalf("a declared-but-absent file must not fail setup: %v", err)
	}
	for _, f := range present {
		if got := mustRead(t, filepath.Join(target, f)); got != "value-of-"+f {
			t.Errorf("%s: got %q", f, got)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "override.properties")); err == nil {
		t.Error("a file absent from the source must not be invented")
	}
	if _, err := os.Stat(filepath.Join(target, ".claude", "settings.json")); err != nil {
		t.Errorf("config dir not copied: %v", err)
	}
}

// A failing provision step must not cost you the rest of the provisioning.
// Stopping early leaves a worktree with no dependencies either, which is
// strictly less usable than one that merely lacks secrets.
func TestSetupContinuesAfterProvisionFailure(t *testing.T) {
	main := committedRepo(t, "MAIN_BRANCH=\"main\"\nBUILD_INIT_COMMAND=\"touch built.txt\"\n")
	p := filepath.Join(main, "bin", "worktree", "provision.sh")
	mustWrite(t, p, "#!/bin/sh\necho 'no AWS credentials' >&2\nexit 1\n")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	ctx, _ := Open(main)

	var buf bytes.Buffer
	err := Setup(ctx, target, SetupOptions{Source: main}, &buf)
	if err == nil {
		t.Fatal("a failed provision must still be reported as an error")
	}
	if _, statErr := os.Stat(filepath.Join(target, "built.txt")); statErr != nil {
		t.Error("build initialisation should still have run")
	}
	out := buf.String()
	if !strings.Contains(out, "wt setup") {
		t.Errorf("output must say how to finish provisioning:\n%s", out)
	}
}
