package github

import (
	"strings"

	"github.com/anders-lindstrom/wt/internal/git"
)

// Remote is a GitHub repository some checkout points at.
type Remote struct {
	// Name is the git remote it was read from, e.g. "origin".
	Name string
	Host string
	// Slug is owner/repo.
	Slug string
}

// score ranks a remote the way gh does: upstream, then github, then origin.
// On a fork with origin=your copy and upstream=the parent, that is the parent.
func score(name string) int {
	switch name {
	case "upstream":
		return 3
	case "github":
		return 2
	case "origin":
		return 1
	}
	return 0
}

// RemoteOf returns the GitHub repository a checkout's pull requests belong to,
// and false when it points at none.
//
// It has to be the repository gh itself resolves, because wt acts on gh's
// answers: it fetches a merge's head commit from the remote named here and
// keys the cache on the slug. `gh repo set-default` writes the choice into git
// config and is honoured first; otherwise gh's ordering of remote names wins.
//
// Only git config is read, so this is cheap enough for `wt list`.
func RemoteOf(dir string) (Remote, bool) {
	lines, err := git.Lines(dir, "config", "--get-regexp", `^remote\..*\.(url|gh-resolved)$`)
	if err != nil {
		return Remote{}, false
	}
	var found []Remote
	resolved := map[string]string{}
	for _, line := range lines {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if name, ok := strings.CutSuffix(strings.TrimPrefix(key, "remote."), ".gh-resolved"); ok {
			resolved[name] = value
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		host, slug, ok := parseURL(value)
		if !ok || !isGitHub(host) {
			continue
		}
		found = append(found, Remote{Name: name, Host: host, Slug: slug})
	}
	if len(found) == 0 {
		return Remote{}, false
	}
	best := found[0]
	for _, r := range found[1:] {
		if score(r.Name) > score(best.Name) {
			best = r
		}
	}
	// `gh repo set-default` writes remote.<name>.gh-resolved: "base" means
	// that remote is it, and an owner/repo means a repository that need not
	// be a remote here at all.
	for _, r := range found {
		switch value := resolved[r.Name]; {
		case value == "":
		case value == "base":
			return r, true
		case strings.Count(value, "/") == 1:
			return Remote{Name: r.Name, Host: r.Host, Slug: value}, true
		}
	}
	return best, true
}

// isGitHub reports whether a host is GitHub's: github.com, or any host with
// "github" in its name, which is how Enterprise installations are spelled in
// practice. A host wt does not recognise means "not on GitHub", not an error.
func isGitHub(host string) bool {
	return host == "github.com" || strings.Contains(host, "github")
}

// parseURL reads the host and owner/repo out of a git remote URL, in the
// three spellings git accepts: scp-like ssh, a URL with a scheme, and a local
// path, which is not GitHub and comes back as not ok.
func parseURL(url string) (host, slug string, ok bool) {
	url = strings.TrimSpace(url)
	if rest, found := cutScheme(url); found {
		// //[user@]host[:port]/owner/repo
		rest = strings.TrimPrefix(rest, "//")
		authority, path, cut := strings.Cut(rest, "/")
		if !cut {
			return "", "", false
		}
		return hostOf(authority), trimSlug(path), trimSlug(path) != ""
	}
	// [user@]host:owner/repo
	authority, path, cut := strings.Cut(url, ":")
	if !cut || strings.Contains(authority, "/") {
		return "", "", false
	}
	return hostOf(authority), trimSlug(path), trimSlug(path) != ""
}

// cutScheme drops a leading "<scheme>://", reporting whether there was one.
func cutScheme(url string) (rest string, found bool) {
	i := strings.Index(url, "://")
	if i < 0 {
		return url, false
	}
	return url[i+len("://"):], true
}

// hostOf drops the user and the port from an authority.
func hostOf(authority string) string {
	if _, after, ok := strings.Cut(authority, "@"); ok {
		authority = after
	}
	if before, _, ok := strings.Cut(authority, ":"); ok {
		authority = before
	}
	return strings.ToLower(authority)
}

// trimSlug reduces a URL path to owner/repo: no leading slash, no .git, and
// nothing deeper than the two segments, which is all a GitHub URL has.
func trimSlug(path string) string {
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}
