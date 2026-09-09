package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/naming"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// MigrateOptions controls a migration.
type MigrateOptions struct {
	// DryRun reports what would happen and changes nothing. Worth reaching for
	// on a worktree carrying work you cannot afford to lose.
	DryRun bool
	// Force moves a worktree an agent session is living in.
	Force bool
	// Agents are the sessions to check against. Nil asks `claude agents`;
	// an empty slice means there are none.
	Agents []wtsync.Agent
}

// MigratePlan is what a migration will do, worked out before anything moves.
//
// It exists for the same reason removal has one: the argument a user types
// does not show what the command will do to their branch. A migration that
// renames is two operations on one piece of work, and the plan is where the
// pair can be read as a whole before either happens.
type MigratePlan struct {
	From string // where the worktree is now
	To   string // the canonical path for the type and name it is getting
	// Branch is what is checked out there; NewBranch is what it will be
	// called afterwards. They are equal when only the path is wrong.
	Branch    string
	NewBranch string
	Work      string
	Dirty     bool
	// Superset records that the old path is inside the tree Superset owns,
	// where a stored workspace path will be left pointing at nothing.
	Superset bool
	// Agent is a session whose working directory is inside the worktree.
	Agent *wtsync.Agent
	// InCwd records that the caller is standing in the worktree being moved.
	InCwd bool
}

// Migrate puts a worktree where this repository's layout says it belongs,
// under a new type or a new name when dest asks for one.
//
// arg is anything `wt list` prints — a work name, a branch, a path — because
// the row you are reading is what you have to type back. dest is
// "<type>/<name>", or a bare name to rename without retyping, or empty to fit
// the branch the worktree is already on to the convention.
func Migrate(ctx *Context, arg, dest string, opts MigrateOptions, w io.Writer) (string, error) {
	wt, err := Locate(ctx, arg)
	if err != nil {
		return "", err
	}
	plan, err := planMigrate(ctx, wt, dest)
	if err != nil {
		return "", err
	}
	plan.Agent = sessionIn(opts, plan.From, w)
	plan.InCwd = standingIn(plan.From)

	if plan.movesNothing() {
		fmt.Fprintf(w, "- %s is already at the canonical path\n", plan.From)
		return plan.From, nil
	}
	plan.Render(w)

	if opts.DryRun {
		fmt.Fprintf(w, "would %s; nothing has changed yet\n", strings.Join(plan.actions(), " and "))
		if plan.Agent != nil && !opts.Force {
			fmt.Fprintf(w, "  but a real run would refuse: %s\n", agentInTheWay(plan.Agent))
		}
		fmt.Fprintln(w, "  (drop --dry-run to do it)")
		return plan.To, nil
	}
	if plan.Agent != nil && !opts.Force {
		return "", fmt.Errorf("%s\n"+
			"  Moving the directory would pull the ground out from under it.\n"+
			"  Stop the session and run this again, or pass --force to move it anyway",
			agentInTheWay(plan.Agent))
	}
	return plan.apply(ctx, w)
}

// relocateWorktree is `wt adopt --relocate`: the same migration, asking no
// questions about agent sessions, because the path came from the person
// typing the command a moment ago.
func relocateWorktree(ctx *Context, path string, w io.Writer) (string, error) {
	return Migrate(ctx, path, "", MigrateOptions{Force: true, Agents: []wtsync.Agent{}}, w)
}

// planMigrate reads every fact the migration depends on and refuses here,
// before anything has moved, when one of them makes it impossible.
func planMigrate(ctx *Context, wt repo.Worktree, dest string) (MigratePlan, error) {
	if wt.Branch == "" {
		return MigratePlan{}, fmt.Errorf(
			"%s has a detached HEAD: there is no branch to rename, and nothing to name a path from.\n"+
				"  Put it on a branch first:  git -C %s switch -c %s\n"+
				"  then run this again",
			wt.Path, wt.Path,
			naming.BranchName(ctx.Config.DefaultType, filepath.Base(wt.Path), ctx.Config.TypeSuffix))
	}
	typ, work, err := migrateTarget(ctx, wt, dest)
	if err != nil {
		return MigratePlan{}, err
	}

	p := MigratePlan{
		From:      wt.Path,
		To:        naming.WorktreeDir(ctx.Repo.Parent, ctx.Repo.Name, typ, work, ctx.Config.TypeSuffix),
		Branch:    wt.Branch,
		NewBranch: naming.BranchName(typ, work, ctx.Config.TypeSuffix),
		Work:      work,
		Superset:  naming.UnderSuperset(wt.Path, ctx.Repo.Parent, ctx.Repo.Name, ctx.Config.TypeSuffix),
	}
	if out, err := git.Run(p.From, "status", "--porcelain"); err == nil && out != "" {
		p.Dirty = true
	}
	if err := p.checkBranchIsFree(ctx); err != nil {
		return MigratePlan{}, err
	}
	if err := p.checkPathIsFree(ctx); err != nil {
		return MigratePlan{}, err
	}
	return p, nil
}

// migrateTarget works out the type and name the worktree ends up under: what
// the destination says, or what its branch already says when there is none.
func migrateTarget(ctx *Context, wt repo.Worktree, dest string) (typ, work string, err error) {
	typ, work, implied := impliedTarget(ctx, wt.Branch)
	if dest == "" {
		if !implied {
			return "", "", fmt.Errorf(
				"%s is on branch %q, which does not say which type or name it should have.\n"+
					"  Say where it should go, for example:\n"+
					"    wt migrate %s %s/%s",
				wt.Path, wt.Branch, wt.Branch, ctx.Config.DefaultType, lastSegment(wt.Branch))
		}
		return typ, work, checkType(ctx, typ)
	}

	// A destination with no type keeps the type the worktree already has:
	// renaming is not retyping.
	fallback := ctx.Config.DefaultType
	if implied {
		fallback = typ
	}
	typ, work, err = naming.ParseSpec(stripTypeSuffix(ctx, dest), fallback, ctx.Config.Types)
	if err != nil {
		return "", "", fmt.Errorf("%w; a destination is <type>/<name>, or a name on its own", err)
	}
	return typ, work, checkType(ctx, typ)
}

// impliedTarget reads a type and a name out of a branch. Branches wt made
// carry both; so, near enough, do the two shapes other tools leave behind —
// "fix/idiotthings" is missing only the type suffix, and "axis_acc" is a bare
// name under the repository's default type. Anything else is a guess, and a
// guess here silently renames somebody's branch.
func impliedTarget(ctx *Context, branch string) (typ, work string, ok bool) {
	if typ, work, ok := naming.ParseBranch(branch, ctx.Config.TypeSuffix); ok {
		return typ, work, true
	}
	if head, rest, found := strings.Cut(branch, "/"); found {
		if rest == "" || strings.Contains(rest, "/") || !typeAllowed(ctx, head) {
			return "", "", false
		}
		return head, rest, true
	}
	if branch == "" {
		return "", "", false
	}
	if t, rest, ok := naming.InferType(branch, ctx.Config.Types); ok {
		return t, rest, true
	}
	return ctx.Config.DefaultType, branch, true
}

// stripTypeSuffix lets a destination be written the way the branch reads —
// "fix_wt/webkey" as well as "fix/webkey".
func stripTypeSuffix(ctx *Context, dest string) string {
	head, rest, found := strings.Cut(dest, "/")
	if !found || ctx.Config.TypeSuffix == "" {
		return dest
	}
	if base, cut := strings.CutSuffix(head, ctx.Config.TypeSuffix); cut && typeAllowed(ctx, base) {
		return base + "/" + rest
	}
	return dest
}

func checkType(ctx *Context, typ string) error {
	if typeAllowed(ctx, typ) {
		return nil
	}
	return fmt.Errorf("unknown worktree type %q; expected one of: %s",
		typ, strings.Join(ctx.Config.Types, " "))
}

func lastSegment(branch string) string {
	if i := strings.LastIndex(branch, "/"); i >= 0 {
		return branch[i+1:]
	}
	return branch
}

// checkBranchIsFree refuses a rename onto a name that is taken. Git would
// refuse too, but only after the message has stopped being about worktrees.
func (p MigratePlan) checkBranchIsFree(ctx *Context) error {
	if p.NewBranch == p.Branch || !ctx.Repo.BranchExists(p.NewBranch) {
		return nil
	}
	if where, err := worktreePathFor(ctx, p.NewBranch); err == nil && where != "" {
		return fmt.Errorf("branch %s already exists and is checked out at %s; pick another name",
			p.NewBranch, where)
	}
	return fmt.Errorf("branch %s already exists; pick another name, or delete it with: git branch -d %s",
		p.NewBranch, p.NewBranch)
}

func (p MigratePlan) checkPathIsFree(ctx *Context) error {
	if p.To == p.From {
		return nil
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return err
	}
	for _, wt := range worktrees {
		if samePath(wt.Path, p.To) {
			return fmt.Errorf("%s is already the worktree of %s; move that one out of the way first",
				p.To, wt.Branch)
		}
	}
	if entries, err := os.ReadDir(p.To); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty; move or delete it first", p.To)
	}
	return nil
}

// movesNothing reports a worktree that is already where it belongs, under the
// name it should have.
func (p MigratePlan) movesNothing() bool {
	return p.From == p.To && p.Branch == p.NewBranch
}

// actions names the halves of the migration that are actually happening, so
// nothing announces a rename it is not doing.
func (p MigratePlan) actions() []string {
	var a []string
	if p.From != p.To {
		a = append(a, "move the worktree")
	}
	if p.Branch != p.NewBranch {
		a = append(a, "rename the branch")
	}
	return a
}

// Render writes the plan: where the worktree goes, what its branch ends up
// called, and what is in it.
func (p MigratePlan) Render(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  from\t%s\n", p.From)
	if p.To == p.From {
		fmt.Fprintf(tw, "  to\t%s (already there)\n", p.To)
	} else {
		fmt.Fprintf(tw, "  to\t%s\n", p.To)
	}
	branch := p.Branch
	if p.NewBranch != p.Branch {
		branch += " -> " + p.NewBranch
	}
	fmt.Fprintf(tw, "  branch\t%s\n", branch)
	state := "clean"
	if p.Dirty {
		state = "uncommitted changes — the move carries them"
	}
	fmt.Fprintf(tw, "  state\t%s\n", state)
	if p.Agent != nil {
		fmt.Fprintf(tw, "  session\t%s is working in it\n", sessionLabel(p.Agent))
	}
	_ = tw.Flush()
	fmt.Fprintln(w)

	// Before the move, and therefore during a dry run — after it, the warning
	// is a post-mortem. Superset stores the absolute path of every workspace
	// it makes, so a migrate leaves that workspace pointing at nothing.
	if p.Superset {
		fmt.Fprintln(w, "! this is Superset's layout: its workspace holds this path and will")
		fmt.Fprintln(w, "  not follow the move. Re-point or recreate the workspace afterwards.")
	}
	if p.From != p.To {
		fmt.Fprintln(w, "  note: tools holding the old absolute path (IDE workspaces, running dev")
		fmt.Fprintln(w, "  servers) will need to be pointed at the new one.")
	}
	if p.InCwd {
		fmt.Fprintf(w, "  note: you are standing in it; afterwards, wt cd %s\n", p.Work)
	}
}

// apply carries out the plan, branch first: a rename is one ref and undoing it
// costs nothing, while a half-moved worktree is a checkout nobody can name.
func (p MigratePlan) apply(ctx *Context, w io.Writer) (string, error) {
	if p.Branch != p.NewBranch {
		if err := ctx.Repo.RenameBranch(p.Branch, p.NewBranch); err != nil {
			return "", fmt.Errorf("renaming %s to %s: %w", p.Branch, p.NewBranch, err)
		}
		fmt.Fprintf(w, "✓ branch renamed to %s\n", p.NewBranch)
	}
	if p.From == p.To {
		return p.To, nil
	}
	if err := ctx.Repo.MoveWorktree(p.From, p.To); err != nil {
		if p.Branch != p.NewBranch {
			if back := ctx.Repo.RenameBranch(p.NewBranch, p.Branch); back == nil {
				fmt.Fprintf(w, "  the branch is back on %s and nothing was moved\n", p.Branch)
			}
		}
		return "", err
	}

	// Verify against git rather than trusting the argument: `git worktree move`
	// has surprising destination semantics, and reporting a path the worktree
	// is not actually at is how one goes missing.
	actual, err := worktreePathFor(ctx, p.NewBranch)
	if err != nil || actual == "" {
		return p.To, fmt.Errorf("moved %s, but git no longer reports a worktree there — check `wt list`", p.From)
	}
	if actual != p.To {
		return actual, fmt.Errorf("asked git to move to %s but it landed at %s", p.To, actual)
	}
	fmt.Fprintf(w, "✓ %s is now at %s\n", p.Work, p.To)
	if husk := pruneEmptyParents(filepath.Dir(p.From), ctx.Repo.Parent); husk != "" {
		fmt.Fprintf(w, "  and %s, left empty by the move, is gone\n", husk)
	}
	return p.To, nil
}

// pruneEmptyParents deletes the directories a move emptied, walking up from
// dir and stopping at stopAt, which is never removed. os.Remove refuses a
// directory that still holds anything, so nothing anybody wants can go with
// them. It returns the highest one removed.
func pruneEmptyParents(dir, stopAt string) string {
	stopAt = filepath.Clean(stopAt)
	removed := ""
	for dir = filepath.Clean(dir); dir != stopAt && strings.HasPrefix(dir, stopAt+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
		removed = dir
	}
	return removed
}

// sessionIn finds an agent session living in the worktree. Not being able to
// ask is reported and then ignored: this command moves a directory, it does
// not rewrite history, and refusing to move anything because `claude` is
// unhappy would be the worse answer.
func sessionIn(opts MigrateOptions, path string, w io.Writer) *wtsync.Agent {
	agents := opts.Agents
	if agents == nil {
		var err error
		if agents, err = wtsync.ListAgents(); err != nil {
			fmt.Fprintf(w, "note: cannot list agent sessions (%v)\n", err)
			return nil
		}
	}
	// A worktree's path can carry a symlink (a macOS /tmp, a mounted home)
	// that an agent's reported cwd has already resolved.
	resolved := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		resolved = r
	}
	return wtsync.AgentAt(agents, resolved)
}

func agentInTheWay(a *wtsync.Agent) string {
	return "an agent session is working in it: " + sessionLabel(a)
}

func sessionLabel(a *wtsync.Agent) string {
	switch {
	case a.Name != "":
		return a.Name
	case a.Kind != "":
		return a.Kind
	}
	return "an unnamed session"
}

// standingIn reports whether the caller's own working directory is inside the
// worktree about to move.
func standingIn(path string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	return samePath(cwd, path) || strings.HasPrefix(filepath.Clean(cwd),
		filepath.Clean(path)+string(filepath.Separator))
}

func worktreePathFor(ctx *Context, branch string) (string, error) {
	if branch == "" {
		return "", nil
	}
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return "", err
	}
	for _, wt := range worktrees {
		if wt.Branch == branch {
			return wt.Path, nil
		}
	}
	return "", nil
}
