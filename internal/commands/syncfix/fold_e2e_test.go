package syncfix

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anlindstrom/wt/internal/commands/synccmd"
	"github.com/anlindstrom/wt/internal/commands/synccmd/synccmdtest"
	"github.com/anlindstrom/wt/internal/commands/syncrun"
	"github.com/anlindstrom/wt/internal/commands/testkit"
	"github.com/anlindstrom/wt/internal/gittest"
	"github.com/anlindstrom/wt/internal/wtsync"
)

// regenerates reports whether the generator, run at tip, leaves gen/ as tip
// has it: git diff --exit-code gen/ after the generator, the check CI runs
// on the layer's PR.
func regenerates(t *testing.T, dir, tip string) bool {
	t.Helper()
	scratch := t.TempDir()
	archive := exec.Command("sh", "-c", "git archive "+tip+" | tar -x -C "+scratch)
	archive.Dir = dir
	if out, err := archive.CombinedOutput(); err != nil {
		t.Fatalf("archive %s: %v\n%s", tip, err, out)
	}
	gittest.RunGen(t, scratch)
	got, err := os.ReadFile(filepath.Join(scratch, "gen", "spec.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(got) == testkit.GitOut(t, dir, "show", tip+":gen/spec.json")+"\n"
}

func subjectsOf(t *testing.T, dir, rng string) []string {
	t.Helper()
	out := testkit.GitOut(t, dir, "log", "--reverse", "--format=%s", rng)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func commitNamed(t *testing.T, dir, tip, subject string) string {
	t.Helper()
	for _, l := range strings.Split(testkit.GitOut(t, dir, "log", "--format=%H %s", tip), "\n") {
		if sha, s, _ := strings.Cut(l, " "); s == subject {
			return sha
		}
	}
	t.Fatalf("no commit %q under %s", subject, tip)
	return ""
}

func layerStepsOf(p *syncrun.SyncParticipant) map[string]syncrun.LayerStepJSON {
	out := map[string]syncrun.LayerStepJSON{}
	for _, s := range p.LayerSteps {
		out[s.Branch] = s
	}
	return out
}

// runAt is the run options with a clock of its own, so each run of a test
// has its own epoch.
func runAt(nanos int64, stack *synccmd.StackOptions) synccmd.RunOptions {
	opts := synccmdtest.NoAgents()
	opts.Now = func() time.Time { return time.Unix(0, nanos) }
	opts.Stack = stack
	return opts
}

// The case end to end, through the verbs an agent runs: a look at the
// stack, a fold of its fixup in place, the rebase onto trunk, and the undo.
// After the rebase every layer tip passes the check CI runs on its PR (the
// generator leaves gen/ as it is), each owner holds its layer's output, the
// versions are as rule says, every replayed commit was checked, and the
// layers moved, the dirty one held with the command that moves it.
//
// f5's "chore: regenerate the spec", made by the fold in place, depends on
// the rule on trunk: under max-plus-patch the folds below already carry it,
// it is left empty and the step commits again (f5Folded false); under
// shift-bump its versions differ from theirs, it survives as f5's owner and
// the step folds into it (f5Folded true).
func foldEndToEnd(t *testing.T, rule string, versions map[string]string, f5Folded bool) {
	if testing.Short() {
		t.Skip("integration: a stack over real repositories and worktrees")
	}
	synccmdtest.FakeDocker(t)
	ctx, s := synccmdtest.FoldLine(t, gittest.FoldYAML(rule))
	main := ctx.Repo.MainRoot

	// 1. wt sync tmp/fold --stack --json: the look.
	o := synccmdtest.OverviewJSONWith(t, ctx, synccmd.SyncOptions{NoFetch: true, Stack: &synccmd.StackLook{Work: gittest.FoldTmp}})
	if o.Stack == nil || len(o.Stack.Layers) != 5 {
		t.Fatalf("look %+v", o.Stack)
	}
	for _, l := range o.Stack.Layers {
		want := map[string]string{"f1": wtsync.HolderClean, "f3": wtsync.HolderDirty}[l.Branch]
		if want == "" {
			want = wtsync.HolderNone
		}
		if l.State != want {
			t.Errorf("look: %s is %s, want %s", l.Branch, l.State, want)
		}
	}

	// 2. wt sync rebase tmp/fold --stack --autosquash: the fixup folded in
	// place; f5's drift is the step's commit, as no commit of f5 owns it.
	res, human, err := synccmdtest.StackRun(t, ctx, gittest.FoldTmp, runAt(200, &synccmd.StackOptions{Autosquash: true}))
	if err == nil || !strings.Contains(err.Error(), "1 layer held") {
		t.Fatalf("autosquash: err %v, want f3 held\n%s", err, human)
	}
	p := synccmdtest.Participant(t, res, gittest.FoldTmp)
	if p.Stack == nil || p.Stack.Refusal != nil {
		t.Fatalf("autosquash: stack %+v\n%s", p.Stack, human)
	}
	steps := layerStepsOf(p)
	if st := steps["f5"]; st.Result != wtsync.LayerCommitted || st.Commit == nil {
		t.Errorf("autosquash: f5 %+v, want the step's commit\n%s", st, human)
	}
	line := testkit.GitOut(t, main, "rev-parse", gittest.FoldTmp)
	for _, subject := range subjectsOf(t, main, s.Base+".."+line) {
		if strings.HasPrefix(subject, "fixup! ") {
			t.Errorf("autosquash left %q", subject)
		}
	}
	if got := testkit.GitOut(t, main, "show", commitNamed(t, main, line, "feat(f2): notes")+":docs/n.txt"); got != "notes, fixed" {
		t.Errorf("the fixup did not land in feat(f2): notes: %q", got)
	}
	f3 := layersOfStack(p.Stack)["f3"]
	if deref(f3.Result) != wtsync.MoveHeld || f3.MoveCommand == nil {
		t.Fatalf("autosquash: f3 %+v", f3)
	}
	// What an agent does with a held layer: runs the command it was given.
	if out, err := exec.Command("sh", "-c", *f3.MoveCommand).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", *f3.MoveCommand, err, out)
	}
	folded := map[string]string{gittest.FoldTmp: line}
	for _, b := range s.Layers {
		folded[b] = testkit.GitOut(t, main, "rev-parse", b)
	}

	// 3. wt sync rebase tmp/fold --stack: onto trunk.
	res, human, err = synccmdtest.StackRun(t, ctx, gittest.FoldTmp, runAt(300, &synccmd.StackOptions{}))
	if err == nil || !strings.Contains(err.Error(), "1 layer held") {
		t.Fatalf("rebase: err %v, want f3 held\n%s", err, human)
	}
	p = synccmdtest.Participant(t, res, gittest.FoldTmp)
	if p.Result != syncrun.ResultRebased || p.Stack == nil || p.Stack.Refusal != nil {
		t.Fatalf("rebase: %+v\n%s", p, human)
	}
	newLine := testkit.GitOut(t, main, "rev-parse", gittest.FoldTmp)
	layers := layersOfStack(p.Stack)
	steps = layerStepsOf(p)
	for b, want := range map[string]string{"f1": wtsync.LayerFolded, "f2": wtsync.LayerUnchanged, "f3": wtsync.LayerFolded,
		"f4": wtsync.LayerUnchanged, "f5": map[bool]string{true: wtsync.LayerFolded, false: wtsync.LayerCommitted}[f5Folded]} {
		if got := steps[b]; got.Result != want {
			t.Errorf("rebase: %s's step %+v, want %s", b, got, want)
		}
	}
	// f5's commit from the fold in place: left empty on trunk and made
	// again by the step, or f5's owner (see f5Folded).
	owners := map[string]string{"f1": "feat(f1): a", "f3": "feat(f3): c"}
	made := 1
	if f5Folded {
		owners["f5"], made = "chore: regenerate the spec", 0
	}
	for _, b := range s.Layers {
		l := layers[b]
		tip := deref(l.NewTip)
		if tip == "" {
			t.Fatalf("rebase: %s has no new tip: %+v", b, l)
		}
		// The check CI runs on the layer's PR.
		if !regenerates(t, main, tip) {
			t.Errorf("rebase: %s's tip %s does not regenerate to itself", b, tip[:7])
		}
		if subject, ok := owners[b]; ok {
			owner := commitNamed(t, main, tip, subject)
			if st := steps[b]; deref(st.Owner) != owner || deref(st.Proof) != wtsync.ProofHeld {
				t.Errorf("rebase: %s folded into %s (proof %s), want %s", b, deref(st.Owner), deref(st.Proof), owner)
			}
			if testkit.GitOut(t, main, "show", owner+":gen/spec.json") != testkit.GitOut(t, main, "show", tip+":gen/spec.json") {
				t.Errorf("rebase: %s's owner does not hold the layer's output", b)
			}
		}
		if v, ok := versions[b]; ok {
			if got := testkit.GitOut(t, main, "show", tip+":v.txt"); got != v {
				t.Errorf("rebase: %s's version is %s, want %s", b, got, v)
			}
		}
		switch b {
		case "f3":
			if deref(l.Result) != wtsync.MoveHeld || l.MoveCommand == nil {
				t.Errorf("rebase: f3 %+v, want held with its command", l)
			}
			if testkit.GitOut(t, main, "rev-parse", b) != folded[b] {
				t.Errorf("rebase: the held f3 moved")
			}
		default:
			if deref(l.Result) != wtsync.MoveMoved || testkit.GitOut(t, main, "rev-parse", b) != tip {
				t.Errorf("rebase: %s %+v, want moved to %s", b, l, tip[:7])
			}
		}
	}
	if head := testkit.GitOut(t, s.Paths["f1"], "rev-parse", "HEAD"); head != deref(layers["f1"].NewTip) {
		t.Error("rebase: f1's clean worktree was not reset with its branch")
	}
	// Every replayed commit was checked as it was picked, and the folded
	// ones and the step's own commit again as they stay.
	replayed := testkit.GitOut(t, main, "rev-list", "--count", "origin/main.."+newLine)
	if got := p.Checked + made; strconv.Itoa(got) != replayed {
		t.Errorf("rebase: %d commits checked, want every one of the %s replayed but the step's own", p.Checked, replayed)
	}
	if p.Rechecked != 5 {
		t.Errorf("rebase: %d folded commits checked again, want f1's two, f3's two and f5's commit", p.Rechecked)
	}

	// 4. wt sync undo tmp/fold: back to where the fold left the stack.
	var out bytes.Buffer
	if err := SyncUndo(ctx, gittest.FoldTmp, noAgentsUndo(), &out); err != nil {
		t.Fatalf("undo: %v\n%s", err, out.String())
	}
	for b, want := range folded {
		if got := testkit.GitOut(t, main, "rev-parse", b); got != want {
			t.Errorf("undo: %s is at %s, want %s", b, got[:7], want[:7])
		}
	}
	if testkit.GitOut(t, s.Paths["f3"], "status", "--porcelain", "--untracked-files=no") == "" {
		t.Error("undo: f3's changes are gone")
	}
}

func layersOfStack(s *syncrun.Stack) map[string]syncrun.StackLayer { return stackLayers(s) }

// The case with the version rule wt has today.
func TestFoldOfAStackEndToEnd(t *testing.T) {
	foldEndToEnd(t, "max-plus-patch", map[string]string{"f1": "1.1.1", "f3": "1.1.2", "f5": "1.1.3"}, false)
}

// The case under shift-bump, which keeps each bump's kind: it runs where wt
// knows the rule, and says so where it does not.
func TestFoldOfAStackEndToEndShiftBump(t *testing.T) {
	if _, err := wtsync.Parse([]byte(gittest.FoldYAML("shift-bump"))); err != nil {
		t.Skipf("this wt does not know shift-bump: %v", err)
	}
	foldEndToEnd(t, "shift-bump", map[string]string{"f1": "1.2.0", "f3": "1.2.1", "f5": "1.2.2"}, true)
}
