package quarantine

// Hooks are the points where a test stops a quarantine, a restore or a purge
// as a crash there would, and the device a path is on. They travel with the
// call rather than sitting in the package, so tests running side by side
// each have their own. A nil *Hooks, and every nil field, is the real thing;
// nothing but tests sets them.
type Hooks struct {
	// AfterSave runs after every write of a record made by Begin or
	// PlanRestore, and once after DoPurge writes its journal ("purge.begin"),
	// with the point just written ("moveCheckout:running"), and an error from
	// it ends the run there.
	AfterSave func(r *Record, point string) error
	// AfterRename runs between a move's rename and its fsync, and an error
	// from it stands for the fsync failing.
	AfterRename func(to string) error
	// AfterPurge runs after each deletion a purge makes, with what it
	// deleted ("pin:head", "entry:checkout", "record"), and an error from it
	// ends the purge there.
	AfterPurge func(point string) error
	// DeviceOf is the device path leads to, following symlinks: a test puts
	// a path on another volume with it.
	DeviceOf func(path string) (uint64, error)
}

func (h *Hooks) afterSave(r *Record, point string) error {
	if h == nil || h.AfterSave == nil {
		return nil
	}
	return h.AfterSave(r, point)
}

func (h *Hooks) afterRename(to string) error {
	if h == nil || h.AfterRename == nil {
		return nil
	}
	return h.AfterRename(to)
}

func (h *Hooks) afterPurge(point string) error {
	if h == nil || h.AfterPurge == nil {
		return nil
	}
	return h.AfterPurge(point)
}

func (h *Hooks) deviceOf(path string) (uint64, error) {
	if h == nil || h.DeviceOf == nil {
		return DeviceOf(path)
	}
	return h.DeviceOf(path)
}
