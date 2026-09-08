package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// Answers are the three keys a repository actually varies. Everything else is
// written commented at its default, where the file itself explains it.
type Answers struct {
	MainBranch   string
	BranchPrefix string
	BuildCommand string
}

// InitOptions controls how Init writes a repository's first configuration.
type InitOptions struct {
	// Ask collects the answers, given the values so far to offer as defaults.
	// A nil Ask writes the detected values without asking, which is what a
	// script, a hook or an agent with no terminal gets.
	Ask func(defaults Answers) (Answers, error)

	// Force replaces a configuration that is already there. Without it an
	// existing file is an error: `wt init` writes defaults and would silently
	// discard whatever a repository had deliberately set.
	Force bool
}

// Init writes a repository's first worktree configuration.
//
// It takes a *repo.Repo rather than a *Context because a Context cannot be
// built without a configuration — which is precisely the situation this
// command exists to end.
func Init(r *repo.Repo, opts InitOptions, w io.Writer) error {
	if !opts.Force {
		if existing, err := existingConfig(r.Root); err != nil {
			return err
		} else if existing != "" {
			return fmt.Errorf("%s already exists; edit it, or re-run with --force to replace it", existing)
		}
	}

	answers := Answers{
		MainBranch:   r.DetectMainBranch(),
		BranchPrefix: "feat_wt",
	}

	cfg, err := resolve(answers)
	var asked Answers
	for err != nil || opts.Ask != nil {
		if opts.Ask == nil {
			return err
		}
		// Only a repeated answer stops the loop, so a correction is always
		// given another chance and a script that cannot correct itself does
		// not spin.
		if err != nil {
			if answers == asked {
				return err
			}
			for _, line := range problemLines(err) {
				fmt.Fprintf(w, "  ! %s\n", line)
			}
			asked = answers
		}
		if answers, err = opts.Ask(answers); err != nil {
			return err
		}
		if cfg, err = resolve(answers); err == nil {
			break
		}
	}

	path := filepath.Join(r.Root, "bin", "worktree", "worktree.conf")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(render(r.Name, answers, cfg)), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(w, "Wrote %s\n", path)
	if rule := ignoreRule(r.Root, path); rule != "" {
		fmt.Fprintf(w, "  ! %s is ignored by %s, so git will not track it — "+
			"un-ignore it, or this configuration stays local to you\n",
			filepath.Base(path), rule)
	}
	// Init writes defaults for everything it did not ask about, and cannot
	// know whether the answered branch exists or the build command runs.
	// Doctor checks all of that, so hand over rather than imply it is done.
	fmt.Fprintln(w, "Next: wt doctor")
	return nil
}

// ignoreRule returns the .gitignore rule excluding path, or "" when git would
// track it. A repository that ignores its whole bin/ directory for build
// output silently ignores its worktree configuration too, and the failure only
// shows up in someone else's clone.
func ignoreRule(root, path string) string {
	out, err := git.Run(root, "check-ignore", "--verbose", "--no-index", path)
	if err != nil || out == "" {
		return ""
	}
	// "<source>:<line>:<pattern>\t<path>" — the source and pattern are the
	// two halves of "where do I go to change this".
	fields := strings.SplitN(out, ":", 3)
	if len(fields) < 3 {
		return "your git ignore rules"
	}
	pattern, _, _ := strings.Cut(fields[2], "\t")
	return fmt.Sprintf("%s (%s)", filepath.Base(fields[0]), pattern)
}

// existingConfig returns the path of the configuration this repository already
// has, in the order Load prefers them, or "" when it has none.
func existingConfig(root string) (string, error) {
	for _, name := range []string{"worktree.toml", "worktree.conf"} {
		path := filepath.Join(root, "bin", "worktree", name)
		switch _, err := os.Stat(path); {
		case err == nil:
			return path, nil
		case !os.IsNotExist(err):
			return "", err
		}
	}
	return "", nil
}

// resolve runs the answers through the same validation the loader applies, so
// a rejected value is reported at the prompt rather than written and then
// discovered by the next command. The resolved Config is what render writes:
// taking the commented defaults from it, rather than from a second copy of the
// same table, is what keeps them honest.
func resolve(a Answers) (*config.Config, error) {
	raw := map[string]config.Value{
		"MAIN_BRANCH":            {Scalar: a.MainBranch},
		"WORKTREE_BRANCH_PREFIX": {Scalar: a.BranchPrefix},
	}
	if a.BuildCommand != "" {
		raw["BUILD_INIT_COMMAND"] = config.Value{Scalar: a.BuildCommand}
	}
	return config.FromRaw(raw, a.MainBranch)
}

// problemLines unwraps the loader's multi-problem error into its sentences,
// dropping the "worktree.conf:" heading — at a prompt there is no file yet for
// it to name.
func problemLines(err error) []string {
	var lines []string
	for _, line := range strings.Split(err.Error(), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasSuffix(line, ":") {
			lines = append(lines, strings.TrimPrefix(line, "- "))
		}
	}
	return lines
}

// render produces the configuration file. Answered keys are written live; every
// other key is written commented at the value the loader resolved anyway, so
// uncommenting one changes nothing until it is edited — which is what makes the
// file this repository's reference instead of the README.
func render(repoName string, a Answers, c *config.Config) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	p("# wt configuration for %s.", repoName)
	p("#")
	p("# Read by wt, not sourced: this is data, not a script. Every key is")
	p("# validated — an unknown or misspelled one is an error, not silence.")
	p("#")
	p("# The commented keys are at their defaults, so uncommenting one as it")
	p("# stands changes nothing; edit it to change how this repository works.")
	p("# Reference: https://github.com/anders-lindstrom/wt")
	p("")
	p("# The branch worktrees are cut from, and merged into.")
	p("MAIN_BRANCH=%s", c.MainBranch)
	p("")
	p("# The branch prefix `wt new <work>` uses when given no type.")
	p("WORKTREE_BRANCH_PREFIX=%s", c.BranchPrefix)
	p("")
	p("# The suffix marking a type in a branch name and worktree path.")
	p("# WORKTREE_TYPE_SUFFIX=%s", c.TypeSuffix)
	p("")
	p("# The type a bare `wt new <work>` takes. Derived from the prefix above;")
	p("# set it only when the two should differ, and to one of WORKTREE_TYPES.")
	p("# WORKTREE_DEFAULT_TYPE=%s", c.DefaultType)
	p("")
	p("# The types a worktree may use: Conventional Commits, plus the two")
	p("# exploratory kinds that produce no feature.")
	p("# WORKTREE_TYPES=(%s)", strings.Join(c.Types, " "))
	p("")
	p("# Untracked developer configuration copied into each new worktree by")
	p("# `wt setup`, since git does not carry it.")
	p("# DEVELOPER_CONFIG_DIRS=(%s)", strings.Join(c.DeveloperConfigDirs, " "))
	p("# DEVELOPER_CONFIG_FILES=(%s)", strings.Join(c.DeveloperConfigFiles, " "))
	p("")
	p("# Command run in a new worktree by `wt setup`, after configuration is")
	p("# copied and after bin/worktree/provision.sh, if this repo has one.")
	if a.BuildCommand != "" {
		p("BUILD_INIT_COMMAND=%q", c.BuildInitCommand)
	} else {
		p("# BUILD_INIT_COMMAND=%s", c.BuildInitCommand)
	}
	p("# BUILD_INIT_ENABLED=%s", strconv.FormatBool(c.BuildInitEnabled))
	p("")
	p("# Tools that must be on PATH for this repository; `wt doctor` checks them.")
	p("# REQUIRED_BINS=(%s)", strings.Join(c.RequiredBins, " "))
	p("")
	p("# Run the tests before removing a worktree. TEST_COMMAND is required")
	p("# whenever this is on.")
	p("# TEST_COMMAND=%s", c.TestCommand)
	p("# RUN_TESTS_BEFORE_REMOVE=%s", strconv.FormatBool(c.RunTestsBeforeRemove))
	return b.String()
}
