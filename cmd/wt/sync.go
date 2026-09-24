package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/anders-lindstrom/wt/internal/commands"
)

func newSyncCmd() *cobra.Command {
	var noFetch bool
	sync := &cobra.Command{
		Use:   "sync [<work>]",
		Short: "Show what a rebase onto trunk would do to each worktree",
		Long: "Fetch trunk, then simulate rebasing every worktree onto origin/<trunk> in\n" +
			"the object store, applying the repository's declared strategies at every\n" +
			"stop, and print the outcome grouped by what to do about it. The fetch moves\n" +
			"only origin/<trunk>; nothing of yours is changed. --no-fetch compares with\n" +
			"trunk as last fetched, and so does a fetch that fails, saying why.\n" +
			"\n" +
			"The flow is look, act, finish.\n" +
			"  look    wt sync                 every worktree, grouped; changes nothing of yours\n" +
			"          wt sync <work>          one worktree in full: every stop, file and key\n" +
			"  act     wt sync run <work>...   rebase; safety ref, strategies at each stop, deferred steps;\n" +
			"                                  asks once when more than one worktree is involved or a\n" +
			"                                  session is idle in one (--yes skips)\n" +
			"          wt sync <work>... --run\n" +
			"          wt sync --run           every worktree the table calls ready, asked first\n" +
			"          wt sync <work> --run --if-ready\n" +
			"          wt up                   the same for the worktree you are in\n" +
			"                                  only if it goes through without needing you; else\n" +
			"                                  say why, touch nothing, and fail\n" +
			"  finish  wt sync resume <work>   continue a rebase run left at a conflict that is yours,\n" +
			"                                  then push, asked first as for run\n" +
			"          wt sync <work> --resume\n" +
			"          wt sync undo <work>     put back every ref that run moved\n" +
			"          wt sync <work> --undo\n" +
			"          wt sync doctor          what a run needs, and --fix / --prune\n" +
			"  keep    wt sync keep start      a launchd job that runs wt sync keep once every 30m:\n" +
			"                                  every ready worktree with nobody in it, then push\n" +
			"Only Claude sessions are detected; a Codex session is not seen.\n" +
			"\n" +
			"--run, --resume and --undo are those same three commands, spelled so a\n" +
			"recalled wt sync <work> line is finished by adding the verb at the end. A\n" +
			"verb's own flags go with it: --yes (-y), --push and --no-push with --run or\n" +
			"--resume, --force with --undo. --run with no worktree named takes every\n" +
			"worktree under ready except recipe?, lists what it leaves alone, and asks\n" +
			"before moving even one (--yes skips).\n" +
			"\n" +
			"--if-ready with --run means: rebase what is ready, and fail if anything\n" +
			"was not. Ready here is conflict-free, or every stop resolved by a\n" +
			"strategy the simulation verified: recipe? is listed under ready but\n" +
			"does not count. A named worktree that is not ready is refused with its\n" +
			"stack and left untouched. With nothing named a run takes only the\n" +
			"ready ones anyway, so --if-ready changes one thing there: a worktree\n" +
			"left behind trunk fails the run (stale and detached ones do not),\n" +
			"where without it the run succeeds.\n" +
			"\n" +
			"--all, --roots or --profile cover many repositories from anywhere: the\n" +
			"overview of each, or with --run every ready worktree in each, planned a\n" +
			"few repositories at a time, asked once for all of them, then rebased one\n" +
			"repository after another. A repository whose trunk declares no\n" +
			".wt-sync.yaml is listed, not failed.\n" + selectionHelp + "\n" +
			"\n" +
			"Groups:\n" +
			"  ready      conflict-free or recipe, with nothing in the way: wt sync\n" +
			"             run <work>, or wt sync --run for all of them but recipe?\n" +
			"  needs you  contested, divergent, dirty, handed over, or not assessed\n" +
			"  skipped    a busy session is in it, nothing is ahead of trunk, or no branch\n" +
			"\n" +
			"Classes:\n" +
			"  conflict-free\n" +
			"             rebases without a conflict\n" +
			"  recipe     every conflict at every stop is claimed by a strategy in\n" +
			"             .wt-sync.yaml; a run completes on its own. recipe? means a\n" +
			"             script owns a path and the replay could not be carried past\n" +
			"             it: a run may still stop later, and hands you a plan if it does\n" +
			"  contested  a conflict somewhere in the replay is nobody's; a run rebases\n" +
			"             up to it, stages what the strategies did resolve, and leaves a\n" +
			"             plan file \u2014 finish it and wt sync resume <work>. A branch\n" +
			"             with a stack above it is put back instead, so a stack is\n" +
			"             never half-applied\n" +
			"  divergent  branch and trunk moved apart structurally; never rebased\n" +
			"             automatically\n" +
			"  stale      nothing ahead of trunk; skipped\n" +
			"  current    already on trunk; not printed\n" +
			"  unknown    the assessment hit an error; the line under it says which\n" +
			"\n" +
			"Under each worktree: the stop that decides the class \u2014 the first one\n" +
			"that is yours, or the first of a run that resolves throughout \u2014 with its\n" +
			"files marked \u2713 resolved or \u2717 yours, then anything else worth knowing,\n" +
			"every list cut to a count. wt sync <work> prints the lists, one item per\n" +
			"line. A session named on a row is a Claude session in that worktree: a busy\n" +
			"one keeps every verb off it. One marked (idle) is waiting for its person; a\n" +
			"verb names it, asks first, and ends with a wt: line to pass on to it. +N\n" +
			"counts the other sessions there. The session wt itself runs under is not\n" +
			"counted. The lines under a row are advisory and never change the class.",
		Example: "  wt sync --no-fetch            # every worktree, against trunk as last fetched\n" +
			"  wt sync login-crash           # that worktree in full\n" +
			"  wt sync . --run --if-ready    # rebase it only if it needs nothing from you\n" +
			"  wt sync --all --run --if-ready # every ready worktree, every repository\n" +
			"  wt sync --profile api         # the overview of a profile's repositories",
		ValidArgsFunction: completeWork,
	}
	var run, resume, undo, yes, force, ifReady bool
	var sel selectionFlags
	var push func() commands.PushMode
	flags := func() syncVerbFlags {
		return syncVerbFlags{run: run, resume: resume, undo: undo,
			yes: yes, force: force, noFetch: noFetch, ifReady: ifReady, push: push()}
	}
	// The count rule is the verb's own. Two verbs at once is a flag mistake,
	// which RunE reports the way --push with --no-push is reported.
	sync.Args = func(cmd *cobra.Command, args []string) error {
		verb, err := flags().verb()
		if err != nil {
			return nil
		}
		return syncArgs(verb)(cmd, args)
	}
	sync.RunE = func(cmd *cobra.Command, args []string) error {
		f := flags()
		if err := f.check(); err != nil {
			return err
		}
		verb, _ := f.verb()
		if sel.selection().Any() {
			return syncAcross(cmd, args, verb, sel.selection(), f)
		}
		switch verb {
		case "run":
			return withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
				return syncRun(cmd, args, ctx, f)
			})(cmd, args)
		case "resume":
			return withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
				return syncResume(cmd, args[0], ctx, f.yes, f.push)
			})(cmd, args)
		case "undo":
			return withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
				return syncUndo(cmd, args[0], ctx, f.force, f.yes)
			})(cmd, args)
		}
		// Lenient: a repository without worktree.conf still has worktrees
		// worth reporting on, and the trunk name falls back to origin/HEAD.
		ctx, err := openLenient(cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		if ctx == nil {
			return commands.ErrNotInRepo
		}
		opts := commands.SyncOptions{NoFetch: noFetch}
		if len(args) == 1 {
			return commands.SyncWorktree(ctx, args[0], opts, cmd.OutOrStdout())
		}
		return commands.Sync(ctx, opts, cmd.OutOrStdout())
	}
	sync.Flags().BoolVar(&noFetch, "no-fetch", false, "compare with origin/<trunk> as last fetched; with --run, rebase onto it")
	sync.Flags().BoolVar(&run, "run", false, "wt sync run <work>..., spelled at the end; alone, every ready worktree")
	sync.Flags().BoolVar(&resume, "resume", false, "wt sync resume <work>, spelled at the end of the line")
	sync.Flags().BoolVar(&undo, "undo", false, "wt sync undo <work>, spelled at the end of the line")
	sync.Flags().BoolVarP(&yes, "yes", "y", false, "with a verb: do not ask first")
	push = addPushFlags(sync, "with --run or --resume: push when done, without asking",
		"with --run or --resume: neither push nor ask; print the push command")
	sync.Flags().BoolVarP(&force, "force", "f", false, "with --run: past a Claude session in it; with --undo: past a moved branch")
	sync.Flags().BoolVar(&ifReady, "if-ready", false, "with --run: rebase only what will go through without needing you, and fail on the rest")
	sel.add(sync, true)
	// The verbs spelled as flags, and their own flags, work on this line but
	// belong to the verbs: their help is wt sync run, resume and undo --help,
	// and the table here keeps to what the overview itself takes.
	for _, name := range []string{"run", "resume", "undo", "yes", "push", "no-push", "force", "if-ready"} {
		_ = sync.Flags().MarkHidden(name)
	}
	sync.AddCommand(newSyncRunCmd(), newSyncResumeCmd(), newSyncUndoCmd(), newSyncDoctorCmd(), newSyncKeepCmd())
	return sync
}

func newSyncKeepCmd() *cobra.Command {
	keep := &cobra.Command{
		Use:   "keep",
		Short: "Keep the ready worktrees rebased on trunk, unattended",
		Long: "A keeper is wt sync --run --yes --push on a timer, with nobody\n" +
			"watching. Each pass fetches trunk and, when its tip has moved since the\n" +
			"last pass, rebases every worktree the table calls ready, pushes what\n" +
			"finished with nothing owed (--force-with-lease --force-if-includes), and\n" +
			"records what it did. It is stricter than a run you start: a worktree\n" +
			"with any Claude session in it, idle or busy, is left alone, since nobody\n" +
			"is there to be asked on its behalf; recipe?, needs you and skipped are\n" +
			"left alone as a run with nothing named leaves them, and a stack goes\n" +
			"whole or not at all. A conflict that is yours is never handed over by a\n" +
			"keeper: the worktree was not ready, so it was not taken.\n" +
			"\n" +
			"  wt sync keep once     one pass, what the job runs; usable from cron\n" +
			"  wt sync keep start    install the job (macOS launchd), every 30m by default\n" +
			"  wt sync keep status   installed or not, the last pass, the next one\n" +
			"  wt sync keep stop     unload the job and remove its plist\n" +
			"\n" +
			"Every pass is appended to .git/wt-sync-keep.log in the main checkout:\n" +
			"the time, trunk before and after, what was rebased, pushed and left, and\n" +
			"the run's own output. The log is rotated at 1 MB, the previous file kept\n" +
			"as .log.1. .git/wt-sync-keep.json holds the last pass for wt sync's\n" +
			"kept-at line and wt sync doctor's keeper row; a pass that failed stays\n" +
			"there as a problem until a pass ends with nothing owed. Two passes never\n" +
			"overlap: the json names the running one, and a pass that finds it alive\n" +
			"says so and exits. A pass takes the same locks a run takes and is\n" +
			"refused by them the same way. Without a subcommand this is wt sync keep\n" +
			"status.",
		Example: "  wt sync keep            # is a keeper installed, and what did it last do\n" +
			"  wt sync keep once       # one pass now, by hand or from cron\n" +
			"  wt sync keep status     # the same as wt sync keep",
		Args: cobra.NoArgs,
		RunE: withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			return commands.SyncKeepStatus(ctx, time.Now(), cmd.OutOrStdout())
		}),
	}
	keep.AddCommand(newSyncKeepRunCmd(), newSyncKeepStartCmd(), newSyncKeepStatusCmd(), newSyncKeepStopCmd())
	return keep
}

func newSyncKeepRunCmd() *cobra.Command {
	var every time.Duration
	var noPush bool
	run := &cobra.Command{
		Use:     "once",
		Aliases: []string{"run"},
		Short:   "One pass: fetch, rebase what is ready and nobody is in, push",
		Long: "What the keeper's job runs every interval, and what to run from cron\n" +
			"where there is no launchd. Fetch trunk; when its tip is the one the last\n" +
			"pass recorded, say so and exit 0. Otherwise rebase every ready worktree\n" +
			"as wt sync --run --yes would, except that a worktree with any Claude\n" +
			"session in it is left alone and named with the session as the reason,\n" +
			"and push each worktree that finished with nothing owed, with\n" +
			"--force-with-lease --force-if-includes. Nothing is asked. The pass is\n" +
			"appended to .git/wt-sync-keep.log and its outcome written to\n" +
			".git/wt-sync-keep.json. The exit code is wt sync run's: non-zero when\n" +
			"anything was refused, restored, failed or owed, a push that did not go\n" +
			"through included; that push is then spelled out as a git command.\n" +
			"Every commit a pass makes, replayed or deferred, is unsigned, whatever\n" +
			"your git config says: a signer that asks has nobody to ask. A pass that\n" +
			"cannot list Claude sessions (no claude on the PATH, or a listing that\n" +
			"fails) rebases nothing: it cannot tell who is in a worktree. The session\n" +
			"wt itself runs under is not counted, as wt sync run does not count it:\n" +
			"under launchd there is none, and a pass you run from inside one is\n" +
			"attended by you.\n" +
			"\n" +
			"A pass that failed does not record trunk, so it is tried again next\n" +
			"interval: a lock a person's run held, or a fetch that timed out, clears\n" +
			"itself that way, and a worktree already rebased is current and skipped.\n" +
			"A refused push is retried every pass until it goes through or the\n" +
			"branch moves; the git command in the log does it by hand. A new\n" +
			"worktree, or a branch that moved, waits for trunk to move. The last\n" +
			"failure stays in the status as a problem until a pass ends with nothing\n" +
			"owed and nothing left unpushed.\n" +
			"\n" +
			"The job passes its interval as --every, for the next-run line the\n" +
			"status shows; nothing else needs it. --no-push rebases only and prints\n" +
			"the push commands, into the log when the job runs it. A pass that finds\n" +
			"another still running exits saying so. Ctrl-C is a run's Ctrl-C: every\n" +
			"lock goes and a worktree caught mid-rebase is named with its way back.",
		Example: "  wt sync keep once              # one pass now\n" +
			"  wt sync keep once --no-push    # rebase only; print the push commands",
		Args: cobra.NoArgs,
		RunE: withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			opts := commands.KeepOptions{Every: every, Push: commands.PushAlways}
			if noPush {
				opts.Push = commands.PushNever
			}
			return commands.SyncKeepRun(ctx, opts, cmd.OutOrStdout())
		}),
	}
	run.Flags().DurationVar(&every, "every", 0, "the job's interval, for the next-run line (default 30m)")
	_ = run.Flags().MarkHidden("every")
	run.Flags().BoolVar(&noPush, "no-push", false, "rebase only; print the push commands")
	return run
}

func newSyncKeepStartCmd() *cobra.Command {
	var every time.Duration
	var noPush bool
	start := &cobra.Command{
		Use:   "start",
		Short: "Install a launchd job that runs wt sync keep once on a timer",
		Long: "Write ~/Library/LaunchAgents/se.wt.sync-keep.<repo>-<hash>.plist, one per\n" +
			"main checkout, running this wt binary's sync keep once from that checkout\n" +
			"every --every (30m), with its output under the checkout's .git, and load\n" +
			"it with launchctl bootstrap. The first pass is one interval after this.\n" +
			"Refused when a job is already installed: wt sync keep status says so.\n" +
			"macOS only; elsewhere this exits 2 and wt sync keep once is for cron.\n" +
			"\n" +
			"A launchd job inherits nothing from your shell, so start captures what\n" +
			"the pass needs into the plist: PATH (git, and claude to see who is in a\n" +
			"worktree), HOME (~/.ssh/config, ~/.gitconfig), and how git push\n" +
			"authenticates: SSH_AUTH_SOCK, or GIT_SSH_COMMAND with the key file it\n" +
			"names. Run start from your own shell and the job pushes through your ssh\n" +
			"agent, as your git does; with 1Password's agent that works while you are\n" +
			"logged in, and it may ask you to approve the key. Run it from an agent\n" +
			"session and the job gets that launcher's key, which is shredded when the\n" +
			"agent exits: start says so. GIT_SSH_COMMAND is captured verbatim, so\n" +
			"keep it free of inline credentials; the 1Password token is never\n" +
			"written, and the plist is readable by you alone. A push that fails is\n" +
			"named in the log with the command that does it by hand.\n" +
			"The plist also turns commit signing off for the job: every commit a\n" +
			"pass makes is unsigned, so a signer that asks never has to.\n" +
			"--no-push installs a job that rebases only and logs the push commands.",
		Example: "  wt sync keep start                # every 30m, pushing as this shell does\n" +
			"  wt sync keep start --every 1h     # a longer interval\n" +
			"  wt sync keep start --no-push      # rebase only; the pushes are yours",
		Args: cobra.NoArgs,
		RunE: withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			return commands.SyncKeepStart(ctx, commands.KeepStartOptions{Every: every, NoPush: noPush}, cmd.OutOrStdout())
		}),
	}
	start.Flags().DurationVar(&every, "every", commands.KeepDefaultInterval, "how often the job runs")
	start.Flags().BoolVar(&noPush, "no-push", false, "the job rebases only and logs the push commands")
	return start
}

func newSyncKeepStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Whether a keeper is installed, its last pass and its next",
		Long: "Whether the job is installed (its plist present and loaded), its\n" +
			"interval, when the last pass ran and what it did, when the next one is\n" +
			"due, and where the log is. The same as wt sync keep with nothing after it.",
		Example: "  wt sync keep status     # installed or not, last pass, next pass\n" +
			"  wt sync keep            # the same",
		Args: cobra.NoArgs,
		RunE: withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			return commands.SyncKeepStatus(ctx, time.Now(), cmd.OutOrStdout())
		}),
	}
}

func newSyncKeepStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Unload the keeper's launchd job and remove its plist",
		Long: "launchctl bootout the job and remove its plist. The log and the json\n" +
			"stay, so wt sync still says when the repository was last kept, marked\n" +
			"keeper stopped. macOS only; elsewhere this exits 2.",
		Example: "  wt sync keep stop       # no more passes; the log stays\n" +
			"  wt sync keep status     # afterwards: stopped",
		Args: cobra.NoArgs,
		RunE: withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			return commands.SyncKeepStop(ctx, cmd.OutOrStdout())
		}),
	}
}

// syncVerbFlags is what wt sync's own flags say: at most one verb, spelled at
// the end of a recalled `wt sync <work>` line, with that verb's own flags.
type syncVerbFlags struct {
	run, resume, undo   bool
	yes, force, noFetch bool
	ifReady             bool
	push                commands.PushMode
}

// verb is the one verb given, "" for the overview, or an error naming the
// verbs given together.
func (f syncVerbFlags) verb() (string, error) {
	var verbs []string
	for _, v := range []struct {
		name string
		set  bool
	}{{"run", f.run}, {"resume", f.resume}, {"undo", f.undo}} {
		if v.set {
			verbs = append(verbs, "--"+v.name)
		}
	}
	switch len(verbs) {
	case 0:
		return "", nil
	case 1:
		return verbs[0][2:], nil
	}
	return "", errors.New(strings.Join(verbs[:len(verbs)-1], ", ") + " and " +
		verbs[len(verbs)-1] + " cannot both be given")
}

// check refuses a verb's flag given without its verb.
func (f syncVerbFlags) check() error {
	verb, err := f.verb()
	if err != nil {
		return err
	}
	rebases := verb == "run" || verb == "resume"
	switch {
	case f.push == commands.PushAlways && !rebases:
		return errors.New("--push needs --run or --resume")
	case f.push == commands.PushNever && !rebases:
		return errors.New("--no-push needs --run or --resume")
	case f.ifReady && verb != "run":
		return errors.New("--if-ready needs --run")
	case f.force && verb != "undo" && verb != "run":
		return errors.New("--force needs --run or --undo")
	case f.yes && verb == "":
		return errors.New("--yes needs --run, --resume or --undo")
	case f.noFetch && verb != "" && verb != "run":
		return errors.New("--no-fetch needs --run, or no verb")
	}
	return nil
}

// syncArgs is the argument count wt sync takes for verb: the rule the verb's
// subcommand declares, so both spellings refuse the same counts with the same
// words. run takes any number, none meaning every ready worktree.
func syncArgs(verb string) cobra.PositionalArgs {
	switch verb {
	case "run":
		return cobra.ArbitraryArgs
	case "resume", "undo":
		return cobra.ExactArgs(1)
	}
	return cobra.MaximumNArgs(1)
}

func newSyncRunCmd() *cobra.Command {
	var noFetch, yes, ifReady, force bool
	var sel selectionFlags
	var push func() commands.PushMode
	run := &cobra.Command{
		Use:   "run [<work>...]",
		Short: "Rebase the named worktrees onto trunk with the declared strategies",
		Long: "Fetch trunk once, then for each named worktree (and the rest of any\n" +
			"stack it belongs to, parents first): pin the old tip under\n" +
			"refs/wt-sync/<branch>/<epoch>, rebase with --no-update-refs --no-gpg-sign,\n" +
			"apply the declared strategy at every stop, and run the deferred steps\n" +
			"once at the end, committing their output when it changes tracked files.\n" +
			"A stop no strategy resolves is left in place, with what the strategies did\n" +
			"resolve staged and a plan file naming what is yours, for wt sync resume to\n" +
			"finish. A branch with a stack above it in the same run is put back instead,\n" +
			"so a stack is never half-applied. A failed deferred step is reported as\n" +
			"owed and never undoes the rebase.\n\n" +
			"With no worktree named, it takes every worktree the overview files under\n" +
			"ready: class conflict-free or recipe, nothing dirty, no session busy in\n" +
			"it, no handover waiting. recipe? is left out, since a run of it may stop\n" +
			"and hand you a plan you did not ask for, and so is everything under needs\n" +
			"you and skipped; what is left alone is listed with what holds it.\n\n" +
			"When it asks first, on a terminal (--yes never asks):\n" +
			"  one worktree named      only when a Claude session is idle in it\n" +
			"  several named           once, for all of them\n" +
			"  nothing named, --all    once, even for one\n" +
			"With no terminal a named worktree goes ahead, and a run with nothing\n" +
			"named, or across repositories, rebases nothing unless --yes says so.\n\n" +
			"Refused, and never touched: a worktree with tracked changes, one a busy\n" +
			"Claude session is in (Codex sessions are not detected), class divergent,\n" +
			"one an earlier run already left waiting on you, and any repository whose\n" +
			"trunk declares no .wt-sync.yaml. An idle Claude session is named before\n" +
			"anything moves under it, whether or not anyone is asked, and a finished\n" +
			"rebase ends with a wt: line to pass on to it. Sessions are listed again\n" +
			"at the lock: one busy by then, or new since, is refused. The session wt\n" +
			"itself runs under is not counted.\n\n" +
			"--force (-f) takes a named worktree past a Claude session in it, busy or\n" +
			"idle: the session is named and told at the finish, and the run goes\n" +
			"ahead. It lifts nothing else, and a run with nothing named refuses it.\n\n" +
			"A worktree whose rebase finished with nothing owed is pushed at the end\n" +
			"with --force-with-lease --force-if-includes, which refuses to overwrite\n" +
			"commits on origin the branch has not seen. On a terminal you are asked\n" +
			"once, and Enter means no. --push pushes without asking; without it,\n" +
			"--no-push, --yes or a run with no terminal prints the push command\n" +
			"instead.\n\n" +
			"Ctrl-C releases every lock the run holds and kills the step it was\n" +
			"running; a worktree caught mid-rebase is named along with the command\n" +
			"that puts it back.\n\n" +
			"--if-ready rebases what is ready and fails if anything was not: a named\n" +
			"worktree that is not ready is refused untouched (see wt sync --help).\n" +
			"--all, --roots and --profile run across repositories, asked once.\n\n" +
			"Also spelled wt sync <work>... --run, with the same flags.",
		Example: "  wt sync run login-crash api-tidy --yes # both, their stacks, not asked first\n" +
			"  wt sync run login-crash --if-ready --force  # clean only, past a session\n" +
			"  wt sync run --all --no-fetch --push    # every ready one, everywhere, pushed\n" +
			"  wt sync run --profile api --if-ready   # the ready ones in a profile's repos\n" +
			"  wt sync --roots work --run --no-push   # spelled on wt sync; print the pushes",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeWork,
		RunE: func(cmd *cobra.Command, args []string) error {
			f := syncVerbFlags{run: true, yes: yes, noFetch: noFetch, ifReady: ifReady, force: force, push: push()}
			if sel.selection().Any() {
				return syncAcross(cmd, args, "run", sel.selection(), f)
			}
			return withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
				return syncRun(cmd, args, ctx, f)
			})(cmd, args)
		},
	}
	run.Flags().BoolVar(&noFetch, "no-fetch", false, "rebase onto origin/<trunk> as last fetched")
	run.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask before anything moves")
	run.Flags().BoolVar(&ifReady, "if-ready", false, "rebase only what will go through without needing you, and fail on the rest")
	run.Flags().BoolVarP(&force, "force", "f", false, "rebase a named worktree even with a Claude session in it")
	sel.add(run, true)
	push = addPushFlags(run, "push the worktrees that finish, without asking",
		"neither push nor ask; print the push command")
	return run
}

// syncRun is wt sync run, whichever way it was spelled.
func syncRun(cmd *cobra.Command, works []string, ctx *commands.Context, f syncVerbFlags) error {
	opts, _ := runOptions(cmd, f, len(works) == 0)
	return commands.SyncRun(ctx, works, opts, cmd.OutOrStdout())
}

// runOptions is a run's options from its flags, and the prompter asking its
// questions, nil with no terminal. One prompter for every question, so an
// answer typed ahead for the push is not lost to the rebase question's reader.
//
// --yes asks nothing, the push included, which then follows --push and
// otherwise prints the push commands. A bulk run — nothing named, or many
// repositories — with nobody at a terminal to ask and no --yes rebases
// nothing, as a sweep deletes nothing: what nobody named and nobody
// confirmed does not move. A worktree named on the line goes ahead.
func runOptions(cmd *cobra.Command, f syncVerbFlags, bulk bool) (commands.RunOptions, *prompter) {
	opts := commands.RunOptions{NoFetch: f.noFetch, IfReady: f.ifReady, Force: f.force}
	opts.Push = f.push
	switch {
	case f.yes:
		return opts, nil
	case !canAsk(cmd):
		if bulk {
			opts.Confirm = noTerminal(cmd.OutOrStdout())
		}
		return opts, nil
	}
	p := newPrompter(cmd.InOrStdin(), cmd.OutOrStdout())
	opts.Confirm = confirmAsk(p, "rebase")
	opts.ConfirmPush = confirmPush(p)
	return opts, p
}

// noTerminal is the answer to a bulk run's question when nobody is there to
// give it: no, with how to say yes.
func noTerminal(w io.Writer) func([]string) (bool, error) {
	return func(works []string) (bool, error) {
		fmt.Fprintf(w, "There is no terminal to ask. Pass --yes to rebase %s.\n", strings.Join(works, ", "))
		return false, nil
	}
}

// syncAcross is wt sync with --all, --roots or --profile: the overview of
// every repository they name, or with --run a run across all of them. The
// other verbs finish one worktree's run, so they name it instead.
func syncAcross(cmd *cobra.Command, args []string, verb string, sel commands.Selection, f syncVerbFlags) error {
	u := loadUserWarn(cmd.ErrOrStderr())
	switch {
	case verb == "resume" || verb == "undo":
		return fmt.Errorf("--%s finishes one worktree's run; name it, without --all, --roots or --profile", verb)
	case len(args) > 0:
		return errors.New("--all, --roots and --profile cover whole repositories; name no worktree with them")
	case verb == "":
		return commands.SyncAll(u, sel, commands.SyncOptions{NoFetch: f.noFetch}, cmd.OutOrStdout())
	}
	opts, p := runOptions(cmd, f, true)
	all := commands.SyncRunAllOptions{RunOptions: opts}
	switch {
	case p != nil:
		all.Ask = func(q string) (bool, error) { return p.yesNo(q, false), nil }
	case !f.yes:
		all.Ask = func(q string) (bool, error) {
			fmt.Fprintf(cmd.OutOrStdout(), "There is no terminal to ask. Pass --yes to %s.\n",
				strings.ToLower(strings.TrimSuffix(q, "?")))
			return false, nil
		}
	}
	return commands.SyncRunAll(u, sel, all, cmd.OutOrStdout())
}

func newSyncResumeCmd() *cobra.Command {
	var yes bool
	var push func() commands.PushMode
	resume := &cobra.Command{
		Use:   "resume <work>",
		Short: "Continue the rebase a run left at a conflict that was yours",
		Long: "Pick up the rebase wt sync run left in this worktree. The plan file in\n" +
			".git/worktrees/<name>/wt-sync-plan.md says what landed, what the declared\n" +
			"strategies already resolved and must not be re-opened, and what is yours.\n" +
			"Resolve those, git add them, then run this.\n\n" +
			"Before continuing it checks that the rebase in progress is the one the run\n" +
			"left, that nothing is unmerged, that nothing tracked is changed but\n" +
			"unstaged, that no file a strategy resolved was hand-merged, and that no\n" +
			"busy Claude session is in the worktree. Any of those is a refusal that changes\n" +
			"nothing: this command never resets the worktree, because your own work is\n" +
			"in it, and a file a strategy owns is never yours to merge: that refusal\n" +
			"points at wt sync undo. Then it drives the rest of the rebase, runs the\n" +
			"deferred steps, pins the result ref and ends with the push, asked or not\n" +
			"as for wt sync run (--push, --no-push). A later conflict that is yours is\n" +
			"handed over again with a fresh plan file. A Claude session idle in the\n" +
			"worktree is named first and you are asked (with no terminal it goes\n" +
			"ahead) before anything is verified, and the finish ends with a wt: line\n" +
			"to pass on to it. The session wt itself runs under is not counted.\n" +
			"--yes asks nothing, neither about that session nor about the push.\n\n" +
			"Carrying on yourself with git rebase --continue is fine: a later stop you\n" +
			"left it at goes through the strategies, and a rebase you finished runs\n" +
			"only what comes after it, naming the strategy-resolved files it could not\n" +
			"re-check. Ctrl-C leaves the rebase and its plan as they are; run this\n" +
			"again. wt sync undo <work> aborts a handed-over rebase and puts the branch\n" +
			"back instead.\n\n" +
			"Also spelled wt sync <work> --resume, with the same flags.",
		Example: "  wt sync resume login-crash            # continue what the run handed you\n" +
			"  wt sync resume fix/login-crash        # the same worktree, by branch\n" +
			"  wt sync resume login-crash --no-push  # then print the push command\n" +
			"  wt sync resume login-crash --yes      # ask nothing; print the push\n" +
			"  wt sync login-crash --resume --push   # resume --push, spelled on wt sync",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			return syncResume(cmd, args[0], ctx, yes, push())
		}),
	}
	push = addPushFlags(resume, "push when done, without asking",
		"neither push nor ask; print the push command")
	resume.Flags().BoolVarP(&yes, "yes", "y", false, "ask nothing: not about an idle session, nor the push")
	return resume
}

// syncResume is wt sync resume, whichever way it was spelled.
func syncResume(cmd *cobra.Command, work string, ctx *commands.Context, yes bool, push commands.PushMode) error {
	var opts commands.ResumeOptions
	opts.Push = push
	// --yes asks nothing, the push included: it then follows --push, and
	// without it prints the push command.
	if canAsk(cmd) && !yes {
		p := newPrompter(cmd.InOrStdin(), cmd.OutOrStdout())
		opts.Confirm = confirmAsk(p, "resume")
		opts.ConfirmPush = confirmPush(p)
	}
	return commands.SyncResume(ctx, work, opts, cmd.OutOrStdout())
}

func newSyncUndoCmd() *cobra.Command {
	var force, yes bool
	undo := &cobra.Command{
		Use:   "undo <work>",
		Short: "Put back every ref the last run on this worktree moved",
		Long: "Find the newest run that touched this worktree's branch and reset every\n" +
			"branch that run rewrote back to its safety ref, restoring a stack as a\n" +
			"whole rather than one branch at a time. A rebase wt sync run handed over\n" +
			"is aborted and its plan file removed, which puts that branch back.\n\n" +
			"Refused, and nothing undone: a checkout involved is dirty, has a busy\n" +
			"Claude session in it, or is mid-rebase with no handover from wt sync run; a\n" +
			"branch that has moved since the run, whose commits the reset would\n" +
			"discard (--force pins those at a fresh safety ref and rewinds anyway); a\n" +
			"branch with a later run, which has to be undone first and which --force\n" +
			"does not override. Nothing is aborted until every branch has passed, and\n" +
			"every abort runs before any branch is reset. If it stops partway it reports\n" +
			"what it did put back; a handover aborted but not rewound says where the\n" +
			"branch still is.\n" +
			"Running it again after it already restored a branch reports that branch\n" +
			"already at its old tip.\n\n" +
			"A Claude session idle in a checkout is named and you are asked first\n" +
			"(--yes skips; with no terminal it goes ahead), and each branch put back\n" +
			"under one ends with a wt: line to pass on to it. The session wt itself\n" +
			"runs under is not counted.\n\n" +
			"Also spelled wt sync <work> --undo, with the same flags.",
		Example: "  wt sync undo login-crash            # back to the safety refs\n" +
			"  wt sync undo login-crash --force    # even if the branch moved since\n" +
			"  wt sync login-crash --undo --force  # the same, spelled on wt sync\n" +
			"  wt sync login-crash --undo --yes    # undo --yes, spelled on wt sync",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWork,
		RunE: withContext(func(cmd *cobra.Command, args []string, ctx *commands.Context) error {
			return syncUndo(cmd, args[0], ctx, force, yes)
		}),
	}
	undo.Flags().BoolVarP(&force, "force", "f", false, "undo a branch that has moved since the run, pinning its tip first")
	undo.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask first when a session is idle in a checkout")
	return undo
}

// syncUndo is wt sync undo, whichever way it was spelled.
func syncUndo(cmd *cobra.Command, work string, ctx *commands.Context, force, yes bool) error {
	opts := commands.UndoOptions{Force: force}
	if canAsk(cmd) && !yes {
		opts.Confirm = confirmAsk(newPrompter(cmd.InOrStdin(), cmd.OutOrStdout()), "undo")
	}
	return commands.SyncUndo(ctx, work, opts, cmd.OutOrStdout())
}

func newSyncDoctorCmd() *cobra.Command {
	var fix, prune bool
	doctor := &cobra.Command{
		Use:   "doctor",
		Short: "Check what a run needs, and clear up what old runs left behind",
		Long: "Check what wt sync run needs before the first run in a repository and\n" +
			"after anything changes: origin/<trunk> is fetched, .wt-sync.yaml parses,\n" +
			"every script strategy's run exists and is executable on trunk, no\n" +
			"pre-rebase or post-rewrite hook is active, no submodules or LFS paths\n" +
			"are declared, Docker answers when a deferred step needs it, and nothing\n" +
			"is left behind by an earlier run: no stale safety ref, no expired lock,\n" +
			"no worktree stopped mid-rebase. A worktree a run handed over is not a\n" +
			"leftover: the plan row names it, with wt sync resume and wt sync undo.\n" +
			"\n" +
			"Never fixes anything on its own. --fix turns on rerere.enabled and\n" +
			"removes expired locks; --prune deletes safety refs old runs no longer\n" +
			"need. A finding that would make a run refuse outright (a missing\n" +
			"trunk, declaration or script) makes this command exit non-zero; the\n" +
			"rest is advisory.",
		Example: "  wt sync doctor          # what a run needs; changes nothing\n" +
			"  wt sync doctor --fix    # turn on rerere, remove expired locks\n" +
			"  wt sync doctor --prune  # delete safety refs no run needs now",
		Args: cobra.NoArgs,
		RunE: withContext(func(cmd *cobra.Command, _ []string, ctx *commands.Context) error {
			return commands.SyncDoctor(ctx, commands.DoctorOptions{Fix: fix, Prune: prune}, cmd.OutOrStdout())
		}),
	}
	doctor.Flags().BoolVar(&fix, "fix", false, "turn on rerere.enabled and remove expired locks")
	doctor.Flags().BoolVar(&prune, "prune", false, "delete prunable safety refs")
	return doctor
}

// confirmAsk asks the one question an acting command gets before it changes
// anything, defaulting to no. What it is about has been printed by then.
func confirmAsk(p *prompter, verb string) func([]string) (bool, error) {
	return func(works []string) (bool, error) {
		return p.yesNo(verb+" "+strings.Join(works, ", ")+"?", false), nil
	}
}

// confirmPush asks the question a run or a resume ends with. It defaults to
// no, like every question wt asks: a push is the one step that reaches the
// remote, so Enter must not be what takes it there.
func confirmPush(p *prompter) func([]string) (bool, error) {
	return func(works []string) (bool, error) {
		return p.yesNo("push "+strings.Join(works, ", ")+" with --force-with-lease?", false), nil
	}
}

// addPushFlags declares --push and --no-push on cmd, refused together, and
// returns the choice they make once cmd has parsed its flags.
func addPushFlags(cmd *cobra.Command, pushUsage, noPushUsage string) func() commands.PushMode {
	var push, noPush bool
	cmd.Flags().BoolVar(&push, "push", false, pushUsage)
	cmd.Flags().BoolVar(&noPush, "no-push", false, noPushUsage)
	cmd.MarkFlagsMutuallyExclusive("push", "no-push")
	return func() commands.PushMode { return pushMode(push, noPush) }
}

// pushMode is --push and --no-push as one choice; cobra has already refused
// the two together.
func pushMode(push, noPush bool) commands.PushMode {
	switch {
	case push:
		return commands.PushAlways
	case noPush:
		return commands.PushNever
	}
	return commands.PushAsk
}
