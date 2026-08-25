// Package config loads and validates a repository's worktree configuration.
package config

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Value is one parsed assignment. A scalar has IsList false; an array has
// IsList true, which is how an explicitly empty array stays distinguishable
// from an absent key.
type Value struct {
	Scalar string
	List   []string
	IsList bool
}

// ParseBash reads the bash subset the existing worktree.conf files use:
// an optional shebang, # comments, KEY=bare, KEY="quoted", KEY=(a b c),
// and multi-line arrays terminated by ) on its own line.
func ParseBash(r io.Reader) (map[string]Value, error) {
	out := map[string]Value{}
	sc := bufio.NewScanner(r)

	var arrayKey string
	var arrayItems []string

	for line := 1; sc.Scan(); line++ {
		raw := strings.TrimSpace(sc.Text())

		if arrayKey != "" {
			if strings.HasPrefix(raw, ")") {
				out[arrayKey] = Value{List: arrayItems, IsList: true}
				arrayKey, arrayItems = "", nil
				continue
			}
			arrayItems = append(arrayItems, splitFields(stripComment(raw))...)
			continue
		}

		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}

		key, rest, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: not an assignment: %q", line, raw)
		}
		key = strings.TrimSpace(key)
		if !validKey(key) {
			return nil, fmt.Errorf("line %d: invalid key %q", line, key)
		}

		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, "(") {
			body := strings.TrimPrefix(rest, "(")
			if closeIdx := strings.Index(body, ")"); closeIdx >= 0 {
				items := splitFields(body[:closeIdx])
				if items == nil {
					items = []string{}
				}
				out[key] = Value{List: items, IsList: true}
				continue
			}
			arrayKey = key
			arrayItems = splitFields(stripComment(body))
			if arrayItems == nil {
				arrayItems = []string{}
			}
			continue
		}
		out[key] = Value{Scalar: unquote(stripComment(rest))}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if arrayKey != "" {
		return nil, fmt.Errorf("unterminated array for %q", arrayKey)
	}
	return out, nil
}

func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// stripComment removes a trailing # comment that is not inside quotes.
func stripComment(s string) string {
	inSingle, inDouble := false, false
	for i, r := range s {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return strings.TrimSpace(s)
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func splitFields(s string) []string {
	var items []string
	for _, f := range strings.Fields(s) {
		if f = unquote(f); f != "" {
			items = append(items, f)
		}
	}
	return items
}
