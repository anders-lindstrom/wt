package commands

import (
	"strings"
	"unicode"
)

// These mirror gittree's RemoteURL (Sources/GitCore/Remotes/RemoteURL.swift)
// rule for rule, since a run's endpoint is read by both: which remote URLs
// carry credentials, and what a URL looks like with its userinfo removed.

// tokenSchemes are the schemes whose user name alone is a secret: a token is
// often passed as the user (https://ghp_…@github.com/…).
var tokenSchemes = map[string]bool{"http": true, "https": true, "ftp": true, "ftps": true}

// hasCredentials reports any userinfo in an http, https, ftp or ftps URL,
// and a password (user:secret@) in any URL, scp-like user:secret@host:path
// included. An ssh user name (git@host:path, ssh://git@host/…) is not a
// secret. A remote-helper address (<transport>::<address>) is judged on its
// address too.
func hasCredentials(url string) bool {
	if address, ok := helperAddress(url); ok && hasCredentials(address) {
		return true
	}
	info, ok := userinfoOf(url)
	if !ok {
		return false
	}
	if scheme, ok := schemeOf(url); ok && tokenSchemes[strings.ToLower(scheme)] {
		return true
	}
	return strings.Contains(info, ":")
}

// redacted is the URL without its userinfo: https://tok@github.com/o/r is
// https://github.com/o/r, git@github.com:o/r is github.com:o/r. A URL
// without userinfo comes back unchanged.
func redacted(url string) string {
	if i := strings.Index(url, "::"); i >= 0 {
		if _, ok := helperAddress(url); ok {
			return url[:i+2] + redacted(url[i+2:])
		}
	}
	start, end, ok := userinfoRange(url)
	if !ok {
		return url
	}
	return url[:start] + url[end:]
}

// schemeOf is https of https://…; false for scp-like and local forms.
func schemeOf(url string) (string, bool) {
	i := strings.Index(url, "://")
	if i <= 0 {
		return "", false
	}
	scheme := url[:i]
	for _, r := range scheme {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && !strings.ContainsRune("+-.", r) {
			return "", false
		}
	}
	return scheme, true
}

// helperAddress is the address of <transport>::<address>, git's
// remote-helper syntax, when the URL has that form.
func helperAddress(url string) (string, bool) {
	sep := strings.Index(url, "::")
	if sep < 0 {
		return "", false
	}
	if s := strings.Index(url, "://"); s >= 0 && s < sep {
		return "", false
	}
	transport := url[:sep]
	if transport == "" || !isASCIIAlnum(rune(transport[0])) {
		return "", false
	}
	for _, r := range transport {
		if !isASCIIAlnum(r) && !strings.ContainsRune("+.-", r) {
			return "", false
		}
	}
	return url[sep+2:], true
}

func isASCIIAlnum(r rune) bool {
	return r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

// userinfoOf is the userinfo without its @.
func userinfoOf(url string) (string, bool) {
	start, end, ok := userinfoRange(url)
	if !ok {
		return "", false
	}
	return url[start : end-1], true
}

// userinfoRange is where the userinfo and its @ are, url[start:end]. In a
// scheme:// URL the authority ends at the first /, ? or # (as git's
// credential_from_url and curl read it) and the userinfo runs to its last @.
// In any other form the userinfo is what precedes the first @ when no /
// comes before it (a local path such as /repos/a@b has none).
func userinfoRange(url string) (start, end int, ok bool) {
	if sep := strings.Index(url, "://"); sep >= 0 {
		if _, isScheme := schemeOf(url); isScheme {
			start = sep + 3
			stop := len(url)
			if j := strings.IndexAny(url[start:], "/?#"); j >= 0 {
				stop = start + j
			}
			at := strings.LastIndex(url[start:stop], "@")
			if at < 0 {
				return 0, 0, false
			}
			return start, start + at + 1, true
		}
	}
	at := strings.Index(url, "@")
	if at < 0 || strings.Contains(url[:at], "/") {
		return 0, 0, false
	}
	return 0, at + 1, true
}
