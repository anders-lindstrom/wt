package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What stands before --resume names the worktree and what stands after it
// is the conversation. A word after --resume is never matched against the
// worktrees, so it can never continue a conversation somewhere it was not
// meant to.
func TestAttachResumeTakesTheWorktreeBeforeItAndTheSessionAfter(t *testing.T) {
	repoWithWorktrees(t)
	main, _ := os.Getwd()
	main, _ = filepath.EvalSymlinks(main)
	t.Setenv("WT_ROOTS", filepath.Dir(main))
	claudeOnPath(t, sessionIn(t, "dddd4444"))
	apiTidy := filepath.Join(filepath.Dir(main), "demo_wt", "feat_wt", "api-tidy")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"api-tidy", "--resume"}, "continue\n\n" + apiTidy + "\nend\n"},
		{[]string{"api-tidy", "--resume", "0b5c9d1e"}, "resume\n0b5c9d1e\n" + apiTidy + "\nend\n"},
		// After --resume, with no worktree named: the one the caller is in.
		{[]string{"--resume", "0b5c9d1e"}, "resume\n0b5c9d1e\n" + main + "\nend\n"},
		// "api-tidy" names a worktree, and after --resume it is not one.
		{[]string{"--resume", "api-tidy"}, "resume\napi-tidy\n" + main + "\nend\n"},
		{[]string{"--resume"}, "continue\n\n" + main + "\nend\n"},
	} {
		out, err := runCmd(t, append([]string{"attach", "--for-shell"}, tc.args...)...)
		if err != nil || out != tc.want {
			t.Errorf("wt attach %v: got %q, %v; want %q", tc.args, out, err, tc.want)
		}
	}

	// A listed session's id after --resume is refused for what it is.
	_, err := runCmd(t, "attach", "--for-shell", "--resume", "dddd4444")
	if err == nil || !strings.Contains(err.Error(), "claude lists that conversation as a session") ||
		strings.Contains(err.Error(), "no worktree matches") {
		t.Errorf("want the live conversation refused, got %v", err)
	}
	for _, args := range [][]string{
		{"api-tidy", "0b5c9d1e", "--resume"},
		{"--resume", "0b5c9d1e", "api-tidy"},
	} {
		if _, err := runCmd(t, append([]string{"attach", "--for-shell"}, args...)...); err == nil ||
			!strings.Contains(err.Error(), "wt attach <pattern> --resume <id>") {
			t.Errorf("wt attach %v: want the two forms named, got %v", args, err)
		}
	}
}
