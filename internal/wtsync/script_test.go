package wtsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeScript commits an executable to origin's main that answers the
// contract by inspecting the index it was given: it exits 0 when stage 2 of
// the file contains "ok", 2 with a reason otherwise, and 1 for a path
// outside its claim. It is committed, not written into the checkout, because
// scripts are read from trunk.
func fakeScript(t *testing.T, local, origin string) string {
	t.Helper()
	body := `#!/usr/bin/env bash
case $1 in
--claims) echo 'special/*.txt' ;;
--check|--resolve)
  [[ $2 == special/* ]] || exit 1
  stages=$(git ls-files -u -- "$2" | awk '{print $3}' | sort -u | tr -d '\n')
  [[ $stages == 123 ]] || { echo "not three-staged: $stages" >&2; exit 2; }
  if git show ":2:$2" | grep -q ok; then exit 0; fi
  echo "trunk side is not ok" >&2; exit 2 ;;
esac
`
	full := filepath.Join(origin, "bin", "conflict", "special")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "add", "bin/conflict/special")
	gitIn(t, origin, "commit", "-q", "-m", "add the special resolver")
	gitIn(t, local, "fetch", "-q", "origin")
	return "bin/conflict/special"
}

func TestScriptChecksThroughATemporaryIndexWithoutTouchingTheRealOne(t *testing.T) {
	local, origin := repoWithOrigin(t)
	run := fakeScript(t, local, origin)
	before := gitIn(t, local, "--no-optional-locks", "status", "--porcelain")

	s := Script{Root: local, Trunk: "origin/main", Run: run}
	c := Conflict{Path: "special/a.txt", Base: []byte("base\n"), Trunk: []byte("ok\n"), Branch: []byte("branch\n")}
	if _, err := s.Resolve(c); err != nil {
		t.Fatalf("expected the script to accept, got %v", err)
	}

	c.Trunk = []byte("nope\n")
	_, err := s.Resolve(c)
	if !IsRefusal(err) || !strings.Contains(err.Error(), "trunk side is not ok") {
		t.Errorf("err = %v", err)
	}

	if after := gitIn(t, local, "--no-optional-locks", "status", "--porcelain"); after != before {
		t.Errorf("the real index changed: %q -> %q", before, after)
	}
	if entries := gitIn(t, local, "ls-files", "-u"); entries != "" {
		t.Errorf("real index has unmerged entries: %s", entries)
	}
}

func TestScriptRunsTrunksCopyNotTheCheckouts(t *testing.T) {
	local, origin := repoWithOrigin(t)
	run := fakeScript(t, local, origin)
	// a different, always-accepting script in the working tree must be ignored
	full := filepath.Join(local, run)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := Script{Root: local, Trunk: "origin/main", Run: run}
	c := Conflict{Path: "special/a.txt", Base: []byte("base\n"), Trunk: []byte("nope\n"), Branch: []byte("branch\n")}
	if _, err := s.Resolve(c); !IsRefusal(err) {
		t.Errorf("trunk's script refuses this; the checkout's copy must not have run: %v", err)
	}
}

func TestScriptNotMineIsARefusalThatSaysSo(t *testing.T) {
	local, origin := repoWithOrigin(t)
	run := fakeScript(t, local, origin)
	s := Script{Root: local, Trunk: "origin/main", Run: run}
	_, err := s.Resolve(Conflict{Path: "other/a.txt", Base: []byte("b"), Trunk: []byte("ok"), Branch: []byte("r")})
	if !IsRefusal(err) || !strings.Contains(err.Error(), "does not claim") {
		t.Errorf("err = %v", err)
	}
}

func TestScriptMissingExecutableIsAnErrorNotARefusal(t *testing.T) {
	local, _ := repoWithOrigin(t)
	s := Script{Root: local, Trunk: "origin/main", Run: "bin/conflict/missing"}
	_, err := s.Resolve(Conflict{Path: "x", Base: []byte("b"), Trunk: []byte("t"), Branch: []byte("r")})
	if err == nil || IsRefusal(err) {
		t.Errorf("err = %v, want a hard error", err)
	}
}

func TestFromRuleBuildsScript(t *testing.T) {
	s, err := FromRule(Rule{Strategy: "script", Run: "bin/conflict/x"}, "/repo", "origin/main")
	if err != nil || s.Name() != "script" {
		t.Errorf("FromRule = %v, %v", s, err)
	}
}
