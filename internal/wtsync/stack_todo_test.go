package wtsync

import (
	"strings"
	"testing"
)

const (
	fullA = "aaa1111111111111111111111111111111111111"
	fullB = "bbb2222222222222222222222222222222222222"
	fullC = "ccc3333333333333333333333333333333333333"
	fullD = "ddd4444444444444444444444444444444444444"
	fullE = "eee5555555555555555555555555555555555555"
)

// Each layer's marker goes after the last line of the layers up to it, a
// layer none of whose commits is in the list gets its marker beside the one
// below it, and the line's own commits above every layer come after them all.
func TestTodoEditorPutsALayerMarkerAfterEachLayersLastPick(t *testing.T) {
	t.Parallel()
	// Layers: 1 = A B, 2 = C (left out by git), 3 = D, then E above them all.
	list := "pick aaa1111 one\npick bbb2222 two\npick ddd4444 four\npick eee5555 five\n# Rebase\n"
	var b strings.Builder
	todoEditor(&b, todoPlan{stack: stackMarks{prefix: "refs/wt-stack-map/7/", top: 3,
		layerOf: map[string]int{fullA: 1, fullB: 1, fullC: 2, fullD: 3, fullE: 4}}})
	got := runEditor(t, b.String(), list)
	want := "pick aaa1111 one\npick bbb2222 two\n" +
		"update-ref refs/wt-stack-map/7/1\nupdate-ref refs/wt-stack-map/7/2\n" +
		"pick ddd4444 four\nupdate-ref refs/wt-stack-map/7/3\npick eee5555 five\n# Rebase\n"
	if got != want {
		t.Errorf("list:\n%s\nwant:\n%s", got, want)
	}
	// The plain list a person continues keeps the markers.
	b.Reset()
	plainEditor(&b)
	if again := runEditor(t, b.String(), got); again != got {
		t.Errorf("plain list:\n%s", again)
	}
}

// In a fold, a check stop and a marker go after the group's last fixup, so
// the check sees the folded commit and the layer carries the fix.
func TestTodoEditorPutsTheCheckAndTheMarkerAfterAFoldGroup(t *testing.T) {
	t.Parallel()
	list := "pick aaa1111 one\nfixup eee5555 fixup! one\npick bbb2222 two\n"
	var b strings.Builder
	todoEditor(&b, todoPlan{checks: []string{fullA}, stack: stackMarks{prefix: "refs/m/", top: 2,
		layerOf: map[string]int{fullA: 1, fullB: 2, fullE: 3}}})
	got := runEditor(t, b.String(), list)
	want := "pick aaa1111 one\nfixup eee5555 fixup! one\nbreak\nupdate-ref refs/m/1\npick bbb2222 two\nupdate-ref refs/m/2\n"
	if got != want {
		t.Errorf("list:\n%s\nwant:\n%s", got, want)
	}
}

// A lift's break before a marked pick comes after the markers of the
// layers below it: it belongs to the pick, not to the layer before.
func TestTodoEditorPutsALiftsBreakAfterTheMarkersBelowIt(t *testing.T) {
	t.Parallel()
	list := "pick aaa1111 one\npick bbb2222 two\n"
	var b strings.Builder
	todoEditor(&b, todoPlan{liftPlan: liftPlan{marks: []string{fullB}}, stack: stackMarks{prefix: "refs/m/", top: 2,
		layerOf: map[string]int{fullA: 1, fullB: 2}}})
	got := runEditor(t, b.String(), list)
	want := "pick aaa1111 one\nupdate-ref refs/m/1\nbreak\nedit bbb2222 two\nupdate-ref refs/m/2\n"
	if got != want {
		t.Errorf("list:\n%s\nwant:\n%s", got, want)
	}
}

// On a resume the list already carries the layer markers: a check break
// still goes straight after its group, before a marker.
func TestTodoEditorClosesACheckBeforeAMarkerAlreadyInTheList(t *testing.T) {
	t.Parallel()
	list := "pick aaa1111 one\nupdate-ref refs/m/1\npick bbb2222 two\n"
	var b strings.Builder
	todoEditor(&b, todoPlan{checks: []string{fullA}})
	got := runEditor(t, b.String(), list)
	want := "pick aaa1111 one\nbreak\nupdate-ref refs/m/1\npick bbb2222 two\n"
	if got != want {
		t.Errorf("list:\n%s\nwant:\n%s", got, want)
	}
}

// A fold group is checked when any of its commits is to be checked: a
// fixup's paths count as well as its target's.
func TestTodoEditorChecksAGroupWhoseFixupIsToBeChecked(t *testing.T) {
	t.Parallel()
	list := "pick aaa1111 one\nfixup eee5555 fixup! one\npick bbb2222 two\n"
	var b strings.Builder
	todoEditor(&b, todoPlan{checks: []string{fullE}})
	got := runEditor(t, b.String(), list)
	want := "pick aaa1111 one\nfixup eee5555 fixup! one\nbreak\npick bbb2222 two\n"
	if got != want {
		t.Errorf("list:\n%s\nwant:\n%s", got, want)
	}
}
