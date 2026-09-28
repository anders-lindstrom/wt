// Command wt manages git worktrees from one implementation, configured per repo.
package main

import (
	"os"

	"github.com/anders-lindstrom/wt/internal/git"
)

func main() {
	if git.IsReaper() {
		git.Reap()
		return
	}
	if exe, err := os.Executable(); err == nil {
		git.UseReaper(exe)
	}
	os.Exit(Execute())
}
