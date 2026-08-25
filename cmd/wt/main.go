// Command wt manages git worktrees from one implementation, configured per repo.
package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: wt <command> [args...]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "wt: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}
