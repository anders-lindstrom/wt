// Package quarantine moves a worktree out of the way instead of deleting it,
// and puts it back. A quarantine is a folder the caller names, on the
// worktree's own volume, holding the checkout, the worktree's admin dir from
// .git/worktrees/ (its submodules' repositories under modules/ included) and
// recovery.json, the journal every step is written to before and after it
// runs. Nothing here deletes anything: every change is a rename, so whatever
// a crash interrupts is resolved from the journal and the two places each
// directory can be.
package quarantine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/schema"
)

// FileName is the journal inside a quarantine folder.
const FileName = "recovery.json"

// The steps of a removal into quarantine, in the order they run: the lock,
// the refs that pin its commits, the checkout, the admin dir, the
// checkout's .git pointed at the quarantined admin dir, the branch. The
// list is closed: another step is a new major of the recovery schema.
const (
	StepLock         = "lock"
	StepPin          = "pin"
	StepMoveCheckout = "moveCheckout"
	StepMoveAdmin    = "moveAdmin"
	StepRelink       = "relink"
	StepBranch       = "branch"
)

// The steps of a restore, in the order they run: branch, moveAdmin,
// relink, moveCheckout, unlock, unpin. The moves and relink reuse the
// removal's names; the restore's list is its own, and as closed.
const (
	StepUnlock = "unlock"
	StepUnpin  = "unpin"
)

var (
	removalSteps = []string{StepLock, StepPin, StepMoveCheckout, StepMoveAdmin, StepRelink, StepBranch}
	restoreSteps = []string{StepBranch, StepMoveAdmin, StepRelink, StepMoveCheckout, StepUnlock, StepUnpin}
)

func pendingSteps(names []string) []Step {
	steps := make([]Step, len(names))
	for i, n := range names {
		steps[i] = Step{Name: n, State: Pending}
	}
	return steps
}

// The states of a step. Running is written before the step changes
// anything, so a crash that leaves it says the step may have happened; the
// places themselves then say whether it did.
const (
	Pending = "pending"
	Running = "running"
	Done    = "done"
	Failed  = "failed"
)

// Place is one directory a quarantine moves: where it was, where it goes,
// and the device and inode it had, which a rename keeps. The inode is how a
// restore tells its own directory from something else at the same path.
type Place struct {
	Path        string `json:"path"`
	Quarantined string `json:"quarantined"`
	Device      uint64 `json:"device"`
	Inode       uint64 `json:"inode"`
}

// ConfigEntry is one line of a branch's local config section, in order.
type ConfigEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// What the removal plans for the branch.
const (
	BranchPlanDelete = "delete"
	BranchPlanKeep   = "keep"
	BranchPlanNone   = "none"
)

// What the removal's branch step came to.
const (
	BranchDeleted   = "deleted"
	BranchRenamed   = "renamed"
	BranchUntouched = "untouched"
	// BranchKept is a delete or rename the removal turned down on purpose:
	// the branch moved, is no longer merged, or a worktree took it.
	BranchKept   = "kept"
	BranchFailed = "failed"
)

// Branch is the branch the worktree had checked out, as the removal found
// it and what it did with it.
type Branch struct {
	Name   string  `json:"name"`
	Tip    string  `json:"tip"`
	Plan   string  `json:"plan"`
	KeepAs *string `json:"keepAs"`
	// Config is the branch's local config section, captured right before
	// the branch step; null until then.
	Config []ConfigEntry `json:"config"`
	Result *string       `json:"result"`
}

// Step is one journalled step and its state.
type Step struct {
	Name  string  `json:"name"`
	State string  `json:"state"`
	Error *string `json:"error"`
}

// What a restore does with the branch, decided from the state it finds.
const (
	// ActionNone: the branch is back, or never went, at the recorded tip.
	ActionNone = "none"
	// ActionRenameBack: the removal kept it under another name; it gets its
	// name back, config included.
	ActionRenameBack = "renameBack"
	// ActionRecreate: the removal deleted it and the name is free; it is
	// made again at the recorded tip, with its recorded config.
	ActionRecreate = "recreate"
	// ActionAttach: its name is taken by another tip, but the kept name
	// still has the recorded tip, so HEAD names that.
	ActionAttach = "attach"
	// ActionDetach: no branch change; HEAD is detached at the recorded
	// commit, because another worktree has the branch or it moved on.
	ActionDetach = "detach"
)

// Pins are the refs that hold the quarantined worktree's commits while it
// is away: its HEAD and its branch's tip, which nothing else may hold once
// the branch is deleted and the admin dir is out of the repository, and a
// blob naming the quarantine folder, so a pin whose folder is gone can be
// told apart. Created before anything moves, deleted when the restore is
// done.
type Pins struct {
	Head *string `json:"head"`
	Tip  *string `json:"tip"`
	Dir  string  `json:"dir"`
}

// Restore is a restore's own journal inside the record.
type Restore struct {
	// Action is what the restore decided for the branch; OccupiedBy is the
	// worktree using the branch when that decided it.
	Action     *string `json:"action"`
	OccupiedBy *string `json:"occupiedBy"`
	// CreatedBranch is set once this restore has made the branch again: it
	// owns that branch, and its config, until the restore is done, also
	// after a step that failed.
	CreatedBranch bool   `json:"createdBranch"`
	Steps         []Step `json:"steps"`
}

// Record is recovery.json.
type Record struct {
	Schema        int    `json:"schema"`
	SchemaVersion string `json:"schemaVersion"`
	// Command is the wt command that made it: "remove" or "sweep".
	Command    string `json:"command"`
	CreatedAt  string `json:"createdAt"`
	Repo       string `json:"repo"`
	CommonDir  string `json:"commonDir"`
	WorktreeID string `json:"worktreeId"`
	Dir        string `json:"dir"`
	Checkout   Place  `json:"checkout"`
	Admin      Place  `json:"admin"`
	// GitFile is the checkout's .git file as it was; while quarantined it
	// names the quarantined admin dir instead.
	GitFile string `json:"gitFile"`
	Pins    Pins   `json:"pins"`
	// Head is the commit checked out there; null before the first commit.
	Head    *string  `json:"head"`
	Branch  *Branch  `json:"branch"`
	Steps   []Step   `json:"steps"`
	Restore *Restore `json:"restore"`
	// Purge is a purge's journal; null until wt quarantine purge begins.
	Purge *Purge `json:"purge"`

	// recordedAs is the folder the file itself names, which Load reads
	// before Dir becomes where it was read from.
	recordedAs string
}

// AfterSave runs, when set, after every write of a record with the point
// just written ("moveCheckout:running"), and an error from it ends the run
// there, as a crash would. Tests set it; nothing else does.
var AfterSave func(r *Record, point string) error

// LockReason is the git worktree lock a quarantine into dir holds while it
// runs. gittree reads it too.
func LockReason(dir string) string { return lockPrefix + dir }

const lockPrefix = "wt quarantine "

// DirOf is the folder a quarantine's lock reason names, and false for a lock
// that is not a quarantine's.
func DirOf(reason string) (string, bool) {
	dir, ok := strings.CutPrefix(reason, lockPrefix)
	return dir, ok && dir != ""
}

// Begin makes dir — the folder itself, never its parents, and never one
// that is there already — and writes r into it as the first journal, every
// removal step pending. Nothing else has changed when it returns.
func Begin(dir string, r Record) (*Record, error) {
	if err := MakeDir(dir); err != nil {
		return nil, err
	}
	r.Schema, r.SchemaVersion = 1, schema.VersionOf("recovery")
	r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	r.Dir = dir
	r.Steps = pendingSteps(removalSteps)
	key := pinKey(r.WorktreeID, dir)
	r.Pins = Pins{Dir: pinRef(key, "dir")}
	if r.Head != nil {
		r.Pins.Head = strPtr(pinRef(key, "head"))
	}
	if r.Branch != nil {
		r.Pins.Tip = strPtr(pinRef(key, "tip"))
	}
	return &r, r.save("begin")
}

// MakeDir makes dir — never its parents, never one that is there already —
// and fsyncs its parent, so the folder is on disk before anything goes in.
func MakeDir(dir string) error {
	if err := os.Mkdir(dir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s is there already: a quarantine goes into a new folder", dir)
		}
		return err
	}
	return syncDir(filepath.Dir(dir))
}

// SyncDir fsyncs a directory, so what was written or renamed in it is on
// disk.
func SyncDir(dir string) error { return syncDir(dir) }

// Load reads dir's recovery.json.
func Load(dir string) (*Record, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	return parse(dir, FileName, data)
}

// parse reads a record from data, the file name in dir.
func parse(dir, name string, data []byte) (*Record, error) {
	file := filepath.Join(dir, name)
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if r.Schema != 1 {
		return nil, fmt.Errorf("%s is schema %d; this wt reads schema 1", file, r.Schema)
	}
	if err := r.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	r.recordedAs, r.Dir = r.Dir, dir
	return &r, nil
}

// validate refuses a record wt cannot act on safely: every step it will
// read must be there once, in a state it knows, and the places named.
func (r *Record) validate() error {
	if r.Repo == "" || r.Checkout.Path == "" || r.Checkout.Quarantined == "" ||
		r.Admin.Path == "" || r.Admin.Quarantined == "" {
		return errors.New("it does not name the repository and both places")
	}
	if r.GitFile == "" || r.Pins.Dir == "" {
		return errors.New("it does not record the checkout's .git file and its pins")
	}
	if err := validSteps(r.Steps, removalSteps...); err != nil {
		return err
	}
	if b := r.Branch; b != nil {
		if b.Name == "" || b.Tip == "" {
			return errors.New("its branch has no name or tip")
		}
		if b.Plan == BranchPlanKeep && deref(b.KeepAs) == "" {
			return errors.New("its branch is to be kept under no name")
		}
	}
	if r.Restore != nil {
		return validSteps(r.Restore.Steps, restoreSteps...)
	}
	return nil
}

func validSteps(steps []Step, names ...string) error {
	if len(steps) != len(names) {
		return fmt.Errorf("it has %d steps where there are %d", len(steps), len(names))
	}
	for i, n := range names {
		switch {
		case steps[i].Name != n:
			return fmt.Errorf("step %d is %q, not %s", i+1, steps[i].Name, n)
		case steps[i].State != Pending && steps[i].State != Running && steps[i].State != Done && steps[i].State != Failed:
			return fmt.Errorf("step %s is in the unknown state %q", n, steps[i].State)
		}
	}
	return nil
}

// Step is the removal step of that name.
func (r *Record) Step(name string) *Step { return find(r.Steps, name) }

// RestoreStep is the restore step of that name, nil before a restore began.
func (r *Record) RestoreStep(name string) *Step {
	if r.Restore == nil {
		return nil
	}
	return find(r.Restore.Steps, name)
}

func find(steps []Step, name string) *Step {
	for i := range steps {
		if steps[i].Name == name {
			return &steps[i]
		}
	}
	return nil
}

// Set records a removal step's new state, and err with it, on disk.
func (r *Record) Set(name, state string, err error) error {
	return r.set(r.Step(name), name, state, err)
}

// SetRestore records a restore step's new state on disk.
func (r *Record) SetRestore(name, state string, err error) error {
	return r.set(r.RestoreStep(name), "restore."+name, state, err)
}

func (r *Record) set(s *Step, point, state string, err error) error {
	if s == nil {
		return fmt.Errorf("recovery.json has no step %s", point)
	}
	s.State, s.Error = state, nil
	if err != nil {
		msg := err.Error()
		s.Error = &msg
	}
	return r.save(point + ":" + state)
}

// Save writes the record as it is now.
func (r *Record) Save(point string) error { return r.save(point) }

// save writes the record durably: a temp file, fsynced, renamed over the
// journal, and the folder fsynced so the rename itself is on disk.
func (r *Record) save(point string) error {
	data, err := r.marshal()
	if err != nil {
		return err
	}
	if err := writeDurable(filepath.Join(r.Dir, FileName), data); err != nil {
		return err
	}
	if AfterSave != nil {
		return AfterSave(r, point)
	}
	return nil
}

// marshal is the record as it is written: dir is the folder the removal
// named, however the folder was reached this time.
func (r *Record) marshal() ([]byte, error) {
	out := *r
	if r.recordedAs != "" {
		out.Dir = r.recordedAs
	}
	data, err := json.MarshalIndent(&out, "", "  ")
	return append(data, '\n'), err
}

// writeDurable writes path through a temp file made new, never followed if
// something left a symlink at its name.
func writeDurable(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// BeginBranch captures the branch's local config section into the record
// and marks the branch step running, before the branch is touched: a
// deleted branch's config goes with it, and a restore puts it back from
// here.
func (r *Record) BeginBranch(rp *repo.Repo) error {
	if r.Branch != nil {
		entries, err := rp.BranchConfig(r.Branch.Name)
		if err != nil {
			_ = r.Set(StepBranch, Failed, err)
			return fmt.Errorf("could not read the config of branch %s: %w", r.Branch.Name, err)
		}
		r.Branch.Config = []ConfigEntry{}
		for _, e := range entries {
			r.Branch.Config = append(r.Branch.Config, ConfigEntry{Key: e.Key, Value: e.Value})
		}
	}
	return r.Set(StepBranch, Running, nil)
}

// RecordBranch writes what the branch step came to: one of the Branch*
// results, and why when it was kept or failed.
func (r *Record) RecordBranch(result string, err error) error {
	if r.Branch != nil {
		r.Branch.Result = &result
	}
	state := Done
	if result == BranchFailed {
		state = Failed
	}
	return r.Set(StepBranch, state, err)
}
