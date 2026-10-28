package testkit

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/gittest"
)

// GhRepo is the remote every test in this file pretends the fixture has.
var GhRepo = github.Remote{Name: "origin", Host: "github.com", Slug: "t/demo"}

// FakeGitHub writes a `gh` that answers the two questions wt asks — the branch
// lookup over `api graphql`, and the wide open listing — and performs
// `pr checkout <n>` by creating that pull request's branch where it is run,
// which is the only part of the real one wt depends on. It points ctx at the
// fake and returns the file every invocation's argv is appended to.
func FakeGitHub(t *testing.T, ctx *kit.Context, prs ...github.PR) string {
	t.Helper()
	return FakeGitHubWith(t, ctx, "", prs...)
}

// FakeGitHubWith is FakeGitHub with extra shell run before the answers, for
// the tests that need gh to fail.
func FakeGitHubWith(t *testing.T, ctx *kit.Context, prelude string, prs ...github.PR) string {
	t.Helper()
	return FakeGitHubAs(t, ctx, "anders", prelude, prs...)
}

// FakeGitHubAs is FakeGitHubWith with the login gh reports itself as, which
// is what decides whose review the picker leads with.
func FakeGitHubAs(t *testing.T, ctx *kit.Context, viewer, prelude string, prs ...github.PR) string {
	t.Helper()
	listed, err := json.Marshal(prs)
	if err != nil {
		t.Fatal(err)
	}
	open := []github.PR{}
	for _, pr := range prs {
		if pr.Open() {
			open = append(open, pr)
		}
	}
	openJSON, err := json.Marshal(open)
	if err != nil {
		t.Fatal(err)
	}
	branches := map[int]string{}
	for _, pr := range prs {
		branches[pr.Number] = pr.LocalBranch("main")
	}
	var cases strings.Builder
	for number, branch := range branches {
		fmt.Fprintf(&cases, "    %d) git checkout -q -b %q ;;\n", number, branch)
	}

	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	exe := filepath.Join(dir, "gh")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + log + "\n" +
		prelude + "\n" +
		headCase(t, prs) +
		graphqlShell(string(openJSON), viewer) +
		"case \"$1 $2\" in\n" +
		"  'api graphql') graphql \"$@\" ;;\n" +
		"  'pr list') cat <<'JSON'\n" + string(listed) + "\nJSON\n    ;;\n" +
		"  'pr view') " + viewCase(t, prs) + " ;;\n" +
		"  'pr checkout') case \"$3\" in\n" + cases.String() + "    *) exit 1 ;;\n  esac ;;\n" +
		"  'auth status') exit 0 ;;\n" +
		"  '--version ') echo 'gh version 2.100.0 (2026-09-03)' ;;\n" +
		"esac\n"
	gittest.WriteExecutable(t, exe, script)
	StubGitHub(ctx, github.CLI{Exe: exe}, GhRepo, true)
	return log
}

// graphqlShell answers the two GraphQL queries wt sends. The branch query is
// recognised by its b<n>=<branch> variables and answered alias by alias, so a
// branch's answer depends on the branch asked about and on nothing else.
func graphqlShell(openJSON, viewer string) string {
	return "graphql() {\n" +
		"  case \"$*\" in\n" +
		"    *viewer*)\n" +
		"      printf '{\"data\":{\"viewer\":{\"login\":\"" + viewer + "\"},\"repository\":{\"pullRequests\":{\"nodes\":%s}}}}\\n' '" + openJSON + "'\n" +
		"      return ;;\n" +
		"  esac\n" +
		"  printf '{\"data\":{\"repository\":{'\n" +
		"  first=1\n" +
		"  for a in \"$@\"; do\n" +
		"    case \"$a\" in\n" +
		"      b[0-9]*=*)\n" +
		"        [ $first -eq 1 ] || printf ','\n" +
		"        first=0\n" +
		"        printf '\"%s\":{\"nodes\":[%s]}' \"${a%%=*}\" \"$(head_nodes \"${a#*=}\")\"\n" +
		"        ;;\n" +
		"    esac\n" +
		"  done\n" +
		"  printf '}}}\\n'\n" +
		"}\n"
}

// headCase answers "which pull requests have this head branch", which is the
// question the branch query asks.
func headCase(t *testing.T, prs []github.PR) string {
	t.Helper()
	byHead := map[string][]github.PR{}
	var heads []string
	for _, pr := range prs {
		if _, seen := byHead[pr.HeadRefName]; !seen {
			heads = append(heads, pr.HeadRefName)
		}
		byHead[pr.HeadRefName] = append(byHead[pr.HeadRefName], pr)
	}
	var b strings.Builder
	b.WriteString("head_nodes() {\n  case \"$1\" in\n")
	for _, head := range heads {
		nodes, err := json.Marshal(wireNodes(t, byHead[head]))
		if err != nil {
			t.Fatal(err)
		}
		// The brackets are the caller's; a branch contributes its nodes.
		fmt.Fprintf(&b, "    %q) printf '%%s' '%s' ;;\n", head, strings.Trim(string(nodes), "[]"))
	}
	b.WriteString("  esac\n}\n")
	return b.String()
}

// wireNodes is prs as GitHub's branch query answers them: a check state
// arrives as the last commit's rollup, not as a field of its own.
func wireNodes(t *testing.T, prs []github.PR) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0, len(prs))
	for _, pr := range prs {
		data, err := json.Marshal(pr)
		if err != nil {
			t.Fatal(err)
		}
		var node map[string]any
		if err := json.Unmarshal(data, &node); err != nil {
			t.Fatal(err)
		}
		delete(node, "checkState")
		var rollup any
		if pr.CheckState != "" {
			rollup = map[string]any{"state": pr.CheckState}
		}
		node["headCommit"] = map[string]any{"nodes": []any{
			map[string]any{"commit": map[string]any{"statusCheckRollup": rollup}}}}
		out = append(out, node)
	}
	return out
}

// viewCase answers `pr view <n>` for each pull request by number.
func viewCase(t *testing.T, prs []github.PR) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("case \"$3\" in\n")
	for _, pr := range prs {
		one, err := json.Marshal(pr)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "    %d) cat <<'JSON'\n%s\nJSON\n    ;;\n", pr.Number, one)
	}
	b.WriteString("    *) echo 'no pull requests found' >&2; exit 1 ;;\n  esac")
	return b.String()
}

// StubGitHub puts a GitHub in front of ctx in place of the machine's: cli,
// none when its Exe is empty, and the repository's remote, none when
// hasRemote is false.
func StubGitHub(ctx *kit.Context, cli github.CLI, remote github.Remote, hasRemote bool) {
	// The fake is a shell script, and on a loaded machine starting one can
	// take longer than the 2s wt list gives gh.
	ctx.GitHubFake = &kit.GitHubFake{CLI: cli, Found: cli.Exe != "", Remote: remote, HasRemote: hasRemote, Deadline: time.Minute}
}

// OpenPR is an open pull request on head.
func OpenPR(number int, head, title string) github.PR {
	return github.PR{Number: number, Title: title, HeadRefName: head, State: "OPEN",
		Author: github.Account{Login: "anders"}}
}
