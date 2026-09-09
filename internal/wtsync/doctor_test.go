package wtsync

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// findCheck fails the test if checks does not carry one named name.
func findCheck(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s check among %v", name, checkNames(checks))
	return Check{}
}

func checkNames(checks []Check) []string {
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = c.Name
	}
	return names
}

func TestDoctorOnAHealthyRepoIsAllOK(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, []map[string]string{{"b.txt": "b2\n"}})
	gitIn(t, dir, "config", "rerere.enabled", "true")
	// runRepo pins core.hooksPath to the repository's own hooks dir, so an
	// ambient global hooks path cannot make this test's hooks check fail.
	checks, err := Doctor(dir, "main", []repo.Worktree{{Path: wt, Branch: "feature"}}, DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour, Docker: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		if !c.OK {
			t.Errorf("%s: %s", c.Name, c.Detail)
		}
	}
	names := []string{}
	for _, c := range checks {
		names = append(names, c.Name)
	}
	want := []string{"trunk", "declaration", "scripts", "rerere", "hooks", "submodules", "lfs", "safety-refs", "locks"} // docker only with defer steps
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("checks %v", names)
	}
}

func TestDoctorFlagsRerereOffWithAFix(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, nil)
	checks, err := Doctor(dir, "main", []repo.Worktree{{Path: wt, Branch: "feature"}}, DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour, Docker: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(t, checks, "rerere")
	if c.OK {
		t.Fatal("expected rerere !OK")
	}
	if c.Fix == nil {
		t.Fatal("expected a fix")
	}
	if err := c.Fix(); err != nil {
		t.Fatal(err)
	}
	if got := gitIn(t, dir, "config", "--get", "rerere.enabled"); got != "true" {
		t.Fatalf("rerere.enabled = %q", got)
	}
}

func TestDoctorFlagsAnActivePreRebaseHook(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, nil)
	hooksPath := gitIn(t, dir, "config", "--get", "core.hooksPath")
	if err := os.WriteFile(filepath.Join(hooksPath, "pre-rebase"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	checks, err := Doctor(dir, "main", []repo.Worktree{{Path: wt, Branch: "feature"}}, DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour, Docker: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(t, checks, "hooks")
	if c.OK {
		t.Fatal("expected hooks !OK")
	}
	if !strings.Contains(c.Detail, "pre-rebase") {
		t.Fatalf("detail %q does not name pre-rebase", c.Detail)
	}
}

func TestDoctorResolvesARelativeHooksPathAgainstTheMainRoot(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, nil)
	gitIn(t, dir, "config", "core.hooksPath", "myhooks")
	if err := os.MkdirAll(filepath.Join(dir, "myhooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "myhooks", "post-rewrite"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	checks, err := Doctor(dir, "main", []repo.Worktree{{Path: wt, Branch: "feature"}}, DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour, Docker: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(t, checks, "hooks")
	if c.OK {
		t.Fatal("expected hooks !OK")
	}
	if !strings.Contains(c.Detail, "post-rewrite") {
		t.Fatalf("detail %q does not name post-rewrite", c.Detail)
	}
}

func TestDoctorFlagsAMissingScriptOnTrunk(t *testing.T) {
	dir := repoWith(t, map[string]string{"a.txt": "a\n"}, nil, nil)
	yaml := "conflicts:\n  - paths: [a.txt]\n    strategy: script\n    run: bin/none\n"
	if err := os.WriteFile(filepath.Join(dir, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "declare")
	gitIn(t, dir, "remote", "add", "origin", dir)
	gitIn(t, dir, "fetch", "-q", "origin")
	checks, err := Doctor(dir, "main", nil, DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(t, checks, "scripts")
	if c.OK {
		t.Fatal("expected scripts !OK")
	}
	if !strings.Contains(c.Detail, "bin/none") {
		t.Fatalf("detail %q does not name bin/none", c.Detail)
	}
}

func TestDoctorListsPrunableSafetyRefsWithAFix(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, nil)
	tip := gitIn(t, dir, "rev-parse", "HEAD")
	now := time.Now()
	epoch40 := now.Add(-40 * 24 * time.Hour).UnixNano()
	epoch50 := now.Add(-50 * 24 * time.Hour).UnixNano()
	if _, err := WriteSafety(dir, "feature", tip, epoch40); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSafety(dir, "feature", tip, epoch50); err != nil {
		t.Fatal(err)
	}
	checks, err := Doctor(dir, "main", []repo.Worktree{{Path: wt, Branch: "feature"}}, DoctorOptions{Now: now, Keep: 30 * 24 * time.Hour, Docker: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(t, checks, "safety-refs")
	if c.OK {
		t.Fatal("expected safety-refs !OK")
	}
	older := SafetyPrefix + "feature/" + strconv.FormatInt(epoch50, 10)
	if !strings.Contains(c.Detail, older) {
		t.Fatalf("detail %q does not name %s", c.Detail, older)
	}
	newer := SafetyPrefix + "feature/" + strconv.FormatInt(epoch40, 10)
	if strings.Contains(c.Detail, newer) {
		t.Fatalf("detail %q wrongly names the newest ref %s", c.Detail, newer)
	}
	if c.Fix == nil {
		t.Fatal("expected a fix")
	}
	if err := c.Fix(); err != nil {
		t.Fatal(err)
	}
	all, err := ListSafety(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Epoch != epoch40 {
		t.Fatalf("safety refs after fix: %+v", all)
	}
}

func TestDoctorFlagsAnExpiredLock(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, nil)
	gitDir, err := GitDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-31 * time.Minute)
	if _, err := Acquire(gitDir, started); err != nil {
		t.Fatal(err)
	}
	checks, err := Doctor(dir, "main", []repo.Worktree{{Path: wt, Branch: "feature"}}, DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour, Docker: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(t, checks, "locks")
	if c.OK {
		t.Fatal("expected locks !OK")
	}
	if c.Fix == nil {
		t.Fatal("expected a fix")
	}
	if err := c.Fix(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ReadLock(gitDir); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("lock still present after fix")
	}
}

func TestDoctorChecksDockerOnlyWhenStepsAreDeferred(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, nil)
	checks, err := Doctor(dir, "main", []repo.Worktree{{Path: wt, Branch: "feature"}}, DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour, Docker: func() error { return errors.New("no docker") }})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		if c.Name == "docker" {
			t.Fatalf("docker check present without any deferred step: %+v", checks)
		}
	}

	yaml := "conflicts:\n  - paths: [v.txt]\n    strategy: owned-line\n    line: '^\\d'\n    rule: max-plus-patch\n  - paths: [w.txt]\n    strategy: take-trunk\ndefer:\n  - run: echo hi\n"
	if err := os.WriteFile(filepath.Join(dir, ".wt-sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "add defer")
	gitIn(t, dir, "fetch", "-q", "origin")
	checks, err = Doctor(dir, "main", []repo.Worktree{{Path: wt, Branch: "feature"}}, DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour, Docker: func() error { return errors.New("no docker") }})
	if err != nil {
		t.Fatal(err)
	}
	d := findCheck(t, checks, "docker")
	if d.OK {
		t.Fatal("expected docker !OK")
	}
}

func TestDoctorFlagsSubmodulesAndLFSOnTrunk(t *testing.T) {
	dir, wt, _ := runRepo(t, nil, nil)
	if err := os.WriteFile(filepath.Join(dir, ".gitmodules"), []byte("[submodule \"x\"]\n\tpath = x\n\turl = https://example.com/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", ".gitattributes"), []byte("*.bin filter=lfs diff=lfs merge=lfs -text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "add submodule and lfs attrs")
	gitIn(t, dir, "fetch", "-q", "origin")
	checks, err := Doctor(dir, "main", []repo.Worktree{{Path: wt, Branch: "feature"}}, DoctorOptions{Now: time.Now(), Keep: 30 * 24 * time.Hour, Docker: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	sub := findCheck(t, checks, "submodules")
	if sub.OK {
		t.Fatal("expected submodules !OK")
	}
	lfs := findCheck(t, checks, "lfs")
	if lfs.OK {
		t.Fatal("expected lfs !OK")
	}
	if !strings.Contains(lfs.Detail, "sub/.gitattributes") {
		t.Fatalf("detail %q does not name sub/.gitattributes", lfs.Detail)
	}
}
