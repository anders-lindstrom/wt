package testkit

import "testing"

// Mine commits file in the checkout at dir.
func Mine(t *testing.T, dir, file, content string) string {
	t.Helper()
	WriteIn(t, dir, file, content)
	GitIn(t, dir, "add", "-A")
	GitIn(t, dir, "commit", "-q", "-m", "mine "+file)
	return GitOut(t, dir, "rev-parse", "HEAD")
}

// Str prints an optional string, <nil> for none.
func Str(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// SafetyRefs lists the safety refs a sync left.
func SafetyRefs(t *testing.T, main string) string {
	t.Helper()
	return GitOut(t, main, "for-each-ref", "--format=%(refname)", "refs/wt-sync/", "refs/wt-sync-result/")
}
