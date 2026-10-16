package testkit

import "testing"

// NoTrunkRepo is an unconfigured repository whose only branch has no
// conventional name and no origin/HEAD, so nothing names its trunk.
func NoTrunkRepo(t *testing.T) string {
	t.Helper()
	main := UnconfiguredRepo(t)
	GitIn(t, main, "branch", "-m", "main", "production")
	return main
}
