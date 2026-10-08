package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// oldRecord is the recovery.json wt remove --quarantine wrote at recovery
// 1.2.0, before the flag was --move-to, for a merged branch it deleted. The
// verbs are, in order: the repository and its git dir, the worktree's id,
// the folder, the checkout's path, where it went, its device and inode, the
// same four for the admin dir, the checkout's .git file, the pins' key, the
// commit, the branch.
const oldRecord = `{
  "schema": 1,
  "schemaVersion": "1.2.0",
  "command": "remove",
  "createdAt": "2026-10-08T09:59:06Z",
  "repo": %[1]q,
  "commonDir": %[2]q,
  "worktreeId": %[3]q,
  "dir": %[4]q,
  "checkout": {
    "path": %[5]q,
    "quarantined": %[6]q,
    "device": %[7]d,
    "inode": %[8]d
  },
  "admin": {
    "path": %[9]q,
    "quarantined": %[10]q,
    "device": %[11]d,
    "inode": %[12]d
  },
  "gitFile": %[13]q,
  "pins": {
    "head": "refs/wt-quarantine/%[14]s/head",
    "tip": "refs/wt-quarantine/%[14]s/tip",
    "dir": "refs/wt-quarantine/%[14]s/dir"
  },
  "head": %[15]q,
  "branch": {
    "name": %[16]q,
    "tip": %[15]q,
    "plan": "delete",
    "keepAs": null,
    "config": [],
    "result": "deleted"
  },
  "steps": [
    {"name": "lock", "state": "done", "error": null},
    {"name": "pin", "state": "done", "error": null},
    {"name": "moveCheckout", "state": "done", "error": null},
    {"name": "moveAdmin", "state": "done", "error": null},
    {"name": "relink", "state": "done", "error": null},
    {"name": "branch", "state": "done", "error": null}
  ],
  "restore": null,
  "purge": null
}
`

// oldFolder moves a new worktree for work into a folder by hand, the way a
// wt from before --move-to left one: every name on disk is written out here
// and none is taken from the code, so a rename there that would strand the
// folders people already have fails these tests. It returns the context,
// where the worktree was, its branch, its tip and the folder.
func oldFolder(t *testing.T, work string) (ctx *Context, path, branch, tip, dir string) {
	t.Helper()
	ctx, path = safetyWorktree(t, work)
	root := ctx.Repo.MainRoot
	branch = strings.TrimSpace(gitOut(t, path, "symbolic-ref", "--short", "HEAD"))
	tip = strings.TrimSpace(gitOut(t, path, "rev-parse", "HEAD"))
	admin := gitDirFor(t, path)
	dir = trashFor(t, ctx)
	checkoutID, err := quarantine.Identify(path)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := quarantine.Identify(admin)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(dir))
	key := filepath.Base(admin) + "-" + hex.EncodeToString(sum[:])[:12]

	gitIn(t, root, "worktree", "lock", "--reason", "wt quarantine "+dir, path)
	gitIn(t, root, "update-ref", "refs/wt-quarantine/"+key+"/head", tip)
	gitIn(t, root, "update-ref", "refs/wt-quarantine/"+key+"/tip", tip)
	blob := filepath.Join(t.TempDir(), "dir")
	if err := os.WriteFile(blob, []byte(dir), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "update-ref", "refs/wt-quarantine/"+key+"/dir",
		strings.TrimSpace(gitOut(t, root, "hash-object", "-w", blob)))
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(dir, "checkout")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(admin, filepath.Join(dir, "admin")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "checkout", ".git"), "gitdir: "+filepath.Join(dir, "admin")+"\n")
	gitIn(t, root, "update-ref", "-d", "refs/heads/"+branch)
	mustWrite(t, filepath.Join(dir, "recovery.json"), fmt.Sprintf(oldRecord, root, filepath.Join(root, ".git"),
		filepath.Base(admin), dir, path, filepath.Join(dir, "checkout"), checkoutID.Device, checkoutID.Inode,
		admin, filepath.Join(dir, "admin"), adminID.Device, adminID.Inode, "gitdir: "+admin+"\n", key, tip, branch))
	return ctx, path, branch, tip, dir
}

// A folder a wt from before --move-to made is put back by this one: its
// record, its lock and its pins are read under the names they were written
// with.
func TestRestorePutsBackAFolderMadeTheOldWay(t *testing.T) {
	ctx, path, branch, tip, dir := oldFolder(t, "fix/old-way")
	res := restore(t, ctx, dir)
	if res.Outcome != quarantine.OutcomeRestored {
		t.Fatalf("outcome %s", res.Outcome)
	}
	assertBack(t, ctx, path, dir, "ref: refs/heads/"+branch)
	if got := strings.TrimSpace(gitOut(t, ctx.Repo.MainRoot, "rev-parse", branch)); got != tip {
		t.Errorf("the branch is at %s, want %s", got, tip)
	}
	if pins := gitOut(t, ctx.Repo.MainRoot, "for-each-ref", "refs/wt-quarantine/"); strings.TrimSpace(pins) != "" {
		t.Errorf("the pins must be gone: %s", pins)
	}
}

// And it is purged by this one, its three pins with it.
func TestPurgeDeletesAFolderMadeTheOldWay(t *testing.T) {
	ctx, _, _, _, dir := oldFolder(t, "fix/old-way")
	res := purge(t, dir)
	if res.Outcome != quarantine.OutcomePurged {
		t.Fatalf("outcome %s", res.Outcome)
	}
	assertPurged(t, ctx, dir)
}
