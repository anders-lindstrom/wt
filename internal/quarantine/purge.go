package quarantine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// PurgingName is the journal of a purge. It replaces recovery.json before
// anything is deleted, so no wt restore — this one or one that predates
// purge — finds a record to restore from while the pins go.
const PurgingName = "recovery.purging.json"

// What a purge finds a folder to be.
const (
	// StateQuarantined: a removal moved a worktree here and finished.
	StateQuarantined = "quarantined"
	// StateRestored: wt restore put it back; the folder and any pins left
	// are leftovers.
	StateRestored = "restored"
	// StatePurging: a purge began here and stopped; it is finished.
	StatePurging = "purging"
	// StateEmpty: an empty folder, which a purge that stopped between its
	// last two steps leaves.
	StateEmpty = "empty"
	// StateUnsettled: a removal or a restore stopped mid-way; wt restore
	// settles it first.
	StateUnsettled = "unsettled"
)

// Why a purge refuses before it deletes anything. It also uses
// ProblemNoRecord, ProblemRepository and ProblemWorktrees.
const (
	ProblemMissing        = "missing"
	ProblemNotAFolder     = "notAFolder"
	ProblemWrongFolder    = "wrongFolder"
	ProblemPins           = "pinsForeign"
	ProblemPinsUnreadable = "pinsUnreadable"
	ProblemRegistered     = "registered"
	ProblemLocked         = "locked"
	ProblemLocation       = "location"
	ProblemUnsettled      = "unsettled"
	// ProblemForeignEntry is something at the folder's top level no
	// quarantine leaves there.
	ProblemForeignEntry = "foreignEntry"
	// ProblemNestedQuarantine is the record of another quarantine inside
	// the folder.
	ProblemNestedQuarantine = "nestedQuarantine"
	// ProblemEmpty is an empty folder: nothing in it names what it was.
	ProblemEmpty = "empty"
	// ProblemPurging is a restore's: a purge began, and its pins may be
	// gone.
	ProblemPurging = "purging"
)

// OutcomePurged is a purge that deleted everything it planned to.
const OutcomePurged = "purged"

// Purge is a purge's own journal inside the record: once it is written,
// the pins may go, and nothing restores the worktree.
type Purge struct {
	StartedAt string `json:"startedAt"`
}

// PurgePin is one ref a quarantine pinned its commits or its folder with:
// head, tip or dir, and where it is now, "" when it is gone.
type PurgePin struct {
	Name string
	Ref  string
	OID  string
}

// PurgeLost is a commit only a pin holds: the commits reachable from it and
// from nothing else, which become unreachable once the pins go.
type PurgeLost struct {
	Kind  string // "head" or "tip"
	OID   string
	Count int
}

// PurgePlan is what a purge finds and would delete.
type PurgePlan struct {
	Dir    string
	Record *Record // nil when the folder has no usable record
	// Data is the record as read, for the fingerprint.
	Data     []byte
	Identity Identity
	State    string
	Pins     []PurgePin
	// Entries is every name in the folder, sorted.
	Entries []string
	// Bytes is the size of every file in the folder, -1 when unknown; Tree
	// is a digest of every path in it with its type, size and time.
	Bytes int64
	Tree  string
	// RepoGone is a repository whose git dir is gone: its pins went with
	// it, or, if it moved, are where wt cannot find them.
	RepoGone   bool
	Lost       []PurgeLost
	ReachError string
	Problems   []Problem
}

// PurgeResult is what a purge came to.
type PurgeResult struct {
	Outcome string
	Error   string
}

// Refusal is every problem in words, "" when there is none.
func (p PurgePlan) Refusal() string {
	var why []string
	for _, pr := range p.Problems {
		why = append(why, pr.Text)
	}
	return strings.Join(why, "; ")
}

func (p *PurgePlan) problem(code, format string, args ...any) {
	p.Problems = append(p.Problems, Problem{Code: code, Text: fmt.Sprintf(format, args...)})
}

// Fingerprint names everything the purge turns on: the folder by path and
// identity, its state, its record byte for byte, every path in it, each pin
// and where it is, and what would be lost. "" when the purge would refuse.
func (p PurgePlan) Fingerprint() string {
	if p.Refusal() != "" {
		return ""
	}
	h := sha256.New()
	field := func(name string, v any) { fmt.Fprintf(h, "%s=%v\x00", name, v) }
	field("wt-quarantine-purge", 1)
	field("dir", p.Dir)
	field("identity", fmt.Sprint(p.Identity.Device, p.Identity.Inode))
	field("state", p.State)
	sum := sha256.Sum256(p.Data)
	field("record", hex.EncodeToString(sum[:]))
	field("entries", strings.Join(p.Entries, "\x01"))
	field("tree", p.Tree)
	for _, pin := range p.Pins {
		field("pin", pin.Ref+" "+pin.OID)
	}
	for _, l := range p.Lost {
		field("lost", fmt.Sprint(l.Kind, l.OID, l.Count))
	}
	field("reachError", p.ReachError)
	field("repoGone", p.RepoGone)
	return hex.EncodeToString(h.Sum(nil))
}

// PlanPurge reads the folder at dir and everything its purge turns on,
// changing nothing. A folder is purged only when its record is one a
// removal wrote for exactly this folder, what is at its checkout and admin
// is what that removal moved there, the pins it names are the ones it made,
// and nothing of the repository — a registered worktree, its git dir, a
// lock taken for it — is in it or around it.
func PlanPurge(dir string) PurgePlan {
	p := PurgePlan{Dir: dir, Bytes: -1}
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		p.problem(ProblemMissing, "%s is not there: nothing to purge", dir)
	case err != nil:
		p.problem(ProblemMissing, "%s cannot be read: %v", dir, err)
	case info.Mode()&os.ModeSymlink != 0:
		p.problem(ProblemNotAFolder, "%s is a symlink: name the folder itself", dir)
	case !info.IsDir():
		p.problem(ProblemNotAFolder, "%s is not a folder", dir)
	}
	if len(p.Problems) > 0 {
		return p
	}
	p.Identity, _ = Identify(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		p.problem(ProblemNotAFolder, "%s cannot be listed: %v", dir, err)
		return p
	}
	for _, e := range entries {
		p.Entries = append(p.Entries, e.Name())
	}
	sort.Strings(p.Entries)
	if len(p.Entries) == 0 {
		p.State, p.Bytes = StateEmpty, 0
		p.problem(ProblemEmpty, "%s is empty: nothing in it says wt moved a worktree there, so purge leaves it "+
			"(a purge stopped at its very last step leaves one; rmdir takes it)", dir)
		return p
	}
	name := PurgingName
	p.Data, err = readRegular(dir, name)
	if errors.Is(err, os.ErrNotExist) {
		name = FileName
		p.Data, err = readRegular(dir, name)
	}
	if errors.Is(err, os.ErrNotExist) {
		p.problem(ProblemNoRecord, "%s has no %s: nothing says wt moved a worktree there, and purge deletes nothing else", dir, FileName)
		return p
	}
	var r *Record
	if err == nil {
		r, err = parse(dir, name, p.Data)
	}
	if err != nil {
		p.problem(ProblemNoRecord, "purge cannot trust the record in %s: %v", dir, err)
		return p
	}
	p.Record = r
	var nested []string
	p.Bytes, p.Tree, nested = treeOf(dir)
	for _, q := range nested {
		p.problem(ProblemNestedQuarantine, "%s holds another folder a worktree was moved to, %s: purge or restore that one first", dir, q)
	}
	if !p.checkFolder(r) {
		return p
	}
	if _, err := os.Lstat(r.CommonDir); errors.Is(err, os.ErrNotExist) {
		// Deleted with its repository, the pins are gone too; moved with
		// it, they are where no path in the record leads.
		p.RepoGone = true
		p.readPins(nil, r)
		p.settle(r, name == PurgingName)
		p.checkEntries()
		return p
	}
	rp, err := repo.Discover(r.Repo)
	if err != nil {
		p.problem(ProblemRepository, "its repository %s cannot be opened: %s", r.Repo, git.Reason(err))
		return p
	}
	if common := commonDir(rp); common == "" || !repo.SamePath(common, r.CommonDir) {
		p.problem(ProblemRepository, "%s is no longer the repository whose git dir is %s", r.Repo, r.CommonDir)
		return p
	}
	p.readPins(rp, r)
	p.settle(r, name == PurgingName)
	p.checkEntries()
	p.checkRepository(rp, r)
	p.readReach(rp)
	return p
}

// checkEntries refuses anything at the folder's top level a quarantine in
// its state does not leave there: the checkout and the admin dir while
// quarantined, the journal and its temp file always, and Finder's
// .DS_Store. Whatever else is there was never wt's.
func (p *PurgePlan) checkEntries() {
	allowed := map[string]bool{FileName: true, PurgingName: true, FileName + ".tmp": true,
		PurgingName + ".tmp": true, ".DS_Store": true}
	if p.State == StateQuarantined || p.State == StatePurging {
		allowed["checkout"], allowed["admin"] = true, true
	}
	for _, e := range p.Entries {
		if !allowed[e] {
			p.problem(ProblemForeignEntry, "%s in it is nothing a removal leaves there: purge deletes only what wt put there",
				filepath.Join(p.Dir, e))
		}
	}
}

// readRegular reads name in dir, refusing anything but a plain file: a
// symlink named like the journal is not one.
func readRegular(dir, name string) ([]byte, error) {
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a plain file", path)
	}
	return os.ReadFile(path)
}

// checkFolder refuses a record written for another folder, one whose
// places are not the folder's checkout and admin, and a checkout or admin
// in the folder that is not the directory the removal moved there: a
// copied record must never make purge delete where it was copied to.
func (p *PurgePlan) checkFolder(r *Record) bool {
	rec := r.recordedDir()
	if r.recordedAs != rec || filepath.Dir(r.Admin.Quarantined) != rec ||
		filepath.Base(r.Checkout.Quarantined) != "checkout" || filepath.Base(r.Admin.Quarantined) != "admin" {
		p.problem(ProblemWrongFolder, "its record does not name this folder's own checkout and git dir")
		return false
	}
	at, err := Identify(rec)
	if err != nil || at != p.Identity {
		p.problem(ProblemWrongFolder, "its record was written for %s, not %s: purge only deletes the folder it names",
			rec, p.Dir)
		return false
	}
	for _, pl := range []Place{r.Checkout, r.Admin} {
		name := filepath.Base(pl.Quarantined)
		id, err := Identify(filepath.Join(p.Dir, name))
		if err == nil && id != (Identity{Device: pl.Device, Inode: pl.Inode}) {
			p.problem(ProblemWrongFolder, "%s in it is not the directory the removal moved there", name)
			return false
		}
	}
	return true
}

// readPins reads each pin the record names, and refuses a name that is not
// this quarantine's, a pin that holds something the removal did not pin,
// and one that cannot be read. With no repository to read them in, each
// is listed as gone.
func (p *PurgePlan) readPins(rp *repo.Repo, r *Record) {
	rec := r.recordedDir()
	key := pinKey(r.WorktreeID, rec)
	tip := ""
	if r.Branch != nil {
		tip = r.Branch.Tip
	}
	for _, pin := range []struct {
		name string
		ref  *string
		want string
	}{{"head", r.Pins.Head, deref(r.Head)}, {"tip", r.Pins.Tip, tip}, {"dir", &r.Pins.Dir, ""}} {
		if pin.ref == nil {
			continue
		}
		if *pin.ref != pinRef(key, pin.name) {
			p.problem(ProblemPins, "its record names the pin %s, which is not this folder's (%s)",
				*pin.ref, pinRef(key, pin.name))
			continue
		}
		if rp == nil {
			p.Pins = append(p.Pins, PurgePin{Name: pin.name, Ref: *pin.ref})
			continue
		}
		oid, err := ReadPin(rp.MainRoot, *pin.ref)
		if err != nil {
			p.problem(ProblemPinsUnreadable, "%s cannot be read: %s", *pin.ref, git.Reason(err))
			continue
		}
		if oid != "" {
			switch pin.name {
			case "dir":
				if named, err := git.Run(rp.MainRoot, "cat-file", "blob", oid); err != nil || named != rec {
					p.problem(ProblemPins, "%s does not name this folder", *pin.ref)
				}
			default:
				if oid != pin.want {
					p.problem(ProblemPins, "%s is at %s, not the %s the removal pinned",
						*pin.ref, git.ShortID(oid, 12), git.ShortID(pin.want, 12))
				}
			}
		}
		p.Pins = append(p.Pins, PurgePin{Name: pin.name, Ref: *pin.ref, OID: oid})
	}
}

// ReadPin is where ref is, "" when it is not there; an error is a ref that
// could not be read, which is not the same as one that is gone.
func ReadPin(mainRoot, ref string) (string, error) {
	out, err := git.Run(mainRoot, "rev-parse", "--verify", "--quiet", ref)
	var gerr *git.Error
	if errors.As(err, &gerr) && gerr.Code == 1 {
		// Missing and broken both exit 1; only a broken one says so.
		if strings.Contains(gerr.Stderr, "broken ref") {
			return "", fmt.Errorf("%s is broken", ref)
		}
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// settle decides the state from the journal: a purge that began, a restore
// that finished, or a removal that did. Anything stopped mid-way is
// wt restore's to settle first: a purge never guesses where a directory
// is.
func (p *PurgePlan) settle(r *Record, purging bool) {
	switch {
	case purging:
		p.State = StatePurging
	case r.Restore != nil:
		for _, s := range r.Restore.Steps {
			if s.State != Done {
				p.State = StateUnsettled
				p.problem(ProblemUnsettled, "a restore from it stopped at %s: wt restore %s finishes it", s.Name, p.Dir)
				return
			}
		}
		p.State = StateRestored
	default:
		for _, s := range r.Steps {
			if s.State != Done && (s.Name != StepBranch || s.State != Failed) {
				p.State = StateUnsettled
				p.problem(ProblemUnsettled, "the removal into it stopped at %s: wt restore %s puts the worktree back",
					s.Name, p.Dir)
				return
			}
		}
		for _, pl := range []Place{r.Checkout, r.Admin} {
			if id, err := Identify(pl.Path); err == nil && id == (Identity{Device: pl.Device, Inode: pl.Inode}) {
				p.State = StateUnsettled
				p.problem(ProblemUnsettled, "%s is back at %s, though the journal says it is not: wt restore %s settles it",
					filepath.Base(pl.Quarantined), pl.Path, p.Dir)
				return
			}
		}
		p.State = StateQuarantined
	}
}

// checkRepository refuses a folder git still has a worktree registered in,
// one a worktree is locked for, and one inside a checkout or the git dir,
// or holding either. Paths are compared by the directories they lead to,
// not by how they are spelled: another case, a symlink on the way.
func (p *PurgePlan) checkRepository(rp *repo.Repo, r *Record) {
	list, err := rp.Worktrees()
	if err != nil {
		p.problem(ProblemWorktrees, "the worktrees cannot be listed: %v", err)
		return
	}
	for _, wt := range list {
		switch {
		case repo.Inside(p.Dir, wt.Path, true) || within(wt.Path, p.Identity):
			p.problem(ProblemRegistered, "git has the worktree %s registered inside it: purge never deletes a registered worktree",
				wt.Path)
		case repo.Inside(wt.Path, p.Dir, true) || holds(wt.Path, p.Dir):
			p.problem(ProblemLocation, "it is inside the checkout %s", wt.Path)
		}
		if q, ok := DirOf(wt.LockReason); ok && wt.Locked &&
			(repo.SamePath(q, p.Dir) || repo.SamePath(q, r.recordedDir()) || sameDir(q, p.Identity)) {
			p.problem(ProblemLocked, "the worktree %s is locked for a removal into this folder: wt restore %s takes that lock off",
				wt.Path, p.Dir)
		}
	}
	if common := commonDir(rp); common != "" {
		if repo.Inside(common, p.Dir, true) || holds(common, p.Dir) ||
			repo.Inside(p.Dir, common, true) || within(common, p.Identity) {
			p.problem(ProblemLocation, "it is inside, or holds, the git dir %s", common)
		}
	}
}

// within reports whether path, or a folder above it, is the directory id.
func within(path string, id Identity) bool {
	for {
		if sameDir(path, id) {
			return true
		}
		up := filepath.Dir(path)
		if up == path {
			return false
		}
		path = up
	}
}

// holds reports whether the directory outer leads to is dir or a folder
// above it.
func holds(outer, dir string) bool {
	info, err := os.Stat(outer)
	if err != nil || !info.IsDir() {
		return false
	}
	return within(dir, identityOf(info))
}

func sameDir(path string, id Identity) bool {
	got, err := Identify(path)
	return err == nil && got == id
}

// readReach counts the commits only the pins hold: what the purge leaves
// unreachable, for gc to take.
func (p *PurgePlan) readReach(rp *repo.Repo) {
	var drop []string
	for _, pin := range p.Pins {
		if pin.OID != "" {
			drop = append(drop, pin.Ref)
		}
	}
	if len(drop) == 0 {
		return
	}
	keep, err := rp.Survivors(drop, "")
	if err != nil {
		p.ReachError = err.Error()
		return
	}
	seen := map[string]bool{}
	for _, pin := range p.Pins {
		if pin.OID == "" || pin.Name == "dir" || seen[pin.OID] {
			continue
		}
		seen[pin.OID] = true
		n, err := rp.CountLost(pin.OID, keep)
		switch {
		case err != nil:
			p.ReachError = strings.TrimSpace(git.Reason(err))
		case n > 0:
			p.Lost = append(p.Lost, PurgeLost{Kind: pin.Name, OID: pin.OID, Count: n})
		}
	}
}

func commonDir(rp *repo.Repo) string {
	out, err := git.Run(rp.MainRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// treeOf adds up the files under dir, digests every path in it with its
// type, size and time, and finds every quarantine below its top level,
// never following a symlink; -1 and "" when something cannot be read.
func treeOf(dir string) (int64, string, []string) {
	var n int64
	var nested []string
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			n += info.Size()
		}
		if d.IsDir() && path != dir && IsQuarantine(path) {
			nested = append(nested, path)
		}
		rel, _ := filepath.Rel(dir, path)
		fmt.Fprintf(h, "%s\x00%v\x00%d\x00%d\x00", rel, info.Mode(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		return -1, "", nested
	}
	return n, hex.EncodeToString(h.Sum(nil)), nested
}

// IsQuarantine reports whether dir holds a wt quarantine's record, as a
// removal or a purge writes it: a project's own file of that name is not
// one.
func IsQuarantine(dir string) bool {
	for _, name := range []string{FileName, PurgingName} {
		if data, err := readRegular(dir, name); err == nil {
			if _, err := parse(dir, name, data); err == nil {
				return true
			}
		}
	}
	return false
}

// InsideQuarantine is the quarantine folder dir is inside, "" when there is
// none: a purge of that one would delete dir with it.
func InsideQuarantine(dir string) string {
	paths := []string{filepath.Dir(dir)}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(dir)); err == nil {
		paths = append(paths, resolved)
	}
	for _, d := range paths {
		for {
			if IsQuarantine(d) {
				return d
			}
			up := filepath.Dir(d)
			if up == d {
				break
			}
			d = up
		}
	}
	return ""
}

// ErrPurgeChanged is a purge whose folder, record or pins are not what the
// plan it was given read.
var ErrPurgeChanged = errors.New("the folder or its pins changed since the plan was read; nothing was purged: read it again")

// LockDir takes the lock a restore and a purge of dir hold while they run,
// so neither changes a quarantine the other is working on; the function it
// returns lets it go. It fails at once when somebody holds it.
func LockDir(dir string) (func(), error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { //nolint:gosec // an fd fits an int
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another wt is restoring or purging %s", dir)
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // as above
		_ = f.Close()
	}, nil
}

// AfterPurge runs, when set, after each deletion a purge makes, with what
// it deleted ("pin:head", "entry:checkout", "record"), and an error from it
// ends the purge there, as a crash would. Tests set it; nothing else does.
var AfterPurge func(point string) error

// DoPurge deletes the quarantine plan was made for, holding the folder's
// lock, and only while the plan read again under it is the same. It
// replaces recovery.json with its own journal, so no restore takes a
// worktree whose pins are going, then deletes the pins, each only at the
// commit read, then — after git's worktrees are read once more — the
// entries the plan listed, then its journal, then the folder. A purge
// stopped anywhere leaves its journal, or an empty folder, and running it
// again finishes it. Every deletion is made inside the folder opened once
// and checked, never through its path again, and nothing is followed out
// of it: a symlink in it is deleted, never what it names.
func DoPurge(plan PurgePlan) (PurgeResult, error) {
	res := PurgeResult{Outcome: OutcomeRefused}
	if why := plan.Refusal(); why != "" {
		return res, errors.New(why)
	}
	unlock, err := LockDir(plan.Dir)
	if err != nil {
		return res, err
	}
	defer unlock()
	p := PlanPurge(plan.Dir)
	if why := p.Refusal(); why != "" {
		return res, errors.New(why)
	}
	if p.Fingerprint() != plan.Fingerprint() {
		return res, ErrPurgeChanged
	}
	root, err := os.OpenRoot(p.Dir)
	if err != nil {
		return res, err
	}
	defer func() { _ = root.Close() }()
	if at, err := root.Lstat("."); err != nil || identityOf(at) != p.Identity {
		return res, ErrPurgeChanged
	}
	if err := p.purgeInside(root, &res); err != nil {
		return res, err
	}
	res.Outcome = OutcomePartial
	if err := removeIn(root, PurgingName); err != nil {
		return res, err
	}
	if err := syncIn(root); err != nil {
		return res, err
	}
	if err := afterPurge("record"); err != nil {
		return res, err
	}
	// rmdir, and only of the folder that was opened: something put at its
	// path since is not it.
	if id, err := Identify(p.Dir); err != nil || id != p.Identity {
		return res, fmt.Errorf("%s is no longer the folder that was purged: it was left", p.Dir)
	}
	if err := os.Remove(p.Dir); err != nil {
		return res, fmt.Errorf("%s is empty, but: %w", p.Dir, err)
	}
	if err := syncDir(filepath.Dir(p.Dir)); err != nil {
		return res, err
	}
	res.Outcome = OutcomePurged
	return res, nil
}

// purgeInside is everything but the last two steps: the journal, the pins,
// and the folder's entries.
func (p PurgePlan) purgeInside(root *os.Root, res *PurgeResult) error {
	r := p.Record
	var rp *repo.Repo
	if !p.RepoGone {
		var err error
		if rp, err = repo.Discover(r.Repo); err != nil {
			return err
		}
	}
	if p.State != StatePurging {
		r.Purge = &Purge{StartedAt: time.Now().UTC().Format(time.RFC3339)}
		data, err := r.marshal()
		if err != nil {
			return err
		}
		res.Outcome = OutcomePartial
		if err := writeIn(root, PurgingName, data); err != nil {
			return err
		}
		if err := removeIn(root, FileName); err != nil {
			return err
		}
		if err := syncIn(root); err != nil {
			return err
		}
		if AfterSave != nil {
			if err := AfterSave(r, "purge.begin"); err != nil {
				return err
			}
		}
	}
	res.Outcome = OutcomePartial
	if rp == nil {
		return p.deleteEntries(root)
	}
	for _, pin := range p.Pins {
		if pin.OID == "" {
			continue
		}
		if _, err := git.Run(rp.MainRoot, "update-ref", "--no-deref", "-d", pin.Ref, pin.OID); err != nil {
			return fmt.Errorf("could not delete %s: %s", pin.Ref, git.Reason(err))
		}
		if err := afterPurge("pin:" + pin.Name); err != nil {
			return err
		}
	}
	// A worktree registered inside it since the plan is never deleted.
	again := PurgePlan{Dir: p.Dir, Identity: p.Identity}
	again.checkRepository(rp, r)
	if why := again.Refusal(); why != "" {
		return errors.New(why)
	}
	return p.deleteEntries(root)
}

// deleteEntries deletes every entry the plan listed but the journals, and
// refuses one it did not list: what appeared since was never shown to
// anyone. The order is fixed, whatever the directory lists first: the
// checkout, then the admin dir, then the rest by name.
func (p PurgePlan) deleteEntries(root *os.Root) error {
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	names, err := d.Readdirnames(-1)
	_ = d.Close()
	if err != nil {
		return err
	}
	rank := map[string]int{"checkout": 0, "admin": 1}
	sort.Slice(names, func(i, j int) bool {
		ri, iok := rank[names[i]]
		rj, jok := rank[names[j]]
		if !iok {
			ri = len(rank)
		}
		if !jok {
			rj = len(rank)
		}
		if ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		switch name {
		case FileName, PurgingName:
			continue
		case FileName + ".tmp", PurgingName + ".tmp":
		default:
			if !listed(p.Entries, name) {
				return fmt.Errorf("%s appeared in %s since the plan was read: it was not deleted", name, p.Dir)
			}
		}
		if err := removeTreeIn(root, name); err != nil {
			return err
		}
		if err := afterPurge("entry:" + name); err != nil {
			return err
		}
	}
	if err := removeIn(root, FileName); err != nil {
		return err
	}
	return syncIn(root)
}

func listed(sorted []string, name string) bool {
	i := sort.SearchStrings(sorted, name)
	return i < len(sorted) && sorted[i] == name
}

// removeTreeIn deletes name and everything under it inside root, never
// following a symlink, making folders writable when one without write
// permission stopped the first try.
func removeTreeIn(root *os.Root, name string) error {
	if err := root.RemoveAll(name); err == nil {
		return nil
	}
	_ = fs.WalkDir(root.FS(), name, func(path string, d fs.DirEntry, _ error) error {
		// A folder is made writable and readable before it is read, so the
		// walk gets into one that was not. A symlink is never a folder here.
		if d != nil && d.IsDir() {
			if info, err := d.Info(); err == nil {
				_ = root.Chmod(path, info.Mode().Perm()|0o700)
			}
		}
		return nil
	})
	return root.RemoveAll(name)
}

func removeIn(root *os.Root, name string) error {
	if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// writeIn writes name inside root durably: a temp file made new, fsynced,
// renamed over name, and the folder fsynced.
func writeIn(root *os.Root, name string, data []byte) error {
	tmp := name + ".tmp"
	if err := removeIn(root, tmp); err != nil {
		return err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
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
	if err := root.Rename(tmp, name); err != nil {
		return err
	}
	return syncIn(root)
}

func syncIn(root *os.Root) error {
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func identityOf(info os.FileInfo) Identity {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Identity{}
	}
	return Identity{Device: uint64(st.Dev), Inode: st.Ino} //nolint:gosec,unconvert,nolintlint // as in Identify
}

func afterPurge(point string) error {
	if AfterPurge != nil {
		return AfterPurge(point)
	}
	return nil
}
