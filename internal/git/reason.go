package git

import (
	"slices"
	"strings"
)

// Reason is the line of a failure worth showing a person: the first line of
// err's text that is not blank and does not start with one of skip, compared
// case-insensitively, once trimmed and without git's "fatal: ". It reads the
// whole text rather than Stderr, so words a caller wrapped around git's stay.
// Text with no such line comes back whole, flattened onto one line.
func Reason(err error, skip ...string) string {
	text := err.Error()
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "fatal: "))
		lower := strings.ToLower(line)
		if line == "" || slices.ContainsFunc(skip, func(p string) bool { return strings.HasPrefix(lower, strings.ToLower(p)) }) {
			continue
		}
		return line
	}
	return strings.Join(strings.Fields(text), " ")
}
