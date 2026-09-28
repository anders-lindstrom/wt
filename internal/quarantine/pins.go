package quarantine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// PinPrefix is where the pins of every quarantine of a repository live.
const PinPrefix = "refs/wt-quarantine/"

// pinKey names one quarantine's pins: the worktree's id, for a person
// reading the refs, and a hash of the folder, because an id comes back
// once a new worktree takes the name.
func pinKey(id, dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return id + "-" + hex.EncodeToString(sum[:])[:12]
}

func pinRef(key, name string) string { return PinPrefix + key + "/" + name }

// Pin creates the refs that hold the worktree's HEAD and its branch's tip
// while it is quarantined, and the one naming the folder. Pinning again
// writes the same values.
func (r *Record) Pin(rp *repo.Repo) error {
	if r.Pins.Head != nil && r.Head != nil {
		if err := setRef(rp, *r.Pins.Head, *r.Head); err != nil {
			return err
		}
	}
	if r.Pins.Tip != nil && r.Branch != nil {
		if err := setRef(rp, *r.Pins.Tip, r.Branch.Tip); err != nil {
			return err
		}
	}
	blob, err := git.Exec(git.Opts{Dir: rp.MainRoot, Stdin: strings.NewReader(r.recordedDir())},
		"hash-object", "-w", "--stdin")
	if err != nil {
		return err
	}
	return setRef(rp, r.Pins.Dir, strings.TrimSpace(string(blob)))
}

func setRef(rp *repo.Repo, ref, oid string) error {
	if _, err := git.Run(rp.MainRoot, "update-ref", "--no-deref", "-m", "wt quarantine", ref, oid); err != nil {
		return fmt.Errorf("could not pin %s at %s: %s", ref, git.ShortID(oid, 12), git.Reason(err))
	}
	return nil
}

// Unpin deletes the quarantine's pins; one already gone is not an error.
func (r *Record) Unpin(rp *repo.Repo) error {
	refs := []string{r.Pins.Dir}
	if r.Pins.Head != nil {
		refs = append(refs, *r.Pins.Head)
	}
	if r.Pins.Tip != nil {
		refs = append(refs, *r.Pins.Tip)
	}
	return deleteRefs(rp, refs)
}

func deleteRefs(rp *repo.Repo, refs []string) error {
	for _, ref := range refs {
		// Deleting a ref that is not there succeeds.
		if _, err := git.Run(rp.MainRoot, "update-ref", "--no-deref", "-d", ref); err != nil {
			return fmt.Errorf("could not delete %s: %s", ref, git.Reason(err))
		}
	}
	return nil
}

// recordedDir is the folder as the removal named it, which the lock reason
// and the pins carry; Dir is where the record was read from.
func (r *Record) recordedDir() string {
	return filepath.Dir(r.Checkout.Quarantined)
}

// DropDiscarded deletes the pins of every quarantine whose folder has been
// deleted — the user emptied the Trash — and names each folder. A folder
// whose parent is gone too may be on a volume that is not mounted, and
// keeps its pins.
func DropDiscarded(rp *repo.Repo) ([]string, error) {
	out, err := git.Lines(rp.MainRoot, "for-each-ref", "--format=%(refname)", PinPrefix)
	if err != nil {
		return nil, err
	}
	byKey := map[string][]string{}
	var keys []string
	for _, ref := range out {
		key, _, ok := strings.Cut(strings.TrimPrefix(ref, PinPrefix), "/")
		if !ok {
			continue
		}
		if _, seen := byKey[key]; !seen {
			keys = append(keys, key)
		}
		byKey[key] = append(byKey[key], ref)
	}
	var dropped []string
	for _, key := range keys {
		dir, err := git.Run(rp.MainRoot, "cat-file", "blob", pinRef(key, "dir"))
		if err != nil || dir == "" {
			continue
		}
		if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		if _, err := os.Stat(filepath.Dir(dir)); err != nil {
			continue
		}
		if err := deleteRefs(rp, byKey[key]); err != nil {
			return dropped, err
		}
		dropped = append(dropped, dir)
	}
	return dropped, nil
}

// Relink writes the checkout's .git, wherever the checkout is now, to name
// the admin dir wherever it is now: the quarantined one while quarantined,
// so a new worktree that takes the id is never the one git in the
// quarantine works on, and the original .git file once back.
func (r *Record) relink(checkoutAt string, back bool) error {
	dir := r.Checkout.Path
	if checkoutAt == InQuarantine {
		dir = r.Checkout.Quarantined
	}
	want := "gitdir: " + r.Admin.Quarantined + "\n"
	if back {
		want = r.GitFile
	}
	file := filepath.Join(dir, ".git")
	if now, err := os.ReadFile(file); err == nil && string(now) == want {
		return nil
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(want); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Relink points the quarantined checkout's .git at the quarantined admin
// dir.
func (r *Record) Relink() error { return r.relink(InQuarantine, false) }

// Unlink is Relink undone: the checkout, wherever it is, names its
// original admin dir again, as its .git file did before.
func (r *Record) Unlink(checkoutAt string) error { return r.relink(checkoutAt, true) }
