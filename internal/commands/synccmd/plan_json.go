package synccmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/anders-lindstrom/wt/internal/agents"
	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
	"github.com/anders-lindstrom/wt/schema"
)

// Why wt up would not start on a worktree, as --json names it.
const (
	IneligibleMainCheckout  = "mainCheckout"
	IneligibleNoConfig      = "noConfiguration"
	IneligibleConfigInvalid = "configurationInvalid"
	IneligibleDetached      = "detachedHead"
	IneligibleOnTrunk       = "onTrunk"
	IneligibleHandedOver    = "handedOver"
	IneligibleNotAWorktree  = "notAWorktree"
	// IneligibleInTrunk is a branch with no commit trunk lacks: it is an
	// ancestor of the trunk wt up rebases onto, so there is nothing of it to
	// rebase. Its own remote being gone changes nothing.
	IneligibleInTrunk = "alreadyOnTrunk"
	// IneligibleOwnDiverged is a branch of the stack that has diverged from
	// its own remote: wt up --allow-diverged goes on.
	IneligibleOwnDiverged = "ownRemoteDiverged"
	// IneligibleOwnBehind is a branch of the stack behind its own remote, in
	// a checkout it cannot be fast-forwarded in.
	IneligibleOwnBehind = "ownRemoteBehind"
)

// PlanWorktree is one worktree as the plan reports it.
type PlanWorktree struct {
	Work   string `json:"work"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	IsMain bool   `json:"isMain"`
	State  string `json:"state"`
	Behind *int   `json:"behind"`
	Ahead  *int   `json:"ahead"`
	// OwnRemote is the branch against its own remote, as last fetched.
	OwnRemote OwnRemote `json:"ownRemote"`
}

// PlanSession is an agent session in one of the plan's worktrees; Kind is
// "claude" or "codex".
type PlanSession struct {
	Work  string `json:"work"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	State string `json:"state"`
}

// PlanStackMember is a worktree wt up would move.
type PlanStackMember struct {
	Work   string `json:"work"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
}

// PlanStack is a worktree wt up would move, as the plan reports it: the
// member, its branch against its own remote, and the deferred steps trunk
// declares, the same on every member.
type PlanStack struct {
	PlanStackMember
	OwnRemote        OwnRemote      `json:"ownRemote"`
	DeferredDeclared []DeclaredStep `json:"deferredDeclared"`
}

// UpPlan is the one object wt status <work> --json prints: what wt up would
// start on, computed from local state alone.
type UpPlan struct {
	Schema             int           `json:"schema"`
	SchemaVersion      string        `json:"schemaVersion"`
	Command            string        `json:"command"`
	Token              *string       `json:"token"`
	Trunk              *string       `json:"trunk"`
	TrunkSource        *string       `json:"trunkSource"`
	TrunkRef           *string       `json:"trunkRef"`
	TrunkRefExists     bool          `json:"trunkRefExists"`
	TrunkTip           *string       `json:"trunkTip"`
	TrunkRefUpdatedAt  *string       `json:"trunkRefUpdatedAt"`
	TrunkSync          *TrunkSync    `json:"trunkSync"`
	Configured         bool          `json:"configured"`
	Worktree           *PlanWorktree `json:"worktree"`
	UpEligible         bool          `json:"upEligible"`
	UpIneligibleCode   *string       `json:"upIneligibleCode"`
	UpIneligibleReason *string       `json:"upIneligibleReason"`
	Stack              []PlanStack   `json:"stack"`
	Sessions           []PlanSession `json:"sessions"`
	SessionsError      *string       `json:"sessionsError"`
}

// UpPlanJSON writes wt status <work> --json. It writes nothing to any repository:
// no fetch, no simulation, no pull-request cache; every git it runs reads.
// A worktree wt up would not start on is a plan with upEligible false and
// the reason, not an error; only a worktree that cannot be found is.
func UpPlanJSON(ctx *kit.Context, arg string, w io.Writer) error {
	p := UpPlan{Schema: 1, SchemaVersion: schema.VersionOf("status"), Command: "status",
		Stack: []PlanStack{}, Sessions: []PlanSession{}}
	trunk := ctx.Config.MainBranch
	ref := "origin/" + trunk
	p.Trunk, p.TrunkSource = kit.Strp(trunk), ctx.TrunkSource()
	p.Configured = ctx.Configured()
	var tip string
	var exists bool
	if trunk != "" {
		p.TrunkRef = kit.Strp(ref)
		// Status only, nothing acts on it: a ref git cannot read reads as absent.
		tip, exists, _ = ctx.Repo.ResolveRef("refs/remotes/" + ref)
		p.TrunkRefExists, p.TrunkTip = exists, kit.Strp(tip)
		p.TrunkRefUpdatedAt = refUpdatedAt(ctx.Repo.MainRoot, "refs/remotes/"+ref)
	}
	// Listed at most once, and only when asked: by the trunk check, when
	// local trunk is checked out, and for the stack's sessions.
	listAgents := sync.OnceValues(func() ([]agents.Agent, error) { return ctx.ListAgents() })
	// What wt up would do to local trunk after its fetch, judged against
	// origin/<trunk> as last fetched: the fetch may bring more.
	p.TrunkSync = SyncLocalTrunk(ctx, tip, TrunkSyncOptions{OptedOut: !ctx.UserConfig().FFTrunk, Agents: listAgents})

	inel := func(code, why string) {
		if p.UpIneligibleCode == nil {
			p.UpIneligibleCode, p.UpIneligibleReason = kit.Strp(code), kit.Strp(why)
		}
	}
	wt, err := kit.Locate(ctx, arg)
	if errors.Is(err, kit.ErrMainCheckout) {
		wt, err = mainWorktree(ctx)
	}
	if err != nil {
		inel(IneligibleNotAWorktree, err.Error())
		return writePlan(w, p)
	}
	work := kit.WorktreeName(ctx, wt.Branch, wt.Path)
	if wt.IsMain {
		work = ctx.Repo.Name
	}
	pw := &PlanWorktree{Work: work, Branch: wt.Branch, Path: wt.Path, IsMain: wt.IsMain, State: "clean",
		OwnRemote: ownRemoteOf(wtsync.OwnRemote{})}
	switch dirty, err := repo.Dirty(wt.Path, false); {
	case err != nil:
		pw.State = "unreadable"
	case dirty:
		pw.State = "dirty"
	}
	base := tip
	if !exists {
		// Status only: a trunk git cannot read leaves ahead and behind unset.
		base, _, _ = ctx.Repo.ResolveRef("refs/heads/" + trunk)
	}
	if wt.Branch != "" && base != "" {
		if a, ok := ctx.Repo.CommitsAhead("refs/heads/"+wt.Branch, base); ok {
			pw.Ahead = &a
		}
		if b, ok := ctx.Repo.CommitsAhead(base, "refs/heads/"+wt.Branch); ok {
			pw.Behind = &b
		}
	}
	p.Worktree = pw

	switch {
	case errors.Is(ctx.ConfigError, config.ErrNoConfig):
		inel(IneligibleNoConfig, kit.OneLine(ctx.ConfigError.Error()))
	case ctx.ConfigError != nil:
		inel(IneligibleConfigInvalid, "the wt configuration does not parse: "+kit.OneLine(ctx.ConfigError.Error()))
	}
	switch {
	case wt.IsMain:
		inel(IneligibleMainCheckout, "the main checkout is trunk's own; wt up moves a worktree")
	case wt.Branch == "":
		inel(IneligibleDetached, "detached HEAD: no branch to rebase")
	case wt.Branch == trunk:
		inel(IneligibleOnTrunk, "this worktree is on trunk itself")
	}
	if gitDir, err := wtsync.GitDir(wt.Path); err == nil {
		if _, ok, _ := wtsync.ReadState(gitDir); ok {
			inel(IneligibleHandedOver, "an earlier wt sync rebase is waiting on you here: wt sync resume or wt sync undo")
		}
	}
	// Every branch against its own remote as last fetched: nothing here
	// fetches. Remotes that cannot be read make every branch unknown.
	own := wtsync.ReadOwnOrUnknown(ctx.Repo.MainRoot, trunk)
	// ownOf is one branch against its own remote, with what of it would keep
	// wt up off the checkout at path.
	ownOf := func(branch, path string) wtsync.OwnRemote {
		o := OwnStateOf(own, branch)
		switch o.State {
		case wtsync.OwnBehind:
			// Unreadable is not clean: a fast-forward needs to know.
			dirty, err := repo.Dirty(path, true)
			listed, _ := listAgents()
			o.Blocks, o.Why = wtsync.OwnBlocks(o, path, dirty || err != nil, agents.SessionsAt(listed, path))
		case wtsync.OwnDiverged:
			// Only a branch wt up would rebase is kept off by it: one on
			// trunk already, or with nothing of its own, is skipped, and
			// nothing is pushed over its remote.
			behind, ok1 := ctx.Repo.CommitsAhead(base, "refs/heads/"+branch)
			ahead, ok2 := ctx.Repo.CommitsAhead(branch, base)
			if base == "" || !ok1 || !ok2 || (behind > 0 && ahead > 0) {
				o.Blocks = wtsync.OwnBlockDiverged
			}
		}
		return o
	}
	// The deferred steps trunk declares, as last fetched: wt up fetches
	// first and reads them from the trunk it then rebases onto. None when
	// trunk declares none, has no declaration, or it cannot be read.
	var declaration *wtsync.Config
	if exists {
		declaration, _ = wtsync.LoadFromRef(ctx.Repo.MainRoot, tip)
	}
	declared := declaredSteps(declaration)
	// The candidate stack, from local refs as of now: what wt up would move.
	stack := []string{}
	if wt.Branch != "" && !wt.IsMain {
		stack = []string{wt.Branch}
		all, err := ctx.Repo.Worktrees()
		if err == nil && base != "" {
			if parents, _, err := wtsync.Parents(ctx.Repo.MainRoot, base, all); err == nil {
				stack = wtsync.Order(parents, wtsync.Members(parents, wt.Branch))
			}
		}
		byBranch := map[string]repo.Worktree{}
		for _, o := range all {
			byBranch[o.Branch] = o
		}
		for _, b := range stack {
			o := byBranch[b]
			p.Stack = append(p.Stack, PlanStack{
				PlanStackMember:  PlanStackMember{Work: kit.WorktreeName(ctx, b, o.Path), Branch: b, Path: o.Path},
				OwnRemote:        ownRemoteOf(ownOf(b, o.Path)),
				DeferredDeclared: declared,
			})
		}
	}
	// One refusal refuses the whole stack, so any member's own remote makes
	// the plan ineligible. A member that cannot be fast-forwarded comes
	// first: --allow-diverged does nothing for it, so a stack with both is
	// not one the flag clears.
	diverged := map[string]string{}
	var behind, apart *PlanStack
	for i := range p.Stack {
		m := &p.Stack[i]
		if m.Branch == wt.Branch {
			pw.OwnRemote = m.OwnRemote
		}
		switch {
		case m.OwnRemote.Blocks == nil:
		case *m.OwnRemote.Blocks == wtsync.OwnBlockDiverged:
			diverged[m.Branch] = *m.OwnRemote.Commit
			if apart == nil {
				apart = m
			}
		case behind == nil:
			behind = m
		}
	}
	switch {
	case behind != nil:
		inel(IneligibleOwnBehind, ownIneligible(*behind, wt.Branch, ownOf(behind.Branch, behind.Path).Why))
	case apart != nil:
		why := ""
		if ownOf(apart.Branch, apart.Path).Elsewhere {
			why = "elsewhere"
		}
		inel(IneligibleOwnDiverged, ownIneligible(*apart, wt.Branch, why))
	}
	// Nothing of the branch is left to bring up: every commit it has is on
	// trunk already. A stack is not judged by its first member alone, and a
	// branch behind or diverged from its own remote keeps those codes above.
	if len(p.Stack) == 1 && pw.Ahead != nil && *pw.Ahead == 0 && wt.Branch != "" && !wt.IsMain {
		onto := trunk
		if p.TrunkRef != nil && exists {
			onto = *p.TrunkRef
		}
		inel(IneligibleInTrunk, fmt.Sprintf("every commit of %s is already on %s: there is nothing to rebase", wt.Branch, onto))
	}
	p.UpEligible = p.UpIneligibleCode == nil
	// A plan held back by divergence alone has a token too: wt up
	// --allow-diverged --expect goes ahead over exactly what it covers.
	if p.UpEligible || *p.UpIneligibleCode == IneligibleOwnDiverged {
		p.Token = kit.Strp(planToken(ctx, trunk, stack, diverged))
	}

	listed, err := listAgents()
	if err != nil {
		p.SessionsError = kit.Strp(err.Error())
	}
	for _, m := range p.Stack {
		for _, a := range agents.SessionsAt(listed, m.Path) {
			state := "busy"
			if a.Idle() {
				state = "idle"
			}
			p.Sessions = append(p.Sessions, PlanSession{Work: m.Work, Name: a.Name, Kind: a.Program(), State: state})
		}
	}
	return writePlan(w, p)
}

// ownIneligible is upIneligibleReason for a member of the stack whose own
// remote keeps wt up off the whole of it: a sentence to show as it stands,
// naming the branch, the remote ref and the counts, and the member when it
// is not the worktree asked about. why is what stops a fast-forward, or for
// a diverged branch "elsewhere" when the remote has all of it in another
// form.
func ownIneligible(m PlanStack, asked, why string) string {
	o := m.OwnRemote
	who := m.Branch
	if m.Branch != asked {
		who = fmt.Sprintf("%s (%s), a worktree in this stack,", m.Branch, m.Work)
	}
	if o.State == string(wtsync.OwnDiverged) && why == "elsewhere" {
		return fmt.Sprintf("%s has diverged from %s: every commit it has (%d) is there in another form, on %s it never had. "+
			"It looks rebased elsewhere and pushed. Take the remote's with git reset --hard %s, or rebase this copy as it stands with --allow-diverged.",
			who, *o.Ref, *o.Ahead, kit.Count(*o.Behind, "commit"), *o.Ref)
	}
	if o.State == string(wtsync.OwnDiverged) {
		return fmt.Sprintf("%s has diverged from %s: it has %s %s lacks, and %s has %s it never had. "+
			"Pull %s in, or rebase it as it stands with --allow-diverged.",
			who, *o.Ref, kit.Count(*o.Ahead, "commit"), *o.Ref, *o.Ref, kit.Count(*o.Behind, "commit"), themOrIt(*o.Behind))
	}
	return fmt.Sprintf("%s is %s behind %s and cannot be fast-forwarded to it: %s.",
		who, kit.Count(*o.Behind, "commit"), *o.Ref, why)
}

func themOrIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

func writePlan(w io.Writer, p UpPlan) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

// refUpdatedAt is when ref last moved, from its reflog: a time that belongs
// to the trunk ref itself, which FETCH_HEAD's does not. Nil with no reflog.
func refUpdatedAt(mainRoot, ref string) *string {
	out, err := git.Run(mainRoot, "reflog", "show", "-1", "--date=iso-strict", "--format=%gd", ref)
	if err != nil {
		return nil
	}
	i, j := strings.Index(out, "@{"), strings.LastIndex(out, "}")
	if i < 0 || j <= i+2 {
		return nil
	}
	t, err := time.Parse(time.RFC3339, out[i+2:j])
	if err != nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// mainWorktree is git's record of the main checkout.
func mainWorktree(ctx *kit.Context) (repo.Worktree, error) {
	all, err := ctx.Repo.Worktrees()
	if err != nil {
		return repo.Worktree{}, err
	}
	for _, wt := range all {
		if wt.IsMain {
			return wt, nil
		}
	}
	return repo.Worktree{}, kit.ErrMainCheckout
}
