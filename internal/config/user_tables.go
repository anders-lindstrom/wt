package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// The two tables of the user file: where this person's repositories live, and
// named selections of them. Both are per machine, which is why they are here
// and not in any repository.
const (
	tableRoots    = "roots"
	tableProfiles = "profiles"
)

// NamedRoot is a directory repositories sit in, one level down, under the
// name --roots selects it by. Path is as the file writes it; a leading ~ is
// the reader's to expand.
type NamedRoot struct {
	Name string
	Path string
}

// Profile is a named list of repositories, each by the path of its main
// checkout as the file writes it.
type Profile struct {
	Name  string
	Repos []string
}

// tableKeys maps the dotted spelling `wt config` takes to the table it
// writes: root.telcred is telcred in [roots].
var tableKeys = map[string]string{"root": tableRoots, "profile": tableProfiles}

// tableEntryName is what a name in either table may be: a bare TOML key, so
// the file needs no quoting and a flag can name it as typed.
var tableEntryName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// tableKey splits root.<name> or profile.<name> into its table and name. ok is
// false for anything else, which is then a plain key or an unknown one.
func tableKey(key string) (table, name string, ok bool) {
	prefix, name, found := strings.Cut(key, ".")
	table, known := tableKeys[prefix]
	if !found || !known {
		return "", "", false
	}
	return table, name, true
}

// IsTableKey reports that key names an entry in [roots] or [profiles]:
// root.<name> or profile.<name>.
func IsTableKey(key string) bool {
	_, _, ok := tableKey(key)
	return ok
}

// Root is the root of that name, and false when the file names none.
func (u *User) Root(name string) (NamedRoot, bool) {
	i := slices.IndexFunc(u.Roots, func(r NamedRoot) bool { return r.Name == name })
	if i < 0 {
		return NamedRoot{}, false
	}
	return u.Roots[i], true
}

// Profile is the profile of that name, and false when the file names none.
func (u *User) Profile(name string) (Profile, bool) {
	i := slices.IndexFunc(u.Profiles, func(p Profile) bool { return p.Name == name })
	if i < 0 {
		return Profile{}, false
	}
	return u.Profiles[i], true
}

// tableValue is one entry's value as `wt config get` prints it: a root's path,
// or a profile's repositories separated by spaces.
func (u *User) tableValue(table, name string) (string, error) {
	switch table {
	case tableRoots:
		if r, ok := u.Root(name); ok {
			return r.Path, nil
		}
	default:
		if p, ok := u.Profile(name); ok {
			return strings.Join(p.Repos, " "), nil
		}
	}
	return "", fmt.Errorf("no %s named %q in %s", strings.TrimSuffix(table, "s"), name, u.Path)
}

// decodeTables reads [roots] and [profiles] into u, in the order the file
// writes them, and says what is wrong with an entry it could not read. keys is
// every key the file has, in its order, which is where the order comes from.
func decodeTables(u *User, md toml.MetaData, doc map[string]toml.Primitive, keys []toml.Key) []string {
	var problems []string
	if prim, ok := doc[tableRoots]; ok {
		var roots map[string]string
		if err := md.PrimitiveDecode(prim, &roots); err != nil {
			problems = append(problems, fmt.Sprintf(`[roots] in %s is not a table of paths (write telcred = "~/src/telcred")`, u.Path))
		} else {
			for _, name := range tableOrder(keys, tableRoots) {
				u.Roots = append(u.Roots, NamedRoot{Name: name, Path: roots[name]})
			}
		}
	}
	if prim, ok := doc[tableProfiles]; ok {
		var profiles map[string][]string
		if err := md.PrimitiveDecode(prim, &profiles); err != nil {
			problems = append(problems, fmt.Sprintf(`[profiles] in %s is not a table of path lists (write backend = ["~/src/server"])`, u.Path))
		} else {
			for _, name := range tableOrder(keys, tableProfiles) {
				u.Profiles = append(u.Profiles, Profile{Name: name, Repos: profiles[name]})
			}
		}
	}
	u.TablesUnreadable = len(problems) > 0
	return problems
}

// tableOrder is the names in one table, in the order the file writes them.
func tableOrder(keys []toml.Key, table string) []string {
	var out []string
	for _, k := range keys {
		if len(k) == 2 && k[0] == table {
			out = append(out, k[1])
		}
	}
	return out
}

// tableLiteral validates an entry being set and returns it as the file will
// write it: a root is one path, a profile one or more, separated by spaces.
func tableLiteral(table, name, value string) (string, error) {
	if !tableEntryName.MatchString(name) {
		return "", fmt.Errorf("%q is not a name wt can use here: letters, digits, - and _ only", name)
	}
	if table == tableRoots {
		value = strings.TrimSpace(value)
		if value == "" {
			return "", errors.New("a root needs the directory its repositories sit in")
		}
		return strconv.Quote(value), nil
	}
	repos := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	if len(repos) == 0 {
		return "", errors.New("a profile needs at least one repository path")
	}
	quoted := make([]string, 0, len(repos))
	for _, r := range repos {
		quoted = append(quoted, strconv.Quote(r))
	}
	return "[" + strings.Join(quoted, ", ") + "]", nil
}

// setTable writes one entry into its table, adding the table at the end of the
// file when it is not there yet.
func setTable(key, value string) (path, set string, err error) {
	table, name, _ := tableKey(key)
	literal, err := tableLiteral(table, name, value)
	if err != nil {
		return "", "", err
	}
	path, content, err := editable()
	if err != nil {
		return path, "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, "", err
	}
	out := assignInTable(content, table, name, literal)
	if err := parses(out); err != nil {
		return path, "", unsafeEdit(path, table)
	}
	return path, literal, os.WriteFile(path, []byte(out), 0o644)
}

// unsetTable takes one entry out of its table. An entry that was never there
// is not an error.
func unsetTable(key string) (path string, removed bool, err error) {
	table, name, _ := tableKey(key)
	path, content, err := editable()
	if err != nil {
		return path, false, err
	}
	out, removed := removeFromTable(content, table, name)
	if !removed {
		return path, false, nil
	}
	if err := parses(out); err != nil {
		return path, false, unsafeEdit(path, table)
	}
	return path, true, os.WriteFile(path, []byte(out), 0o644)
}

// unsafeEdit is the refusal for a file whose [table] is written in a shape the
// line-by-line writer cannot edit — an inline or dotted table, a value over
// several lines — which it can tell because its edit would not parse.
func unsafeEdit(path, table string) error {
	return fmt.Errorf("%s writes [%s] in a shape wt cannot edit line by line (an inline or dotted table, "+
		"or a value over several lines); nothing was written: edit it by hand", path, table)
}

// assignInTable rewrites content with name set inside [table], editing the
// line in place when it is there and adding the table at the end of the file
// when it is not. Line by line, like assign, so comments and order survive.
func assignInTable(content, table, name, literal string) string {
	line := name + " = " + literal
	if content == "" {
		return userFileHeader + "[" + table + "]\n" + line + "\n"
	}
	lines, crlf, final := splitUserFile(content)
	start, end, found := tableSpan(lines, table)
	if !found {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, "", "["+table+"]", line)
		return joinUserFile(lines, crlf, true)
	}
	for i := start + 1; i < end; i++ {
		if rest, ok := assignmentTo(lines[i], name); ok {
			lines[i] = line + trailingComment(rest)
			return joinUserFile(lines, crlf, final)
		}
	}
	return joinUserFile(insertAt(lines, end, line), crlf, final)
}

// removeFromTable takes name's line out of [table], reporting whether there
// was one.
func removeFromTable(content, table, name string) (string, bool) {
	lines, crlf, final := splitUserFile(content)
	start, end, found := tableSpan(lines, table)
	if !found {
		return content, false
	}
	for i := start + 1; i < end; i++ {
		if _, ok := assignmentTo(lines[i], name); ok {
			return joinUserFile(slices.Delete(slices.Clone(lines), i, i+1), crlf, final), true
		}
	}
	return content, false
}

// tableSpan is where [table] starts and where the next table starts, or the
// end of the file.
func tableSpan(lines []string, table string) (start, end int, found bool) {
	start = -1
	for i, l := range lines {
		header := strings.TrimSpace(l)
		if c := strings.Index(header, "#"); c >= 0 {
			header = strings.TrimSpace(header[:c])
		}
		if !strings.HasPrefix(header, "[") {
			continue
		}
		if start >= 0 {
			return start, i, true
		}
		if strings.ReplaceAll(header, " ", "") == "["+table+"]" {
			start = i
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	return start, len(lines), true
}
