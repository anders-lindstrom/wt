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

// preferred is the order remotes are considered in. A repository with both
// has origin as the one you work against and upstream as the one you forked;
// anything else is taken in the order git lists it.
var preferred = []string{"origin", "upstream"}

// RemoteOf returns the GitHub repository a checkout points at, and false when
// it points at none. Only the URL is read: no process is started beyond the
// one git that lists the remotes, so this is cheap enough for `wt list`.
func RemoteOf(dir string) (Remote, bool) {
	lines, err := git.Lines(dir, "config", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		return Remote{}, false
	}
	var found []Remote
	for _, line := range lines {
		key, url, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		host, slug, ok := parseURL(url)
		if !ok || !isGitHub(host) {
			continue
		}
		found = append(found, Remote{Name: name, Host: host, Slug: slug})
	}
	if len(found) == 0 {
		return Remote{}, false
	}
	for _, want := range preferred {
		for _, r := range found {
			if r.Name == want {
				return r, true
			}
		}
	}
	return found[0], true
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
