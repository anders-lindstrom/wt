package wtsync

import (
	"cmp"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// OwnState is how a branch stands against its own remote: the
// remote-tracking ref a push of it would replace.
type OwnState string

// The states. The zero value reads as OwnNone.
const (
	OwnNone     OwnState = "none"     // nothing to compare with
	OwnGone     OwnState = "gone"     // the branch names a ref the remote no longer has
	OwnUnknown  OwnState = "unknown"  // where a push lands cannot be read from a tracking ref
	OwnInSync   OwnState = "inSync"   // the same commit
	OwnAhead    OwnState = "ahead"    // only the branch has commits of its own
	OwnBehind   OwnState = "behind"   // only the remote has
	OwnRebased  OwnState = "rebased"  // both have, and the remote's are old versions of the branch's
	OwnDiverged OwnState = "diverged" // both have, and the remote's are not
)

// Why a branch's own remote keeps a run off it, as --json names it.
const (
	OwnBlockDiverged  = "diverged"
	OwnBlockOperation = "operation"
	OwnBlockDirty     = "dirty"
	OwnBlockSession   = "session"
)

// OwnRemote is one branch against its own remote.
type OwnRemote struct {
	// Ref is the remote-tracking ref as a person says it, "origin/feat/x";
	// "" for none and unknown.
	Ref   string
	State OwnState
	// Local is the branch's tip as compared; Commit the remote's, "" for
	// none, gone and unknown.
	Local, Commit string
	// Ahead counts the commits only the branch has, Behind the ones only
	// the remote has.
	Ahead, Behind int
	// Fetched says the remote side comes from this command's fetch rather
	// than the last one.
	Fetched bool
	// Why is what a person is told: for unknown, why it cannot be read; for
	// a branch that is behind and blocked, what is in the way.
	Why string
	// Blocks is why a run may not go on, "" when it may: diverged, or behind
	// and not safe to fast-forward.
	Blocks string
	// Allowed is a diverged branch a run was told to rebase as it stands.
	Allowed bool
	// FetchFailed is why this command could not fetch the branch's own
	// remote, "" when it could or did not try: the rest is then as last
	// fetched, and a run does not go on over what it could not look at.
	FetchFailed string
	// Elsewhere is a diverged branch every commit of which the remote has in
	// another form: rebased on another machine and pushed, with this
	// checkout left on the old versions. It changes what a person is told,
	// not what a run does.
	Elsewhere bool
}

// Compared reports a state that has a remote commit to count against.
func (o OwnRemote) Compared() bool {
	switch o.State {
	case OwnInSync, OwnAhead, OwnBehind, OwnRebased, OwnDiverged:
		return true
	}
	return false
}

// Refuses reports that a run must leave the branch alone.
func (o OwnRemote) Refuses() bool { return o.FetchFailed != "" || (o.Blocks != "" && !o.Allowed) }

// FastForwards reports a branch a run moves to its remote's commit before
// rebasing it.
func (o OwnRemote) FastForwards() bool {
	return o.State == OwnBehind && o.Blocks == "" && o.FetchFailed == ""
}

// Refusal is the refusal in words.
func (o OwnRemote) Refusal() string {
	if o.FetchFailed != "" {
		return "its own remote could not be fetched (" + o.FetchFailed + "), so it is not known how the branch stands against it"
	}
	if o.State == OwnDiverged && o.Elsewhere {
		return fmt.Sprintf("%s has every commit of this branch in another form, on %d commit%s this branch never had: "+
			"it looks rebased elsewhere and pushed. git reset --hard %s takes the remote's, or --allow-diverged rebases this copy as it stands",
			o.Ref, o.Behind, pluralPlan(o.Behind), o.Ref)
	}
	if o.State == OwnDiverged {
		return fmt.Sprintf("%s has %d commit%s this branch never had: pull %s in, or --allow-diverged rebases it as it stands",
			o.Ref, o.Behind, pluralPlan(o.Behind), themOrIt(o.Behind))
	}
	return fmt.Sprintf("%d behind %s and cannot be fast-forwarded: %s", o.Behind, o.Ref, o.Why)
}

func themOrIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// OwnBlocks is why a run may not go on with a branch standing as o does, as
// a code and in words: diverged, or behind in a checkout a fast-forward is
// not safe in. dirty is tracked changes, the dirt that refuses a rebase too;
// an untracked file blocks neither.
func OwnBlocks(o OwnRemote, path string, dirty bool, sessions Sessions) (code, why string) {
	switch o.State {
	case OwnDiverged:
		return OwnBlockDiverged, ""
	case OwnBehind:
	default:
		return "", ""
	}
	// Read from the git dir, so a rebase is among them.
	switch op, err := repo.OperationInProgress(path); {
	case err != nil:
		return OwnBlockOperation, "cannot read " + path + ": " + err.Error()
	case op != "":
		return OwnBlockOperation, "a " + op + " is in progress"
	case dirty:
		return OwnBlockDirty, "tracked changes in the worktree"
	}
	if busy := sessions.Busy(); len(busy) > 0 {
		return OwnBlockSession, "an agent session is busy in it"
	}
	return "", ""
}

// Own is a reading of a repository's branches and remotes: enough to say,
// for any branch, which remote-tracking ref a push would replace and how the
// branch stands against it. Nothing here reaches a network; a caller that
// fetches hands the result to Fetched and reads again with Refresh.
type Own struct {
	mainRoot string
	trunkRef string
	remotes  map[string]*ownRemoteConfig
	heads    map[string]ownHead
	tracking map[string]string
	// asked is the tracking refs this command's fetch asked a remote for,
	// and seen the ones the remote has.
	asked, seen map[string]bool
	// failed is why the fetch of a tracking ref did not go through.
	failed map[string]string
	// broken is why nothing could be read, for ReadOwnOrUnknown.
	broken string
	// pushDefault is remote.pushDefault; pushRemote and branchRemote are
	// branch.<name>.pushRemote and branch.<name>.remote, by branch: the
	// remote git pushes a branch to, in the order git asks them.
	pushDefault              string
	pushRemote, branchRemote map[string]string
	// states is what State answered, until the next Refresh.
	states map[string]OwnRemote
}

type ownRemoteConfig struct {
	// The URLs as git uses them, insteadOf and pushInsteadOf applied; read
	// once, when a branch pushes to the remote.
	urlsRead            bool
	fetchURLs, pushURLs []string
	mirror              bool
	// standard is the refspec a clone writes, refs/heads/* to
	// refs/remotes/<name>/*: the only layout whose refs can be asked for by
	// name without writing one the configuration does not map.
	standard bool
}

type ownHead struct{ tip, push, upstream string }

// ReadOwn reads the branches, the remote-tracking refs and the remotes of
// the repository at mainRoot. trunk is trunk's name.
func ReadOwn(mainRoot, trunk string) (*Own, error) {
	o := &Own{mainRoot: mainRoot, trunkRef: "refs/remotes/origin/" + trunk,
		remotes: map[string]*ownRemoteConfig{}, asked: map[string]bool{}, seen: map[string]bool{}, failed: map[string]string{},
		pushRemote: map[string]string{}, branchRemote: map[string]string{}}
	remote := func(name string) *ownRemoteConfig {
		if o.remotes[name] == nil {
			o.remotes[name] = &ownRemoteConfig{}
		}
		return o.remotes[name]
	}
	// git prints the section and the variable in lower case and leaves the
	// name between them as it is.
	out, _, err := gitEnvAllow(mainRoot, nil, nil, 1, "config", "--get-regexp",
		`^(remote\..*\.(url|fetch|mirror)|remote\.pushdefault|branch\..*\.(pushremote|remote))$`)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		key, value, _ := strings.Cut(line, " ")
		if name, ok := strings.CutPrefix(key, "branch."); ok {
			if b, ok := strings.CutSuffix(name, ".pushremote"); ok {
				o.pushRemote[b] = value
			} else if b, ok := strings.CutSuffix(name, ".remote"); ok {
				o.branchRemote[b] = value
			}
			continue
		}
		if key == "remote.pushdefault" {
			o.pushDefault = value
			continue
		}
		key = strings.TrimPrefix(key, "remote.")
		switch {
		case strings.HasSuffix(key, ".url"):
			remote(strings.TrimSuffix(key, ".url"))
		case strings.HasSuffix(key, ".mirror"):
			remote(strings.TrimSuffix(key, ".mirror")).mirror = value == "true"
		case strings.HasSuffix(key, ".fetch"):
			name := strings.TrimSuffix(key, ".fetch")
			if strings.TrimPrefix(value, "+") == "refs/heads/*:refs/remotes/"+name+"/*" {
				remote(name).standard = true
			}
		}
	}
	return o, o.Refresh()
}

// ReadOwnOrUnknown is ReadOwn for a command that reports rather than acts:
// when the branches or the remotes cannot be read, every branch reads as
// unknown with the reason, instead of as having nothing to check.
func ReadOwnOrUnknown(mainRoot, trunk string) *Own {
	o, err := ReadOwn(mainRoot, trunk)
	if err != nil {
		return &Own{mainRoot: mainRoot, broken: "the remotes cannot be read: " + firstLine(err.Error())}
	}
	return o
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// urls reads how git reaches a remote, once. git remote -v is not read for
// it: a partial clone has it print the filter after the URL.
func (o *Own) urls(name string) *ownRemoteConfig {
	r := o.remotes[name]
	if r.urlsRead {
		return r
	}
	r.urlsRead = true
	lines := func(args ...string) []string {
		out, err := gitEnv(o.mainRoot, nil, nil, append([]string{"remote", "get-url"}, args...)...)
		if err != nil || out == "" {
			return nil
		}
		return strings.Split(out, "\n")
	}
	r.fetchURLs = lines("--all", name)
	for _, u := range lines("--push", "--all", name) {
		if !containsString(r.pushURLs, u) {
			r.pushURLs = append(r.pushURLs, u)
		}
	}
	return r
}

// Refresh reads the branches and the remote-tracking refs again: after a
// fetch, or after a branch moved.
func (o *Own) Refresh() error {
	if o.broken != "" {
		return nil
	}
	out, err := gitEnv(o.mainRoot, nil, nil, "for-each-ref",
		"--format=%(refname)%00%(objectname)%00%(push)%00%(upstream)", "refs/heads", "refs/remotes")
	if err != nil {
		return err
	}
	o.heads, o.tracking, o.states = map[string]ownHead{}, map[string]string{}, map[string]OwnRemote{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 4 {
			continue
		}
		if branch, ok := strings.CutPrefix(f[0], "refs/heads/"); ok {
			o.heads[branch] = ownHead{tip: f[1], push: f[2], upstream: f[3]}
		} else {
			o.tracking[f[0]] = f[1]
		}
	}
	return nil
}

// ownDest is where a push of one branch lands.
type ownDest struct {
	remote    string
	tracking  string // refs/remotes/origin/feat/x
	remoteRef string // refs/heads/feat/x
	// named says the branch's own configuration names this ref as its
	// upstream, so that its absence is a ref that went away rather than a
	// branch never pushed.
	named bool
	// none is nothing to check: no such branch, or the destination is trunk.
	none bool
	// unknown is why the destination cannot be read from a tracking ref.
	unknown string
}

// dest is the remote-tracking ref a push of branch would replace:
// <branch>@{push} when git resolves it, else the branch's own name on the
// remote it pushes to. That remote is origin, the one wt's own push writes,
// unless branch.<name>.pushRemote or remote.pushDefault names another: git
// then pushes the branch there under its own name, and under push.default
// simple resolves no @{push} for it.
func (o *Own) dest(branch string) ownDest {
	h, ok := o.heads[branch]
	if !ok {
		return ownDest{none: true}
	}
	d := ownDest{tracking: h.push}
	if !strings.HasPrefix(d.tracking, "refs/remotes/") {
		// A local branch, which push.default=upstream makes of a stack child
		// that tracks its parent: git resolves no destination on a remote.
		d.tracking = ""
	}
	pushesTo := cmp.Or(o.pushRemote[branch], o.pushDefault)
	if d.tracking == "" {
		d.remote = "origin"
		if o.remotes[pushesTo] != nil {
			d.remote = pushesTo
		}
		if o.remotes[d.remote] == nil {
			// No origin at all: nothing a push could replace.
			return ownDest{none: true}
		}
		d.tracking = "refs/remotes/" + d.remote + "/" + branch
	} else {
		// The remote git pushes the branch to, when the ref sits under it: a
		// remote named origin/team must not claim origin's team/x. Otherwise
		// the longest remote name the ref sits under.
		if r := cmp.Or(pushesTo, o.branchRemote[branch], "origin"); o.remotes[r] != nil && strings.HasPrefix(d.tracking, "refs/remotes/"+r+"/") {
			d.remote = r
		} else {
			for name := range o.remotes {
				if strings.HasPrefix(d.tracking, "refs/remotes/"+name+"/") && len(name) > len(d.remote) {
					d.remote = name
				}
			}
		}
	}
	if d.tracking == o.trunkRef {
		return ownDest{none: true}
	}
	d.named = h.upstream == d.tracking
	if d.remote == "" {
		d.unknown = "its push destination " + d.tracking + " belongs to no remote"
		return d
	}
	d.remoteRef = "refs/heads/" + strings.TrimPrefix(d.tracking, "refs/remotes/"+d.remote+"/")
	if d.remoteRef == "refs/heads/"+strings.TrimPrefix(o.trunkRef, "refs/remotes/origin/") {
		// Trunk on another remote, which push.default=upstream makes of a
		// branch tracking it: no more the branch's own than origin's trunk.
		return ownDest{none: true}
	}
	r := o.urls(d.remote)
	switch {
	case r.mirror:
		d.unknown = d.remote + " is a mirror remote"
	case len(r.pushURLs) > 1:
		d.unknown = fmt.Sprintf("%s has %d push URLs", d.remote, len(r.pushURLs))
	case len(r.pushURLs) == 1 && (len(r.fetchURLs) == 0 || !sameRepository(r.pushURLs[0], r.fetchURLs[0])):
		d.unknown = d.remote + " pushes to another URL than it fetches from"
	}
	return d
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// sameRepository reports two URLs that name one repository: the same text,
// or the same host and path under two transports, which is what
// pushInsteadOf makes of a remote fetched over https and pushed over ssh.
func sameRepository(a, b string) bool {
	return a == b || (repositoryKey(a) != "" && repositoryKey(a) == repositoryKey(b))
}

// repositoryKey is host/path of a network URL, without scheme, user, port,
// a trailing slash or .git; "" for a local path, which only its own text
// equals.
func repositoryKey(url string) string {
	var rest string
	if i := strings.Index(url, "://"); i > 0 {
		rest = url[i+3:]
	} else if host, path, ok := strings.Cut(url, ":"); ok && !strings.Contains(host, "/") && host != "" {
		// scp-like: user@host:path
		rest = host + "/" + strings.TrimPrefix(path, "/")
	} else {
		return ""
	}
	host, path, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	path = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git"), "/")
	if host == "" || path == "" {
		return ""
	}
	return strings.ToLower(host) + "/" + path
}

// OwnAsk is one branch's own-remote ref, as a fetch asks a remote for it.
type OwnAsk struct {
	Branch, Remote string
	// RemoteRef is the ref on the remote, refs/heads/<name>; Tracking the
	// remote-tracking ref it lands in.
	RemoteRef, Tracking string
}

// Refspec is the exact refspec that fetches it, forced: a remote-tracking
// ref follows its remote wherever that went.
func (a OwnAsk) Refspec() string { return "+" + a.RemoteRef + ":" + a.Tracking }

// Asks is what a fetch asks each remote for to bring the own-remote ref of
// every one of branches up to date, by remote, each ref once. A remote
// whose destination cannot be read, or whose fetch refspec is not the
// standard one, is asked for nothing: a ref written by name there would be
// one its configuration never maps.
func (o *Own) Asks(branches []string) map[string][]OwnAsk {
	asks := map[string][]OwnAsk{}
	if o.broken != "" {
		return asks
	}
	seen := map[string]bool{}
	for _, b := range branches {
		d := o.dest(b)
		if d.none || d.unknown != "" || !o.remotes[d.remote].standard || seen[d.tracking] {
			continue
		}
		seen[d.tracking] = true
		asks[d.remote] = append(asks[d.remote], OwnAsk{Branch: b, Remote: d.remote, RemoteRef: d.remoteRef, Tracking: d.tracking})
	}
	return asks
}

// Remotes is the remotes Asks names, origin first.
func Remotes(asks map[string][]OwnAsk) []string {
	var names []string
	for name := range asks {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == "origin") != (names[j] == "origin") {
			return names[i] == "origin"
		}
		return names[i] < names[j]
	})
	return names
}

// TrunkFetched records that this command fetched trunk from origin.
func (o *Own) TrunkFetched() { o.asked[o.trunkRef] = true }

// Seen records that this command fetched the ref: the remote has it.
func (o *Own) Seen(a OwnAsk) { o.asked[a.Tracking], o.seen[a.Tracking] = true, true }

// Gone records that the remote was asked and has no such ref.
func (o *Own) Gone(a OwnAsk) { o.asked[a.Tracking] = true }

// Failed records that the ref could not be fetched, and why.
func (o *Own) Failed(a OwnAsk, why string) { o.failed[a.Tracking] = why }

// State is how branch stands against its own remote. Blocks is left for the
// caller, which knows the checkout.
func (o *Own) State(branch string) OwnRemote {
	if o.broken != "" {
		return OwnRemote{State: OwnUnknown, Why: o.broken}
	}
	if own, ok := o.states[branch]; ok {
		return own
	}
	own := o.state(branch)
	o.states[branch] = own
	return own
}

func (o *Own) state(branch string) OwnRemote {
	own := OwnRemote{State: OwnNone, Local: o.heads[branch].tip}
	d := o.dest(branch)
	if d.none {
		own.Fetched = o.asked[o.trunkRef]
		return own
	}
	if d.unknown != "" {
		// A ref nobody names and nothing holds is nothing to check, however
		// the remote is set up.
		if _, there := o.tracking[d.tracking]; !there && !d.named {
			return own
		}
		own.State, own.Why = OwnUnknown, d.unknown
		return own
	}
	own.Fetched, own.FetchFailed = o.asked[d.tracking], o.failed[d.tracking]
	commit, there := o.tracking[d.tracking]
	short := strings.TrimPrefix(d.tracking, "refs/remotes/")
	// The fetch asked and the remote has no such ref: whatever the tracking
	// ref still says is from before it went.
	if missing := !there || (own.Fetched && !o.seen[d.tracking]); missing {
		if there || d.named {
			own.State, own.Ref = OwnGone, short
		}
		return own
	}
	own.Ref, own.Commit = short, commit
	if own.Local == commit {
		own.State = OwnInSync
		return own
	}
	out, err := gitEnv(o.mainRoot, nil, nil, "rev-list", "--left-right", "--count", own.Local+"..."+commit, "--")
	fields := strings.Fields(out)
	if err != nil || len(fields) != 2 {
		own.State, own.Commit = OwnUnknown, ""
		own.Why = "cannot compare it with " + short
		return own
	}
	own.Ahead, _ = strconv.Atoi(fields[0])
	own.Behind, _ = strconv.Atoi(fields[1])
	switch {
	case own.Behind == 0:
		own.State = OwnAhead
	case own.Ahead == 0:
		own.State = OwnBehind
	case o.rebased(branch, own.Local, commit):
		own.State = OwnRebased
	default:
		own.State = OwnDiverged
		// Nothing of the branch's own is missing from the remote, patch for
		// patch: the other way round from rebased.
		left, err := gitEnv(o.mainRoot, nil, nil, "rev-list", "--left-only", "--cherry-pick", own.Local+"..."+commit, "--")
		own.Elsewhere = err == nil && left == ""
	}
	return own
}

// rebased reports that what the remote has and the branch lacks is only old
// versions of the branch's own commits, so that pushing over it loses
// nothing: the remote's tip is a tip the branch once had, or every commit
// only the remote has is matched, patch for patch, by one only the branch
// has. A merge has no patch to match, so one on the remote side is the
// former-tip rule's to clear or nobody's.
func (o *Own) rebased(branch, local, remote string) bool {
	if formerTip(o.mainRoot, branch, remote) {
		return true
	}
	// A commit that changes nothing has the patch every other such commit
	// has, so one on the remote side would be matched by any on the
	// branch's: it proves nothing, and neither rule clears it.
	out, err := gitEnv(o.mainRoot, nil, nil, "log", "--right-only", "--no-merges", "--format=%x00%H", "--name-only", local+"..."+remote, "--")
	if err != nil {
		return false
	}
	for _, commit := range strings.Split(out, "\x00")[1:] {
		if _, files, _ := strings.Cut(strings.TrimSpace(commit), "\n"); strings.TrimSpace(files) == "" {
			return false
		}
	}
	out, err = gitEnv(o.mainRoot, nil, nil, "rev-list", "--right-only", "--cherry-pick", local+"..."+remote, "--")
	return err == nil && out == ""
}

// formerTip reports that commit is a tip branch once had: in the branch's
// reflog, under a safety ref a run or a forced undo pinned for it, where a
// run left it, or where a run fast-forwarded it to and then finished. A
// fast-forward whose run did not finish, or was undone or put back, is no
// former tip: the commit was the remote's, the branch held it only because
// wt moved it there, and Disown has taken the traces of it away.
//
// A ref wt pinned before the branch was created belongs to another branch
// that had the name, and says nothing about this one. That is only known
// while the reflog still starts at the creation.
func formerTip(mainRoot, branch, commit string) bool {
	// by is the tips wt pinned for this branch under prefix, by run.
	by := func(prefix string) map[int64]string {
		tips := map[int64]string{}
		out, err := gitEnv(mainRoot, nil, nil, "for-each-ref", "--format=%(refname) %(objectname)", prefix+branch+"/")
		if err != nil {
			return tips
		}
		for _, line := range strings.Split(out, "\n") {
			ref, tip, _ := strings.Cut(line, " ")
			// Digits only: refs/…/feat/x/1 is feat/x's, not feat's.
			if epoch, err := strconv.ParseInt(strings.TrimPrefix(ref, prefix+branch+"/"), 10, 64); err == nil {
				tips[epoch] = tip
			}
		}
		return tips
	}
	results, forwards := by(ResultPrefix), by(ForwardPrefix)
	// unfinished is since when a reflog entry at commit says nothing: a run
	// fast-forwarded the branch there and has no result, so the branch was
	// there only on the way through a run that did not finish, however it
	// was put back. Undo and restore take the entry away; an interrupt
	// leaves it, and a reset by hand after one must not read as a rebase.
	unfinished := int64(-1)
	for epoch, tip := range forwards {
		if _, finished := results[epoch]; !finished && tip == commit {
			if at := epoch / int64(time.Second); unfinished < 0 || at < unfinished {
				unfinished = at
			}
		}
	}
	created := int64(0)
	// A branch with no reflog makes this fail, which is no former tip there.
	if out, err := gitEnv(mainRoot, nil, nil, "reflog", "show", "--format=%H %gd %gs", "--date=unix", "refs/heads/"+branch, "--"); err == nil && out != "" {
		lines := strings.Split(out, "\n")
		when := func(line string) int64 {
			f := strings.SplitN(line, " ", 3)
			if len(f) < 2 {
				return 0
			}
			open := strings.LastIndex(f[1], "@{")
			if open < 0 {
				return 0
			}
			at, _ := strconv.ParseInt(strings.TrimSuffix(f[1][open+2:], "}"), 10, 64)
			return at
		}
		for _, line := range lines {
			if sha, _, _ := strings.Cut(line, " "); sha == commit && (unfinished < 0 || when(line) < unfinished) {
				return true
			}
		}
		oldest := lines[len(lines)-1]
		if f := strings.SplitN(oldest, " ", 3); len(f) == 3 && strings.HasPrefix(f[2], "branch: Created from") {
			created = when(oldest)
		}
	}
	// A ref pinned before the branch was created is another branch's.
	ours := func(epoch int64) bool { return epoch/int64(time.Second) >= created }
	for _, tips := range []map[int64]string{by(SafetyPrefix), results} {
		for epoch, tip := range tips {
			if tip == commit && ours(epoch) {
				return true
			}
		}
	}
	for epoch, tip := range forwards {
		if _, finished := results[epoch]; finished && tip == commit && ours(epoch) {
			return true
		}
	}
	return false
}
