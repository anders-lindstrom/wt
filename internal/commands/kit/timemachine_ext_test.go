package kit_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/testkit"
)

// tmContext is a repository at <parent>/demo whose warnings go to the
// returned buffer, and the folder its worktrees go in.
func tmContext(t *testing.T) (ctx *kit.Context, warned *bytes.Buffer, container string) {
	t.Helper()
	ctx, err := kit.Open(testkit.FixtureRepo(t, testkit.MinimalConf), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	warned = &bytes.Buffer{}
	ctx.WarnTo(warned)
	container = filepath.Join(ctx.Repo.Parent, "demo_wt")
	for _, work := range []string{"x", "y"} {
		if err := os.MkdirAll(filepath.Join(container, "feat_wt", work), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, warned, container
}

// A worktree made under <repo>_wt/ excludes <repo>_wt/ itself, so every
// worktree made in it later is covered by the same sticky exclusion.
func TestTimeMachineExcludesTheRepositorysWorktreeFolder(t *testing.T) {
	ctx, warned, container := tmContext(t)
	tm := testkit.FakeTmutil(t, ctx, false)
	var w bytes.Buffer
	kit.ExcludeFromTimeMachine(ctx, filepath.Join(container, "feat_wt", "x"), &w)
	if got := tm.Excluded(t); !slices.Equal(got, []string{container}) {
		t.Errorf("excluded %q, want only %s", got, container)
	}
	if !strings.Contains(w.String(), "excluded "+container+" from Time Machine") {
		t.Errorf("said %q", w.String())
	}
	if warned.Len() != 0 {
		t.Errorf("warned %q", warned)
	}
}

// A folder that is already excluded is asked about and left alone, in
// silence.
func TestTimeMachineLeavesAnExcludedFolderAlone(t *testing.T) {
	ctx, warned, container := tmContext(t)
	tm := testkit.FakeTmutil(t, ctx, false)
	tm.AlreadyExcluded(t, container)
	var w bytes.Buffer
	kit.ExcludeFromTimeMachine(ctx, filepath.Join(container, "feat_wt", "x"), &w)
	if got := tm.Calls(t); !slices.Equal(got, []string{"isexcluded " + container}) {
		t.Errorf("tmutil was called %q", got)
	}
	if w.Len() != 0 || warned.Len() != 0 {
		t.Errorf("said %q, warned %q", w.String(), warned)
	}
}

// A worktree outside <repo>_wt/ gets the exclusion on its own folder.
func TestTimeMachineExcludesAWorktreeElsewhereByItself(t *testing.T) {
	ctx, _, container := tmContext(t)
	tm := testkit.FakeTmutil(t, ctx, false)
	elsewhere := filepath.Join(t.TempDir(), "agent-checkout")
	if err := os.Mkdir(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	kit.ExcludeFromTimeMachine(ctx, elsewhere, io.Discard)
	if got := tm.Excluded(t); !slices.Equal(got, []string{elsewhere}) {
		t.Errorf("excluded %q, want only %s (not %s)", got, elsewhere, container)
	}
}

// The main checkout, and any folder holding it, is never excluded.
func TestTimeMachineNeverExcludesTheMainCheckout(t *testing.T) {
	ctx, warned, _ := tmContext(t)
	tm := testkit.FakeTmutil(t, ctx, false)
	kit.ExcludeFromTimeMachine(ctx, ctx.Repo.MainRoot, io.Discard)
	kit.ExcludeFromTimeMachine(ctx, ctx.Repo.Parent, io.Discard)
	if got := tm.Calls(t); len(got) != 0 {
		t.Errorf("tmutil was called %q", got)
	}
	if warned.Len() != 0 {
		t.Errorf("warned %q", warned)
	}
}

// Switched off, or off macOS, wt runs no tmutil at all and says nothing.
func TestTimeMachineOffRunsNoTmutil(t *testing.T) {
	for name, set := range map[string]func(*testing.T, *kit.Context){
		"switched off": func(t *testing.T, ctx *kit.Context) { testkit.TimeMachineSetting(t, ctx, "false") },
		"not macOS": func(t *testing.T, ctx *kit.Context) {
			testkit.TimeMachineSetting(t, ctx, "true")
			ctx.Machine.GOOS = "linux"
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, warned, container := tmContext(t)
			tm := testkit.FakeTmutil(t, ctx, false)
			set(t, ctx)
			var w bytes.Buffer
			kit.ExcludeFromTimeMachine(ctx, filepath.Join(container, "feat_wt", "x"), &w)
			if got := tm.Calls(t); len(got) != 0 {
				t.Errorf("tmutil was called %q", got)
			}
			if w.Len() != 0 || warned.Len() != 0 {
				t.Errorf("said %q, warned %q", w.String(), warned)
			}
		})
	}
}

// A Mac whose PATH finds no tmutil is not a state anything here reports.
func TestTimeMachineWithoutTmutilIsSilent(t *testing.T) {
	ctx, warned, container := tmContext(t)
	testkit.OnMac(ctx)
	var w bytes.Buffer
	kit.ExcludeFromTimeMachine(ctx, filepath.Join(container, "feat_wt", "x"), &w)
	if w.Len() != 0 || warned.Len() != 0 {
		t.Errorf("said %q, warned %q", w.String(), warned)
	}
}

// Switched on with tmutil there, a failing tmutil is one line on the warning
// stream, in tmutil's own words, and nothing on the command's own.
func TestTimeMachineFailureIsOneWarningLine(t *testing.T) {
	ctx, warned, container := tmContext(t)
	testkit.FakeTmutil(t, ctx, true)
	var w bytes.Buffer
	kit.ExcludeFromTimeMachine(ctx, filepath.Join(container, "feat_wt", "x"), &w)
	kit.ExcludeFromTimeMachine(ctx, filepath.Join(container, "feat_wt", "y"), &w)
	lines := strings.Split(strings.TrimSpace(warned.String()), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "wt: ") ||
		!strings.Contains(lines[0], container) || !strings.Contains(lines[0], "Error setting exclusion") {
		t.Errorf("warned %q, want one wt: line naming %s and tmutil's complaint", warned, container)
	}
	if w.Len() != 0 {
		t.Errorf("said %q on the command's own stream", w.String())
	}
}
