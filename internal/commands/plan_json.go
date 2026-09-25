package commands

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/config"
	"github.com/anders-lindstrom/wt/internal/git"
	"github.com/anders-lindstrom/wt/internal/repo"
	"github.com/anders-lindstrom/wt/internal/wtsync"
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
}

// PlanSession is a Claude session in one of the plan's worktrees.
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

// UpPlan is the one object wt status <work> --json prints: what wt up would
// start on, computed from local state alone.
type UpPlan struct {
	Schema             int               `json:"schema"`
	Command            string            `json:"command"`
	Token              *string           `json:"token"`
	Trunk              *string           `json:"trunk"`
	TrunkRef           *string           `json:"trunkRef"`
	TrunkRefExists     bool              `json:"trunkRefExists"`
	TrunkTip           *string           `json:"trunkTip"`
	TrunkRefUpdatedAt  *string           `json:"trunkRefUpdatedAt"`
	Configured         bool              `json:"configured"`
	Worktree           *PlanWorktree     `json:"worktree"`
	UpEligible         bool              `json:"upEligible"`
	UpIneligibleCode   *string           `json:"upIneligibleCode"`
	UpIneligibleReason *string           `json:"upIneligibleReason"`
	Stack              []PlanStackMember `json:"stack"`
	Sessions           []PlanSession     `json:"sessions"`
	SessionsError      *string           `json:"sessionsError"`
}

// UpPlanJSON writes wt status <work> --json. It writes nothing to any repository:
// no fetch, no simulation, no pull-request cache; every git it runs reads.
// A worktree wt up would not start on is a plan with upEligible false and
// the reason, not an error; only a worktree that cannot be found is.
func UpPlanJSON(ctx *Context, arg string, w io.Writer) error {
	p := UpPlan{Schema: 1, Command: "status", Stack: []PlanStackMember{}, Sessions: []PlanSession{}}
	trunk := ctx.Config.MainBranch
	ref := "origin/" + trunk
	p.Trunk, p.TrunkRef = strp(trunk), strp(ref)
	p.Configured = ctx.ConfigError == nil
	tip, exists := ctx.Repo.ResolveRef("refs/remotes/" + ref)
	p.TrunkRefExists, p.TrunkTip = exists, strp(tip)
	p.TrunkRefUpdatedAt = refUpdatedAt(ctx.Repo.MainRoot, "refs/remotes/"+ref)

	inel := func(code, why string) {
		if p.UpIneligibleCode == nil {
			p.UpIneligibleCode, p.UpIneligibleReason = strp(code), strp(why)
		}
	}
	wt, err := Locate(ctx, arg)
	if errors.Is(err, errMainCheckout) {
		wt, err = mainWorktree(ctx)
	}
	if err != nil {
		inel(IneligibleNotAWorktree, err.Error())
		return writePlan(w, p)
	}
	work := worktreeName(ctx, wt.Branch, wt.Path)
	if wt.IsMain {
		work = ctx.Repo.Name
	}
	pw := &PlanWorktree{Work: work, Branch: wt.Branch, Path: wt.Path, IsMain: wt.IsMain, State: "clean"}
	switch dirty, err := repo.Dirty(wt.Path, false); {
	case err != nil:
		pw.State = "unreadable"
	case dirty:
		pw.State = "dirty"
	}
	base := tip
	if !exists {
		base, _ = ctx.Repo.ResolveRef("refs/heads/" + trunk)
	}
	if wt.Branch != "" && base != "" {
		if a, ok := ctx.Repo.CommitsAhead(wt.Branch, base); ok {
			pw.Ahead = &a
		}
		if b, ok := ctx.Repo.CommitsAhead(base, "refs/heads/"+wt.Branch); ok {
			pw.Behind = &b
		}
	}
	p.Worktree = pw

	switch {
	case errors.Is(ctx.ConfigError, config.ErrNoConfig):
		inel(IneligibleNoConfig, "no wt configuration (bin/worktree/worktree.conf); wt init sets it up")
	case ctx.ConfigError != nil:
		inel(IneligibleConfigInvalid, "the wt configuration does not parse: "+oneLine(ctx.ConfigError.Error()))
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
			inel(IneligibleHandedOver, "an earlier wt sync run is waiting on you here: wt sync resume or wt sync undo")
		}
	}
	p.UpEligible = p.UpIneligibleCode == nil

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
			p.Stack = append(p.Stack, PlanStackMember{Work: worktreeName(ctx, b, o.Path), Branch: b, Path: o.Path})
		}
	}
	if p.UpEligible {
		p.Token = strp(planToken(ctx, trunk, stack))
	}

	agents, err := wtsync.ListOtherAgents()
	if err != nil {
		p.SessionsError = strp(err.Error())
	}
	for _, m := range p.Stack {
		for _, a := range wtsync.SessionsAt(agents, m.Path) {
			state := "busy"
			if a.Idle() {
				state = "idle"
			}
			p.Sessions = append(p.Sessions, PlanSession{Work: m.Work, Name: a.Name, Kind: "claude", State: state})
		}
	}
	return writePlan(w, p)
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
func mainWorktree(ctx *Context) (repo.Worktree, error) {
	all, err := ctx.Repo.Worktrees()
	if err != nil {
		return repo.Worktree{}, err
	}
	for _, wt := range all {
		if wt.IsMain {
			return wt, nil
		}
	}
	return repo.Worktree{}, errMainCheckout
}
