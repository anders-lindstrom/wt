package commands

import "testing"

// The cases of gittree's RemoteURLTests, so the two agree on what a
// credential is and what a redacted URL looks like.
func TestRemoteURLCredentials(t *testing.T) {
	for _, url := range []string{
		"https://ghp_abc123@github.com/o/r.git",
		"https://user:pass@github.com/o/r.git",
		"HTTPS://token@host/r",
		"http://user@host:8080/r",
		"ftp://u@host/r",
		"ftps://u:p@host/r",
		"ssh://git:secret@host/r.git",
		"git+ssh://u:p@host/r",
		"file://u:p@localhost/tmp/r",
		"user:secret@host:path/r.git",
		"https://@host/r",
		"https://a@b@host/r",
		"https::https://token@host/r",
		"1x::https://tok@host/r",
	} {
		if !hasCredentials(url) {
			t.Errorf("%q has credentials", url)
		}
	}
	for _, url := range []string{
		"https://github.com/o/r.git",
		"https://host/path@not-userinfo",
		"https://host?q=a@b",
		"ssh://git@github.com/o/r.git",
		"git@github.com:o/r.git",
		"file:///tmp/a@b/r",
		"/tmp/repos/a@b",
		"./a:b@c",
		"../r",
		"ssh://[::1]/r",
		"",
	} {
		if hasCredentials(url) {
			t.Errorf("%q has no credentials", url)
		}
	}
}

func TestRemoteURLRedacted(t *testing.T) {
	for in, want := range map[string]string{ //nolint:gosec // G101: made-up credentials, to test their removal
		"https://ghp_abc@github.com/o/r.git": "https://github.com/o/r.git",
		"https://u:p@host:8443/r?x=1":        "https://host:8443/r?x=1",
		"https://a@b@host/r":                 "https://host/r",
		"ssh://git@host/r":                   "ssh://host/r",
		"git@github.com:o/r.git":             "github.com:o/r.git",
		"user:secret@host:r":                 "host:r",
		"https::https://tok@host/r":          "https::https://host/r",
		"https://host/path@x":                "https://host/path@x",
		"/tmp/a@b":                           "/tmp/a@b",
		"user:pw@[::1]:repo":                 "[::1]:repo",
		"user:pw@[fe80::1]:o/r":              "[fe80::1]:o/r",
		"1x::https://tok@host/r":             "1x::https://host/r",
	} {
		if got := redacted(in); got != want {
			t.Errorf("redacted(%q) = %q, want %q", in, got, want)
		}
	}
	for _, url := range []string{"https://t@h/r", "ssh://u:p@h/r", "u:p@h:r", "user:pw@[::1]:repo"} {
		if hasCredentials(redacted(url)) {
			t.Errorf("redacted(%q) still has credentials", url)
		}
	}
}
