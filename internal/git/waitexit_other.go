//go:build !darwin && !linux

package git

// waitExited cannot wait without reaping here; RunBounded then takes the
// group off the reaper's list only after reaping it.
func waitExited(int) {}
