package wtsync

import (
	"strings"
	"testing"
)

// A pick to check gets a break after it, after its edit when it is also
// marked, and none when it is dropped; the plain editor takes them out again.
func TestTodoEditorPutsABreakAfterEachPickToCheck(t *testing.T) {
	t.Parallel()
	const full1, full2, full3 = "aaa1111111111111111111111111111111111111", "bbb2222222222222222222222222222222222222", "ccc3333333333333333333333333333333333333"
	list := "pick aaa1111 one\npick bbb2222 two\npick ccc3333 three\n"
	var b strings.Builder
	todoEditor(&b, todoPlan{liftPlan: liftPlan{marks: []string{full3}, drops: []string{full2}}, checks: []string{full1, full2, full3}})
	got := runEditor(t, b.String(), list)
	want := "pick aaa1111 one\nbreak\nbreak\nedit ccc3333 three\nbreak\n"
	if got != want {
		t.Errorf("list:\n%s\nwant:\n%s", got, want)
	}
	b.Reset()
	plainEditor(&b)
	if got := runEditor(t, b.String(), got); got != "pick aaa1111 one\npick ccc3333 three\n" {
		t.Errorf("plain list:\n%s", got)
	}
}
