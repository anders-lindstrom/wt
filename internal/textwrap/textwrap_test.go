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

func TestWrapBreakingInsideAGapLeavesNoTrailingSpaces(t *testing.T) {
	got := Wrap(0, "wt sync --rebase          every worktree", 20, "      ")
	if want := "wt sync --rebase\n      every worktree"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A CJK character or an emoji takes two terminal columns, so a line is
// measured in columns, not in characters.
func TestWrapMeasuresDisplayColumns(t *testing.T) {
	for _, c := range []struct {
		name, s, want string
		start, width  int
	}{
		{"CJK", "日本語 日本語 日本語", "日本語\n日本語\n日本語", 0, 8},
		{"emoji", "🚀🚀 🚀🚀 🚀🚀", "🚀🚀\n🚀🚀\n🚀🚀", 0, 6},
		{"åäö mixed with CJK", "åäö 日本 åäö", "åäö\n日本 åäö", 2, 8},
		{"åäö alone", "åäö åäö åäö", "åäö åäö\nåäö", 0, 8},
	} {
		if got := Wrap(c.start, c.s, c.width, ""); got != c.want {
			t.Errorf("%s: Wrap(%d, %q, %d, \"\") = %q, want %q", c.name, c.start, c.s, c.width, got, c.want)
		}
	}
}

func TestWidth(t *testing.T) {
	for s, want := range map[string]int{
		"":        0,
		"wt list": 7,
		"åäö":     3,
		"日本語":     6,
		"🚀":       2,
		"é":      1, // e and a combining acute accent
		"👩‍💻":     4, // two emoji joined by a zero-width joiner
	} {
		if got := Width(s); got != want {
			t.Errorf("Width(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestWrapEdges(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, s      string
		start, w     int
		indent, want string
	}{
		{"empty input", "", 0, 10, "  ", ""},
		{"only spaces", "   ", 0, 10, "  ", ""},
		{"a line exactly as wide fits", "abc def", 0, 7, "", "abc def"},
		{"one column short breaks", "abc def", 0, 6, "", "abc\ndef"},
		{"the start counts against the first line", "abc def", 3, 9, "", "abc\ndef"},
		{"the indent counts against the next line", "a bb cc", 0, 4, "  ", "a bb\n  cc"},
		{"a word wider than the width stays whole", "aaaaaaaaaa b", 0, 5, "  ", "aaaaaaaaaa\n  b"},
		{"a long word after a short one goes to its own line", "a bbbbbbbbbb c", 0, 5, "", "a\nbbbbbbbbbb\nc"},
		{"leading and trailing spaces go", "  one two  ", 0, 20, "", "one two"},
		// Width is counted in columns, not bytes: each word is 3 columns and 6
		// bytes, so counting bytes would break after every word.
		{"åäö is counted a column a letter", "åäö åäö åäö", 0, 7, "", "åäö åäö\nåäö"},
		{"a multi-byte indent is counted a column a letter", "a b c", 0, 4, "→ ", "a b\n→ c"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Wrap(c.start, c.s, c.w, c.indent); got != c.want {
				t.Errorf("Wrap(%d, %q, %d, %q) = %q, want %q", c.start, c.s, c.w, c.indent, got, c.want)
			}
		})
	}
}
