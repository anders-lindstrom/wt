package testkit

import "testing"

// CommittedRepo is FixtureRepo with an identity and one commit.
func CommittedRepo(t *testing.T, conf string) string {
	t.Helper()
	main := FixtureRepo(t, conf)
	GitIn(t, main, "config", "user.email", "t@example.com")
	GitIn(t, main, "config", "user.name", "T")
	GitIn(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	return main
}
