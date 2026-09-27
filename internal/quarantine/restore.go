package quarantine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// Where one of the two directories is now.
const (
	// AtOriginal: at the path it came from — moved back, or never moved.
	// The inode says it is the same directory.
	AtOriginal = "original"
	// InQuarantine: in the quarantine folder.
	InQuarantine = "quarantined"
	// Taken: something else is at the path it came from.
	Taken = "taken"
	// Missing: in neither place.
	Missing = "missing"
)

// What a restore came to.
const (
	OutcomeRestored = "restored"
	OutcomeRefused  = "refused"
	OutcomePartial  = "partial"
)

// Why a restore refuses before it changes anything.
const (
	ProblemNoRecord        = "noRecord"
	ProblemRepository      = "repository"
	ProblemCheckoutTaken   = "checkoutTaken"
	ProblemCheckoutMissing = "checkoutMissing"
	ProblemAdminTaken      = "adminTaken"
	ProblemAdminMissing    = "adminMissing"
	ProblemVolume          = "volume"
	ProblemCommitMissing   = "commitMissing"
	ProblemWorktrees       = "worktreesUnknown"
)

// Problem is one reason a restore refuses.
type Problem struct {
	Code string
	Text string
}

// RestorePlan is what a restore finds and would do.
type RestorePlan struct {
	Dir    string
	Record *Record // nil when the folder has no recovery.json
	// Checkout and Admin are where each directory is now: one of the
	// AtOriginal, InQuarantine, Taken, Missing values.
	Checkout string
	Admin    string
	// Action is what happens to the branch, one of the Action* values;
	// OccupiedBy is the worktree using the branch when that decided it.
	Action     string
	OccupiedBy string
	// Locked is the quarantine's own lock on the worktree, which the
	// restore takes off last.
	Locked bool
	// Orphan is a worktree locked by this quarantine that has no
	// recovery.json: stopped before anything moved, so restoring it is
	// unlocking it.
	Orphan   string
	Problems []Problem
}

// RestoreResult is what a restore did.
type RestoreResult struct {
	Outcome    string
	Action     string
	OccupiedBy string
	Checkout   string
	Admin      string
	Unlocked   bool
	Error      string
}

// Refusal is every problem in words, "" when there is none.
func (p RestorePlan) Refusal() string {
	var why []string
	for _, pr := range p.Problems {
		why = append(why, pr.Text)
	}
	return strings.Join(why, "; ")
}

// PlanRestore reads the quarantine at dir and everything its restore turns
// on, changing nothing. rp is the repository to look for an orphaned lock
// in when dir has no recovery.json; nil when there is none to look in.
func PlanRestore(dir string, rp *repo.Repo) RestorePlan {
	p := RestorePlan{Dir: dir}
	r, err := Load(dir)
	if errors.Is(err, os.ErrNotExist) {
		p.planOrphan(rp)
		return p
	}
	if err != nil {
		p.problem(ProblemNoRecord, err.Error())
		return p
	}
	p.Record = r
	p.Checkout, p.Admin = Locate(r.Checkout), Locate(r.Admin)
	p.checkPlace("checkout", r.Checkout, p.Checkout, ProblemCheckoutTaken, ProblemCheckoutMissing)
	p.checkPlace("git dir", r.Admin, p.Admin, ProblemAdminTaken, ProblemAdminMissing)
	if reason, _ := lockReason(r.Admin, p.Admin); reason != "" {
		q, ok := DirOf(reason)
		p.Locked = ok && repo.SamePath(q, dir)
	}
	rr, err := repo.Discover(r.Repo)
	if err != nil {
		p.problem(ProblemRepository, fmt.Sprintf("the repository %s cannot be opened: %v", r.Repo, err))
		return p
	}
	p.Action, p.OccupiedBy = p.decideBranch(rr)
	return p
}

func (p *RestorePlan) problem(code, text string) {
	p.Problems = append(p.Problems, Problem{Code: code, Text: text})
}

// planOrphan handles a folder with no recovery.json. A removal writes that
// before it locks anything, so a worktree locked for this folder without
// one was stopped before anything moved: restoring it is unlocking it.
func (p *RestorePlan) planOrphan(rp *repo.Repo) {
	if rp != nil {
		if list, err := rp.Worktrees(); err == nil {
			for _, wt := range list {
				if q, ok := DirOf(wt.LockReason); ok && wt.Locked && repo.SamePath(q, p.Dir) {
					p.Orphan, p.Locked, p.Action = wt.Path, true, ActionNone
					return
				}
			}
		}
	}
	p.problem(ProblemNoRecord, fmt.Sprintf("%s has no %s, and no worktree here is locked for it: nothing to restore",
		p.Dir, FileName))
}

// Locate finds where a directory the quarantine moved is now. Only the
// directory itself counts, by the device and inode a rename keeps, at
// either place: anything else put at the quarantine path — another
// directory, a symlink — is not it.
func Locate(pl Place) string {
	want := Identity{Device: pl.Device, Inode: pl.Inode}
	orig, oerr := Identify(pl.Path)
	if oerr == nil && orig == want {
		return AtOriginal
	}
	q, qerr := Identify(pl.Quarantined)
	switch {
	case qerr == nil && q == want && errors.Is(oerr, os.ErrNotExist):
		return InQuarantine
	case oerr == nil:
		return Taken
	}
	return Missing
}

// checkPlace refuses a directory that cannot go back: its path is taken or
// it is nowhere, or it would come back across volumes.
func (p *RestorePlan) checkPlace(what string, pl Place, at, taken, missing string) {
	switch at {
	case Taken:
		p.problem(taken, fmt.Sprintf("its %s's path %s is taken by something else", what, pl.Path))
	case Missing:
		p.problem(missing, fmt.Sprintf("its %s is neither at %s nor at %s (the directory that was moved, by inode)",
			what, pl.Path, pl.Quarantined))
	case InQuarantine:
		from, err := DeviceOf(filepath.Dir(pl.Quarantined))
		if err != nil {
			p.problem(missing, err.Error())
			return
		}
		// The folder it came from may be gone — git worktree prune takes an
		// empty .git/worktrees, and a checkout's parents can be tidied away:
		// the restore makes them again, on the volume of what is left.
		to, err := DeviceOf(existingAncestor(filepath.Dir(pl.Path)))
		if err != nil {
			p.problem(missing, fmt.Sprintf("where its %s came from cannot be read: %v", what, err))
			return
		}
		if from != to {
			p.problem(ProblemVolume, fmt.Sprintf("its %s would come back from another volume: a restore only renames", what))
		}
	}
}

// lockReason reads git's lock on the worktree from its admin dir, wherever
// that is now.
func lockReason(admin Place, at string) (string, bool) {
	dir := admin.Path
	if at == InQuarantine {
		dir = admin.Quarantined
	}
	b, err := os.ReadFile(filepath.Join(dir, "locked"))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// decideBranch is §8 step 3 of gittree's spec: what happens to the branch,
// from the refs and the other worktrees as they are now. A restore that
// already detached or attached HEAD keeps to that.
func (p *RestorePlan) decideBranch(rp *repo.Repo) (action, occupiedBy string) {
	r := p.Record
	if r.Branch == nil || p.neverUnregistered() {
		return ActionNone, ""
	}
	if r.Restore != nil && r.Restore.Action != nil {
		if a := *r.Restore.Action; a == ActionDetach || a == ActionAttach {
			return a, deref(r.Restore.OccupiedBy)
		}
	}
	b := r.Branch
	keep := deref(b.KeepAs)
	if b.Plan != BranchPlanKeep {
		keep = ""
	}
	use, err := occupancy(rp, r.Checkout.Path, b.Name, keep)
	if err != nil {
		p.problem(ProblemWorktrees, err.Error())
		return "", ""
	}
	if use != "" {
		return ActionDetach, use
	}
	orig, origOK := rp.ResolveRef("refs/heads/" + b.Name)
	kept, keptOK := "", false
	if keep != "" {
		kept, keptOK = rp.ResolveRef("refs/heads/" + keep)
	}
	switch {
	case origOK && orig == b.Tip:
		return ActionNone, ""
	case !origOK && keptOK && kept == b.Tip:
		return ActionRenameBack, ""
	case !origOK && !keptOK:
		if _, err := git.Run(rp.MainRoot, "cat-file", "-e", b.Tip+"^{commit}"); err != nil {
			p.problem(ProblemCommitMissing, fmt.Sprintf("branch %s cannot be made again: its commit %s is not in the repository",
				b.Name, git.ShortID(b.Tip, 12)))
			return "", ""
		}
		return ActionRecreate, ""
	case keptOK && kept == b.Tip:
		return ActionAttach, ""
	}
	return ActionDetach, ""
}

// neverUnregistered is a removal that stopped before its admin dir moved:
// the worktree stayed registered, and may have been worked in since, so
// its branch and HEAD are its own business. A restore only unlocks it.
func (p *RestorePlan) neverUnregistered() bool {
	return p.Admin == AtOriginal && p.Record.Step(StepMoveAdmin).State != Done
}

// existingAncestor is path, or the nearest folder above it that exists.
func existingAncestor(path string) string {
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		up := filepath.Dir(path)
		if up == path {
			return path
		}
		path = up
	}
}

// occupancy names the worktree, other than the one at self, using either
// name: as its HEAD, as the branch a rebase returns to, or as the branch a
// bisect started from. "" when none is.
func occupancy(rp *repo.Repo, self string, names ...string) (string, error) {
	list, err := rp.Worktrees()
	if err != nil {
		return "", fmt.Errorf("%w: %w", repo.ErrWorktreesUnknown, err)
	}
	var others repo.Worktrees
	for _, wt := range list {
		if !repo.SamePath(wt.Path, self) {
			others = append(others, wt)
		}
	}
	users := others.Users()
	for _, n := range names {
		if n == "" {
			continue
		}
		if u, ok := users[n]; ok {
			if u.By != "" {
				return u.Path + " (" + u.By + " of " + n + ")", nil
			}
			return u.Path + " (" + n + ")", nil
		}
	}
	return "", nil
}

// DoRestore carries out the restore of the quarantine at dir: the branch
// first, while the worktree is still unregistered, then — after the
// branch's occupancy is read once more — the admin dir back, then the
// checkout, then the quarantine's lock off. Every step is journalled in
// recovery.json before and after it runs, and each one is idempotent, so a
// restore stopped anywhere is finished by the next.
func DoRestore(dir string, rp *repo.Repo) (RestoreResult, error) {
	p := PlanRestore(dir, rp)
	res := RestoreResult{Outcome: OutcomeRefused, Action: p.Action, OccupiedBy: p.OccupiedBy,
		Checkout: p.Checkout, Admin: p.Admin}
	if why := p.Refusal(); why != "" {
		return res, errors.New(why)
	}
	if p.Record == nil {
		if err := rp.UnlockWorktree(p.Orphan); err != nil {
			return res, fmt.Errorf("could not unlock %s: %s", p.Orphan, git.Reason(err))
		}
		res.Outcome, res.Unlocked, res.Checkout, res.Admin = OutcomeRestored, true, AtOriginal, AtOriginal
		return res, nil
	}
	r := p.Record
	rr, err := repo.Discover(r.Repo)
	if err != nil {
		return res, err
	}
	res.Outcome = OutcomePartial
	if r.Restore == nil {
		r.Restore = &Restore{Steps: pendingSteps(restoreSteps)}
		if err := r.Save("restore.begin"); err != nil {
			return res, err
		}
	}

	if st := r.RestoreStep(StepBranch); st.State != Done {
		action, occupied := p.Action, p.OccupiedBy
		if st.State == Running && deref(r.Restore.Action) == ActionRecreate && !p.neverUnregistered() {
			// This restore made the branch again and stopped: it finishes
			// that, config included, rather than taking the branch for
			// somebody else's.
			action, occupied = ActionRecreate, ""
		}
		r.Restore.Action, r.Restore.OccupiedBy = &action, strPtr(occupied)
		if err := r.SetRestore(StepBranch, Running, nil); err != nil {
			return res, err
		}
		if err := restoreBranch(rr, r, action, p.Admin); err != nil {
			_ = r.SetRestore(StepBranch, Failed, err)
			return res, err
		}
		if err := r.SetRestore(StepBranch, Done, nil); err != nil {
			return res, err
		}
	}
	res.Action, res.OccupiedBy = deref(r.Restore.Action), deref(r.Restore.OccupiedBy)

	if Locate(r.Admin) == InQuarantine {
		// The branch may have been checked out, moved or deleted since it
		// was decided — by a restore that stopped, since an earlier run:
		// the last read before the worktree is registered again.
		if err := recheckBranch(rr, r); err != nil {
			return res, err
		}
		res.Action, res.OccupiedBy = deref(r.Restore.Action), deref(r.Restore.OccupiedBy)
	}
	if err := moveBack(r, StepMoveAdmin, r.Admin); err != nil {
		return res, err
	}
	res.Admin = AtOriginal
	// The checkout's .git names the original admin dir again before it is
	// back where git will look for it.
	if st := r.RestoreStep(StepRelink); st.State != Done {
		if err := r.SetRestore(StepRelink, Running, nil); err != nil {
			return res, err
		}
		if at := Locate(r.Checkout); at == AtOriginal || at == InQuarantine {
			if err := r.Unlink(at); err != nil {
				_ = r.SetRestore(StepRelink, Failed, err)
				return res, err
			}
		}
		if err := r.SetRestore(StepRelink, Done, nil); err != nil {
			return res, err
		}
	}
	if err := moveBack(r, StepMoveCheckout, r.Checkout); err != nil {
		return res, err
	}
	res.Checkout = AtOriginal

	if r.RestoreStep(StepUnlock).State != Done {
		if err := r.SetRestore(StepUnlock, Running, nil); err != nil {
			return res, err
		}
		if reason, _ := lockReason(r.Admin, AtOriginal); reason != "" {
			if q, ok := DirOf(reason); ok && repo.SamePath(q, dir) {
				if err := rr.UnlockWorktree(r.Checkout.Path); err != nil {
					err = fmt.Errorf("could not unlock %s: %s", r.Checkout.Path, git.Reason(err))
					_ = r.SetRestore(StepUnlock, Failed, err)
					return res, err
				}
				res.Unlocked = true
			}
		}
		if err := r.SetRestore(StepUnlock, Done, nil); err != nil {
			return res, err
		}
	}
	if r.RestoreStep(StepUnpin).State != Done {
		if err := r.SetRestore(StepUnpin, Running, nil); err != nil {
			return res, err
		}
		if err := r.Unpin(rr); err != nil {
			_ = r.SetRestore(StepUnpin, Failed, err)
			return res, err
		}
		if err := r.SetRestore(StepUnpin, Done, nil); err != nil {
			return res, err
		}
	}
	res.Outcome = OutcomeRestored
	return res, nil
}

// moveBack moves one directory back unless it is back already. A path
// taken, or a directory gone, since the plan is a failure.
func moveBack(r *Record, step string, pl Place) error {
	if r.RestoreStep(step).State == Done && Locate(pl) == AtOriginal {
		return nil
	}
	if err := r.SetRestore(step, Running, nil); err != nil {
		return err
	}
	var err error
	switch at := Locate(pl); at {
	case AtOriginal:
	case InQuarantine:
		// Its parents, never itself: they may have been tidied away.
		if err = os.MkdirAll(filepath.Dir(pl.Path), 0o755); err == nil {
			err = Move(pl.Quarantined, pl.Path)
		}
	case Taken:
		err = fmt.Errorf("%s was taken by something else while the restore ran", pl.Path)
	default:
		err = fmt.Errorf("%s is no longer at %s", pl.Quarantined, pl.Path)
	}
	if err != nil {
		_ = r.SetRestore(step, Failed, err)
		return err
	}
	return r.SetRestore(step, Done, nil)
}

// restoreBranch carries out a branch action. Each is idempotent: a ref
// already where the action puts it is left, config is added only to an
// empty section, and HEAD is written whole.
func restoreBranch(rp *repo.Repo, r *Record, action, adminAt string) error {
	b := r.Branch
	switch action {
	case ActionRenameBack:
		if err := rp.RenameBranch(deref(b.KeepAs), b.Name); err != nil {
			return fmt.Errorf("could not rename %s back to %s: %s", deref(b.KeepAs), b.Name, git.Reason(err))
		}
	case ActionRecreate:
		if _, ok := rp.ResolveRef("refs/heads/" + b.Name); !ok {
			if _, err := git.Run(rp.MainRoot, "update-ref", "--no-deref", "refs/heads/"+b.Name, b.Tip, ""); err != nil {
				return fmt.Errorf("could not make branch %s again at %s: %s", b.Name, git.ShortID(b.Tip, 12), git.Reason(err))
			}
		}
	case ActionAttach:
		return writeHead(r, adminAt, "ref: refs/heads/"+deref(b.KeepAs))
	case ActionDetach:
		if r.Head == nil {
			return errors.New("recovery.json records no HEAD commit to detach at")
		}
		return writeHead(r, adminAt, *r.Head)
	}
	return restoreConfig(rp, r, action == ActionRecreate)
}

// restoreConfig puts a deleted branch's config section back, entry by
// entry, when the removal's branch step started and it was a delete, and
// either this restore made the branch again or its section is empty: a
// branch somebody else made keeps the config they gave it, and old values
// stacked on new would make a multi-valued merge an octopus. Each recorded
// entry the section does not have is added, in order, and one it has
// counts once, so a restore stopped between two entries adds the rest and
// none twice.
func restoreConfig(rp *repo.Repo, r *Record, recreated bool) error {
	b := r.Branch
	if b == nil || b.Plan != BranchPlanDelete || r.Step(StepBranch).State == Pending || len(b.Config) == 0 {
		return nil
	}
	now, err := rp.BranchConfig(b.Name)
	if err != nil || (!recreated && len(now) > 0) {
		return err
	}
	have := map[ConfigEntry]int{}
	for _, e := range now {
		have[ConfigEntry{Key: e.Key, Value: e.Value}]++
	}
	for _, e := range b.Config {
		if have[e] > 0 {
			have[e]--
			continue
		}
		if err := rp.AddConfig(e.Key, e.Value); err != nil {
			return fmt.Errorf("could not restore %s of branch %s: %s", e.Key, b.Name, git.Reason(err))
		}
	}
	return nil
}

// recheckBranch reads the branch HEAD will name once more, right before
// the admin dir goes back: it must still be there, at the recorded tip, and
// in no other worktree. A branch taken, moved or deleted since detaches
// HEAD at the recorded commit instead.
func recheckBranch(rp *repo.Repo, r *Record) error {
	if r.Branch == nil {
		return nil
	}
	action := deref(r.Restore.Action)
	name := r.Branch.Name
	switch action {
	case ActionDetach:
		return nil
	case ActionAttach:
		name = deref(r.Branch.KeepAs)
	}
	use, err := occupancy(rp, r.Checkout.Path, name)
	if err != nil {
		return err
	}
	if tip, ok := rp.ResolveRef("refs/heads/" + name); ok && tip == r.Branch.Tip && use == "" {
		return nil
	}
	detach := ActionDetach
	r.Restore.Action, r.Restore.OccupiedBy = &detach, strPtr(use)
	if err := writeHead(r, InQuarantine, deref(r.Head)); err != nil {
		return err
	}
	return r.Save("restore.recheck")
}

// writeHead writes the worktree's HEAD in its admin dir, wherever that is.
func writeHead(r *Record, adminAt, head string) error {
	if head == "" {
		return errors.New("no HEAD to write")
	}
	dir := r.Admin.Path
	if adminAt == InQuarantine {
		dir = r.Admin.Quarantined
	}
	return writeDurable(filepath.Join(dir, "HEAD"), []byte(head+"\n"))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
