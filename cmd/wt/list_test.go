package main

import (
	"strings"
	"testing"
)

// wt status takes one worktree at most: the whole table, or one in full.
func TestStatusTakesAtMostOneWorktree(t *testing.T) {
	_, err := runCmd(t, "status", "login-crash", "api-tidy")
	if err == nil || !strings.Contains(err.Error(), "at most 1 arg") {
		t.Errorf("want an argument-count error for two worktrees, got %v", err)
	}
}
