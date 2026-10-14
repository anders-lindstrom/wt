# Removing a worktree

Remove the worktree and decide what happens to its branch. A branch
merged into trunk is deleted, whoever created it: merged means nothing
is lost. It is moved into a pin under refs/wt-swept/<run>/, as wt refs
sweep moves backups, so wt refs restore <run> puts it back until wt refs
purge. An unmerged branch wt made is renamed out of the <type>_wt/
prefix, so the work survives its worktree; an unmerged branch wt did
not make is left exactly as it is.

Merged means the branch's tip is reachable from origin/<trunk> as last
fetched, or from the local trunk, so a pull request merged on the
remote counts even while the main checkout's trunk is behind. Remove
never fetches. A branch that moves after the plan is kept.

A squash or rebase merge leaves no such tip, so git reads the branch as
unmerged for ever. Where `wt list` or `wt sweep` has already recorded
that GitHub merged this branch into trunk at exactly this commit, remove
deletes it too, and says which pull request it believed. It reads that
from the file they left behind and never from the network: remove runs
from git hooks. With nothing recorded, the branch is kept as it always
was.

A branch whose every commit is on trunk already under another id — a
rebase merge whose pull request carried the rebased tip, or a
cherry-pick — is deleted too, since nothing is lost: git cherry finds
each change on trunk, and each patch must match byte for byte, so one
trunk has only with other whitespace keeps it. A merge commit or an
empty commit on the branch keeps it, because neither can be found
that way.

The plan says where the branch stands either way — merged, or how many
commits ahead of origin/<trunk> (or trunk, without it) it is — because
that is the fact the whole decision turns on. "clean" is about the
checkout, not the branch: it means nothing is uncommitted.

The worktree can be named by anything `wt list` prints — the work name,
the branch, or the path — or by . for the one you are standing in.
Matching is exact and stays inside this repository; a work name used
under two types is named with its type, as in wt remove fix/login-crash.

What the removal will do is printed before it does it. In a terminal you
are then asked to confirm; --yes skips the question, and a script or hook
with no terminal is never asked: it named the worktree. --dry-run prints
the plan and stops.

Before the question, removal refuses a worktree with anything
uncommitted — in a submodule too, whatever git's config says — a status
that cannot be read, a rebase, merge, cherry-pick, revert or bisect in
progress, a HEAD holding commits no branch has, a submodule commit only
this worktree holds, or another worktree inside it. No flag goes past
those: commit, discard or finish first. The checkout is read once more
right before it is deleted.

It also refuses a worktree an agent session is working in, idle or busy,
or whose sessions cannot be listed, and one with files git status is
told not to look at (assume-unchanged, skip-worktree). The session
running wt remove — the one a WorktreeRemove hook fires in — does not
count, and without claude on the PATH there are none. A git lock whose
process has exited is released; one whose holder is still running
refuses. Bare --force goes past a session, a failed listing, a held lock,
and hidden files still as committed; an edited one is uncommitted work.
--force=<list> goes past only what it names: idle-sessions,
busy-sessions (idle ones too), sessions-unknown, hidden-files, lock;
the list goes after an =. The sessions are listed again right before
the checkout goes, and one that turned busy or arrived since refuses
unless --force names its state.

--move-to <dir> moves the worktree aside instead of deleting it.
<dir> is a new folder on the worktree's volume — its parent must exist
— outside every checkout and the git dir; removal refuses, changing
nothing, when it is not: only renames, never a copy. After the last
check it writes <dir>/recovery.json, locks the worktree with a reason
that names <dir>, pins its commits with refs of its own so gc keeps
them, moves the checkout to <dir>/checkout and its admin dir,
.git/worktrees/<id> with its submodules' repositories, to <dir>/admin,
which unregisters it; then the branch goes as it would, held by those
pins rather than one under refs/wt-swept/. A file written after the
check moves with the checkout. Each step is journalled in
recovery.json; wt restore <dir> puts it all back, wt purge <dir>
deletes it for good, and wt sweep drops the pins once <dir> is deleted.
Without --move-to the checkout is deleted, and no command puts it back.

With `wt config set superset true`, once the worktree has left its path
and git no longer lists it, its Superset workspace is deleted too.
Superset's delete removes whatever checkout is at the path it
recorded, so a worktree made at that path in the instant between
wt's check and the delete would go with it. --keep-superset leaves
the workspace alone and asks Superset nothing; a tool that makes
worktrees while it removes others passes it.

--json is for a tool driving wt. With --dry-run, or without --yes, it
prints the plan as one JSON object on stdout — the checkout, its HEAD,
where the branch stands and what becomes of it, every reason it would
refuse, what would be lost, and a token — and changes nothing. With
--yes it removes and prints one result object, effect by effect, also
when interrupted; the plan and the progress go to stderr. --expect
<token> refuses the removal, touching nothing, unless the plan made now
has that token. wt schema remove-plan and wt schema remove print the
schemas; docs/json.md explains them.
