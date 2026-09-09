package wtsync

// TakeTrunk resolves a generated file by taking trunk's copy wholesale: a
// lockfile merged by hand describes a tree nobody has ever installed, and the
// deferred step declared beside it reconciles trunk's copy against the merged
// manifests once, at the end of the rebase.
type TakeTrunk struct{}

// Name identifies this strategy in errors and reports.
func (TakeTrunk) Name() string { return "take-trunk" }

// Resolve returns trunk's blob unchanged.
func (TakeTrunk) Resolve(c Conflict) ([]byte, error) { return c.Trunk, nil }
