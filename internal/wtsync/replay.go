package wtsync

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// messagesPath stands in for a conflict merge-tree reports only in its
// messages, with no three blobs of its own to show.
const messagesPath = "(see messages)"

// Stop is one commit at which a rebase stops, with what the declared
// strategies answered for every file it conflicts on.
type Stop struct {
	Index   int // 1-based position among the commits the rebase replays
	Total   int
	Commit  string
	Subject string
	// Conflicts carries the three blobs of each conflicted file. It is kept
	// only for the stop that stops the replay: a long branch can stop
	// dozens of times on a megabyte file, and holding every stop's blobs
	// would make one assessment cost hundreds of megabytes for bytes
	// nothing reads again.
	Conflicts []Conflict
	Messages  string
	// Files is one outcome per conflict, in the same order. Empty when
	// merge-tree reported a conflict with no blobs of its own.
	Files []FileOutcome
	// Resolved is true when every conflict here was answered by a strategy.
	Resolved bool
}

// Replay is the outcome of simulating a rebase commit by commit, with the
// declared strategies applied at every stop.
type Replay struct {
	Commits int
	// Stops is every stop the replay reached, in order.
	Stops []Stop
	// Stop is the first stop the strategies did not resolve — the one a
	// person owns. nil when every stop resolved, or there was none.
	Stop *Stop
	// Truncated is set when the replay could not carry past a stop it
	// resolved: a script claimed a path, and a script can only be checked
	// in the object store, never asked for bytes. Why says which.
	Truncated bool
	Why       string
	// Err collects strategy failures — a script that would not run, a
	// declaration that will not build. A file whose strategy failed is not
	// resolved, so the class fails closed; this carries the reason.
	Err error
}

// simEnv makes the throwaway commits deterministic and free of any signing
// or identity configuration.
var simEnv = []string{
	"GIT_AUTHOR_NAME=wt sync", "GIT_AUTHOR_EMAIL=wt-sync@localhost",
	"GIT_COMMITTER_NAME=wt sync", "GIT_COMMITTER_EMAIL=wt-sync@localhost",
	"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
}

// SimulateRebase replays branch onto `onto` inside the object store, the way
// `git rebase onto` would, applying cfg's declared strategies at every stop
// and carrying their answers forward. It reports every stop it reached and
// the first one the strategies did not resolve. No ref, index or working
// tree is touched; the only objects created are unreachable blobs, trees and
// commits. A nil cfg claims nothing, so the first stop is the last.
func SimulateRebase(mainRoot, onto, branch string, cfg *Config) (Replay, error) {
	var rep Replay
	// The same selection and order the rebase sequencer uses: right side
	// only, patch-equivalent commits dropped, merges flattened, topological.
	out, err := gitEnv(mainRoot, nil, nil, "rev-list", "--reverse", "--topo-order", "--right-only", "--cherry-pick", "--no-merges", onto+"..."+branch, "--")
	if err != nil {
		return rep, err
	}
	var commits []string
	if out != "" {
		commits = strings.Split(out, "\n")
	}
	rep.Commits = len(commits)
	base, err := gitEnv(mainRoot, nil, nil, "rev-parse", "--verify", onto+"^{commit}", "--")
	if err != nil {
		return rep, err
	}
	for i, c := range commits {
		tree, clean, conflicts, messages, err := mergeTree(mainRoot, c+"^", base, c)
		if err != nil {
			return rep, err
		}
		if !clean {
			subject, err := gitEnv(mainRoot, nil, nil, "log", "-1", "--format=%s", c, "--")
			if err != nil {
				return rep, err
			}
			stop := Stop{
				Index: i + 1, Total: len(commits), Commit: c, Subject: subject,
				Conflicts: conflicts, Messages: messages,
			}
			resolved := map[string][]byte{}
			script := ""
			// A conflict merge-tree reports only in its messages has no
			// blobs to put to a strategy, so it is nobody's but a person's.
			stop.Resolved = len(conflicts) > 0
			for _, cf := range conflicts {
				r, rerr := resolveConflict(mainRoot, onto, cfg, cf, "")
				rep.Err = errors.Join(rep.Err, rerr)
				stop.Files = append(stop.Files, r.Outcome)
				if !r.Outcome.Resolved {
					stop.Resolved = false
					continue
				}
				if r.Outcome.Strategy == "script" {
					// --check said the script owns it, which is all a
					// script can say here: it resolves against a real
					// index in a worktree, never in the object store.
					if rule, ok := cfg.RuleFor(cf.Path); ok && script == "" {
						script = fmt.Sprintf("%s owns %s and can only be checked before a run", rule.Run, cf.Path)
					}
					continue
				}
				resolved[cf.Path] = r.Content
			}
			if !stop.Resolved {
				rep.Stops = append(rep.Stops, stop)
				last := rep.Stops[len(rep.Stops)-1]
				rep.Stop = &last
				return rep, nil
			}
			// Past here the stop is resolved and nothing reads its bytes
			// again: keep the outcomes, drop the blobs.
			stop.Conflicts = nil
			rep.Stops = append(rep.Stops, stop)
			if script != "" {
				rep.Truncated = true
				rep.Why = fmt.Sprintf("replayed to stop %d/%d only: %s", stop.Index, stop.Total, script)
				return rep, nil
			}
			if tree, err = resolvedTree(mainRoot, tree, resolved); err != nil {
				return rep, err
			}
		}
		// A commit whose changes are already present replays to the same
		// tree; rebase drops it, so no simulated commit is made for it.
		// base is always a SHA computed above (verified, or a commit-tree
		// result), never a user-supplied ref, so it carries no path
		// ambiguity; no "--" here, since plain (non-`--verify`) `rev-parse`
		// echoes a trailing "--" back as a second output line, which would
		// corrupt this comparison.
		baseTree, err := gitEnv(mainRoot, nil, nil, "rev-parse", base+"^{tree}")
		if err != nil {
			return rep, err
		}
		if baseTree == tree {
			continue
		}
		base, err = gitEnv(mainRoot, simEnv, nil, "commit-tree", tree, "-p", base, "-m", "wt sync simulation")
		if err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// Endpoint merges the two tips and returns the conflicts, nil when clean. A
// conflict merge-tree reports only in its messages (no three blobs) comes
// back as one Conflict with Incomplete set and the message as its path
// description.
func Endpoint(mainRoot, onto, branch string) ([]Conflict, error) {
	_, clean, conflicts, messages, err := mergeTree(mainRoot, "", onto, branch)
	if err != nil {
		return nil, err
	}
	if clean {
		return nil, nil
	}
	if len(conflicts) == 0 && messages != "" {
		conflicts = []Conflict{{Path: messagesPath, Incomplete: messages}}
	}
	return conflicts, nil
}

// BehindAhead counts the commits branch lacks from onto, and onto from branch.
func BehindAhead(mainRoot, onto, branch string) (behind, ahead int, err error) {
	out, err := gitEnv(mainRoot, nil, nil, "rev-list", "--left-right", "--count", onto+"..."+branch, "--")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q", out)
	}
	behind, _ = strconv.Atoi(fields[0])
	ahead, _ = strconv.Atoi(fields[1])
	return behind, ahead, nil
}

// mergeTree runs one merge in the object store. mergeBase may be "" to let
// git find it. clean reports whether the merge succeeded outright, taken
// straight from merge-tree's exit status. On conflict it returns every
// conflicted file with its blobs (a missing stage or a non-blob entry marks
// the conflict Incomplete) and merge-tree's informational messages.
func mergeTree(mainRoot, mergeBase, onto, commit string) (tree string, clean bool, conflicts []Conflict, messages string, err error) {
	args := []string{"merge-tree", "--write-tree", "-z"}
	if mergeBase != "" {
		args = append(args, "--merge-base="+mergeBase)
	}
	args = append(args, "--", onto, commit)
	out, code, err := gitEnvAllow(mainRoot, nil, nil, 1, args...)
	if err != nil {
		return "", false, nil, "", fmt.Errorf("git merge-tree: %w", err)
	}
	// With -z the output is NUL-separated: the tree, then for a conflict one
	// record per index entry ("<mode> <oid> <stage>\t<path>"), then an empty
	// record ending the section, then the informational messages.
	records := strings.Split(out, "\x00")
	tree = strings.TrimSpace(records[0])
	if code == 0 {
		return tree, true, nil, "", nil
	}
	stages := map[string]*Conflict{}
	seenStage := map[string]map[int]bool{}
	var order []string
	i := 1
	for ; i < len(records); i++ {
		rec := records[i]
		if rec == "" {
			break
		}
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			continue
		}
		stage, _ := strconv.Atoi(fields[2])
		c, seen := stages[path]
		if !seen {
			c = &Conflict{Path: path}
			stages[path] = c
			seenStage[path] = map[int]bool{}
			order = append(order, path)
		}
		seenStage[path][stage] = true
		if fields[0] != "100644" && fields[0] != "100755" {
			c.Incomplete = "not a regular file (mode " + fields[0] + ")"
			continue
		}
		data, err := catFileRaw(mainRoot, fields[1])
		if err != nil {
			return "", false, nil, "", err
		}
		switch stage {
		case 1:
			c.Base = data
		case 2:
			c.Trunk = data
		case 3:
			c.Branch = data
		}
	}
	if i+1 < len(records) {
		messages = parseMessages(records[i+1:])
	}
	for _, p := range order {
		c := stages[p]
		if c.Incomplete == "" && (!seenStage[p][1] || !seenStage[p][2] || !seenStage[p][3]) {
			switch {
			case !seenStage[p][1] && seenStage[p][2] && seenStage[p][3]:
				c.Incomplete = "both sides added it"
			default:
				c.Incomplete = "one side deleted or renamed it"
			}
		}
		conflicts = append(conflicts, *c)
	}
	return tree, false, conflicts, messages, nil
}

// parseMessages extracts the human-readable text from merge-tree's
// informational section: NUL-separated groups of a path count, that many
// paths, a conflict-type label, and the message itself
// (e.g. "1\x00v.txt\x00CONFLICT (contents)\x00CONFLICT (content): ...\n").
// Only the message of each group is kept, one per line.
func parseMessages(records []string) string {
	var msgs []string
	i := 0
	for i < len(records) {
		n, err := strconv.Atoi(records[i])
		if err != nil {
			break
		}
		i++
		i += n // the group's paths, not needed: Conflicts already names them
		if i >= len(records) {
			break
		}
		i++ // the conflict-type label, redundant with the message
		if i >= len(records) {
			break
		}
		if msg := strings.TrimSpace(records[i]); msg != "" {
			msgs = append(msgs, msg)
		}
		i++
	}
	return strings.Join(msgs, "\n")
}

// catFileRaw reads a blob. Trailing newlines are preserved: gitEnv trims
// them, so this goes through the same deadline and process group by asking
// for the raw bytes with gitEnvRaw instead.
func catFileRaw(mainRoot, oid string) ([]byte, error) {
	out, err := gitEnvRaw(mainRoot, "cat-file", "blob", oid)
	if err != nil {
		return nil, fmt.Errorf("git cat-file blob %s: %w", oid, err)
	}
	return out, nil
}
