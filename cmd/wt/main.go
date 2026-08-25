// Command wt manages git worktrees from one implementation, configured per repo.
package main

import (
	"fmt"
	"os"

	"github.com/anders-lindstrom/wt/internal/commands"
	"github.com/anders-lindstrom/wt/internal/naming"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: wt <command> [args...]")
		os.Exit(2)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fail(err)
	}

	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "config":
		ctx, err := commands.Open(cwd)
		if err != nil {
			fail(err)
		}
		shell := len(os.Args) > 2 && os.Args[2] == "--shell"
		if err := commands.Config(ctx, shell, os.Stdout); err != nil {
			fail(err)
		}
	case "path", "branch":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: wt %s <type>/<work>\n", os.Args[1])
			os.Exit(2)
		}
		ctx, err := commands.Open(cwd)
		if err != nil {
			fail(err)
		}
		resolve := commands.Path
		if os.Args[1] == "branch" {
			resolve = commands.Branch
		}
		out, err := resolve(ctx, os.Args[2])
		if err != nil {
			fail(err)
		}
		fmt.Println(out)
	case "branch-strip":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: wt branch-strip <branch>")
			os.Exit(2)
		}
		ctx, err := commands.Open(cwd)
		if err != nil {
			fail(err)
		}
		fmt.Println(naming.StripPrefix(os.Args[2], ctx.Config.TypeSuffix))
	default:
		fmt.Fprintf(os.Stderr, "wt: unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "wt: %v\n", err)
	os.Exit(1)
}
