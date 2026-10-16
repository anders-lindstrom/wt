//go:build !darwin && !linux

package provision

// cloneTreeCOW has no copy-on-write clone to use here.
func cloneTreeCOW(_, _ string) error {
	return errCloneUnsupported{"wt has no copy-on-write clone on this platform"}
}

// removeStaleClones has nothing to remove: there is no clone to leave one.
func removeStaleClones(_, _ string) error { return nil }
