package commands

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
)

// SweptPrefix is where wt refs sweep pins what it sweeps, one folder per
// run, beside refs/wt-quarantine/ and refs/wt-sync/.
const SweptPrefix = "refs/wt-swept/"

// The kinds of ref wt refs sweeps, for --json.
const (
	RefBranch       = "branch"
	RefTag          = "tag"
	RefRemoteBranch = "remoteBranch"
	RefRemoteTag    = "remoteTag"
)

// refSpaces is the folder under a run each kind is pinned in.
var refSpaces = map[string]string{
	RefBranch: "heads", RefTag: "tags", RefRemoteBranch: "remote-heads", RefRemoteTag: "remote-tags",
}

// remoteKind reports that the ref is on origin rather than here.
func remoteKind(kind string) bool { return kind == RefRemoteBranch || kind == RefRemoteTag }

// refOf is the ref a kind and short name stand for, on the side it lives:
// refs/heads/<name> for a branch here and on origin alike.
func refOf(kind, name string) string {
	if kind == RefTag || kind == RefRemoteTag {
		return "refs/tags/" + name
	}
	return "refs/heads/" + name
}

// refID is how --json and --only name a ref: the ref itself here, and
// origin:<ref> on origin.
func refID(kind, name string) string {
	if remoteKind(kind) {
		return "origin:" + refOf(kind, name)
	}
	return refOf(kind, name)
}

// pinOf is where a run pins one ref.
func pinOf(runID, kind, name string) string {
	return SweptPrefix + runID + "/" + refSpaces[kind] + "/" + name
}

// runIDPattern is a run id: the UTC second the run began and four to 32 hex
// digits, 20260930T091500Z-3f2a. wt mints four; a caller naming its own run
// with --run-id may use more.
var runIDPattern = regexp.MustCompile(`^([0-9]{8}T[0-9]{6}Z)-[0-9a-f]{4,32}$`)

// Version is the wt that is running, as a run's meta records it. main sets it.
var Version = "dev"

// newRunID names a sweep that is about to move something.
func newRunID(now time.Time) string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b)
}

// runTime is when a run began, read from its id.
func runTime(runID string) (time.Time, bool) {
	m := runIDPattern.FindStringSubmatch(runID)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102T150405Z", m[1])
	return t, err == nil
}

// sweptRef is one pin: the ref it holds, as the pin's path names it, and
// the object it holds.
type sweptRef struct {
	RunID, Kind, Name, Pin, Object string
}

func (s sweptRef) ID() string  { return refID(s.Kind, s.Name) }
func (s sweptRef) Ref() string { return refOf(s.Kind, s.Name) }

// parsePin reads a pin's path: its run, kind and short name. False for a
// ref under refs/wt-swept/ that wt did not write.
func parsePin(pin string) (sweptRef, bool) {
	rest, ok := strings.CutPrefix(pin, SweptPrefix)
	if !ok {
		return sweptRef{}, false
	}
	runID, rest, ok := strings.Cut(rest, "/")
	if !ok || !runIDPattern.MatchString(runID) {
		return sweptRef{}, false
	}
	space, name, ok := strings.Cut(rest, "/")
	if !ok || name == "" {
		return sweptRef{}, false
	}
	for kind, s := range refSpaces {
		if s == space {
			return sweptRef{RunID: runID, Kind: kind, Name: name, Pin: pin}, true
		}
	}
	return sweptRef{}, false
}

// sweptRun is one run's pins, and its meta when it has one.
type sweptRun struct {
	RunID string
	// At is when the run began: its meta's sweptAt, else the time in its id.
	At   time.Time
	Refs []sweptRef
	// Meta is what the run wrote before it moved anything; nil when it has
	// none or it cannot be read. MetaOID is the blob, "" when there is none.
	Meta    *runMeta
	MetaOID string
}

// readSwept is every run with pins or a meta left, oldest first, and each
// run's pins in ref order.
func readSwept(mainRoot string) ([]sweptRun, error) {
	lines, err := git.Lines(mainRoot, "for-each-ref", "--format=%(refname)%00%(objectname)", SweptPrefix)
	if err != nil {
		return nil, fmt.Errorf("could not read the pins under %s: %w", SweptPrefix, err)
	}
	byRun := map[string]*sweptRun{}
	var runs []*sweptRun
	run := func(id string) *sweptRun {
		r := byRun[id]
		if r == nil {
			at, _ := runTime(id)
			r = &sweptRun{RunID: id, At: at}
			byRun[id] = r
			runs = append(runs, r)
		}
		return r
	}
	for _, line := range lines {
		ref, oid, _ := strings.Cut(line, "\x00")
		if id, ok := metaRun(ref); ok {
			run(id).MetaOID = oid
			continue
		}
		s, ok := parsePin(ref)
		if !ok {
			continue
		}
		s.Object = oid
		r := run(s.RunID)
		r.Refs = append(r.Refs, s)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].RunID < runs[j].RunID })
	out := make([]sweptRun, 0, len(runs))
	for _, r := range runs {
		if r.MetaOID != "" {
			if m, err := readMeta(mainRoot, r.MetaOID); err == nil {
				r.Meta = m
				if m.SweptAt > 0 {
					r.At = time.Unix(m.SweptAt, 0).UTC()
				}
			}
		}
		out = append(out, *r)
	}
	return out, nil
}

// metaRef is where a run keeps its meta.
func metaRef(runID string) string { return SweptPrefix + runID + "/meta" }

// metaRun is the run a meta ref belongs to.
func metaRun(ref string) (string, bool) {
	rest, ok := strings.CutPrefix(ref, SweptPrefix)
	if !ok {
		return "", false
	}
	id, name, ok := strings.Cut(rest, "/")
	return id, ok && name == "meta" && runIDPattern.MatchString(id)
}

// Endpoint is the one URL a run with --remote talks to origin through, as
// git resolves it for fetching and pushing alike, with its userinfo removed,
// and a digest of the whole URL.
type Endpoint struct {
	URL    string `json:"url"`
	Digest string `json:"digest"`
	// raw is the effective URL itself, which every remote call is made
	// through; it is never printed or recorded.
	raw string
}

// errRemoteAmbiguous is an origin that does not fetch from and push to one
// and the same URL.
var errRemoteAmbiguous = errors.New("origin does not fetch from and push to one and the same URL, so what " +
	"--remote deletes there could not be told to have gone back to the same place")

// errRemoteCredentials is an origin URL that carries a secret. It names only
// the remote.
var errRemoteCredentials = errors.New("origin's URL contains a password or token; wt won't use it for " +
	"--remote. Store the credential with a credential helper instead")

// resolveEndpoint reads origin's effective fetch and push URLs, insteadOf
// and pushInsteadOf applied, and requires them to be one URL with no
// secret in it.
func resolveEndpoint(mainRoot string) (*Endpoint, error) {
	fetch, err := git.Lines(mainRoot, "remote", "get-url", "--all", "origin")
	if err != nil {
		return nil, fmt.Errorf("could not read origin's URLs: %s", git.Reason(err))
	}
	push, err := git.Lines(mainRoot, "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return nil, fmt.Errorf("could not read origin's push URLs: %s", git.Reason(err))
	}
	if len(fetch) != 1 || len(push) != 1 || fetch[0] != push[0] {
		return nil, fmt.Errorf("%w (%d fetch and %d push URLs)", errRemoteAmbiguous, len(fetch), len(push))
	}
	u := fetch[0]
	if hasCredentials(u) {
		return nil, errRemoteCredentials
	}
	sum := sha256.Sum256([]byte(u))
	return &Endpoint{URL: redacted(u), Digest: "sha256:" + hex.EncodeToString(sum[:]), raw: u}, nil
}

// runMeta is what a run writes before it moves anything, as a blob at
// refs/wt-swept/<runId>/meta: a blob, so no history graph shows it.
type runMeta struct {
	RunID     string    `json:"runId"`
	Repo      string    `json:"repo"`
	SweptAt   int64     `json:"sweptAt"`
	Endpoint  *Endpoint `json:"endpoint"`
	WtVersion string    `json:"wtVersion"`
}

// writeMeta creates a run's meta, only where nothing is.
func writeMeta(mainRoot string, m runMeta) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	blob, err := git.Exec(git.Opts{Dir: mainRoot, Stdin: bytes.NewReader(data)}, "hash-object", "-w", "--stdin")
	if err != nil {
		return fmt.Errorf("could not write the run's meta: %s", git.Reason(err))
	}
	if err := updateRefs(mainRoot, "wt refs sweep", "create "+metaRef(m.RunID)+" "+strings.TrimSpace(string(blob))); err != nil {
		return fmt.Errorf("could not write the run's meta: %s", git.Reason(err))
	}
	return nil
}

// readMeta reads a meta blob.
func readMeta(mainRoot, oid string) (*runMeta, error) {
	out, err := git.Exec(git.Opts{Dir: mainRoot}, "cat-file", "blob", oid)
	if err != nil {
		return nil, err
	}
	var m runMeta
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// runTaken reports that something is under refs/wt-swept/<runId>/ already.
func runTaken(mainRoot, runID string) (bool, error) {
	out, err := git.Run(mainRoot, "for-each-ref", "--count=1", "--format=%(refname)", SweptPrefix+runID+"/")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// refValue is what a ref holds now, unpeeled: the tag object of an
// annotated tag. ok is false when there is no such ref.
func refValue(mainRoot, ref string) (string, bool, error) {
	out, err := git.Exec(git.Opts{Dir: mainRoot}, "rev-parse", "--verify", "--quiet", ref)
	if code, aerr := git.Answer(err, 1); aerr != nil {
		return "", false, aerr
	} else if code == 1 {
		return "", false, nil
	}
	v := strings.TrimSpace(string(out))
	return v, v != "", nil
}

// updateRefs runs one update-ref transaction: every line goes, or none does.
func updateRefs(mainRoot, message string, lines ...string) error {
	in := "start\n" + strings.Join(lines, "\n") + "\nprepare\ncommit\n"
	_, err := git.Exec(git.Opts{Dir: mainRoot, Stdin: strings.NewReader(in)},
		"update-ref", "--no-deref", "-m", message, "--stdin")
	return err
}

// deleteRefAt deletes ref only while it holds oid.
func deleteRefAt(mainRoot, ref, oid string) error {
	_, err := git.Run(mainRoot, "update-ref", "--no-deref", "-m", "wt refs", "-d", ref, oid)
	return err
}

// remoteRefs is what origin has under refs/heads/ and refs/tags/, from one
// ls-remote: each ref's own value, and the commit an annotated tag peels to.
type remoteRefs struct {
	Values map[string]string
	Peeled map[string]string
}

// readRemoteRefs asks origin, at url, for its branches and tags.
func readRemoteRefs(mainRoot, url string) (remoteRefs, error) {
	out, err := git.RunTimeout(mainRoot, networkTimeout, "ls-remote", url)
	if err != nil {
		return remoteRefs{}, fmt.Errorf("could not list origin's refs: %s", git.Reason(err))
	}
	r := remoteRefs{Values: map[string]string{}, Peeled: map[string]string{}}
	for _, line := range strings.Split(out, "\n") {
		oid, ref, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if !strings.HasPrefix(ref, "refs/heads/") && !strings.HasPrefix(ref, "refs/tags/") {
			continue
		}
		if base, peeled := strings.CutSuffix(ref, "^{}"); peeled {
			r.Peeled[base] = oid
			continue
		}
		r.Values[ref] = oid
	}
	return r, nil
}

// remoteValue is what origin, at url, holds at one ref now; ok is false
// when it has no such ref.
func remoteValue(mainRoot, url, ref string, timeout time.Duration) (string, bool, error) {
	out, err := git.RunTimeout(mainRoot, timeout, "ls-remote", url, ref)
	if err != nil {
		return "", false, fmt.Errorf("could not ask origin about %s: %s", ref, git.Reason(err))
	}
	for _, line := range strings.Split(out, "\n") {
		if oid, name, ok := strings.Cut(line, "\t"); ok && name == ref {
			return oid, true, nil
		}
	}
	return "", false, nil
}

// objectInfo is what wt reads out of a commit or a tag object.
type objectInfo struct {
	Type    string // commit, tag, tree, blob; "" when the object is not here
	Target  string // a tag's object
	Time    int64  // a commit's committer time, a tag's tagger time; 0 when none
	Subject string
}

// readObjects reads each object in one cat-file --batch: its type, what a
// tag points at, its date and its subject. An object that is not here comes
// back with no type.
func readObjects(mainRoot string, oids []string) (map[string]objectInfo, error) {
	out := map[string]objectInfo{}
	var in strings.Builder
	var want []string
	for _, oid := range oids {
		if _, seen := out[oid]; seen || oid == "" {
			continue
		}
		out[oid] = objectInfo{}
		want = append(want, oid)
		in.WriteString(oid + "\n")
	}
	if len(want) == 0 {
		return out, nil
	}
	raw, err := git.Exec(git.Opts{Dir: mainRoot, Stdin: strings.NewReader(in.String())}, "cat-file", "--batch")
	if err != nil {
		return nil, fmt.Errorf("could not read objects: %s", git.Reason(err))
	}
	rd := bufio.NewReader(bytes.NewReader(raw))
	for range want {
		header, err := rd.ReadString('\n')
		if err != nil {
			return nil, errors.New("could not read objects: cat-file stopped early")
		}
		f := strings.Fields(header)
		if len(f) == 2 && f[1] == "missing" {
			continue
		}
		if len(f) != 3 {
			return nil, fmt.Errorf("could not read objects: cat-file said %q", strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, fmt.Errorf("could not read objects: cat-file said %q", strings.TrimSpace(header))
		}
		body := make([]byte, size+1)
		if _, err := io.ReadFull(rd, body); err != nil {
			return nil, errors.New("could not read objects: cat-file stopped early")
		}
		out[f[0]] = parseObject(f[1], body[:size])
	}
	return out, nil
}

// parseObject reads the headers of a commit or a tag, and the subject.
func parseObject(typ string, body []byte) objectInfo {
	info := objectInfo{Type: typ}
	if typ != "commit" && typ != "tag" {
		return info
	}
	head, msg, _ := strings.Cut(string(body), "\n\n")
	for _, line := range strings.Split(head, "\n") {
		key, rest, _ := strings.Cut(line, " ")
		switch {
		case key == "object" && typ == "tag":
			info.Target = rest
		case (key == "committer" && typ == "commit") || (key == "tagger" && typ == "tag"):
			info.Time = identTime(rest)
		}
	}
	for _, line := range strings.Split(msg, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			info.Subject = line
			break
		}
	}
	return info
}

// identTime is the time in an identity line: "Name <email> 1759220000 +0200".
func identTime(ident string) int64 {
	i := strings.LastIndex(ident, ">")
	if i < 0 {
		return 0
	}
	f := strings.Fields(ident[i+1:])
	if len(f) == 0 {
		return 0
	}
	t, _ := strconv.ParseInt(f[0], 10, 64)
	return t
}

// peelCommit follows tags from oid to the commit they end at, reading what
// it has to; "" when they end at something else or at nothing here.
func peelCommit(mainRoot, oid string, known map[string]objectInfo) string {
	for range 10 {
		info, ok := known[oid]
		if !ok {
			more, err := readObjects(mainRoot, []string{oid})
			if err != nil {
				return ""
			}
			info = more[oid]
			known[oid] = info
		}
		switch info.Type {
		case "commit":
			return oid
		case "tag":
			oid = info.Target
		default:
			return ""
		}
	}
	return ""
}

// uniqueCommits reads, in one rev-list pass, how many commits each tip has
// that none of keep reaches: 0 for a tip keep contains.
func uniqueCommits(mainRoot string, tips, keep []string) (map[string]int, error) {
	var in strings.Builder
	for _, t := range tips {
		in.WriteString(t + "\n")
	}
	for _, k := range keep {
		in.WriteString("^" + k + "\n")
	}
	raw, err := git.Exec(git.Opts{Dir: mainRoot, Stdin: strings.NewReader(in.String())},
		"rev-list", "--parents", "--stdin")
	if err != nil {
		return nil, fmt.Errorf("could not read what each backup holds: %s", git.Reason(err))
	}
	parents := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 {
			parents[f[0]] = f[1:]
		}
	}
	out := map[string]int{}
	for _, tip := range tips {
		if _, done := out[tip]; done {
			continue
		}
		seen := map[string]bool{}
		stack := []string{tip}
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			ps, unique := parents[c]
			if !unique || seen[c] {
				continue
			}
			seen[c] = true
			stack = append(stack, ps...)
		}
		out[tip] = len(seen)
	}
	return out, nil
}

// refDates reads when each branch was created, from the first entry of its
// reflog. A branch with no reflog is left out.
type refDates struct {
	mainRoot, logs string
	reftable       bool
	once           sync.Once
}

func (d *refDates) init() {
	d.once.Do(func() {
		if dir, err := git.Run(d.mainRoot, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
			d.logs = filepath.Join(dir, "logs")
		}
		if format, err := git.Run(d.mainRoot, "rev-parse", "--show-ref-format"); err == nil {
			d.reftable = format == "reftable"
		}
	})
}

// created is the time of the first entry of ref's reflog.
func (d *refDates) created(ref string) (int64, bool) {
	d.init()
	if d.reftable {
		lines, err := git.Lines(d.mainRoot, "reflog", "show", "--date=unix", "--format=%gd", ref, "--")
		if err != nil || len(lines) == 0 {
			return 0, false
		}
		last := lines[len(lines)-1]
		i, j := strings.LastIndex(last, "@{"), strings.LastIndex(last, "}")
		if i < 0 || j <= i+2 {
			return 0, false
		}
		t, err := strconv.ParseInt(last[i+2:j], 10, 64)
		return t, err == nil
	}
	if d.logs == "" {
		return 0, false
	}
	f, err := os.Open(filepath.Join(d.logs, filepath.FromSlash(ref)))
	if err != nil {
		return 0, false
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && line == "" {
		return 0, false
	}
	head, _, _ := strings.Cut(line, "\t")
	t := identTime(head)
	return t, t > 0
}

// beginRun writes a run's meta, the first thing a run does, so a run stopped
// anywhere after is found by its id. Its create is a compare-and-swap: a run
// id taken meanwhile refuses here.
func beginRun(mainRoot, runID string, at time.Time, endpoint *Endpoint) error {
	gitDir, err := git.Run(mainRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("could not write the run's meta: %s", git.Reason(err))
	}
	return writeMeta(mainRoot, runMeta{RunID: runID, Repo: gitDir, SweptAt: at.Unix(), Endpoint: endpoint,
		WtVersion: Version})
}

// afterBranchMoved runs, when set, between moveBranch's transaction and its
// config removal. Tests set it to deliver a signal there.
var afterBranchMoved func(name string)

// moveBranch pins branch name at pin and deletes it, in one transaction and
// only at tip, then drops its config, as git branch -D does. A branch a
// worktree uses is refused with a repo.BranchInUseError.
func moveBranch(ctx *Context, name, tip, pin, message string) error {
	users, err := ctx.Repo.BranchUsers()
	if err != nil {
		return err
	}
	if use, ok := users[name]; ok {
		return &repo.BranchInUseError{Path: use.Path, By: use.By}
	}
	if err := updateRefs(ctx.Repo.MainRoot, message, "create "+pin+" "+tip, "delete refs/heads/"+name+" "+tip); err != nil {
		return err
	}
	if afterBranchMoved != nil {
		afterBranchMoved(name)
	}
	// A branch with no config has no section to remove; that is not a failure.
	_, _ = git.Run(ctx.Repo.MainRoot, "config", "--remove-section", "branch."+name)
	return nil
}
