package syncrun

import (
	"fmt"
	"io"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/jsonout"
	"github.com/anders-lindstrom/wt/internal/wtsync"
)

// How a layer was found, for --json.
const (
	LayerFoundTip   = "tipOnLine" // a local branch whose tip is on the line
	LayerFoundNamed = "named"     // named with --layer
)

// StackLayer is one layer of a stack held on one line, as --json reports
// it: what the look sees, and, from a run, what came of it. The run's fields
// are null in the look.
type StackLayer struct {
	Branch      string  `json:"branch"`
	OldTip      string  `json:"oldTip"`
	Commits     int     `json:"commits"`
	Subject     string  `json:"subject"`
	FoundBy     string  `json:"foundBy"`
	Worktree    *string `json:"worktree"`
	State       string  `json:"state"`
	StateDetail *string `json:"stateDetail"`
	// The run's: the new tip and its count from the new base, how its
	// marker landed, whether its change is the same patch, and the move.
	NewTip       *string  `json:"newTip"`
	CommitsAfter *int     `json:"commitsAfter"`
	Mapped       *string  `json:"mapped"`
	Patch        *string  `json:"patch"`
	PatchWhy     *string  `json:"patchWhy"`
	Result       *string  `json:"result"`
	How          *string  `json:"how"`
	Held         *string  `json:"held"`
	MoveCommand  *string  `json:"moveCommand"`
	PushCommand  []string `json:"pushCommand"`
	NoPushReason *string  `json:"noPushReason"`
}

// StackSkipped is a branch on the line the run does not take as a layer.
type StackSkipped struct {
	Branch string `json:"branch"`
	Tip    string `json:"tip"`
	Reason string `json:"reason"`
}

// Stack is a stack held on one line, as --json reports it.
type Stack struct {
	Branch     string         `json:"branch"`
	Base       string         `json:"base"`
	Head       string         `json:"head"`
	Autosquash bool           `json:"autosquash"`
	Layers     []StackLayer   `json:"layers"`
	Skipped    []StackSkipped `json:"skipped"`
	// Refusal is why the run moved no layer: a check of the new tips, or a
	// fold that changed the line's content. Null in the look, and when the
	// layers were moved or held one by one.
	Refusal *string `json:"refusal"`
}

// StackOf is the look's report of a line: its layers and what holds each.
func StackOf(line wtsync.Line, autosquash bool, holders []wtsync.LayerHolder) *Stack {
	s := &Stack{Branch: line.Branch, Base: line.Base, Head: line.Head, Autosquash: autosquash,
		Layers: []StackLayer{}, Skipped: []StackSkipped{}}
	for i, l := range line.Layers {
		found := LayerFoundTip
		if l.Named {
			found = LayerFoundNamed
		}
		sl := StackLayer{Branch: l.Branch, OldTip: l.Tip, Commits: l.Count, Subject: l.Subject, FoundBy: found,
			State: wtsync.HolderNone, PushCommand: nil}
		if i < len(holders) {
			h := holders[i]
			sl.State = h.State
			if h.Path != "" {
				sl.Worktree = jsonout.Strp(h.Path)
			}
			if h.Detail != "" {
				sl.StateDetail = jsonout.Strp(h.Detail)
			}
		}
		s.Layers = append(s.Layers, sl)
	}
	for _, k := range line.Skipped {
		s.Skipped = append(s.Skipped, StackSkipped{Branch: k.Branch, Tip: k.Tip, Reason: k.Why})
	}
	return s
}

// ReadHolders reads what holds each layer of line: its worktree, and in it
// tracked changes, a rebase, a session or a process.
func ReadHolders(ctx *kit.Context, line wtsync.Line, listed []agents.Agent) ([]wtsync.LayerHolder, error) {
	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return nil, err
	}
	var scan *agents.ProcessScan
	var scanErr error
	for _, l := range line.Layers {
		if _, ok := worktrees.ByBranch(l.Branch); ok {
			scan, scanErr = agents.ScanHolders(listed)
			break
		}
	}
	holders := make([]wtsync.LayerHolder, len(line.Layers))
	for i, l := range line.Layers {
		holders[i] = wtsync.ReadHolder(l.Branch, worktrees, listed, scan, scanErr)
	}
	return holders, nil
}

// PrintLayers is the look's list of the layers, bottom first.
func PrintLayers(w io.Writer, s *Stack) {
	fmt.Fprintf(w, "  %s on the line of %s, bottom first:\n", kit.Count(len(s.Layers), "layer"), s.Branch)
	for _, l := range s.Layers {
		where := "no worktree"
		if l.Worktree != nil {
			where = l.State
			if l.StateDetail != nil {
				where += ": " + *l.StateDetail
			}
		}
		fmt.Fprintf(w, "    %-24s %s  %3d  %-50s  %s\n", l.Branch, git.ShortID(l.OldTip, 7), l.Commits, l.Subject, where)
	}
	for _, k := range s.Skipped {
		fmt.Fprintf(w, "    %-24s %s  not taken: %s\n", k.Branch, git.ShortID(k.Tip, 7), k.Reason)
	}
}

// StackFinish is what FinishStack needs: the run's record, the rebase's
// result, what it rebased onto, and how to move.
type StackFinish struct {
	Record *wtsync.StackRecord
	OldTip string // the line's tip before the rebase
	NewTip string // the line's tip after it, before any deferred step
	Onto   string
	Own    *wtsync.Own
	Listed []agents.Agent
	Force  bool
	Now    time.Time
	// AfterMove is a test's hook into the moves; nil in a real run.
	AfterMove func(wtsync.LayerMove) error
}

// FinishStack checks every layer's new tip and moves the layers, once the
// rebase of the line has finished: in a run, and in the resume of one that
// was handed over. It prints a line per layer and returns the report and
// the failure the run ends with, empty when every layer moved. err is a
// move that could not be recorded, or moves stopped part way; the record on
// disk then says what moved.
func FinishStack(ctx *kit.Context, w io.Writer, in StackFinish) (s *Stack, failure string, err error) {
	rec, main := in.Record, ctx.Repo.MainRoot
	holders := make([]wtsync.LayerHolder, len(rec.Line.Layers))
	for i, l := range rec.Line.Layers {
		holders[i] = wtsync.LayerHolder{Branch: l.Branch, State: wtsync.HolderNone}
	}
	s = StackOf(rec.Line, rec.Autosquash, holders)
	refuse := func(why string) (*Stack, string, error) {
		fmt.Fprintf(w, "  ✗ no layer moved: %s\n", why)
		s.Refusal = jsonout.Strp(why)
		if ferr := wtsync.ForgetStack(main, rec.Epoch); ferr != nil {
			fmt.Fprintf(w, "  note: %v\n", ferr)
		}
		return s, "no layer moved", nil
	}
	if rec.Autosquash {
		was, err1 := git.Run(main, "rev-parse", in.OldTip+"^{tree}")
		is, err2 := git.Run(main, "rev-parse", in.NewTip+"^{tree}")
		if err1 != nil || err2 != nil || was != is {
			return refuse(fmt.Sprintf("the fold changed the line's content (tree %s before, %s after); the line is folded, wt sync undo %s puts it back",
				git.ShortID(was, 7), git.ShortID(is, 7), rec.Work))
		}
	}
	tips, cerr := wtsync.CheckLayers(main, in.NewTip, in.Onto, rec.Plans)
	if cerr != nil {
		return refuse(cerr.Error())
	}
	scan, scanErr := agents.ScanHolders(in.Listed)
	merr := wtsync.MoveLayers(main, rec, tips, wtsync.MoveOptions{Listed: in.Listed, Scan: scan, ScanErr: scanErr,
		Force: in.Force, Now: in.Now, AfterMove: in.AfterMove})
	if merr != nil {
		return s, "", merr
	}
	held := 0
	oldBase, newBase := rec.Line.Base, in.Onto
	for i, t := range tips {
		m := rec.Moves[i]
		l := &s.Layers[i]
		l.NewTip, l.CommitsAfter, l.Mapped = jsonout.Strp(t.New), &t.Expect, jsonout.Strp(t.Mapped)
		l.Result, l.How = jsonout.Strp(m.Result), nil
		if m.How != "" {
			l.How = jsonout.Strp(m.How)
		}
		if m.Path != "" {
			l.Worktree = jsonout.Strp(m.Path)
		}
		same, perr := wtsync.SamePatch(main, oldBase, t.Old, newBase, t.New)
		patch, why := "same", ""
		switch {
		case perr != nil:
			patch, why = "unknown", perr.Error()
		case same:
		case t.Mapped == wtsync.MappedEmptied:
			patch, why = "changed", "trunk already carries all of its commits"
		case t.LeftOut > 0:
			patch, why = "changed", fmt.Sprintf("trunk already carries %s of it", kit.Count(t.LeftOut, "commit"))
		case rec.Autosquash:
			patch, why = "changed", "a fixup was folded into it"
		default:
			patch, why = "changed", "the rebase changed it: a stop resolved, a version lifted, or trunk's changes around it"
		}
		l.Patch = jsonout.Strp(patch)
		if why != "" {
			l.PatchWhy = jsonout.Strp(why)
		}
		mapped := map[string]string{wtsync.MappedTip: "", wtsync.MappedTipDropped: ", tip dropped: trunk carries it",
			wtsync.MappedEmptied: ", emptied: trunk carries every commit of it"}[t.Mapped]
		switch m.Result {
		case wtsync.MoveMoved:
			how := "branch moved"
			if m.How == wtsync.MoveByWorktree {
				how = "worktree reset"
			}
			fmt.Fprintf(w, "  ✓ %s  %s → %s  %s%s; %s\n", m.Branch, git.ShortID(m.Old, 7), git.ShortID(m.New, 7), kit.Count(t.Expect, "commit"), mapped, how)
			push, noPush := layerPush(in.Own, m.Branch, m.Path, main)
			l.PushCommand = push
			if noPush != "" {
				l.NoPushReason = jsonout.Strp(noPush)
			}
		case wtsync.MoveUnchanged:
			fmt.Fprintf(w, "  = %s  %s unchanged%s\n", m.Branch, git.ShortID(m.Old, 7), mapped)
		case wtsync.MoveHeld:
			held++
			fmt.Fprintf(w, "  ⏸ %s held: %s; to move it later: %s\n", m.Branch, m.Held, m.Command)
			l.Held, l.MoveCommand = jsonout.Strp(m.Held), jsonout.Strp(m.Command)
		}
		oldBase, newBase = t.Old, t.New
	}
	if ferr := wtsync.ForgetStack(main, rec.Epoch); ferr != nil {
		fmt.Fprintf(w, "  note: %v\n", ferr)
	}
	if held > 0 {
		return s, kit.Count(held, "layer") + " held", nil
	}
	return s, "", nil
}

// layerPush is the push of a moved layer: to the branch's own remote, with a
// lease on the commit wt last fetched there, or why there is no safe one.
func layerPush(own *wtsync.Own, branch, path, mainRoot string) ([]string, string) {
	if own == nil {
		return nil, "its own remote was not read"
	}
	to := own.Push(branch)
	if to.Remote == "" {
		return nil, to.Why
	}
	st := own.State(branch)
	switch st.State {
	case wtsync.OwnNone, wtsync.OwnGone:
		return nil, "there is no remote branch of its own yet"
	case wtsync.OwnBehind, wtsync.OwnDiverged:
		return nil, to.Ref() + " has commits this branch never had"
	case wtsync.OwnInSync, wtsync.OwnAhead, wtsync.OwnRebased:
	default:
		return nil, "it cannot be compared with " + to.Ref() + ": " + st.Why
	}
	dir := path
	if dir == "" {
		dir = mainRoot
	}
	return []string{"git", "-C", dir, "push", "--force-with-lease=refs/heads/" + to.Branch + ":" + st.Commit,
		to.Remote, "refs/heads/" + branch + ":refs/heads/" + to.Branch}, ""
}

// StackAfterRebase records the plan a rebase of the line read from its list,
// once the rebase has returned however it ended.
func StackAfterRebase(ctx *kit.Context, rec *wtsync.StackRecord, res wtsync.Result) error {
	if rec == nil {
		return nil
	}
	rec.Plans = res.Layers
	return wtsync.WriteStackRecord(ctx.Repo.MainRoot, rec)
}
