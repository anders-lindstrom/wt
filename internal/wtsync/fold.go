package wtsync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
)

// LayerStep is what one per-layer step came to at the end of one layer: why
// it ran or not, and what became of its output.
type LayerStep struct {
	Layer, Layers int // from 1, bottom first, of how many
	Branch        string
	Step          Deferred
	Ran           bool
	Why           string
	Result        string
	// Owner is the commit the output was folded into, as rewritten, with
	// its subject; Commit the step's own commit at the layer's tip.
	Owner, OwnerSubject string
	Commit              string
	// Proof is ProofHeld for a fold whose trees were proved, or why it was
	// not; empty when nothing was folded.
	Proof   string
	Err     error
	Output  string
	Elapsed time.Duration
}

// Owed is whether the step is owed for this layer.
func (s LayerStep) Owed() bool { return s.Result == LayerOwed || s.Result == LayerFailed }

// What a per-layer step came to.
const (
	LayerUnchanged = "unchanged" // ran, and changed nothing
	LayerFolded    = "folded"    // its output folded into the layer's owner
	LayerCommitted = "committed" // its output committed at the layer's tip
	LayerSkipped   = "skipped"   // not due: no listed path changed in the layer
	LayerOwed      = "owed"      // not run: a need did not answer, or it failed below
	LayerFailed    = "failed"    // ran and failed, or its output could not be kept
	ProofHeld      = "held"
)

// layerStops is how the run stops at the end of each layer: whether it
// does, and whether the list ends in a stop of its own.
func (d *driver) layerStops() (stops, tail bool) {
	if len(d.layerSteps()) == 0 {
		return false, false
	}
	if d.req.Stack == nil {
		return true, true
	}
	l := d.req.Stack.Line
	top := 0
	if n := len(l.Layers); n > 0 {
		top = l.Layers[n-1].Count
	}
	return true, len(l.Commits) > top
}

// layerSteps is the per-layer steps this rebase runs.
func (d *driver) layerSteps() []Deferred {
	var out []Deferred
	for _, s := range d.cfg.LayerSteps() {
		if d.req.Steps == nil || d.req.Steps[s.Run] {
			out = append(out, s)
		}
	}
	return out
}

// segments is how many layers the stops cover: the stack's layers and the
// line's own commits above them, or the one branch.
func (d *driver) segments() int {
	if d.req.Stack == nil {
		return 1
	}
	_, tail := d.layerStops()
	n := len(d.req.Stack.Line.Layers)
	if tail {
		n++
	}
	return n
}

// segment is layer n's branch and its tip before the rebase.
func (d *driver) segment(n int) (branch, old string) {
	if d.req.Stack == nil {
		return d.req.Branch, d.old
	}
	l := d.req.Stack.Line
	if n <= len(l.Layers) {
		return l.Layers[n-1].Branch, l.Layers[n-1].Tip
	}
	return l.Branch, l.Head
}

// layerAt reports whether the break the sequencer stopped at is the end of
// a layer, and which: the next command is that layer's update-ref, or, for
// the last layer of a list that ends in a stop, there is none.
func (d *driver) layerAt() (int, bool, error) {
	stops, tail := d.layerStops()
	if !stops {
		return 0, false, nil
	}
	dir, err := rebaseDir(d.req.Path)
	if err != nil {
		return 0, false, err
	}
	todo, err := os.ReadFile(filepath.Join(dir, "git-rebase-todo"))
	if err != nil && !os.IsNotExist(err) {
		return 0, false, err
	}
	for _, line := range strings.Split(string(todo), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if f[0] != "update-ref" || len(f) < 2 || d.req.Stack == nil {
			return 0, false, nil
		}
		prefix := StackMapPrefix + strconv.FormatInt(d.req.Epoch, 10) + "/"
		n, ok := layerNumber(f[1], prefix)
		return n, ok, nil
	}
	if !tail {
		return 0, false, nil
	}
	return d.segments(), true, nil
}

// layerNumber is the layer a marker ref of the run names, by its prefix.
func layerNumber(ref, prefix string) (int, bool) {
	if !strings.HasPrefix(ref, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(ref, prefix))
	return n, err == nil && n > 0
}

// layerStartPath is where the run notes the commit the next layer starts
// from, in the sequencer's own directory, for a resume.
func layerStartPath(wtPath string) (string, error) {
	dir, err := rebaseDir(wtPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wt-sync-layer-start"), nil
}

// layerStart is the commit layer n's replayed commits start after: where
// the layer below ended, as its stop left it, or what the rebase is onto.
func (d *driver) layerStart(n int) (string, error) {
	path, err := layerStartPath(d.req.Path)
	if err != nil {
		return "", err
	}
	if b, err := os.ReadFile(path); err == nil {
		if at, sha, ok := strings.Cut(strings.TrimSpace(string(b)), " "); ok && at == strconv.Itoa(n) {
			return sha, nil
		}
	}
	return d.git("rev-parse", "--verify", d.req.Onto+"^{commit}")
}

func (d *driver) noteLayerStart(n int, sha string) error {
	path, err := layerStartPath(d.req.Path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(n)+" "+sha+"\n"), 0o644)
}

// atLayerEnd runs the per-layer steps at the end of layer n, HEAD that
// layer's new tip, and leaves HEAD at the tip the rest of the rebase
// replays onto: the folded one, or the step's commit, or as it was.
func (d *driver) atLayerEnd(n int) error {
	total := d.segments()
	branch, old := d.segment(n)
	start, err := d.layerStart(n)
	if err != nil {
		return err
	}
	head, err := d.git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	var commits []string
	if out, err := d.git("rev-list", "--reverse", start+"..HEAD"); err != nil {
		return err
	} else if out != "" {
		commits = strings.Fields(out)
	}
	changed, err := ChangedPaths(d.req.Path, old, head)
	if err != nil {
		return err
	}
	touched := map[string][]string{}
	for _, c := range commits {
		paths, err := pathsOfCommit(d.req.Path, c)
		if err != nil {
			return err
		}
		touched[c] = paths
		changed = append(changed, paths...)
	}
	final := n == total
	for _, step := range d.layerSteps() {
		if step.Each != EachLayer && !final {
			continue
		}
		r := LayerStep{Layer: n, Layers: total, Branch: branch, Step: step}
		stepCommits := commits
		if step.Each != EachLayer {
			// Without each: layer, everything the rebase replayed is one
			// layer, ending here.
			if all, err := d.git("rev-list", "--reverse", d.req.Onto+"..HEAD"); err == nil && all != "" {
				stepCommits = strings.Fields(all)
				for _, c := range stepCommits {
					if _, ok := touched[c]; !ok {
						paths, err := pathsOfCommit(d.req.Path, c)
						if err != nil {
							return err
						}
						touched[c] = paths
						changed = append(changed, paths...)
					}
				}
			}
		}
		switch below, failed := d.stepFailed[step.Run]; {
		case failed:
			r.Result, r.Why = LayerOwed, fmt.Sprintf("not run: it failed at layer %d (%s)", below, d.layerName(below))
		case !d.req.ForceSteps && len(step.Paths) > 0 && !anyMatches(step.Paths, changed):
			r.Result, r.Why = LayerSkipped, "no listed path changed in this layer"
		case d.unavailable(step.Needs) != "":
			r.Result, r.Why = LayerOwed, "not run: "+d.unavailable(step.Needs)
		default:
			if err := d.runLayerStep(&r, stepCommits, touched); err != nil {
				return err
			}
		}
		if r.Owed() {
			r.Err = cmpErr(r.Err, r.Why)
			if _, ok := d.stepFailed[step.Run]; !ok {
				d.stepFailed[step.Run] = n
			}
		}
		d.logLayerStep(r)
		d.res.LayerSteps = append(d.res.LayerSteps, r)
	}
	tip, err := d.git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if d.made[tip] && n <= len(d.res.Layers) {
		subject, err := d.git("log", "-1", "--format=%s", tip)
		if err != nil {
			return err
		}
		d.res.Layers[n-1].Subject = subject
	}
	return d.noteLayerStart(n+1, tip)
}

func cmpErr(err error, why string) error {
	if err != nil {
		return err
	}
	return errors.New(why)
}

// layerName is layer n's branch, for a message.
func (d *driver) layerName(n int) string {
	b, _ := d.segment(n)
	return b
}

// runLayerStep runs step r.Step at HEAD, the end of a layer whose replayed
// commits are commits, and keeps its output: folded into the owner, or
// committed, or refused. Anything not kept is taken out of the worktree, so
// the rebase goes on from HEAD as it was. The error return is for git
// failing; a step's own failure is in r.
func (d *driver) runLayerStep(r *LayerStep, commits []string, touched map[string][]string) error {
	step := r.Step
	head, err := d.git("rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if d.log != nil {
		fmt.Fprintf(d.log, "  ▸ step %s layer %d/%d %s\n", step.Run, r.Layer, r.Layers, r.Branch)
	}
	r.Ran = true
	r.Why = "a listed path changed in this layer"
	if d.req.ForceSteps {
		r.Why = "asked for"
	}
	already, _, err := d.stepOutput(step.Paths, nil)
	if err != nil {
		return err
	}
	var failed error
	r.Output, r.Elapsed, failed = runStep(d.req.Path, step.Run, DeferTimeout)
	files, outside, err := d.stepOutput(step.Paths, already)
	if err != nil {
		return err
	}
	refuse := func(why string) error {
		r.Result, r.Why = LayerFailed, why
		return d.backTo(head, files)
	}
	if failed != nil {
		return refuse(failed.Error())
	}
	if len(outside) > 0 {
		return refuse("changed tracked files outside its paths: " + strings.Join(outside, ", "))
	}
	if len(files) == 0 {
		r.Result = LayerUnchanged
		return nil
	}
	owner := -1
	for i, c := range commits {
		if len(step.Paths) > 0 && anyMatches(step.Paths, touched[c]) {
			owner = i
		}
	}
	switch {
	case step.Fold == FoldOwner && owner >= 0:
		return d.foldInto(r, commits[owner:], files, head)
	case step.Commit != "":
		if _, err := d.git(append([]string{"--literal-pathspecs", "add", "-A", "--"}, files...)...); err != nil {
			return err
		}
		if _, err := d.git("commit", "--no-gpg-sign", "-q", "-m", step.Commit); err != nil {
			return refuse("commit failed: " + kitOneLine(err.Error()))
		}
		sha, err := d.git("rev-parse", "HEAD")
		if err != nil {
			return err
		}
		d.made[sha] = true
		r.Result, r.Commit = LayerCommitted, sha
		for i := r.Layer - 1; i < len(d.res.Layers); i++ {
			d.res.Layers[i].Expect++
		}
		return nil
	case step.Fold == FoldOwner:
		return refuse("no commit of the layer changed its paths, and it declares no commit")
	default:
		return refuse("left tracked changes but declares no commit")
	}
}

func kitOneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// stepOutput is what a step left in the worktree: the files it changed
// under its paths, tracked or new, and the tracked files it changed outside
// them. A step with no paths owns every file. A file in before was there
// before the step ran, a person's own untracked file, and is not its output.
func (d *driver) stepOutput(paths, before []string) (files, outside []string, err error) {
	out, err := gitEnv(d.req.Path, nil, nil, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, nil, fmt.Errorf("status could not be read: %w", err)
	}
	// A rename or a copy is two paths, and both ends are the step's doing:
	// one outside its paths is output outside them, whichever end it is.
	type change struct{ code, path string }
	var changes []change
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		code := e[:2]
		changes = append(changes, change{code, e[3:]})
		if (code[0] == 'R' || code[0] == 'C') && i+1 < len(entries) {
			i++
			changes = append(changes, change{code, entries[i]})
		}
	}
	for _, c := range changes {
		mine := len(paths) == 0 || matchesAny(paths, c.path)
		switch {
		case slices.Contains(before, c.path):
		case mine:
			if !slices.Contains(files, c.path) {
				files = append(files, c.path)
			}
		case c.code != "??":
			outside = append(outside, c.path)
		}
	}
	return files, outside, nil
}

// backTo puts the worktree back at head as the stop found it: tracked
// files reset, and the new files the step wrote, files, removed. Nothing
// else untracked is touched.
func (d *driver) backTo(head string, files []string) error {
	if _, err := d.git("reset", "-q", "--hard", head); err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	_, err := d.git(append([]string{"--literal-pathspecs", "clean", "-fq", "--"}, files...)...)
	return err
}

// foldInto rewrites run, the owner and every commit of the layer after it,
// with files taken from the worktree, and moves HEAD to the rewritten tip.
// No commit after the owner touched the step's paths, so each keeps its own
// tree with those files replaced, and nothing can conflict. The proof comes
// before HEAD moves: the rewritten tip's tree is the worktree's, and outside
// the step's paths every rewritten commit has the tree of the one it
// replaces. A proof that fails puts HEAD back as the stop found it.
func (d *driver) foldInto(r *LayerStep, run, files []string, head string) error {
	gitDir, err := GitDir(d.req.Path)
	if err != nil {
		return err
	}
	index := filepath.Join(gitDir, "wt-sync-fold-index")
	defer func() { _ = os.Remove(index) }()
	env := []string{"GIT_INDEX_FILE=" + index}
	ig := func(args ...string) (string, error) { return gitEnv(d.req.Path, env, nil, args...) }
	if _, err := ig("read-tree", head); err != nil {
		return err
	}
	if _, err := ig(append([]string{"--literal-pathspecs", "add", "-A", "--"}, files...)...); err != nil {
		return err
	}
	worktree, err := ig("write-tree")
	if err != nil {
		return err
	}
	var info strings.Builder
	listed, err := gitEnv(d.req.Path, nil, nil, append([]string{"--literal-pathspecs", "ls-tree", "-r", "-z", "--full-tree", worktree, "--"}, files...)...)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, e := range strings.Split(listed, "\x00") {
		meta, p, ok := strings.Cut(e, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta) // mode type sha
		present[p] = true
		fmt.Fprintf(&info, "%s %s\t%s\x00", f[0], f[2], p)
	}
	for _, p := range files {
		if !present[p] {
			fmt.Fprintf(&info, "0 %s\t%s\x00", strings.Repeat("0", len(head)), p)
		}
	}
	parent, err := d.git("rev-parse", run[0]+"^")
	if err != nil {
		return err
	}
	rewritten := make([]string, len(run))
	for i, c := range run {
		if _, err := ig("read-tree", c); err != nil {
			return err
		}
		if _, err := gitEnv(d.req.Path, env, strings.NewReader(info.String()), "update-index", "-z", "--index-info"); err != nil {
			return err
		}
		tree, err := ig("write-tree")
		if err != nil {
			return err
		}
		sha, err := recommit(d.req.Path, c, tree, parent)
		if err != nil {
			return err
		}
		rewritten[i], parent = sha, sha
	}
	tip := rewritten[len(rewritten)-1]
	refuse := func(why string) error {
		r.Result, r.Proof, r.Why = LayerFailed, why, "the fold was not proved: "+why
		return d.backTo(head, files)
	}
	if tree, err := d.git("rev-parse", tip+"^{tree}"); err != nil {
		return err
	} else if tree != worktree {
		return refuse(fmt.Sprintf("the folded tip's tree %s is not the worktree's %s", git.ShortID(tree, 7), git.ShortID(worktree, 7)))
	}
	for i, c := range run {
		diff, err := ChangedPaths(d.req.Path, c, rewritten[i])
		if err != nil {
			return err
		}
		for _, p := range diff {
			if !matchesAny(r.Step.Paths, p) {
				return refuse(fmt.Sprintf("%s changed %s, outside the step's paths", git.ShortID(c, 7), p))
			}
		}
	}
	if _, err := d.git("reset", "-q", "--hard", tip); err != nil {
		return err
	}
	if f, err := d.recheckFold(rewritten, files); err != nil {
		return err
	} else if f != nil {
		return refuse(fmt.Sprintf("the check %q failed at the folded %s: %s", f.Step, git.ShortID(f.Commit, 7), f.Err))
	}
	subject, err := d.git("log", "-1", "--format=%s", rewritten[0])
	if err != nil {
		return err
	}
	r.Result, r.Owner, r.OwnerSubject, r.Proof = LayerFolded, rewritten[0], subject, ProofHeld
	return nil
}

// recommit commits tree on parent with c's message, author and committer,
// dates included.
func recommit(dir, c, tree, parent string) (string, error) {
	raw, err := gitEnv(dir, nil, nil, "cat-file", "commit", c)
	if err != nil {
		return "", err
	}
	_, message, _ := strings.Cut(raw, "\n\n")
	who, err := gitEnv(dir, nil, nil, "log", "-1", "--format=%an%x00%ae%x00%ad%x00%cn%x00%ce%x00%cd", "--date=raw", c)
	if err != nil {
		return "", err
	}
	f := strings.Split(who, "\x00")
	if len(f) != 6 {
		return "", fmt.Errorf("the author of %s could not be read", git.ShortID(c, 7))
	}
	env := []string{"GIT_AUTHOR_NAME=" + f[0], "GIT_AUTHOR_EMAIL=" + f[1], "GIT_AUTHOR_DATE=" + f[2],
		"GIT_COMMITTER_NAME=" + f[3], "GIT_COMMITTER_EMAIL=" + f[4], "GIT_COMMITTER_DATE=" + f[5]}
	return gitEnv(dir, env, strings.NewReader(message+"\n"), "commit-tree", "--no-gpg-sign", tree, "-p", parent, "-F", "-")
}

// recheckFold runs again, at every rewritten commit, the each_commit checks
// that read what the fold changed: one with no paths, or with one of files
// among them. HEAD is the folded tip before and after.
func (d *driver) recheckFold(rewritten, files []string) (*CheckFailure, error) {
	steps := d.cfg.checksFor(files)
	if len(steps) == 0 {
		return nil, nil
	}
	tip := rewritten[len(rewritten)-1]
	defer func() { _, _ = d.git("reset", "-q", "--hard", tip) }()
	for _, c := range rewritten {
		if _, err := d.git("reset", "-q", "--hard", c); err != nil {
			return nil, err
		}
		for _, s := range steps {
			if d.unavailable(s.Needs) != "" {
				continue
			}
			if why, output := runCheck(d.req.Path, s.Run, EachCommitTimeout); why != "" {
				return &CheckFailure{Step: s.Run, Commit: c, Err: why, Output: output}, nil
			}
		}
		d.res.Rechecked++
	}
	return nil, nil
}

// pathsOfCommit is the paths commit c changes against its parent.
func pathsOfCommit(dir, c string) ([]string, error) {
	out, err := gitEnv(dir, nil, nil, "diff-tree", "--no-commit-id", "--name-only", "-r", "-z", "--root", c)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func (d *driver) logLayerStep(r LayerStep) {
	if d.log == nil {
		return
	}
	switch r.Result {
	case LayerSkipped:
		fmt.Fprintf(d.log, "  ⏭ step %s layer %d/%d %s: %s\n", r.Step.Run, r.Layer, r.Layers, r.Branch, r.Why)
	case LayerOwed:
		fmt.Fprintf(d.log, "  ✗ step %s layer %d/%d %s owed: %s\n", r.Step.Run, r.Layer, r.Layers, r.Branch, r.Why)
	case LayerFailed:
		fmt.Fprintf(d.log, "    ✗ owed: %s\n", r.Why)
		for _, l := range lastOutputLines(r.Output, 20) {
			fmt.Fprintf(d.log, "      %s\n", l)
		}
	case LayerUnchanged:
		fmt.Fprintf(d.log, "    ✓ nothing to fold\n")
	case LayerFolded:
		fmt.Fprintf(d.log, "    ✓ folded into %s %s\n", git.ShortID(r.Owner, 7), r.OwnerSubject)
	case LayerCommitted:
		fmt.Fprintf(d.log, "    ✓ no commit of the layer owns it: committed %s %s\n", git.ShortID(r.Commit, 7), r.Step.Commit)
	}
}

func lastOutputLines(s string, n int) []string {
	if s == "" {
		return nil
	}
	return strings.Split(tailLines(s, n), "\n")
}
