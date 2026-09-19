package github

import (
	"os/exec"
	"testing"
)

func TestParseURL(t *testing.T) {
	for url, want := range map[string]struct{ host, slug string }{
		"git@github.com:Telcred/ai-tooling.git":        {"github.com", "Telcred/ai-tooling"},
		"git@github.com:Telcred/ai-tooling":            {"github.com", "Telcred/ai-tooling"},
		"https://github.com/Telcred/ai-tooling.git":    {"github.com", "Telcred/ai-tooling"},
		"https://anders@github.com/Telcred/ai-tooling": {"github.com", "Telcred/ai-tooling"},
		"ssh://git@github.com/Telcred/ai-tooling.git":  {"github.com", "Telcred/ai-tooling"},
		"ssh://git@github.com:2222/Telcred/ai-tooling": {"github.com", "Telcred/ai-tooling"},
		"git@github.acme.example:Team/thing.git":       {"github.acme.example", "Team/thing"},
		"https://GitHub.com/Telcred/ai-tooling":        {"github.com", "Telcred/ai-tooling"},
		"https://gitlab.com/Telcred/ai-tooling":        {"gitlab.com", "Telcred/ai-tooling"},
		"/srv/git/mirror.git":                          {"", ""},
		"https://github.com/Telcred":                   {"", ""},
		"git@github.com:":                              {"", ""},
	} {
		t.Run(url, func(t *testing.T) {
			host, slug, ok := parseURL(url)
			if (want.host != "") != ok {
				t.Fatalf("parseURL(%q) ok = %v", url, ok)
			}
			if ok && (host != want.host || slug != want.slug) {
				t.Errorf("parseURL(%q) = %q, %q; want %q, %q", url, host, slug, want.host, want.slug)
			}
		})
	}
}

// Only a host wt reads as GitHub's counts; everything else means "this
// repository is not on GitHub", which is an ordinary state.
func TestIsGitHub(t *testing.T) {
	for host, want := range map[string]bool{
		"github.com":          true,
		"github.acme.example": true,
		"ghe.github.corp":     true,
		"gitlab.com":          false,
		"bitbucket.org":       false,
		"":                    false,
	} {
		if got := isGitHub(host); got != want {
			t.Errorf("isGitHub(%q) = %v, want %v", host, got, want)
		}
	}
}

// origin is the remote you work against, so it wins; a repository with no
// GitHub remote at all answers no.
func TestRemoteOfPrefersOrigin(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", ".")
	runGit(t, dir, "remote", "add", "upstream", "git@github.com:sharkdp/fd.git")
	runGit(t, dir, "remote", "add", "origin", "git@github.com:anders/fd.git")
	runGit(t, dir, "remote", "add", "mirror", "/srv/git/fd.git")

	r, ok := RemoteOf(dir)
	if !ok {
		t.Fatal("RemoteOf found nothing")
	}
	if r.Name != "origin" || r.Slug != "anders/fd" || r.Host != "github.com" {
		t.Errorf("RemoteOf() = %+v", r)
	}
}

func TestRemoteOfFallsBackAndGivesUp(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", ".")
	runGit(t, dir, "remote", "add", "fork", "https://github.com/anders/fd")
	if r, ok := RemoteOf(dir); !ok || r.Name != "fork" {
		t.Errorf("RemoteOf() = %+v, %v; want the only GitHub remote", r, ok)
	}

	bare := t.TempDir()
	runGit(t, bare, "init", "-q", ".")
	runGit(t, bare, "remote", "add", "origin", "https://gitlab.com/t/x.git")
	if r, ok := RemoteOf(bare); ok {
		t.Errorf("RemoteOf() = %+v on a repository that is not on GitHub", r)
	}
	if _, ok := RemoteOf(t.TempDir()); ok {
		t.Error("RemoteOf found a remote outside a repository")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
