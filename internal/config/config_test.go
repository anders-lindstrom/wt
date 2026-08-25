package config

import (
	"strings"
	"testing"
)

func raw(t *testing.T, s string) map[string]Value {
	t.Helper()
	v, err := ParseBash(strings.NewReader(s))
	if err != nil {
		t.Fatalf("ParseBash: %v", err)
	}
	return v
}

func TestUnknownKeyIsAnError(t *testing.T) {
	_, err := FromRaw(raw(t, "DEVELOPER_CONFIG_FILE=(.env)\n"), "main")
	if err == nil || !strings.Contains(err.Error(), "DEVELOPER_CONFIG_FILE") {
		t.Fatalf("want error naming the key, got %v", err)
	}
}

func TestRetiredKeysExplainThemselves(t *testing.T) {
	for key, want := range map[string]string{
		"REPO_NAME":         "derived",
		"AWS_SETUP_ENABLED": "provision.sh",
		"WORKTREE_LAYOUT":   "no longer configurable",
	} {
		_, err := FromRaw(raw(t, key+"=x\n"), "main")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want error mentioning %q, got %v", key, want, err)
		}
	}
}

func TestMainBranchFallsBackToDetected(t *testing.T) {
	c, err := FromRaw(raw(t, ""), "trunk")
	if err != nil {
		t.Fatalf("FromRaw: %v", err)
	}
	if c.MainBranch != "trunk" {
		t.Errorf("MainBranch = %q, want trunk", c.MainBranch)
	}
}

func TestBuildInitCommandRequiredWhenEnabled(t *testing.T) {
	_, err := FromRaw(raw(t, "BUILD_INIT_ENABLED=true\n"), "main")
	if err == nil || !strings.Contains(err.Error(), "BUILD_INIT_COMMAND") {
		t.Fatalf("want required-when-enabled error, got %v", err)
	}
	if _, err := FromRaw(raw(t, "BUILD_INIT_ENABLED=false\n"), "main"); err != nil {
		t.Errorf("disabled build init should not require a command: %v", err)
	}
}

// The rival-fix resolution: a prefix that names no valid type is an error
// naming WORKTREE_DEFAULT_TYPE as the remedy, rather than a silent guess.
func TestPrefixNamingNoValidTypeIsAnError(t *testing.T) {
	_, err := FromRaw(raw(t, "WORKTREE_BRANCH_PREFIX=wip\n"), "main")
	if err == nil || !strings.Contains(err.Error(), "WORKTREE_DEFAULT_TYPE") {
		t.Fatalf("want error naming the remedy, got %v", err)
	}
}

func TestAllSevenRealConfigsValidate(t *testing.T) {
	for _, path := range mustGlob(t, "testdata/*.conf") {
		t.Run(path, func(t *testing.T) {
			c, err := FromRaw(mustParseFile(t, path), "main")
			if err != nil {
				t.Fatalf("FromRaw: %v", err)
			}
			if c.DefaultType != "feat" {
				t.Errorf("DefaultType = %q, want feat", c.DefaultType)
			}
		})
	}
}
