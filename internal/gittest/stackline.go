package gittest

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// StackLine is a stack held on one first-parent line, shaped like a real
// one: five layer branches of two to four commits, a tmp branch at the top
// in its own worktree, and trunk moved on since the stack's base with a
// version bump and commits patch-equivalent to layer commits.
//
//	s1  two commits, in a clean worktree
//	s2  three commits; its tip is also on trunk, patch for patch (tip dropped)
//	s3  two commits, one bumping v.txt, in a worktree with tracked changes
//	s4  two commits, both also on trunk (emptied); no worktree
//	s5  three commits, in a worktree a process is working in
//	tmp/stack  at s5's tip, in its own worktree
//
// Trunk is main, and origin is the repository itself, so origin/main is
// trunk as last fetched.
type StackLine struct {
	Dir    string
	Base   string            // the stack's base on trunk
	Layers []string          // bottom first
	Tips   map[string]string // each layer's tip, and tmp/stack's
	Paths  map[string]string // the worktree of a branch that has one
	// Process is the pid of the process working in s5's worktree; it is
	// ended when the test ends.
	Process int
	// Subjects is each layer's commits' subjects, oldest first.
	Subjects map[string][]string
}

// The branch names StackLine uses.
const (
	StackTmp = "tmp/stack"
)

// NewStackLine builds a StackLine in dir, a repository on main with at least
// one commit and v.txt absent. wtDir is where it puts the worktrees. yaml,
// when not empty, is committed on trunk as .wt-sync.yaml after the stack's
// base, so the line itself does not carry it.
func NewStackLine(t testing.TB, dir, wtDir, yaml string) *StackLine {
	t.Helper()
	s := &StackLine{Dir: dir, Layers: []string{"s1", "s2", "s3", "s4", "s5"},
		Tips: map[string]string{}, Paths: map[string]string{}, Subjects: map[string][]string{}}
	write := func(rel, content string) { WriteFile(t, filepath.Join(dir, rel), content) }
	commit := func(msg string) string {
		Git(t, dir, "add", "-A")
		Git(t, dir, "commit", "-q", "--no-verify", "-m", msg)
		return Git(t, dir, "rev-parse", "HEAD")
	}
	write("v.txt", "1.0.0\n")
	write("t.txt", "t\n")
	s.Base = commit("base")
	Git(t, dir, "checkout", "-q", "-b", StackTmp)
	layer := func(name string, files ...string) {
		for _, f := range files {
			subject := "feat(" + name + "): " + f
			if f == "v.txt" {
				write("v.txt", "1.0.1\n")
				subject = "chore(" + name + "): bump to 1.0.1"
			} else {
				write(f, name+" "+f+"\n")
			}
			commit(subject)
			s.Subjects[name] = append(s.Subjects[name], subject)
		}
		s.Tips[name] = Git(t, dir, "rev-parse", "HEAD")
		Git(t, dir, "branch", name)
	}
	layer("s1", "a1.txt", "a2.txt")
	layer("s2", "b1.txt", "b2.txt", "b3.txt")
	layer("s3", "c1.txt", "v.txt")
	layer("s4", "d1.txt", "d2.txt")
	layer("s5", "e1.txt", "e2.txt", "e3.txt")
	s.Tips[StackTmp] = Git(t, dir, "rev-parse", "HEAD")
	Git(t, dir, "checkout", "-q", "main")
	// Trunk: its own change, a version bump, and s2's tip and both of s4's
	// commits picked patch for patch.
	if yaml != "" {
		write(".wt-sync.yaml", yaml)
		commit("chore: declare")
	}
	write("t.txt", "t2\n")
	commit("feat: trunk's own")
	write("v.txt", "1.0.5\n")
	commit("chore: bump to 1.0.5")
	for _, c := range []string{s.Tips["s2"], s.Tips["s4"] + "~1", s.Tips["s4"]} {
		Git(t, dir, "cherry-pick", c)
	}
	Git(t, dir, "remote", "add", "origin", dir)
	Git(t, dir, "fetch", "-q", "origin")
	add := func(branch string) string {
		path := filepath.Join(wtDir, strings.ReplaceAll(branch, "/", "_"))
		Git(t, dir, "worktree", "add", "-q", path, branch)
		s.Paths[branch] = path
		return path
	}
	add("s1")
	s3 := add("s3")
	WriteFile(t, filepath.Join(s3, "c1.txt"), "work in progress\n")
	s5 := add("s5")
	add(StackTmp)
	s.Process = startIn(t, s5)
	return s
}

// startIn starts a process that works in dir until the test ends, as a dev
// server started in a terminal tab would.
func startIn(t testing.TB, dir string) int {
	t.Helper()
	cmd := exec.Command("sleep", "600")
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}
