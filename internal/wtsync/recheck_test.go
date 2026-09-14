package wtsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecheckOfACleanWorktreeAnswersNothing(t *testing.T) {
	_, wt, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	gitDir, err := GitDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	s := Recheck(wt, gitDir, nil)
	if s.Err != nil || s.Rebasing || s.Plan || s.Dirty || len(s.Sessions) != 0 {
		t.Fatalf("standing %+v", s)
	}
}

func TestRecheckSeesTrackedChangesButNotUntrackedFiles(t *testing.T) {
	_, wt, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	gitDir, err := GitDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Recheck(wt, gitDir, nil); s.Err != nil || s.Dirty {
		t.Fatalf("an untracked file counts as dirt: %+v", s)
	}
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Recheck(wt, gitDir, nil); s.Err != nil || !s.Dirty || s.Rebasing {
		t.Fatalf("standing %+v", s)
	}
}

// A handed-over rebase is Rebasing with a Plan; the staged resolutions it
// carries are not reported as dirt.
func TestRecheckTellsAHandoverFromSomebodysOwnRebase(t *testing.T) {
	_, wt, gitDir, _ := handedOverRepo(t, 5, false)
	s := Recheck(wt, gitDir, nil)
	if s.Err != nil || !s.Rebasing || !s.Plan || s.Dirty {
		t.Fatalf("handover: %+v", s)
	}
	if err := RemovePlan(gitDir); err != nil {
		t.Fatal(err)
	}
	s = Recheck(wt, gitDir, nil)
	if s.Err != nil || !s.Rebasing || s.Plan || s.Dirty {
		t.Fatalf("a rebase with no handover: %+v", s)
	}
}

func TestRecheckListsTheSessionsInTheWorktree(t *testing.T) {
	_, wt, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	gitDir, err := GitDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}
	agents := []Agent{
		{Name: "in", Cwd: resolved, Status: "idle"},
		{Name: "elsewhere", Cwd: t.TempDir(), Status: "busy"},
	}
	s := Recheck(wt, gitDir, agents)
	if s.Err != nil || len(s.Sessions) != 1 || s.Sessions[0].Name != "in" {
		t.Fatalf("sessions %+v", s.Sessions)
	}
	if s := RecheckGit(wt, gitDir); s.Err != nil || len(s.Sessions) != 0 {
		t.Fatalf("the git part alone lists sessions: %+v", s)
	}
}

// A worktree whose state cannot be read is not a clean one: the error is
// reported, and no answer is claimed for what was not read.
func TestRecheckReportsAWorktreeItCannotRead(t *testing.T) {
	_, wt, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	gitDir, err := GitDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(err)
	}
	if s := Recheck(wt, gitDir, nil); s.Err == nil || s.Dirty || s.Rebasing {
		t.Fatalf("standing %+v", s)
	}
}
