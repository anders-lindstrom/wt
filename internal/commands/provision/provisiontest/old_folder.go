package provisiontest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
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

// OldFolder moves a new worktree for work into a folder by hand, the way a
// wt from before --move-to left one: every name on disk is written out here
// and none is taken from the code, so a rename there that would strand the
// folders people already have fails these tests. It returns the context,
// where the worktree was, its branch, its tip and the folder.
func OldFolder(t *testing.T, work string) (ctx *kit.Context, path, branch, tip, dir string) {
	t.Helper()
	ctx, path = SafetyWorktree(t, work)
	root := ctx.Repo.MainRoot
	branch = strings.TrimSpace(testkit.GitOut(t, path, "symbolic-ref", "--short", "HEAD"))
	tip = strings.TrimSpace(testkit.GitOut(t, path, "rev-parse", "HEAD"))
	admin := testkit.GitDirFor(t, path)
	dir = testkit.TrashFor(t, ctx)
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

	testkit.GitIn(t, root, "worktree", "lock", "--reason", "wt quarantine "+dir, path)
	testkit.GitIn(t, root, "update-ref", "refs/wt-quarantine/"+key+"/head", tip)
	testkit.GitIn(t, root, "update-ref", "refs/wt-quarantine/"+key+"/tip", tip)
	blob := filepath.Join(t.TempDir(), "dir")
	if err := os.WriteFile(blob, []byte(dir), 0o644); err != nil {
		t.Fatal(err)
	}
	testkit.GitIn(t, root, "update-ref", "refs/wt-quarantine/"+key+"/dir",
		strings.TrimSpace(testkit.GitOut(t, root, "hash-object", "-w", blob)))
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(dir, "checkout")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(admin, filepath.Join(dir, "admin")); err != nil {
		t.Fatal(err)
	}
	testkit.MustWrite(t, filepath.Join(dir, "checkout", ".git"), "gitdir: "+filepath.Join(dir, "admin")+"\n")
	testkit.GitIn(t, root, "update-ref", "-d", "refs/heads/"+branch)
	testkit.MustWrite(t, filepath.Join(dir, "recovery.json"), fmt.Sprintf(oldRecord, root, filepath.Join(root, ".git"),
		filepath.Base(admin), dir, path, filepath.Join(dir, "checkout"), checkoutID.Device, checkoutID.Inode,
		admin, filepath.Join(dir, "admin"), adminID.Device, adminID.Inode, "gitdir: "+admin+"\n", key, tip, branch))
	return ctx, path, branch, tip, dir
}
