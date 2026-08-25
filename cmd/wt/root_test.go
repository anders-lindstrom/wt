package main

import (
	"bytes"
	"strings"
	"testing"
)

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	root := newRootCmd()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestRootListsSubcommands(t *testing.T) {
	out, err := runCmd(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"config", "path", "branch"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help does not mention %q:\n%s", want, out)
		}
	}
}

func TestVersion(t *testing.T) {
	out, err := runCmd(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("version printed nothing")
	}
}

func TestUnknownCommandIsAnError(t *testing.T) {
	if _, err := runCmd(t, "wibble"); err == nil {
		t.Error("want an error for an unknown command")
	}
}
