package kit

import (
	"crypto/sha256"
	"os"
	"path/filepath"
)

// ConfigFingerprint is the bytes of the configuration file ctx read, the
// worktree's own or else the main checkout's, the way loadFor finds it.
func ConfigFingerprint(ctx *Context) []byte {
	for _, root := range []string{ctx.Repo.Root, ctx.Repo.MainRoot} {
		for _, name := range []string{"worktree.toml", "worktree.conf"} {
			if data, err := os.ReadFile(filepath.Join(root, "bin", "worktree", name)); err == nil {
				sum := sha256.Sum256(data)
				return sum[:]
			}
		}
	}
	return nil
}
