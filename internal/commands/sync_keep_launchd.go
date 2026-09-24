package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// keepGOOS is the platform the keeper's start and stop check for launchd;
// tests set it.
var keepGOOS = runtime.GOOS

// keepJob is one repository's launchd job: everything the plist is made of.
type keepJob struct {
	Label     string
	PlistPath string
	Exe       string // the wt binary, absolute
	MainRoot  string
	GitDir    string
	Every     time.Duration
	NoPush    bool
	// Env is what the job runs with. launchd gives a job neither the shell's
	// PATH nor its ssh settings, so start captures them.
	Env map[string]string
}

// keepEnvNames is what start captures from the shell it runs in. PATH finds
// git and claude (a keeper that cannot list sessions changes nothing); HOME
// finds ~/.ssh/config and ~/.gitconfig; the rest is how git push
// authenticates: the ssh agent socket, or the GIT_SSH_COMMAND an agent
// launcher set, with the key file that command reads. The 1Password service
// token is never captured: a plist is a plain file in the home directory.
var keepEnvNames = []string{"PATH", "HOME", "SSH_AUTH_SOCK", "GIT_SSH_COMMAND", "OP_AGENT_KEYFILE"}

// keepJobFor names the job for ctx's repository: a label from the
// repository's name and a hash of the main checkout's path, so two checkouts
// of one repository get two jobs, under ~/Library/LaunchAgents.
func keepJobFor(ctx *Context) (keepJob, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return keepJob{}, err
	}
	sum := sha256.Sum256([]byte(ctx.Repo.MainRoot))
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, ctx.Repo.Name)
	label := "se.wt.sync-keep." + name + "-" + hex.EncodeToString(sum[:4])
	return keepJob{
		Label:     label,
		PlistPath: filepath.Join(home, "Library", "LaunchAgents", label+".plist"),
		MainRoot:  ctx.Repo.MainRoot,
	}, nil
}

// keepPlist is the job as a launchd property list.
func keepPlist(j keepJob) string {
	args := []string{j.Exe, "sync", "keep", "once", "--every", fmtEvery(j.Every)}
	if j.NoPush {
		args = append(args, "--no-push")
	}
	var b strings.Builder
	esc := func(s string) string {
		var e strings.Builder
		_ = xml.EscapeText(&e, []byte(s))
		return e.String()
	}
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t<string>%s</string>\n", esc(j.Label))
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range args {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", esc(a))
	}
	b.WriteString("\t</array>\n")
	fmt.Fprintf(&b, "\t<key>WorkingDirectory</key>\n\t<string>%s</string>\n", esc(j.MainRoot))
	fmt.Fprintf(&b, "\t<key>StartInterval</key>\n\t<integer>%d</integer>\n", int(j.Every.Seconds()))
	b.WriteString("\t<key>RunAtLoad</key>\n\t<false/>\n")
	// The pass's own output is in the log already; only what wt itself
	// could not start on (a missing binary, a repository that is gone) is
	// worth a file, and that is on stderr.
	b.WriteString("\t<key>StandardOutPath</key>\n\t<string>/dev/null</string>\n")
	fmt.Fprintf(&b, "\t<key>StandardErrorPath</key>\n\t<string>%s</string>\n", esc(filepath.Join(j.GitDir, "wt-sync-keep.err")))
	// Every commit a pass makes is unsigned, whatever ~/.gitconfig says: a
	// signer that asks has nobody to ask under launchd. keep once appends
	// the same on top of whatever stack it finds; here the stack is this.
	env := map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "commit.gpgsign", "GIT_CONFIG_VALUE_0": "false"}
	for n, v := range j.Env {
		env[n] = v
	}
	names := make([]string, 0, len(env))
	for n := range env {
		names = append(names, n)
	}
	sort.Strings(names)
	b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
	for _, n := range names {
		fmt.Fprintf(&b, "\t\t<key>%s</key>\n\t\t<string>%s</string>\n", esc(n), esc(env[n]))
	}
	b.WriteString("\t</dict>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// launchctlDriver runs launchctl, found on the PATH each time so a test can
// stand in for it.
type launchctlDriver struct{}

var launchctl launchctlDriver

func (launchctlDriver) run(args ...string) (string, error) {
	exe, err := exec.LookPath("launchctl")
	if err != nil {
		return "", errors.New("launchctl is not on the PATH")
	}
	out, err := exec.Command(exe, args...).CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			text = err.Error()
		}
		return "", errors.New(text)
	}
	return strings.TrimSpace(string(out)), nil
}

func (d launchctlDriver) domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// loaded asks launchd whether the job is in the user's domain.
func (d launchctlDriver) loaded(label string) bool {
	_, err := d.run("print", d.domain()+"/"+label)
	return err == nil
}

// load bootstraps the plist into the user's domain, falling back to the
// older load verb where bootstrap is refused.
func (d launchctlDriver) load(plist string) error {
	_, err := d.run("bootstrap", d.domain(), plist)
	if err == nil {
		return nil
	}
	if _, lerr := d.run("load", plist); lerr == nil {
		return nil
	}
	return fmt.Errorf("launchctl bootstrap: %v", err)
}

// unload takes the job out of the user's domain; a job that is not there
// is not an error, the plist is going anyway.
func (d launchctlDriver) unload(label, plist string) error {
	_, err := d.run("bootout", d.domain()+"/"+label)
	if err == nil {
		return nil
	}
	if _, lerr := d.run("unload", plist); lerr == nil {
		return nil
	}
	if strings.Contains(err.Error(), "No such process") || strings.Contains(err.Error(), "not find") {
		return nil
	}
	return fmt.Errorf("launchctl bootout: %v", err)
}

// KeepStartOptions tunes SyncKeepStart.
type KeepStartOptions struct {
	Every  time.Duration
	NoPush bool
	// Getenv reads the shell's environment; nil is os.Getenv.
	Getenv func(string) string
	Now    func() time.Time
}

// ErrNoLaunchd is start's and stop's answer off macOS. The command exits 2
// with it: not a failure of the repository, a platform with no launchd.
var ErrNoLaunchd = errors.New("not supported on this platform; run wt sync keep once from cron")

// SyncKeepStart installs the keeper as a launchd job for this repository
// and loads it. It refuses when one is already installed.
func SyncKeepStart(ctx *Context, opts KeepStartOptions, w io.Writer) error {
	if keepGOOS != "darwin" {
		return ErrNoLaunchd
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	every := opts.Every
	if every <= 0 {
		every = KeepDefaultInterval
	}
	if every < time.Minute {
		return fmt.Errorf("--every %s is under a minute; a pass fetches trunk every time", every)
	}
	job, err := keepJobFor(ctx)
	if err != nil {
		return err
	}
	if _, err := os.Stat(job.PlistPath); err == nil {
		return fmt.Errorf("a keeper is already installed here: %s; wt sync keep status", job.PlistPath)
	}
	// The path as invoked, symlink and all: an install that points a stable
	// name at the current version keeps the job on the current version.
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	gitDir, logPath, statePath, err := keepPaths(ctx)
	if err != nil {
		return err
	}
	job.Exe, job.GitDir, job.Every, job.NoPush = exe, gitDir, every, opts.NoPush
	job.Env = map[string]string{}
	for _, n := range keepEnvNames {
		if v := getenv(n); v != "" {
			job.Env[n] = v
		}
	}
	if err := os.MkdirAll(filepath.Dir(job.PlistPath), 0o755); err != nil {
		return err
	}
	// Owner-only: the plist carries the shell's GIT_SSH_COMMAND verbatim,
	// whatever that names. launchd asks only that a user agent's plist be
	// the user's own and not writable by anyone else.
	if err := os.WriteFile(job.PlistPath, []byte(keepPlist(job)), 0o600); err != nil {
		return err
	}
	if err := launchctl.load(job.PlistPath); err != nil {
		_ = os.Remove(job.PlistPath)
		return err
	}
	// The state file says when the first pass is due, for the table line;
	// what an earlier keeper recorded is kept.
	st, _, err := readKeepState(statePath)
	if err != nil {
		return err
	}
	st.Interval = fmtEvery(every)
	st.NextRun = now().Add(every)
	st.Job = job.Label
	if err := writeKeepState(statePath, st); err != nil {
		return err
	}
	fmt.Fprintf(w, "installed %s\n", job.PlistPath)
	fmt.Fprintf(w, "runs wt sync keep once every %s in %s; first at %s\n", fmtEvery(every), ctx.Repo.MainRoot, keepClock(st.NextRun, now()))
	fmt.Fprintln(w, keepAuthLine(job))
	fmt.Fprintf(w, "log %s · wt sync keep status\n", logPath)
	return nil
}

// keepAuthLine says how the job's push authenticates, from what start
// captured: the ssh command an agent launcher set, the shell's agent
// socket, or neither.
func keepAuthLine(j keepJob) string {
	if j.NoPush {
		return "rebases only (--no-push); the push commands go to the log"
	}
	// An agent launcher's key, in either form the launcher uses: the
	// op-agent-ssh wrapper, or a plain ssh -i on the op_agent_ssh.* file.
	cmd := j.Env["GIT_SSH_COMMAND"]
	agentKey := j.Env["OP_AGENT_KEYFILE"] != "" || strings.Contains(cmd, "op-agent-ssh") || strings.Contains(cmd, "op_agent_ssh")
	switch {
	case agentKey:
		return "pushes through this shell's GIT_SSH_COMMAND, an agent launcher's key: it is shredded when that agent exits, and every push after that fails; run wt sync keep start from your own shell to push through your ssh agent"
	case cmd != "":
		return "pushes through this shell's GIT_SSH_COMMAND"
	case j.Env["SSH_AUTH_SOCK"] != "":
		return "pushes through this shell's ssh agent (SSH_AUTH_SOCK captured; 1Password may ask to approve the key)"
	}
	return "pushes with no ssh agent: only a key ~/.ssh/config names with no passphrase will work"
}

// SyncKeepStop unloads the keeper's job and removes its plist. The log and
// the state file stay, so wt sync still says when the repository was last
// kept.
func SyncKeepStop(ctx *Context, w io.Writer) error {
	if keepGOOS != "darwin" {
		return ErrNoLaunchd
	}
	job, err := keepJobFor(ctx)
	if err != nil {
		return err
	}
	_, statErr := os.Stat(job.PlistPath)
	installed := statErr == nil
	// A job whose plist was removed by hand is still loaded until logout;
	// it is booted out by its label, plist or no plist.
	if !installed && !launchctl.loaded(job.Label) {
		return errors.New("no keeper is installed here; wt sync keep start")
	}
	if err := launchctl.unload(job.Label, job.PlistPath); err != nil {
		return err
	}
	if !installed {
		fmt.Fprintf(w, "stopped %s; its plist was already gone\n", job.Label)
		return nil
	}
	if err := os.Remove(job.PlistPath); err != nil {
		return err
	}
	fmt.Fprintf(w, "stopped %s; removed %s\n", job.Label, job.PlistPath)
	return nil
}
