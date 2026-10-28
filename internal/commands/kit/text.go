package kit

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ShellQuote single-quotes a value so that eval cannot reinterpret it.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CommitsWord is n commits, in words.
func CommitsWord(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

// ElideRight shortens s to at most limit runes, keeping its start.
func ElideRight(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

// NoteWidth caps a free-text note under a row of the overview; wt sync <work>
// prints it whole.
const NoteWidth = 100

// Ago is a duration as a person reads an age: the largest whole unit.
func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}

// Truncate shortens s to at most n runes, ending it with an ellipsis.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// NetworkTimeout bounds the calls that reach the network: a run's fetch, and
// the push it ends with. It is shorter than the default git deadline: one
// that has not finished by now is not going to, and every worktree in the run
// is waiting on it.
const NetworkTimeout = 5 * time.Minute

// Count is n of noun, in words: "1 commit", "3 commits". many is the plural
// where it is not noun and an s: Count(n, "branch", "branches").
func Count(n int, noun string, many ...string) string {
	switch {
	case n == 1:
		return "1 " + noun
	case len(many) > 0:
		return strconv.Itoa(n) + " " + many[0]
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
