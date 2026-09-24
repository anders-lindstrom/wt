package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// The per-user settings: whether wt uses each integration on this machine.
// worktree.conf is committed, so it cannot answer this for whoever clones the
// repository.
const (
	// UserKeySuperset decides whether wt talks to the Superset desktop app at
	// all. Off by default.
	UserKeySuperset = "superset"
	// UserKeyGitHub decides whether wt uses the GitHub CLI. On by default.
	UserKeyGitHub = "github"
	// UserKeyBranchSuffix is the suffix this person's branches carry, for
	// every repository that does not insist on one of its own. Empty is a
	// value: it asks for plain feature/login-crash branches.
	UserKeyBranchSuffix = "branch_suffix"
	// UserKeyTypeNames is what this person calls each type in a branch name,
	// as <type>=<name> pairs, for every repository that does not name its
	// types itself.
	UserKeyTypeNames = "type_names"
)

// userKind is how a setting's value is written: the file, the validator, the
// printer and `wt config set` all read it here.
type userKind int

const (
	userBool userKind = iota
	userString
	userList
)

// userKey is one setting in the user file. Default is the value as the file
// would write it, so one field covers both kinds.
type userKey struct {
	Name     string
	Kind     userKind
	Default  string
	Doc      string
	boolAt   func(*User) *bool
	stringAt func(*User) *string
	listAt   func(*User) *[]string
}

// userKeys is every key the user file accepts; the reader, the writer, the
// printer and the unknown-key check are all derived from it.
var userKeys = []userKey{
	{Name: UserKeySuperset, Kind: userBool, Default: "false",
		Doc:    "register worktrees wt creates as Superset workspaces",
		boolAt: func(u *User) *bool { return &u.Superset }},
	{Name: UserKeyGitHub, Kind: userBool, Default: "true",
		Doc:    "use the GitHub CLI for branches and pull requests",
		boolAt: func(u *User) *bool { return &u.GitHub }},
	{Name: UserKeyBranchSuffix, Kind: userString, Default: DefaultTypeSuffix,
		Doc:      `the suffix your branches carry ("" for none), where a repo does not say`,
		stringAt: func(u *User) *string { return &u.BranchSuffix }},
	{Name: UserKeyTypeNames, Kind: userList, Default: "",
		Doc:    "what your branches call each type, as feat=feature pairs",
		listAt: func(u *User) *[]string { return &u.TypeNames }},
}

// UserKeyNames is every key the user file accepts, in the order `wt config`
// prints them.
func UserKeyNames() []string {
	out := make([]string, 0, len(userKeys))
	for _, k := range userKeys {
		out = append(out, k.Name)
	}
	return out
}

// UserLiteral is one value as the user file writes it: a bare true or false,
// a quoted string. `wt config` prints values this way, so a branch suffix of
// nothing at all is something a person can see.
func UserLiteral(name, value string) string {
	k, ok := userKeyByName(name)
	if !ok {
		return value
	}
	return k.literal(value)
}

// UserKeyDoc is the one-line explanation of a key, or "" for a key wt does
// not read.
func UserKeyDoc(name string) string {
	if k, ok := userKeyByName(name); ok {
		return k.Doc
	}
	return ""
}

func userKeyByName(name string) (userKey, bool) {
	i := slices.IndexFunc(userKeys, func(k userKey) bool { return k.Name == name })
	if i < 0 {
		return userKey{}, false
	}
	return userKeys[i], true
}

// User is the per-user configuration. A missing file means the built-in
// defaults; nothing creates the file but `wt config set`.
type User struct {
	Superset bool
	GitHub   bool
	// BranchSuffix is read only where the repository leaves the choice open
	// (see Config.BranchSuffixSet), so IsSet says whether it was chosen.
	BranchSuffix string
	// TypeNames is <type>=<name> pairs, read the same way and against each
	// repository's own types: a pair for a type a repository does not have
	// is a name for somewhere else, not a mistake.
	TypeNames []string
	// Roots and Profiles are the [roots] and [profiles] tables, in the order
	// the file writes them; nil when it has none.
	Roots    []NamedRoot
	Profiles []Profile
	// TablesUnreadable says [roots] or [profiles] is there and could not be
	// read, so the roots it names are not known.
	TablesUnreadable bool
	// Path is the file these values would be read from, whether or not it
	// exists.
	Path string
	// Exists says the file was there.
	Exists bool
	// Unusable says the file was there and wt could not read it. Every
	// integration is then off for the run: a file nobody can parse must not
	// hand back a default that switches one on.
	Unusable bool
	// fromFile records which keys the file set, so `wt config` can say where
	// a value came from.
	fromFile map[string]bool
}

// DefaultUser is the configuration a machine with no user file has.
func DefaultUser() *User { return defaultUser("") }

func defaultUser(path string) *User {
	u := &User{Path: path, fromFile: map[string]bool{}}
	for _, k := range userKeys {
		k.put(u, k.Default)
	}
	return u
}

// put writes a value into the User, taking it as the file spells it. A bool
// that does not parse is left at false, which is what a misread key costs.
func (k userKey) put(u *User, value string) {
	switch k.Kind {
	case userString:
		*k.stringAt(u) = value
	case userList:
		*k.listAt(u) = strings.Fields(value)
	default:
		*k.boolAt(u) = value == "true"
	}
}

// get reads the value back as the file would spell it.
func (k userKey) get(u *User) string {
	switch k.Kind {
	case userString:
		return *k.stringAt(u)
	case userList:
		return strings.Join(*k.listAt(u), " ")
	default:
		return strconv.FormatBool(*k.boolAt(u))
	}
}

// literal is the value as TOML writes it: a bare true or false, a quoted
// string.
func (k userKey) literal(value string) string {
	switch k.Kind {
	case userString:
		return strconv.Quote(value)
	case userList:
		items := strings.Fields(value)
		quoted := make([]string, 0, len(items))
		for _, item := range items {
			quoted = append(quoted, strconv.Quote(item))
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	default:
		return value
	}
}

// unusableUser is the configuration of a run whose file is there and cannot
// be read: every integration off, whatever its own default is. The person
// wrote something into that file, and wt does not know what.
//
// Only the integrations. A setting that switches nothing on — what a branch is
// called — stays at its default and leaves the repository to decide, because
// refusing to name a branch is not something wt can do safely.
func unusableUser(path string) *User {
	u := defaultUser(path)
	u.Exists, u.Unusable = true, true
	for _, k := range userKeys {
		if k.Kind == userBool {
			*k.boolAt(u) = false
		}
	}
	return u
}

// Origin says where a key's value came from: "user file", "default", or the
// unreadable file that turned every integration off.
func (u *User) Origin(name string) string {
	switch {
	case u == nil:
		return "default"
	case u.Unusable:
		return "file unreadable"
	case u.fromFile[name]:
		return "user file"
	}
	return "default"
}

// Value is one key's current value, as the file spells it.
func (u *User) Value(name string) (string, error) {
	if table, entry, ok := tableKey(name); ok {
		return u.tableValue(table, entry)
	}
	k, ok := userKeyByName(name)
	if !ok {
		return "", unknownUserKey(name)
	}
	return k.get(u), nil
}

// IsSet says the file chose this value, rather than wt defaulting it. It is
// what lets a person's branch suffix apply only where they wrote one down.
func (u *User) IsSet(name string) bool {
	return u != nil && !u.Unusable && u.fromFile[name]
}

func unknownUserKey(name string) error {
	return fmt.Errorf("unknown key %q; the user config takes: %s root.<name> profile.<name>",
		name, strings.Join(UserKeyNames(), " "))
}

// userFile is the file's name under the wt configuration directory.
const userFile = "config.toml"

// UserPath is where the user file lives: under XDG_CONFIG_HOME when the
// environment sets one, and ~/.config otherwise.
func UserPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "wt", userFile), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "wt", userFile), nil
}

// LoadUser reads the user configuration. A missing file is not an error, and
// a usable User comes back even when there is one.
func LoadUser() (*User, error) {
	path, err := UserPath()
	if err != nil {
		return DefaultUser(), err
	}
	return loadUser(path)
}

func loadUser(path string) (*User, error) {
	u := defaultUser(path)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return u, nil
	}
	if err != nil {
		return unusableUser(path), err
	}
	u.Exists = true

	var doc map[string]toml.Primitive
	md, err := toml.Decode(string(data), &doc)
	if err != nil {
		return unusableUser(path), fmt.Errorf("%s: %w", path, err)
	}
	problems := decodeTables(u, md, doc, md.Keys())
	for _, name := range md.Keys() {
		// Every setting is top-level; a key inside a table is covered by the
		// table's own entry, which is already unknown.
		if len(name) != 1 || name[0] == tableRoots || name[0] == tableProfiles {
			continue
		}
		k, ok := userKeyByName(name[0])
		if !ok {
			problems = append(problems, fmt.Sprintf("unknown key %q in %s", name[0], path))
			continue
		}
		value, err := decodeUser(md, doc[k.Name], k)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s in %s %s", k.Name, path, err))
			continue
		}
		k.put(u, value)
		u.fromFile[k.Name] = true
	}
	if len(problems) > 0 {
		// The keys that did parse are kept: one mistyped key must not cost
		// the person the setting they wrote on the line above it.
		return u, fmt.Errorf("%s (wt's settings are: %s, and the [roots] and [profiles] tables)",
			strings.Join(problems, "; "), strings.Join(UserKeyNames(), " "))
	}
	return u, nil
}

// decodeUser reads one value as its kind says it is written, and says what it
// should have been when it is not.
func decodeUser(md toml.MetaData, prim toml.Primitive, k userKey) (string, error) {
	if k.Kind == userList {
		var l []string
		if err := md.PrimitiveDecode(prim, &l); err != nil {
			return "", errors.New(`is not a list of strings (write it as ["feat=feature"])`)
		}
		return strings.Join(l, " "), nil
	}
	if k.Kind == userString {
		var s string
		if err := md.PrimitiveDecode(prim, &s); err != nil {
			return "", errors.New(`is not a string (write it in quotes, "" for none)`)
		}
		return s, nil
	}
	var b bool
	if err := md.PrimitiveDecode(prim, &b); err != nil {
		return "", errors.New("is not a boolean (true or false)")
	}
	return strconv.FormatBool(b), nil
}

// SetUser writes one key to the user file, creating the file if it is not
// there, and returns the path and the parsed value. An unknown key, a value
// that is not a boolean, or a file wt cannot parse writes nothing.
func SetUser(name, value string) (path string, set string, err error) {
	if _, _, ok := tableKey(name); ok {
		return setTable(name, value)
	}
	literal, err := userValue(name, value)
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
	return path, literal, os.WriteFile(path, []byte(assign(content, name, literal)), 0o644)
}

// UnsetUser removes one key from the user file, so it falls back to its
// built-in default. Removing a key that was never written is not an error.
func UnsetUser(name string) (path string, removed bool, err error) {
	if _, _, ok := tableKey(name); ok {
		return unsetTable(name)
	}
	if _, ok := userKeyByName(name); !ok {
		return "", false, unknownUserKey(name)
	}
	path, content, err := editable()
	if err != nil {
		return path, false, err
	}
	out, removed := remove(content, name)
	if !removed {
		return path, false, nil
	}
	return path, true, os.WriteFile(path, []byte(out), 0o644)
}

// userValue validates the value a key is being set to and returns it as the
// file will spell it. A boolean is exactly true or false, the two words the
// file, the help and `wt config` all use: strconv.ParseBool would also take 1,
// t, T and TRUE, which nothing here writes or documents. A branch suffix is
// anything a branch can carry before its slash, the empty string included.
func userValue(name, value string) (string, error) {
	k, ok := userKeyByName(name)
	if !ok {
		return "", unknownUserKey(name)
	}
	if k.Kind == userList {
		// The types are not known here — they are each repository's — so the
		// pairs are checked for shape only, and read against the types where
		// they are used.
		for _, pair := range strings.Fields(value) {
			typ, word, found := strings.Cut(pair, "=")
			if !found || typ == "" || word == "" || strings.Contains(word, "/") {
				return "", fmt.Errorf(
					"%s: %q is not a <type>=<name> pair, e.g. feat=feature", name, pair)
			}
		}
		return k.literal(value), nil
	}
	if k.Kind == userString {
		if strings.ContainsAny(value, "/ \t") {
			return "", fmt.Errorf(
				"%s=%q may not contain a slash or a space; it is what a name carries before one",
				name, value)
		}
		return k.literal(value), nil
	}
	switch value {
	case "true", "false":
		return k.literal(value), nil
	}
	return "", fmt.Errorf("%s=%q is not a boolean; write true or false", name, value)
}

// editable is the path of the user file and the content the writer may edit.
// A file whose TOML does not parse is refused: the writer works line by line,
// and a line edited into a file wt has not understood can only make it worse.
func editable() (path, content string, err error) {
	if path, err = UserPath(); err != nil {
		return "", "", err
	}
	if content, err = readOrEmpty(path); err != nil {
		return path, "", err
	}
	if err := parses(content); err != nil {
		return path, "", fmt.Errorf("%s: %w\n  fix it by hand, then try again", path, err)
	}
	return path, content, nil
}

// parses reports the file's own syntax error, or nil for a file wt can read.
func parses(content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	var doc map[string]toml.Primitive
	_, err := toml.Decode(content, &doc)
	return err
}

func readOrEmpty(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// userFileHeader opens a file wt creates from nothing.
const userFileHeader = "# wt's own settings, for this user on this machine.\n" +
	"# `wt config` prints them; `wt config set <key> <value>` changes them.\n\n"

// assign rewrites content with name set to the given TOML literal, editing the
// line in place when the key is already there. Line by line, not
// decode-and-re-encode: a TOML encoder drops the comments and the ordering.
func assign(content, name, literal string) string {
	line := name + " = " + literal
	if content == "" {
		return userFileHeader + line + "\n"
	}
	lines, crlf, final := splitUserFile(content)
	top := topLevel(lines)
	for i, l := range lines[:top] {
		if rest, ok := assignmentTo(l, name); ok {
			lines[i] = line + trailingComment(rest)
			return joinUserFile(lines, crlf, final)
		}
	}
	return joinUserFile(insertAt(lines, top, line), crlf, final)
}

// remove takes name's assignment out of content, reporting whether it found
// one.
func remove(content, name string) (string, bool) {
	lines, crlf, final := splitUserFile(content)
	for i, l := range lines[:topLevel(lines)] {
		if _, ok := assignmentTo(l, name); ok {
			return joinUserFile(slices.Delete(slices.Clone(lines), i, i+1), crlf, final), true
		}
	}
	return content, false
}

// splitUserFile cuts content into lines to edit, and carries the two things
// the rewrite has to give back: CRLF endings, and whether the file ended with
// a newline at all.
func splitUserFile(content string) (lines []string, crlf, final bool) {
	crlf = strings.Contains(content, "\r\n")
	if crlf {
		content = strings.ReplaceAll(content, "\r\n", "\n")
	}
	final = strings.HasSuffix(content, "\n")
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n"), crlf, final
}

// joinUserFile writes the lines back the way the file had them.
func joinUserFile(lines []string, crlf, final bool) string {
	out := strings.Join(lines, "\n")
	if final {
		out += "\n"
	}
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out
}

// topLevel is how many of lines stand before the first `[table]` header. wt's
// settings are all top-level, so a `github` written under a table is that
// table's key and none of the writer's business.
func topLevel(lines []string) int {
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			return i
		}
	}
	return len(lines)
}

// assignmentTo reports whether line assigns name, and returns what stands
// after the `=`. TOML lets a key be quoted, and `"github" = false` is the same
// assignment as `github = false`.
func assignmentTo(line, name string) (rest string, ok bool) {
	key, rest, found := strings.Cut(line, "=")
	if !found || unquoteKey(strings.TrimSpace(key)) != name {
		return "", false
	}
	return rest, true
}

// unquoteKey takes off the quotes TOML allows around a key. Every key wt
// reads is a bare word, so there is nothing else to undo.
func unquoteKey(key string) string {
	for _, q := range []string{`"`, "'"} {
		if len(key) >= 2 && strings.HasPrefix(key, q) && strings.HasSuffix(key, q) {
			return key[1 : len(key)-1]
		}
	}
	return key
}

// trailingComment keeps a `# ...` written after the value, so setting a key
// does not delete the note explaining it.
func trailingComment(rest string) string {
	i := strings.Index(rest, "#")
	if i < 0 {
		return ""
	}
	return "  " + strings.TrimSpace(rest[i:])
}

// insertAt puts a new assignment at the end of the top-level section, above
// the blank lines that stand there — before a `[table]` header, or at the end
// of a file that has none.
func insertAt(lines []string, at int, line string) []string {
	for at > 0 && strings.TrimSpace(lines[at-1]) == "" {
		at--
	}
	return slices.Insert(slices.Clone(lines), at, line)
}
