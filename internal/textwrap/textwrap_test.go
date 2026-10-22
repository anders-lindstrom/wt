package textwrap

import "testing"

func TestWrapKeepsTheGapsBetweenColumns(t *testing.T) {
	const row = "look    wt list, wt status    what exists"
	if got := Wrap(2, row, 100, "  "); got != row {
		t.Errorf("a row that fits changed: %q", got)
	}
	if got := Wrap(0, row, 0, ""); got != row {
		t.Errorf("width 0 changed the row: %q", got)
	}
}

func TestWrapHangsUnderTheIndent(t *testing.T) {
	got := Wrap(0, "one two three four", 9, "  ")
	if want := "one two\n  three\n  four"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
