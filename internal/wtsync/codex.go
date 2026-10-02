package wtsync

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/repo"
)

// What a session is a session of, as --json's kind names it.
const (
	ToolClaude = "claude"
	ToolCodex  = "codex"
)

// codexRole is what a codex process is to a session.
type codexRole int

const (
	// codexNone is a codex that works in no directory: login, completion,
	// the exec-server, and every command that is not Codex at all.
	codexNone codexRole = iota
	// codexRunner is one session in its own process, working where the
	// process stands: codex exec, codex review, and the interactive codex.
	codexRunner
	// codexHost is an app-server: any number of threads, each with a
	// directory of its own that the process's says nothing about. The
	// desktop app, the managed daemon and the Claude Code plugin run one.
	codexHost
)

// Codex's options that take a value, which is then not the subcommand.
var codexValueFlags = map[string]bool{
	"-c": true, "--config": true, "--enable": true, "--disable": true, "--remote": true,
	"--remote-auth-token-env": true, "-i": true, "--image": true, "-m": true, "--model": true,
	"--local-provider": true, "-p": true, "--profile": true, "-s": true, "--sandbox": true,
	"-a": true, "--ask-for-approval": true, "-C": true, "--cd": true, "--add-dir": true,
}

// Codex's subcommands that are not a session working in a directory.
var codexOtherCommands = map[string]bool{
	"agents": true, "login": true, "logout": true, "mcp": true, "plugin": true, "remote-control": true,
	"app": true, "completion": true, "update": true, "doctor": true, "sandbox": true, "debug": true,
	"apply": true, "a": true, "queue": true, "archive": true, "delete": true, "migrate-rollouts": true,
	"unarchive": true, "cloud": true, "exec-server": true, "features": true, "help": true,
}

// classifyCodex reads a command line. Codex is a program called codex, or a
// node or bun running a script called codex: a command that only has the
// word in a path or an argument is not. The first argument that is not an
// option is the subcommand, and anything that is not a known one is a
// prompt, so a subcommand this does not know counts as an interactive
// session rather than as nothing. exec is true for the commands that run
// one turn and exit, cd is --cd's directory.
//
// exact says argv is the arguments as given. Split on spaces instead, a
// prompt is many words and its first may be a subcommand's name, so a
// command that would be ruled out with more words after it is a session.
func classifyCodex(argv []string, exact bool) (role codexRole, exec bool, cd string) {
	if len(argv) == 0 {
		return codexNone, false, ""
	}
	args := argv[1:]
	switch base := filepath.Base(argv[0]); {
	case base == "codex", strings.HasPrefix(base, "codex-x86_64-"), strings.HasPrefix(base, "codex-aarch64-"):
	case base == "node" || base == "nodejs" || base == "bun":
		// The script is the first path among node's own arguments.
		script := -1
		for i, arg := range args {
			if name := filepath.Base(arg); name == "codex" || name == "codex.js" {
				script = i
				break
			}
			if !strings.HasPrefix(arg, "-") && strings.Contains(arg, "/") {
				break
			}
		}
		if script < 0 {
			return codexNone, false, ""
		}
		args = args[script+1:]
	default:
		return codexNone, false, ""
	}
	// next is the argument right after the subcommand.
	command, next, words := "", "", 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			// Everything after it is the prompt.
			if rest := args[i+1:]; len(rest) > 0 && command != "" {
				words += len(rest)
			} else if len(rest) > 0 {
				command, words = "\x00prompt", len(rest)-1
			}
			break
		}
		if dir, ok := strings.CutPrefix(arg, "--cd="); ok {
			cd = dir
		}
		switch {
		case (arg == "-C" || arg == "--cd") && i+1 < len(args):
			i++
			cd = args[i]
		case codexValueFlags[arg]:
			i++
		case strings.HasPrefix(arg, "-"):
		case command == "":
			command = arg
			if i+1 < len(args) {
				next = args[i+1]
			}
		default:
			words++
		}
	}
	prompt := !exact && words > 0
	switch {
	case command == "app-server" || command == "mcp-server":
		// Its own options take values; a word right after it is the
		// daemon's tooling, as in app-server daemon ….
		if next == "" || strings.HasPrefix(next, "-") {
			return codexHost, false, ""
		}
		if exact || next == "daemon" {
			return codexNone, false, ""
		}
	case codexOtherCommands[command] && !prompt:
		return codexNone, false, ""
	}
	return codexRunner, command == "exec" || command == "e" || command == "review", cd
}

// codexSlack is how much earlier than the process that owns it a rollout may
// seem to have been written: ps reports a start in whole seconds.
const codexSlack = 5 * time.Second

// codexHostWindow is how far back an app-server's threads are looked for. A
// turn that has written nothing for this long is not one in flight, so a
// thread whose own app-server died under it stops counting after this long
// even with another app-server alive.
const codexHostWindow = 24 * time.Hour

type codexProcess struct {
	Process
	exec bool
	// dir is where it works; placed is false when nobody can tell.
	dir    string
	placed bool
	pids   []int
}

// codexFamily is what a thread's root says about who runs it.
type codexFamily struct {
	root codexThread
	// hosted is a thread an app-server runs; exec one of a codex exec.
	// Neither is a thread of an interactive codex in its own process.
	hosted, exec bool
}

// codexSessions is the Codex sessions on the machine other than the one wt
// runs under, from the process table and Codex's own session logs.
//
// A codex process working in a directory is a session there. codex exec is
// busy for as long as it lives. An interactive codex is idle when it has a
// thread of its own in its directory, written since it started, and no
// thread it could be running has a turn open. It is busy when one has, or
// when it has no thread that can be read: not finding the log is not
// knowing, never idle.
//
// A thread an app-server runs has no process of its own, so it is a session
// only while its turn is open and an app-server that could be running it is
// alive. An open turn with nothing alive behind it is a crash and nobody.
func codexSessions(procs []Process, ancestors map[int]bool) ([]Agent, error) {
	uid := os.Getuid()
	var runners []*codexProcess
	var hosts []Process
	for _, p := range procs {
		if p.UID != uid {
			continue
		}
		switch role, exec, cd := classifyCodex(p.Argv, p.Exact); role {
		case codexRunner:
			runners = append(runners, &codexProcess{Process: p, exec: exec, dir: cd, pids: []int{p.PID}})
		case codexHost:
			hosts = append(hosts, p)
		}
	}
	if len(runners) == 0 && len(hosts) == 0 {
		return nil, nil
	}
	runners, err := placeRunners(runners, procs)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	full := now
	for _, r := range runners {
		if start := r.Started.Add(-codexSlack); start.Before(full) {
			full = start
		}
	}
	since, underHost := full, false
	for _, h := range hosts {
		underHost = underHost || ancestors[h.PID]
		if start := hostWindow(h, now); start.Before(since) {
			since = start
		}
	}
	threads, err := loadCodexThreads(codexHome(), since, full)
	if err != nil {
		return nil, fmt.Errorf("cannot read Codex's session logs: %w", err)
	}
	byID := map[string]codexThread{}
	for i := range threads {
		threads[i].Cwd = resolved(threads[i].Cwd)
		if !threads[i].Unplaced {
			byID[threads[i].ID] = threads[i]
		}
	}
	families := make([]codexFamily, len(threads))
	for i, t := range threads {
		root := codexRoot(byID, t)
		families[i] = codexFamily{root: root, hosted: appServerRuns(root),
			exec: root.Source == "exec" || (root.Source == "subagent" && root.Originator == "codex_exec")}
	}
	open := func(t codexThread) bool { return t.Turn == turnOpen || t.Turn == turnUnknown }
	runs := func(r *codexProcess, t codexThread) bool { return !t.Modified.Before(r.Started.Add(-codexSlack)) }
	dirs := map[string]bool{}
	for _, r := range runners {
		if r.placed {
			dirs[r.dir] = true
		}
	}

	var sessions []Agent
	for _, r := range runners {
		a := Agent{Tool: ToolCodex, ID: "codex-" + strconv.Itoa(r.PID), Name: "codex", Cwd: r.dir, PID: r.PID,
			Status: "busy", pids: r.pids}
		if r.exec {
			a.Name = "codex exec"
		}
		if !r.placed {
			// Where it stands cannot be read, so it is wherever a thread
			// it could be running says it is.
			at := map[string]bool{}
			for i, t := range threads {
				if t.Unplaced || !runs(r, t) || families[i].hosted || families[i].exec != r.exec || at[t.Cwd] {
					continue
				}
				at[t.Cwd] = true
				a.Cwd = t.Cwd
				sessions = append(sessions, a)
			}
			if len(at) == 0 {
				return nil, fmt.Errorf("cannot read the working directory of codex process %d", r.PID)
			}
			continue
		}
		if !r.exec {
			own, busy := false, false
			for i, t := range threads {
				f := families[i]
				switch {
				case !runs(r, t) || (!t.Unplaced && f.exec):
					// Not its thread: a codex exec has a process of its own.
				case t.Unplaced:
					busy = busy || open(t)
				case filepath.Clean(t.Cwd) == r.dir:
					own, busy = true, busy || open(t)
					a.viaHost = a.viaHost || f.hosted
				case !f.hosted && !dirs[filepath.Clean(t.Cwd)]:
					// A thread recorded elsewhere with no codex there: it
					// may be one this codex resumed.
					busy = busy || open(t)
				}
			}
			if own && !busy {
				a.Status = "idle"
			}
		}
		sessions = append(sessions, a)
	}

	listed := map[string]bool{}
	for i, t := range threads {
		if !open(t) || (!t.Unplaced && !families[i].hosted) {
			continue
		}
		var alive []int
		for _, h := range hosts {
			if !t.Modified.Before(hostWindow(h, now)) {
				alive = append(alive, h.PID)
			}
		}
		if len(alive) == 0 {
			continue
		}
		if t.Unplaced {
			return nil, fmt.Errorf("cannot read the Codex session log %s", t.Path)
		}
		root := families[i].root
		key := root.ID + "\x00" + t.Cwd
		if listed[key] {
			continue
		}
		listed[key] = true
		a := Agent{Tool: ToolCodex, ID: root.ID, Name: "codex", Cwd: t.Cwd, Status: "busy", pids: alive, hosted: true}
		if root.Originator != "" {
			a.Name = "codex (" + root.Originator + ")"
		}
		if len(alive) == 1 {
			a.PID = alive[0]
		}
		sessions = append(sessions, a)
	}
	return withoutCallingCodex(sessions, ancestors, underHost), nil
}

// hostWindow is the earliest a thread an app-server may be running was last
// written: since the app-server started, and within codexHostWindow.
func hostWindow(h Process, now time.Time) time.Time {
	start := h.Started.Add(-codexSlack)
	if floor := now.Add(-codexHostWindow); start.Before(floor) {
		return floor
	}
	return start
}

// placeRunners gives each runner its directory and folds a codex whose
// parent is a codex in the same directory into it: the npm launcher and the
// binary it starts are one session. A runner that exited while it was being
// looked at is dropped.
//
// A codex that hardens itself may not let its working directory be read.
// Its launcher's is the same, and failing that its code-mode helper's; with
// neither it comes back unplaced.
func placeRunners(runners []*codexProcess, procs []Process) ([]*codexProcess, error) {
	pids := make([]int, len(runners))
	byPID := map[int]*codexProcess{}
	for i, r := range runners {
		pids[i] = r.PID
		byPID[r.PID] = r
	}
	cwds, denied, err := processCwds(pids)
	if err != nil {
		return nil, err
	}
	var helpers []int
	helperOf := map[int]int{}
	for _, p := range procs {
		if denied[p.PPID] && len(p.Argv) > 0 && filepath.Base(p.Argv[0]) == "codex-code-mode-host" {
			helpers = append(helpers, p.PID)
			helperOf[p.PPID] = p.PID
		}
	}
	helperCwds, _, err := processCwds(helpers)
	if err != nil {
		return nil, err
	}
	var live []*codexProcess
	for _, r := range runners {
		cwd, ok := cwds[r.PID]
		if !ok && !denied[r.PID] {
			continue
		}
		if !ok {
			if parent := byPID[r.PPID]; parent != nil {
				cwd, ok = cwds[parent.PID]
			}
		}
		if !ok {
			cwd, ok = helperCwds[helperOf[r.PID]]
		}
		switch {
		case r.dir != "" && filepath.IsAbs(r.dir):
			r.placed = true
		case ok && r.dir == "":
			r.dir, r.placed = cwd, true
		case ok:
			r.dir, r.placed = filepath.Join(cwd, r.dir), true
		}
		if r.placed {
			r.dir = filepath.Clean(resolved(r.dir))
		}
		live = append(live, r)
	}
	var tops []*codexProcess
	for _, r := range live {
		top := r
		for parent := byPID[top.PPID]; r.placed && parent != nil && parent.placed && parent.dir == r.dir && parent != r; parent = byPID[top.PPID] {
			top = parent
		}
		if top == r {
			tops = append(tops, r)
			continue
		}
		top.pids = append(top.pids, r.PID)
		top.exec = top.exec || r.exec
	}
	return tops, nil
}

// codexRoot is the thread t was spawned from, through however many
// subagents, as far as the threads read reach.
func codexRoot(byID map[string]codexThread, t codexThread) codexThread {
	for range len(byID) {
		parent, ok := byID[t.Parent]
		if t.Parent == "" || !ok {
			break
		}
		t = parent
	}
	return t
}

// appServerRuns reports whether a thread is one an app-server runs rather
// than a codex process of its own. A subagent whose parent was not read is
// judged by who started the family.
func appServerRuns(root codexThread) bool {
	switch root.Source {
	case "exec", "cli":
		return false
	case "subagent":
		return root.Originator != "codex_exec" && root.Originator != "codex_cli_rs"
	}
	return true
}

// withoutCallingCodex drops the Codex session wt runs under. A codex
// process above wt is that session. Under an app-server (underHost) the
// thread is not named anywhere wt can read, so it is taken to be the one
// working where wt was started: every thread that app-server may run there,
// and an interactive codex there whose thread an app-server runs, which is
// that thread's terminal. A second such session in the same directory goes
// with it; one anywhere else still counts.
func withoutCallingCodex(sessions []Agent, ancestors map[int]bool, underHost bool) []Agent {
	here := ""
	if wd, err := os.Getwd(); err == nil {
		here = resolved(wd)
	}
	var others []Agent
	for _, a := range sessions {
		above := slices.ContainsFunc(a.pids, func(pid int) bool { return ancestors[pid] })
		atHere := here != "" && repo.Inside(a.Cwd, here, false)
		switch {
		case a.hosted && above && atHere:
			continue
		case !a.hosted && (above || (underHost && atHere && a.viaHost)):
			continue
		}
		others = append(others, a)
	}
	return others
}

// resolved is path with its symlinks followed, or path when it cannot be.
func resolved(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}
