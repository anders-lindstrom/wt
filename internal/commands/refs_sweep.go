package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/github"
	"github.com/anders-lindstrom/wt/internal/quarantine"
)

// What wt refs sweep made of a backup, for --json.
const (
	// RefBackupContained is a backup another ref contains: nothing on it is
	// only on it.
	RefBackupContained = "backupContained"
	// RefBackupOld holds commits no other ref has, and is older than the age.
	RefBackupOld = "backupOld"
	// RefBackupYoung holds commits no other ref has, and is younger.
	RefBackupYoung = "backupYoung"
	// RefKept is a backup something holds; kept names what.
	RefKept = "kept"
)

// Why wt refs sweep keeps a backup, for --json. heldByRebase and
// heldByBisect are sweep's own codes.
const (
	KeptWorktree           = "worktree"
	KeptQuarantine         = "quarantine"
	KeptProtected          = "protected"
	KeptObjectMissing      = "objectMissing"
	KeptPullRequestOpen    = "pullRequestOpen"
	KeptPullRequestUnknown = "pullRequestUnknown"
	KeptNotContained       = "notContained"
)

// Where a backup's date comes from, for --json.
const (
	DateReflog = "reflog"
	DateTagger = "tagger"
	DateCommit = "commit"
)

// What became of one row of a ref sweep that ran. The set is exhaustive.
const (
	RefSwept       = "swept"
	RefResultKept  = "kept"
	RefFailed      = "failed"
	RefNotRun      = "notRun"
	RefInterrupted = "interrupted"
	RefNotSelected = "notSelected"
)

// Why a ref sweep refused, for --json. planChanged is the other plans'
// code.
const (
	// ProblemRemoteAmbiguous is --remote with an origin that does not push to
	// exactly one URL.
	ProblemRemoteAmbiguous = "remoteAmbiguous"
	// ProblemRemoteCredentials is --remote with an origin URL that carries a
	// password, or any userinfo on http(s).
	ProblemRemoteCredentials = "remoteCredentialsInUrl" //nolint:gosec // G101: a problem code, not a credential
	// ProblemRunIDTaken is --run-id naming a run that has refs already.
	ProblemRunIDTaken = "runIdTaken"
)

// RefProblem is one reason a ref sweep refused, for --json.
type RefProblem struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// refsPRDeadline bounds the one question GitHub is asked about origin's
// backup branches.
const refsPRDeadline = 15 * time.Second

// RefRow is one backup and what the plan made of it.
type RefRow struct {
	ID, Kind, Name string
	// Ref is the ref on its own side: refs/heads/<name> here or on origin.
	Ref      string
	Pattern  string
	Category string
	Selected bool
	// Tip is the commit; Object the ref's own value, which is the tag
	// object of an annotated tag. Tip is "" when the object is not here.
	Tip, Object string
	Annotated   *bool
	// ContainedIn is the first container holding Tip; Unique the commits
	// on no container, nil when contained or unknown.
	ContainedIn string
	Unique      *int
	Date        int64
	DateSource  string
	Kept        []string
	Subject     string
}

// RefsPlan is what wt refs sweep would do.
type RefsPlan struct {
	Repo, Trunk     string
	TrunkSource     *string
	Fetched, Remote bool
	Patterns        []string
	Age             string
	Now             int64
	// Endpoint is origin as --remote resolved it; nil without --remote.
	Endpoint *Endpoint
	Problems []RefProblem
	Rows     []RefRow
}

// Selected is the rows the sweep moves.
func (p RefsPlan) Selected() []RefRow {
	var out []RefRow
	for _, r := range p.Rows {
		if r.Selected {
			out = append(out, r)
		}
	}
	return out
}

// selectable reports that some row could be swept: it is a backup nothing
// holds.
func (p RefsPlan) selectable() bool {
	return slices.ContainsFunc(p.Rows, func(r RefRow) bool { return r.Category != RefKept })
}

// RefsOptions carries what wt refs sweep was asked.
type RefsOptions struct {
	Remote  bool
	NoFetch bool
	// Only replaces the default selection with these ids; nil keeps it.
	Only   []string
	DryRun bool
	Yes    bool
	// Confirm, when set and without Yes, is asked once the plan is printed.
	Confirm func(RefsPlan) bool
	// Expect refuses the sweep unless the plan made now has this token.
	Expect string
	// OpenPRs says which of origin's branches have an open pull request,
	// for a test with no gh; nil asks GitHub.
	OpenPRs func(branches []string) (map[string]bool, error)
	// RunID names the run instead of the id wt would mint; it must be in
	// wt's format and have nothing under refs/wt-swept/<RunID>/ yet.
	RunID string
	// Now is the clock, for a test; nil is time.Now.
	Now func() time.Time
	// Journal records the run for --json; nil records nothing.
	Journal *RefsJournal
}

func (o RefsOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// refCandidate is a ref as the plan reads it before sorting it.
type refCandidate struct {
	kind, name, ref, object, tip string
	annotated                    bool
}

// container is a ref that matches no pattern: what can contain a backup.
type container struct {
	name, tip string
	order     int
}

// planRefs reads everything a ref sweep depends on: it fetches unless told
// not to, lists the refs here, and origin's with --remote, and sorts every
// backup. progress gets what the fetch says.
func planRefs(ctx *Context, opts RefsOptions, progress io.Writer) (RefsPlan, error) {
	u := ctx.UserConfig()
	p := RefsPlan{Repo: ctx.Repo.MainRoot, Trunk: ctx.Config.MainBranch, TrunkSource: ctx.trunkSource(),
		Remote: opts.Remote, Patterns: append([]string{}, u.RefSweepPatterns...), Age: u.RefSweepAge,
		Now: opts.now().Unix()}
	if err := ctx.noTrunk(); err != nil {
		return p, err
	}
	age, err := config.ParseAge(p.Age)
	if err != nil {
		return p, fmt.Errorf("%s: %w", config.UserKeyRefSweepAge, err)
	}
	if opts.Remote {
		if !ctx.Repo.HasRemote("origin") {
			return p, errors.New("--remote sweeps origin's backups, and there is no origin remote here")
		}
		ep, err := resolveEndpoint(ctx.Repo.MainRoot)
		if err != nil {
			switch {
			case errors.Is(err, errRemoteAmbiguous):
				p.Problems = append(p.Problems, RefProblem{Code: ProblemRemoteAmbiguous, Text: err.Error()})
			case errors.Is(err, errRemoteCredentials):
				p.Problems = append(p.Problems, RefProblem{Code: ProblemRemoteCredentials, Text: err.Error()})
			}
			return p, err
		}
		p.Endpoint = ep
	}
	if err := sweepFetch(ctx, opts.NoFetch, progress); err != nil {
		return p, err
	}
	p.Fetched = !opts.NoFetch && ctx.Repo.HasRemote("origin")
	patterns := config.CompileRefPatterns(p.Patterns)
	root := ctx.Repo.MainRoot

	local, containers, err := readLocalRefs(ctx, patterns)
	if err != nil {
		return p, err
	}
	var remote []refCandidate
	var remoteContainers []container
	if opts.Remote {
		if remote, remoteContainers, err = readOriginRefs(ctx, p.Endpoint.raw, patterns); err != nil {
			return p, err
		}
	}
	var oids []string
	for _, c := range append(append([]refCandidate{}, local...), remote...) {
		oids = append(oids, c.object, c.tip)
	}
	objects, err := readObjects(root, oids)
	if err != nil {
		return p, err
	}
	for i := range local {
		if local[i].tip == "" {
			local[i].tip = peelCommit(root, local[i].object, objects)
		}
	}

	worktrees, err := ctx.Repo.Worktrees()
	if err != nil {
		return p, fmt.Errorf("could not list worktrees, so cannot tell which branches are in use: %w", err)
	}
	users := worktrees.Users()
	heldNames, heldTips, err := quarantineHolds(ctx)
	if err != nil {
		return p, err
	}
	originHead, _ := ctx.Repo.OriginHead()
	dates := &refDates{mainRoot: root}
	cutoff := p.Now - int64(age/time.Second)

	var rows []RefRow
	var localTips []string
	for _, c := range local {
		r := newRefRow(c, patterns, objects)
		if c.kind == RefBranch {
			if t, ok := dates.created(c.ref); ok {
				r.Date, r.DateSource = t, DateReflog
			}
			if use, ok := users[c.name]; ok {
				switch use.By {
				case "":
					r.Kept = append(r.Kept, KeptWorktree)
				default:
					r.Kept = append(r.Kept, heldByCode(use.By))
				}
			}
			if protectedBranch(c.name, p.Trunk, originHead) {
				r.Kept = append(r.Kept, KeptProtected)
			}
		}
		if heldNames[c.ref] || (r.Tip != "" && heldTips[r.Tip]) {
			r.Kept = append(r.Kept, KeptQuarantine)
		}
		if r.Tip == "" {
			r.Kept = append(r.Kept, KeptObjectMissing)
		} else {
			localTips = append(localTips, r.Tip)
		}
		rows = append(rows, r)
	}
	if err := contain(root, rows, localTips, containers, false); err != nil {
		return p, err
	}
	if opts.Remote {
		remoteRows, err := planOrigin(ctx, opts, p.Endpoint.raw, patterns, remote, remoteContainers, objects, p.Trunk, originHead)
		if err != nil {
			return p, err
		}
		rows = append(rows, remoteRows...)
	}
	for i := range rows {
		r := &rows[i]
		switch {
		case len(r.Kept) > 0:
			r.Category = RefKept
		case r.ContainedIn != "":
			r.Category = RefBackupContained
		case remoteKind(r.Kind):
			r.Category, r.Kept = RefKept, []string{KeptNotContained}
		case r.Date < cutoff:
			r.Category = RefBackupOld
		default:
			r.Category = RefBackupYoung
		}
		r.Selected = r.Category == RefBackupContained || r.Category == RefBackupOld
	}
	p.Rows = rows
	return p, nil
}

// newRefRow is a candidate as a row, with its tip, its subject and the date
// of its own object: a tag's tagger date, else its commit's.
func newRefRow(c refCandidate, patterns []config.RefPattern, objects map[string]objectInfo) RefRow {
	pattern, _ := config.MatchRefPattern(patterns, c.name)
	r := RefRow{ID: refID(c.kind, c.name), Kind: c.kind, Name: c.name, Ref: c.ref, Pattern: pattern,
		Tip: c.tip, Object: c.object, Kept: []string{}}
	if c.kind == RefTag || c.kind == RefRemoteTag {
		a := objects[c.object].Type == "tag" || c.annotated
		r.Annotated = &a
		if a && objects[c.object].Time > 0 {
			r.Date, r.DateSource = objects[c.object].Time, DateTagger
		}
	}
	if r.Tip != "" {
		info := objects[r.Tip]
		if info.Type != "commit" {
			r.Tip = ""
		} else {
			r.Subject = info.Subject
			if r.DateSource == "" {
				r.Date, r.DateSource = info.Time, DateCommit
			}
		}
	}
	return r
}

// readLocalRefs lists this repository's branches and tags, and its copy of
// origin's branches: the ones a pattern matches are candidates, the rest
// containers, trunk first, then origin's, then the others.
func readLocalRefs(ctx *Context, patterns []config.RefPattern) ([]refCandidate, []container, error) {
	lines, err := git.Lines(ctx.Repo.MainRoot, "for-each-ref",
		"--format=%(refname)%00%(objectname)%00%(objecttype)%00%(*objectname)%00%(*objecttype)",
		"refs/heads/", "refs/tags/", "refs/remotes/origin/")
	if err != nil {
		return nil, nil, fmt.Errorf("could not list refs: %w", err)
	}
	trunk := ctx.Config.MainBranch
	var cands []refCandidate
	var conts []container
	for _, line := range lines {
		f := strings.Split(line, "\x00")
		if len(f) != 5 {
			continue
		}
		ref, oid, typ, peeled, peeledType := f[0], f[1], f[2], f[3], f[4]
		tip := ""
		switch {
		case typ == "commit":
			tip = oid
		case typ == "tag" && peeledType == "commit":
			tip = peeled
		case typ != "tag":
			continue // a tag of a tree or a blob holds no history
		}
		var kind, name string
		order := 3
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			kind, name = RefBranch, strings.TrimPrefix(ref, "refs/heads/")
			if name == trunk {
				order = 0
			}
		case strings.HasPrefix(ref, "refs/tags/"):
			kind, name = RefTag, strings.TrimPrefix(ref, "refs/tags/")
		default:
			name = strings.TrimPrefix(ref, "refs/remotes/origin/")
			if name == "HEAD" {
				continue
			}
			order = 2
			if name == trunk {
				order = 1
			}
		}
		if _, ok := config.MatchRefPattern(patterns, name); ok {
			if kind != "" {
				cands = append(cands, refCandidate{kind: kind, name: name, ref: ref, object: oid, tip: tip})
			}
			continue
		}
		if tip == "" {
			// A tag of a tag: its commit is read when it is a candidate, and
			// as a container it is too rare to be worth a git of its own.
			continue
		}
		conts = append(conts, container{name: ref, tip: tip, order: order})
	}
	sortContainers(conts)
	return cands, conts, nil
}

func sortContainers(c []container) {
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].order != c[j].order {
			return c[i].order < c[j].order
		}
		return c[i].name < c[j].name
	})
}

// readOriginRefs lists origin's branches and tags, from ls-remote: the
// candidates, and the containers a remote backup is measured against —
// origin's own refs, since other clones cannot see a branch here.
func readOriginRefs(ctx *Context, url string, patterns []config.RefPattern) ([]refCandidate, []container, error) {
	rr, err := readRemoteRefs(ctx.Repo.MainRoot, url)
	if err != nil {
		return nil, nil, err
	}
	trunk := ctx.Config.MainBranch
	var cands []refCandidate
	var conts []container
	for ref, oid := range rr.Values {
		kind, name := RefRemoteBranch, strings.TrimPrefix(ref, "refs/heads/")
		order := 1
		if strings.HasPrefix(ref, "refs/tags/") {
			kind, name, order = RefRemoteTag, strings.TrimPrefix(ref, "refs/tags/"), 2
		} else if name == trunk {
			order = 0
		}
		tip := oid
		peeled, annotated := rr.Peeled[ref]
		if annotated {
			tip = peeled
		}
		if _, ok := config.MatchRefPattern(patterns, name); ok {
			cands = append(cands, refCandidate{kind: kind, name: name, ref: ref, object: oid, tip: tip,
				annotated: annotated})
			continue
		}
		conts = append(conts, container{name: "origin:" + ref, tip: tip, order: order})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].ref < cands[j].ref })
	sortContainers(conts)
	return cands, conts, nil
}

// planOrigin makes the rows of origin's backups. A remote backup is only
// ever swept when origin's own refs contain it; a branch with an open pull
// request is kept, since deleting it closes the pull request, and so is
// every branch when GitHub cannot be asked.
func planOrigin(ctx *Context, opts RefsOptions, url string, patterns []config.RefPattern, cands []refCandidate, conts []container,
	objects map[string]objectInfo, trunk, originHead string) ([]RefRow, error) {
	root := ctx.Repo.MainRoot
	var branches []string
	for _, c := range cands {
		if c.kind == RefRemoteBranch {
			branches = append(branches, c.name)
		}
	}
	open, prErr := map[string]bool{}, error(nil)
	if len(branches) > 0 {
		open, prErr = openPullRequests(ctx, opts, branches, url)
		if prErr != nil {
			ctx.Warnf(WarnGitHub, "keeping origin's backup branches: cannot ask GitHub about their pull requests (%v)",
				oneLine(prErr.Error()))
		}
	}
	// A container whose commit is not here cannot contain anything wt can
	// see; leaving it out only keeps more.
	var cOIDs []string
	for _, c := range conts {
		cOIDs = append(cOIDs, c.tip)
	}
	have, err := readObjects(root, cOIDs)
	if err != nil {
		return nil, err
	}
	var usable []container
	for _, c := range conts {
		if have[c.tip].Type == "commit" {
			usable = append(usable, c)
		}
	}
	var rows []RefRow
	var tips []string
	for _, c := range cands {
		r := newRefRow(c, patterns, objects)
		if c.kind == RefRemoteBranch && protectedBranch(c.name, trunk, originHead) {
			r.Kept = append(r.Kept, KeptProtected)
		}
		if objects[c.object].Type == "" || r.Tip == "" {
			r.Tip = ""
			r.Kept = append(r.Kept, KeptObjectMissing)
		} else {
			tips = append(tips, r.Tip)
		}
		if c.kind == RefRemoteBranch {
			switch {
			case prErr != nil:
				r.Kept = append(r.Kept, KeptPullRequestUnknown)
			case open[c.name]:
				r.Kept = append(r.Kept, KeptPullRequestOpen)
			}
		}
		rows = append(rows, r)
	}
	if err := contain(root, rows, tips, usable, true); err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].Tip != "" && rows[i].ContainedIn == "" && !slices.Contains(rows[i].Kept, KeptNotContained) {
			rows[i].Kept = append(rows[i].Kept, KeptNotContained)
		}
	}
	return rows, nil
}

// openPullRequests is which of origin's branches have an open pull request
// whose head is that branch of origin: asked of origin's own repository,
// and of the one gh resolves when that is another (the parent of a fork,
// where a pull request from origin's branch lives). A branch is open when a
// pull request there has origin's owner as its head's owner. An origin that
// is not on GitHub cannot be asked.
func openPullRequests(ctx *Context, opts RefsOptions, branches []string, url string) (map[string]bool, error) {
	if opts.OpenPRs != nil {
		return opts.OpenPRs(branches)
	}
	gh, err := openGitHub(ctx)
	if err != nil {
		return nil, err
	}
	origin, ok := github.RemoteFromURL("origin", url)
	if !ok {
		return nil, errors.New("origin is not a GitHub repository, so its pull requests cannot be read")
	}
	owner, _, _ := strings.Cut(origin.Slug, "/")
	repos := []github.Remote{origin}
	if !strings.EqualFold(gh.Remote.Slug, origin.Slug) {
		repos = append(repos, gh.Remote)
	}
	open := map[string]bool{}
	for _, repo := range repos {
		prs, err := gh.CLI.PRsOnBranches(ctx.Repo.MainRoot, repo, branches, refsPRDeadline)
		if err != nil {
			return nil, gh.fail(err)
		}
		for _, pr := range prs {
			ownHead := strings.EqualFold(repo.Slug, origin.Slug) && !pr.IsCrossRepository
			if pr.Open() && (ownHead || strings.EqualFold(pr.HeadRepositoryOwner.Login, owner)) {
				open[pr.HeadRefName] = true
			}
		}
	}
	return open, nil
}

// contain fills in, for every row with a tip, the first container holding
// it or the commits no container has. One rev-list pass says which tips
// are contained and counts the rest; then the containers are asked in
// order, trunk first, until every contained tip has one. Rows here are
// asked with for-each-ref --merged, a container at a time; origin's, whose
// refs are not here, with merge-base --is-ancestor.
func contain(root string, rows []RefRow, tips []string, conts []container, remote bool) error {
	if len(tips) == 0 {
		return nil
	}
	keep := make([]string, 0, len(conts))
	for _, c := range conts {
		keep = append(keep, c.tip)
	}
	unique, err := uniqueCommits(root, tips, keep)
	if err != nil {
		return err
	}
	var waiting []*RefRow
	for i := range rows {
		r := &rows[i]
		if r.Tip == "" || remoteKind(r.Kind) != remote {
			continue
		}
		if n := unique[r.Tip]; n > 0 {
			r.Unique = &n
			continue
		}
		waiting = append(waiting, r)
	}
	for _, c := range conts {
		if len(waiting) == 0 {
			break
		}
		var still []*RefRow
		if remote {
			for _, r := range waiting {
				_, err := git.Exec(git.Opts{Dir: root}, "merge-base", "--is-ancestor", r.Tip, c.tip)
				switch code, aerr := git.Answer(err, 1); {
				case aerr != nil:
					return fmt.Errorf("could not compare %s with %s: %s", r.ID, c.name, git.Reason(aerr))
				case code == 0:
					r.ContainedIn = c.name
				default:
					still = append(still, r)
				}
			}
			waiting = still
			continue
		}
		byRef := map[string]*RefRow{}
		args := []string{"for-each-ref", "--merged=" + c.tip, "--format=%(refname)"}
		for _, r := range waiting {
			byRef[r.Ref] = r
			args = append(args, r.Ref)
		}
		merged, err := git.Lines(root, args...)
		if err != nil {
			return fmt.Errorf("could not compare the backups with %s: %w", c.name, err)
		}
		for _, ref := range merged {
			if r := byRef[ref]; r != nil {
				r.ContainedIn = c.name
			}
		}
		for _, r := range waiting {
			if r.ContainedIn == "" {
				still = append(still, r)
			}
		}
		waiting = still
	}
	// rev-list found nothing only on these, yet no container answered for
	// them one by one, which only a ref moving meanwhile explains. They hold
	// no commits of their own, and say so rather than naming a container.
	for _, r := range waiting {
		zero := 0
		r.Unique = &zero
	}
	return nil
}

// quarantineHolds is what the live quarantines of this repository hold:
// the branch each moved aside and the name it was kept under, read from
// the recovery.json its pins name, and, for a quarantine whose folder is
// there but cannot be read, the commit its tip pin holds. A quarantine is
// known here only by its pins under refs/wt-quarantine/: one whose folder
// is gone is dead, and one whose pins are gone cannot be seen at all.
func quarantineHolds(ctx *Context) (names, tips map[string]bool, err error) {
	names, tips = map[string]bool{}, map[string]bool{}
	root := ctx.Repo.MainRoot
	lines, err := git.Lines(root, "for-each-ref", "--format=%(refname)%00%(objectname)", quarantine.PinPrefix)
	if err != nil {
		return nil, nil, fmt.Errorf("could not read the quarantines' pins, so cannot tell what they hold: %w", err)
	}
	type pins struct{ dir, tip string }
	byKey := map[string]*pins{}
	var keys []string
	for _, line := range lines {
		ref, oid, _ := strings.Cut(line, "\x00")
		key, name, ok := strings.Cut(strings.TrimPrefix(ref, quarantine.PinPrefix), "/")
		if !ok {
			continue
		}
		p := byKey[key]
		if p == nil {
			p = &pins{}
			byKey[key] = p
			keys = append(keys, key)
		}
		switch name {
		case "dir":
			p.dir = ref
		case "tip":
			p.tip = oid
		}
	}
	for _, key := range keys {
		p := byKey[key]
		dir := ""
		if p.dir != "" {
			dir, _ = git.Run(root, "cat-file", "blob", p.dir)
		}
		if dir != "" {
			if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
				continue
			}
			if r, err := quarantine.Load(dir); err == nil {
				if b := r.Branch; b != nil {
					names["refs/heads/"+b.Name] = true
					if b.KeepAs != nil && *b.KeepAs != "" {
						names["refs/heads/"+*b.KeepAs] = true
					}
				}
				continue
			}
		}
		if p.tip != "" {
			tips[p.tip] = true
		}
	}
	return names, tips, nil
}

// applyOnly replaces the default selection with the ids given. An id the
// plan does not have, or a row it keeps, refuses the lot.
func (p *RefsPlan) applyOnly(only []string) error {
	if only == nil {
		return nil
	}
	want := map[string]bool{}
	for _, id := range only {
		want[id] = true
	}
	var unknown, kept []string
	for id := range want {
		i := slices.IndexFunc(p.Rows, func(r RefRow) bool { return r.ID == id })
		switch {
		case i < 0:
			unknown = append(unknown, id)
		case p.Rows[i].Category == RefKept:
			kept = append(kept, id+" ("+strings.Join(p.Rows[i].Kept, ", ")+")")
		}
	}
	sort.Strings(unknown)
	sort.Strings(kept)
	switch {
	case len(unknown) > 0:
		return fmt.Errorf("nothing was swept: --only names what the plan does not have: %s",
			strings.Join(unknown, ", "))
	case len(kept) > 0:
		return fmt.Errorf("nothing was swept: --only names what the plan keeps: %s", strings.Join(kept, ", "))
	}
	for i := range p.Rows {
		p.Rows[i].Selected = want[p.Rows[i].ID]
	}
	return nil
}

// refsToken names a plan: the repository, trunk, --remote, the patterns and
// the age, and every row with what it stands on. The selection is not in
// it, so --only picks from the plan a token names. Nil when no row could be
// swept.
func refsToken(p RefsPlan) *string {
	if !p.selectable() {
		return nil
	}
	h := sha256.New()
	h.Write([]byte("wt-refs-sweep-1\x00" + p.Repo + "\x00" + p.Trunk + "\x00" + strconv.FormatBool(p.Remote) +
		"\x00" + strings.Join(p.Patterns, " ") + "\x00" + p.Age))
	if p.Endpoint != nil {
		h.Write([]byte("\nendpoint\x00" + p.Endpoint.Digest))
	}
	for _, r := range p.Rows {
		unique := ""
		if r.Unique != nil {
			unique = strconv.Itoa(*r.Unique)
		}
		h.Write([]byte("\n" + strings.Join([]string{r.ID, r.Pattern, r.Category, r.Tip, r.Object, r.ContainedIn,
			unique, strconv.FormatInt(r.Date, 10), r.DateSource, strings.Join(r.Kept, ",")}, "\x00")))
	}
	token := "rs1-" + hex.EncodeToString(h.Sum(nil))[:32]
	return &token
}

// errRefsPlanChanged is --expect refusing a sweep whose plan is not the one
// read.
var errRefsPlanChanged = errors.New("the ref sweep plan changed since it was read (a backup moved, " +
	"was created or deleted, or gained or lost a reason to be kept); nothing was swept: read the plan again")

// RefsSweep sweeps the backups the plan selects: each is pinned under
// refs/wt-swept/<run>/ and deleted in one step, so nothing is lost until
// wt refs purge.
func RefsSweep(ctx *Context, opts RefsOptions, w io.Writer) (err error) {
	j := opts.Journal
	if j != nil {
		defer setInterruptJournal(j)()
		defer watchSignals(w, nil)()
		defer func() {
			j.fail(err)
			j.Finish()
		}()
	}
	if opts.RunID != "" {
		if !runIDPattern.MatchString(opts.RunID) {
			return fmt.Errorf("nothing was swept: --run-id %q is not a run id; they look like "+
				"20260930T091500Z-3f2a (four to 32 hex digits)", opts.RunID)
		}
		switch taken, err := runTaken(ctx.Repo.MainRoot, opts.RunID); {
		case err != nil:
			return err
		case taken:
			err := fmt.Errorf("nothing was swept: run %s has refs under %s already", opts.RunID, SweptPrefix)
			j.problem(ProblemRunIDTaken, err)
			return err
		}
	}
	plan, err := planRefs(ctx, opts, w)
	j.planned(plan, nil)
	if err != nil {
		return err
	}
	token := refsToken(plan)
	j.planned(plan, token)
	if opts.Expect != "" && deref(token) != opts.Expect {
		j.problem(ProblemPlanChanged, errRefsPlanChanged)
		return errRefsPlanChanged
	}
	if err := plan.applyOnly(opts.Only); err != nil {
		return err
	}
	j.planned(plan, token)
	plan.Render(w)
	selected := plan.Selected()
	if len(selected) == 0 {
		fmt.Fprintln(w, "Nothing to sweep.")
		return nil
	}
	switch {
	case opts.DryRun:
		fmt.Fprintln(w, "Nothing was swept: --dry-run.")
		return nil
	case opts.Yes:
	case opts.Confirm != nil:
		if !opts.Confirm(plan) {
			fmt.Fprintln(w, "Nothing was swept.")
			return nil
		}
		// A question can sit for minutes: the sweep goes by the plan read
		// now, and only if it is the one that was asked about.
		fresh, err := planRefs(ctx, RefsOptions{Remote: opts.Remote, NoFetch: true, OpenPRs: opts.OpenPRs,
			Now: opts.Now}, io.Discard)
		if err != nil {
			return err
		}
		if err := fresh.applyOnly(opts.Only); err != nil {
			return err
		}
		if deref(refsToken(fresh)) != deref(token) {
			j.problem(ProblemPlanChanged, errRefsPlanChanged)
			return errRefsPlanChanged
		}
	default:
		fmt.Fprintf(w, "Nothing was swept: there is no terminal to ask. Pass --yes to sweep %s.\n",
			refCount(len(selected)))
		return nil
	}
	// Origin is read again right before anything goes to it: a URL changed
	// since the plan would send the deletes somewhere else.
	if plan.Endpoint != nil {
		now, err := resolveEndpoint(ctx.Repo.MainRoot)
		if err != nil || now.Digest != plan.Endpoint.Digest {
			return errors.New("nothing was swept: origin's URL changed since the plan was made; read the plan again")
		}
	}
	runID := opts.RunID
	if runID == "" {
		runID = newRunID(opts.now())
	}
	// The meta goes first, so a run stopped anywhere after is found by its id
	// and, with --remote, bound to the origin it deleted from. Its create is
	// a compare-and-swap: a run id taken meanwhile refuses here.
	j.begin(runID)
	gitDir, err := git.Run(ctx.Repo.MainRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err == nil {
		err = writeMeta(ctx.Repo.MainRoot, runMeta{RunID: runID, Repo: gitDir, SweptAt: opts.now().Unix(),
			Endpoint: plan.Endpoint, WtVersion: Version})
	}
	if err != nil {
		j.abandon()
		return fmt.Errorf("nothing was swept: %w", err)
	}
	return plan.apply(ctx, runID, j, w)
}

func refCount(n int) string {
	if n == 1 {
		return "1 ref"
	}
	return fmt.Sprintf("%d refs", n)
}

// apply moves each selected row, reading it once more right before.
func (p RefsPlan) apply(ctx *Context, runID string, j *RefsJournal, w io.Writer) error {
	root := ctx.Repo.MainRoot
	failed := 0
	for _, r := range p.Selected() {
		pin := pinOf(runID, r.Kind, r.Name)
		j.start(r.ID)
		var st refState
		if remoteKind(r.Kind) {
			st = sweepRemoteRef(root, p.Endpoint.raw, r, pin)
		} else {
			st = sweepLocalRef(ctx, r, pin)
		}
		j.settle(r.ID, st)
		switch {
		case st.kept != "":
			fmt.Fprintf(w, "- kept %s: %s\n", r.ID, st.kept)
			failed++
		case st.err != nil || !st.pinned || !st.deleted:
			why := "not finished"
			if st.err != nil {
				why = oneLine(st.err.Error())
			}
			fmt.Fprintf(w, "! %s: %s\n", r.ID, why)
			failed++
		default:
			fmt.Fprintf(w, "✓ swept %s to %s\n", r.ID, pin)
		}
	}
	// A run that moved nothing leaves nothing behind, its meta included.
	dropEmptyRun(root, runID, w)
	if failed > 0 {
		return fmt.Errorf("%d of %s kept or not finished; wt refs restore %s puts back what went",
			failed, refCount(len(p.Selected())), runID)
	}
	fmt.Fprintf(w, "wt refs restore %s puts them back; wt refs purge %s deletes them for good\n", runID, runID)
	return nil
}

// refState is what one row came to: kept on purpose (with why), failed,
// and what is true of its pin and its ref when it was read back.
type refState struct {
	kept            string
	err             error
	pinned, deleted bool
}

// sweepLocalRef pins and deletes one ref here in a single transaction, at
// the object the plan read, and drops a branch's config after it, as git
// branch -D does.
func sweepLocalRef(ctx *Context, r RefRow, pin string) refState {
	root := ctx.Repo.MainRoot
	now, ok, err := refValue(root, r.Ref)
	switch {
	case err != nil:
		return refState{err: err}
	case !ok:
		return refState{kept: "it was deleted after the plan was made"}
	case now != r.Object:
		return refState{kept: "it moved after the plan was made"}
	}
	if r.Kind == RefBranch {
		users, err := ctx.Repo.BranchUsers()
		if err != nil {
			return refState{kept: err.Error()}
		}
		if _, used := users[r.Name]; used {
			return refState{kept: "it was checked out after the plan was made"}
		}
	}
	err = updateRefs(root, "wt refs sweep", "create "+pin+" "+r.Object, "delete "+r.Ref+" "+r.Object)
	if err == nil && r.Kind == RefBranch {
		_, _ = git.Run(root, "config", "--remove-section", "branch."+r.Name)
	}
	st := readLocalState(root, r, pin)
	st.err = err
	return st
}

// readLocalState reads back a local row's pin and ref.
func readLocalState(root string, r RefRow, pin string) refState {
	var st refState
	if v, ok, err := refValue(root, pin); err == nil && ok && v == r.Object {
		st.pinned = true
	}
	if _, ok, err := refValue(root, r.Ref); err == nil && !ok {
		st.deleted = true
	}
	return st
}

// sweepRemoteRef pins one of origin's refs by fetching it into the pin,
// and deletes it on origin only if the pin is at the planned object, with
// a push leased on that object. Every call goes to url, origin's one URL.
// A pin that holds anything else is dropped; so is the pin of a ref the
// push did not delete and origin still has.
func sweepRemoteRef(root, url string, r RefRow, pin string) refState {
	if _, err := git.RunTimeout(root, networkTimeout, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head",
		"--no-recurse-submodules", url, "+"+r.Ref+":"+pin); err != nil {
		return refState{err: fmt.Errorf("could not pin it: %s", git.Reason(err))}
	}
	got, ok, err := refValue(root, pin)
	if err != nil {
		return refState{err: err}
	}
	if !ok || got != r.Object {
		if ok {
			if derr := deleteRefAt(root, pin, got); derr != nil {
				return refState{pinned: false, err: fmt.Errorf("origin moved it, and the pin at %s could not be "+
					"dropped: %s", git.ShortID(got, 12), git.Reason(derr))}
			}
		}
		return refState{kept: "it moved on origin after the plan was made"}
	}
	_, pushErr := git.RunTimeout(root, networkTimeout, "push", "--quiet", "--no-verify",
		"--force-with-lease="+r.Ref+":"+r.Object, url, ":"+r.Ref)
	if pushErr == nil {
		// The delete landed. Whatever origin has there now is somebody
		// else's, and the pin is all that holds what was deleted.
		return refState{pinned: true, deleted: true}
	}
	st := refState{pinned: true}
	now, there, err := remoteValue(root, url, r.Ref, networkTimeout)
	switch {
	case err != nil:
		// Not knowing whether the delete landed, the pin stays.
		st.err = fmt.Errorf("%s; and %w", git.Reason(pushErr), err)
	case !there:
		// The push reported failure but the ref is gone: the pin stays.
		st.deleted, st.err = true, fmt.Errorf("the push reported %s, yet origin has no %s", git.Reason(pushErr), r.Ref)
	default:
		if derr := deleteRefAt(root, pin, r.Object); derr != nil {
			st.err = fmt.Errorf("could not delete it on origin (%s), nor drop the pin: %s", git.Reason(pushErr),
				git.Reason(derr))
			return st
		}
		st.pinned = false
		if now != r.Object {
			st.kept = "it moved on origin after the plan was made"
		} else {
			st.err = fmt.Errorf("could not delete it on origin: %s", git.Reason(pushErr))
		}
	}
	return st
}

// Render writes the plan: what goes, what stays for being young, and what
// is kept and why.
func (p RefsPlan) Render(w io.Writer) {
	fmt.Fprintf(w, "backups are the branches and tags named\n  %s\n"+
		"one another ref contains goes; one holding commits of its own goes\n"+
		"once it is older than %s\n\n", strings.Join(p.Patterns, " "), p.Age)
	var sel, young, kept [][]string
	for _, r := range p.Rows {
		why := ""
		switch {
		case r.ContainedIn != "":
			why = "contained in " + r.ContainedIn
		case r.Unique != nil:
			why = commitsWord(*r.Unique) + " no other ref has"
		}
		when := ""
		if r.Date > 0 {
			when = time.Unix(r.Date, 0).Format("2006-01-02")
		}
		row := []string{"  " + r.ID, when, why, r.Subject}
		switch {
		case r.Selected:
			sel = append(sel, row)
		case r.Category == RefKept:
			kept = append(kept, []string{"  " + r.ID, strings.Join(r.Kept, ", "), why})
		default:
			young = append(young, row)
		}
	}
	if len(sel) == 0 {
		fmt.Fprintln(w, "No backups to sweep.")
	} else {
		fmt.Fprintf(w, "Will be swept, %s, each pinned under %s so it can be restored:\n",
			refCount(len(sel)), SweptPrefix)
		_ = printTable(w, sel)
	}
	if len(young) > 0 {
		fmt.Fprintf(w, "\nNot selected (younger than %s, or left out by --only):\n", p.Age)
		_ = printTable(w, young)
	}
	if len(kept) > 0 {
		fmt.Fprintln(w, "\nKept:")
		_ = printTable(w, kept)
	}
	fmt.Fprintln(w)
}
