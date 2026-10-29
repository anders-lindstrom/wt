package kit

import "testing"

// A row fits by what the terminal draws: a character two columns wide
// counts as two, and a name is cut by columns too.
func TestRowsFitByDisplayWidth(t *testing.T) {
	rows := func(session string) [][]string {
		return [][]string{
			{"", "WORK", "SESSION", "PATH"},
			{"", "w", session, "/a/path/that/is/long/enough/to/count"},
		}
	}
	ascii, wide := rows("ab · done"), rows("修正 · done")
	width := 1
	for !PathTableFits(ascii, width) {
		width++
	}
	if !PathTableFits(wide, width+2) || PathTableFits(wide, width+1) {
		t.Errorf("two wide characters need two more columns than %d", width)
	}
	for s, want := range map[string]int{"abc": 3, "修正": 4, "e\u0301": 1, "🙂": 2, "": 0} {
		if got := DisplayWidth(s); got != want {
			t.Errorf("displayWidth(%q) = %d, want %d", s, got, want)
		}
	}
	if got := CutToWidth("修正修正修正", 7); got != "修正修…" || DisplayWidth(got) != 7 {
		t.Errorf("CutToWidth gave %q (%d columns)", got, DisplayWidth(got))
	}
	if got := CutToWidth("修正修正", 8); got != "修正修正" {
		t.Errorf("a name that fits is left whole, got %q", got)
	}
}
