package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// T3 Code keeps its worktrees in <data>/worktrees until its settings name
// another directory, where ~ is the home directory and a relative path is
// refused, by T3 and so here.
func TestT3WorktreesDirReadsT3Settings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	data := filepath.Join(home, "t3data")
	t.Setenv("T3CODE_HOME", data)

	if got, want := t3WorktreesDir(), filepath.Join(data, "worktrees"); got != want {
		t.Errorf("without settings: got %q, want %q", got, want)
	}
	if err := os.MkdirAll(filepath.Join(data, "userdata"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		setting, want string
	}{
		{`""`, filepath.Join(data, "worktrees")},
		{`"~/programmering/t3_wt"`, filepath.Join(home, "programmering", "t3_wt")},
		{`"/abs/t3_wt/"`, "/abs/t3_wt"},
		{`"relative/t3_wt"`, ""},
		{`"/"`, ""},
	}
	for _, c := range cases {
		settings := `{"worktreesDirectory":` + c.setting + `}`
		if err := os.WriteFile(filepath.Join(data, "userdata", "settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := t3WorktreesDir(); got != c.want {
			t.Errorf("%s: got %q, want %q", c.setting, got, c.want)
		}
	}
	if err := os.WriteFile(filepath.Join(data, "userdata", "settings.json"), []byte(`{"worktreesDirectory":"/abs/t3_wt"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := t3Root("demo"), "/abs/t3_wt/demo"; got != want {
		t.Errorf("t3Root: got %q, want %q", got, want)
	}
}
