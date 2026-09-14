package wtsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// A version rule runs at a conflict. When the branch bumped a version and
// trunk independently took the same number, git sees the same edit on both
// sides and merges the file clean, so no strategy ever runs and the branch
// silently lands on trunk's number, without the version of its own that
// max-plus-patch exists to give it. The lift closes that gap: at every pick,
// for each file a max-plus-patch rule claims that git merged clean, the rule
// reads the version from the pick's parent, from what the pick is applied
// on, and from the pick itself, and when both sides changed it and the
// merged value is not above trunk's, rewrites the merged file to trunk's
// value plus one patch, exactly as the strategy would have answered a
// conflict. The rewrite rides in the pick, in the simulation and the run
// alike.

// lifter is a strategy whose rule may lift a version on a file git merged
// clean. Lift answers nil when the file needs nothing: the branch did not
// change the line, trunk did not, or the merged value is already above
// trunk's. It answers a *Refusal when a lift is due and the values are not
// X.Y.Z on both sides: the same refusal the strategy gives a conflict.
type lifter interface {
	Lift(path string, base, trunk, branch, merged []byte) (*Lift, error)
}

// Lift is one file rewritten: the bytes, and for the report the versions
// trunk took and the ones the branch now carries, in the file's own order.
type Lift struct {
	Content []byte
	From    []string
	To      []string
}

// note is what a lift says beside its path: "lifted the version to 1.2.4:
// trunk took 1.2.3", or with two owned lines "lifted the versions to 1.2.4
// and 1.5.3: trunk took 1.2.3 and 1.5.2".
func (l Lift) note() string {
	noun := "version"
	if len(l.To) > 1 {
		noun = "versions"
	}
	return fmt.Sprintf("lifted the %s to %s: trunk took %s", noun, strings.Join(l.To, " and "), strings.Join(l.From, " and "))
}

// lifts reports whether a declaration's rule lifts: max-plus-patch on the
// line owned-line owns, or on info.version of an openapi document. keep-branch
// and keep-trunk decide a conflict and never move a number, so a clean merge
// under them is left as git made it.
func (r Rule) lifts() bool {
	switch r.Strategy {
	case StrategyOwnedLine:
		return r.Rule == RuleMaxPlusPatch
	case StrategyOpenAPI:
		return r.Rule == "" || r.Rule == RuleMaxPlusPatch
	}
	return false
}

// liftPatterns is every path pattern a lifting rule claims.
func (c *Config) liftPatterns() []string {
	if c == nil {
		return nil
	}
	var patterns []string
	for _, r := range c.Conflicts {
		if r.lifts() {
			patterns = append(patterns, r.Paths...)
		}
	}
	return patterns
}

// shapeRefusal is the reason a lift gives a file whose owned lines do not
// pair up across the sides. The plan file recognises it: the advice for it
// is to check the lines, not to write a version.
const shapeRefusal = "the sides differ in the number of owned lines"

// versionValueRE says an owned line carries a version number of some kind —
// 1.2.3, 1.2.3-SNAPSHOT, 1.2.3.4 — rather than a placeholder such as
// ${git.build.version}, which the same regex claims and no bump ever moves.
var versionValueRE = regexp.MustCompile(`\d+\.\d+`)

// Lift rewrites each owned line both sides changed to the rule's answer over
// the merged line and trunk's. Lines are paired by position among the owned
// lines carrying a version: the file's shape is the same on every side when
// git merged it clean and both sides only bumped. A side with a different
// number of them is not that shape — one side added or removed a version
// line — and is refused, as the strategy refuses a block it cannot pair,
// rather than left to land on whatever git merged.
func (s OwnedLine) Lift(path string, base, trunk, branch, merged []byte) (*Lift, error) {
	if _, ok := s.Rule.(maxPlusPatch); !ok {
		return nil, nil
	}
	baseLines, trunkLines, branchLines := s.owned(base), s.owned(trunk), s.owned(branch)
	lines := strings.SplitAfter(string(merged), "\n")
	var mergedAt []int
	for i, l := range lines {
		if s.versioned(l) {
			mergedAt = append(mergedAt, i)
		}
	}
	n := len(mergedAt)
	if len(baseLines) != n || len(trunkLines) != n || len(branchLines) != n {
		return nil, Refuse(path, shapeRefusal)
	}
	if n == 0 {
		return nil, nil
	}
	var lift Lift
	for i, at := range mergedAt {
		if ownedValue(baseLines[i]) == ownedValue(branchLines[i]) || ownedValue(baseLines[i]) == ownedValue(trunkLines[i]) {
			continue
		}
		out, err := s.Rule.Apply(lines[at], trunkLines[i])
		if err != nil {
			return nil, Refuse(path, "%v", err)
		}
		if out == lines[at] {
			continue
		}
		lines[at] = out
		lift.From = append(lift.From, findSemver(trunkLines[i]))
		lift.To = append(lift.To, findSemver(out))
	}
	if len(lift.To) == 0 {
		return nil, nil
	}
	lift.Content = []byte(strings.Join(lines, ""))
	return &lift, nil
}

// owned is the lines of data matching the owned-line regex and carrying a
// version, in order.
func (s OwnedLine) owned(data []byte) []string {
	var out []string
	for _, l := range strings.SplitAfter(string(data), "\n") {
		if s.versioned(l) {
			out = append(out, l)
		}
	}
	return out
}

// versioned says line is an owned line carrying a version number.
func (s OwnedLine) versioned(line string) bool {
	return s.Line.MatchString(line) && versionValueRE.MatchString(line)
}

// ownedValue is what an owned line says: its version when it carries one,
// otherwise the whole line, so a line changed to something that is not a
// version still counts as changed.
func ownedValue(line string) string {
	if v := findSemver(line); v != "" {
		return v
	}
	return strings.TrimSpace(line)
}

// Lift rewrites info.version in place, touching no other byte of the
// document: the generated file is left exactly as the merge made it but for
// the one value.
func (s OpenAPI) Lift(path string, base, trunk, branch, merged []byte) (*Lift, error) {
	if s.Rule != "" && s.Rule != RuleMaxPlusPatch {
		return nil, nil
	}
	// A side whose info.version cannot be read as one plain literal is
	// refused, never compared as "": an unreadable base would make every
	// change look like a bump, and an unreadable merged document would let a
	// due lift pass in silence.
	baseV, _, baseOK := infoVersion(base)
	trunkV, _, trunkOK := infoVersion(trunk)
	branchV, _, branchOK := infoVersion(branch)
	mergedV, at, mergedOK := infoVersion(merged)
	if !baseOK || !trunkOK || !branchOK || !mergedOK {
		return nil, Refuse(path, "info.version is not one plain string literal on every side")
	}
	if baseV == branchV || baseV == trunkV {
		return nil, nil
	}
	version, err := s.version(path, mergedV, trunkV)
	if err != nil {
		return nil, err
	}
	if version == mergedV {
		return nil, nil
	}
	out := make([]byte, 0, len(merged)+len(version)-len(mergedV))
	out = append(out, merged[:at]...)
	out = append(out, mustRaw(version)...)
	out = append(out, merged[at+len(mustRaw(mergedV)):]...)
	return &Lift{Content: out, From: []string{trunkV}, To: []string{version}}, nil
}

// infoVersion finds info.version in a document and where its literal starts.
// ok is false when the document has none, or the literal is not the plain
// JSON encoding of the value (an escape in it), in which case there is no
// single span to rewrite.
func infoVersion(data []byte) (value string, at int, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	// One frame per open object or array; key says the next token in an
	// object is a key, since the decoder gives no other sign of which is
	// which. info is -1 once the info key is seen and its value is next, the
	// depth of the info object while inside it, and 0 otherwise.
	type frame struct{ object, key bool }
	var stack []frame
	info := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", 0, false
		}
		if d, isDelim := tok.(json.Delim); isDelim {
			switch d {
			case '{':
				stack = append(stack, frame{object: true, key: true})
				if info == -1 {
					info = len(stack)
				}
			case '[':
				stack = append(stack, frame{})
				if info == -1 {
					info = 0
				}
			default:
				if info == len(stack) {
					// The info object closed without a version.
					return "", 0, false
				}
				stack = stack[:len(stack)-1]
				if n := len(stack); n > 0 && stack[n-1].object {
					stack[n-1].key = true
				}
			}
			continue
		}
		if len(stack) == 0 {
			return "", 0, false
		}
		top := &stack[len(stack)-1]
		if !top.object || !top.key {
			// A scalar value; the next token in this object is a key.
			if top.object {
				top.key = true
			}
			if info == -1 {
				info = 0
			}
			continue
		}
		top.key = false
		key, _ := tok.(string)
		switch {
		case len(stack) == 1 && key == "info":
			info = -1
		case len(stack) == 2 && info == 2 && key == "version":
			v, err := dec.Token()
			if err != nil {
				return "", 0, false
			}
			s, isString := v.(string)
			if !isString {
				return "", 0, false
			}
			end := int(dec.InputOffset())
			lit := mustRaw(s)
			if end < len(lit) || !bytes.Equal(data[end-len(lit):end], lit) {
				return "", 0, false
			}
			return s, end - len(lit), true
		}
	}
}

// liftSides names the trees one lift reads at one pick: the pick's parent,
// what the pick is applied on, the pick, and where git left the merged
// result, as a cat-file prefix: "<tree>:" for a tree, ":0:" for the index.
type liftSides struct {
	base, trunk, branch string
	merged              string
}

// lifted is one file a lift answered: the outcome as a stop reports it, and
// the bytes to write when it lifted.
type lifted struct {
	Outcome FileOutcome
	Content []byte
}

// liftAt asks each lifting rule about the files the pick changed that git
// merged clean, and returns what they answered, in path order. skip is the
// paths the stop already put to a strategy: a conflict is the strategy's,
// never a lift's. root and onto are what FromRule needs.
func liftAt(dir string, cfg *Config, s liftSides, skip map[string]bool, root, onto string) ([]lifted, error) {
	patterns := cfg.liftPatterns()
	if len(patterns) == 0 {
		return nil, nil
	}
	changed, err := changedBlobs(dir, s.base, s.branch)
	if err != nil {
		return nil, err
	}
	var paths []string
	for p := range changed {
		if !skip[p] && matchesAny(patterns, p) {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil, nil
	}
	sort.Strings(paths)
	// A file trunk did not change since the pick's parent carries base's
	// value on trunk's side, so nothing is lifted there.
	onTrunk, err := changedBlobs(dir, s.base, s.trunk, paths...)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, ok := onTrunk[p]; ok {
			names = append(names, s.merged+p)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	mergedOIDs, err := resolveNames(dir, names)
	if err != nil {
		return nil, err
	}
	var want []string
	seen := map[string]bool{}
	add := func(oid string) {
		if oid != "" && !seen[oid] {
			seen[oid] = true
			want = append(want, oid)
		}
	}
	for _, p := range paths {
		if _, ok := onTrunk[p]; !ok {
			continue
		}
		add(changed[p][0])
		add(changed[p][1])
		add(onTrunk[p][1])
		add(mergedOIDs[s.merged+p])
	}
	blobs, err := catFileBatch(dir, want)
	if err != nil {
		return nil, err
	}
	var out []lifted
	for _, p := range paths {
		merged, ok := mergedOIDs[s.merged+p]
		if !ok {
			continue
		}
		rule, ok := cfg.RuleFor(p)
		if !ok || !rule.lifts() {
			continue
		}
		strategy, err := FromRule(rule, root, onto)
		if err != nil {
			return out, fmt.Errorf("%s: %w", p, err)
		}
		l, ok := strategy.(lifter)
		if !ok {
			continue
		}
		res, err := l.Lift(p, blobs[changed[p][0]], blobs[onTrunk[p][1]], blobs[changed[p][1]], blobs[merged])
		outcome := FileOutcome{Path: p, Strategy: rule.Strategy}
		switch {
		case err != nil:
			ref := refusalOf(err)
			if ref == nil {
				return out, fmt.Errorf("%s: %w", p, err)
			}
			outcome.Lifted, outcome.Note = true, ref.Reason
			out = append(out, lifted{Outcome: outcome})
		case res != nil:
			outcome.Resolved, outcome.Lifted, outcome.Note = true, true, res.note()
			out = append(out, lifted{Outcome: outcome, Content: res.Content})
		}
	}
	return out, nil
}

// changedBlobs is the regular files modified in place between two trees, as
// path → (from, to) blob ids, limited to paths when any are given. An added,
// deleted, renamed or non-regular entry is not a line both sides could have
// bumped, and is left out.
func changedBlobs(dir, from, to string, paths ...string) (map[string][2]string, error) {
	args := []string{"--literal-pathspecs", "diff-tree", "-r", "-z", "--no-commit-id", "--no-renames", "--diff-filter=M", from, to, "--"}
	args = append(args, paths...)
	out, err := gitEnv(dir, nil, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("diff-tree %s %s: %w", from, to, err)
	}
	changed := map[string][2]string{}
	records := strings.Split(out, "\x00")
	for i := 0; i+1 < len(records); i += 2 {
		f := strings.Fields(strings.TrimPrefix(records[i], ":"))
		if len(f) != 5 || !regularMode(f[0]) || !regularMode(f[1]) {
			continue
		}
		changed[records[i+1]] = [2]string{f[2], f[3]}
	}
	return changed, nil
}

func regularMode(mode string) bool { return mode == "100644" || mode == "100755" }

// resolveNames resolves object names ("<tree>:<path>", ":0:<path>") to blob
// ids with one git, leaving out the ones that name nothing.
func resolveNames(dir string, names []string) (map[string]string, error) {
	out, err := runGit(dir, nil, strings.NewReader(strings.Join(names, "\n")+"\n"), "cat-file", "--batch-check")
	if err != nil {
		return nil, fmt.Errorf("git cat-file --batch-check: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(names) {
		return nil, fmt.Errorf("git cat-file --batch-check: %d answers for %d names", len(lines), len(names))
	}
	oids := map[string]string{}
	for i, line := range lines {
		f := strings.Fields(line)
		if len(f) == 3 && f[1] == "blob" {
			oids[names[i]] = f[0]
		}
	}
	return oids, nil
}

// commitPaths is one commit of a log with the paths it changed, and whether
// the other side of a symmetric range already carries its patch (OnTrunk:
// git's cherry mark), which a rebase drops before it is ever applied.
type commitPaths struct {
	SHA     string
	Subject string
	Paths   []string
	OnTrunk bool
}

// logWithPaths lists commits with their subjects, cherry marks and changed
// paths, one git for the whole range; args select and order them as `git
// log` would. The mark is only meaningful for a symmetric range.
func logWithPaths(dir string, args ...string) ([]commitPaths, error) {
	full := append([]string{"log", "--cherry-mark", "--format=%x01%m%H%x00%s", "--name-only", "-z", "--no-renames"}, args...)
	out, err := runGit(dir, nil, nil, full...)
	if err != nil {
		return nil, err
	}
	var commits []commitPaths
	for _, rec := range strings.Split(string(out), "\x01") {
		if rec == "" {
			continue
		}
		// With -z the format ends in NUL, and a newline then opens the paths.
		sha, rest, _ := strings.Cut(rec, "\x00")
		subject, rest, _ := strings.Cut(rest, "\x00")
		c := commitPaths{SHA: sha, Subject: subject}
		if len(sha) > 40 && strings.ContainsRune("<>=-", rune(sha[0])) {
			c.SHA, c.OnTrunk = sha[1:], sha[0] == '='
		}
		for _, p := range strings.Split(strings.TrimPrefix(rest, "\n"), "\x00") {
			if p != "" {
				c.Paths = append(c.Paths, p)
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// touchesAny reports whether any of paths matches any of patterns.
func touchesAny(patterns, paths []string) bool {
	for _, p := range paths {
		if matchesAny(patterns, p) {
			return true
		}
	}
	return false
}

// sequenceEditor writes the script a run hands git as its sequence editor:
// every pick of a commit in marks is preceded by a break and made an edit,
// so the run sees HEAD before the pick is applied and stops after it, and
// every pick of a commit in drops is taken out, which is what git itself
// does with a commit trunk already carries when the list is not asked to
// keep them. The break is what tells a pick git dropped as already upstream
// from one it committed: at the edit stop HEAD either moved or did not. The
// list names each commit by the abbreviation the repository is configured
// for, so a pick is matched by its id being a prefix of the full one; the
// abbreviations of a list are unique in the repository, and so is the
// match. Every other line is left as git wrote it. POSIX sh only, since git
// runs it through the shell wherever the run is.
func sequenceEditor(w io.Writer, marks, drops []string) {
	fmt.Fprintln(w, "#!/bin/sh")
	fmt.Fprintln(w, "# wt sync: stop before and after the picks that may need a version lift,")
	fmt.Fprintln(w, "# and leave out the picks trunk already carries")
	fmt.Fprintf(w, "marks='%s'\n", strings.Join(marks, " "))
	fmt.Fprintf(w, "drops='%s'\n", strings.Join(drops, " "))
	fmt.Fprintln(w, `while IFS= read -r line || [ -n "$line" ]; do`)
	fmt.Fprintln(w, `  case "$line" in`)
	fmt.Fprintln(w, `    "pick "*)`)
	fmt.Fprintln(w, `      id=${line#pick }; id=${id%% *}; out=$line`)
	fmt.Fprintln(w, `      for m in $marks; do case "$m" in "$id"*) printf 'break\n'; out="edit ${line#pick }"; break ;; esac; done`)
	fmt.Fprintln(w, `      for m in $drops; do case "$m" in "$id"*) out=""; break ;; esac; done`)
	fmt.Fprintf(w, "%s\n", `      [ -n "$out" ] && printf '%s\n' "$out" ;;`)
	fmt.Fprintf(w, "%s\n", `    *) printf '%s\n' "$line" ;;`)
	fmt.Fprintln(w, `  esac`)
	fmt.Fprintln(w, `done < "$1" > "$1.wt" && mv "$1.wt" "$1"`)
}

// plainEditor writes the sequence editor that takes a run's stops out of the
// list again: every break dropped, every edit a pick. A run's list holds no
// edit or break of anybody else's, so nothing else is touched.
func plainEditor(w io.Writer) {
	fmt.Fprintln(w, "#!/bin/sh")
	fmt.Fprintln(w, "# wt sync: the list a plain rebase would have, for a person to continue")
	fmt.Fprintln(w, `while IFS= read -r line || [ -n "$line" ]; do`)
	fmt.Fprintln(w, `  case "$line" in`)
	fmt.Fprintln(w, `    break) ;;`)
	fmt.Fprintf(w, "%s\n", "    \"edit \"*) printf '%s\\n' \"pick ${line#edit }\" ;;")
	fmt.Fprintf(w, "%s\n", "    *) printf '%s\\n' \"$line\" ;;")
	fmt.Fprintln(w, `  esac`)
	fmt.Fprintln(w, `done < "$1" > "$1.wt" && mv "$1.wt" "$1"`)
}
