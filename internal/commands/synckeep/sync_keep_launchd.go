package synckeep

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/commands/synckeep/keepstate"
)

// keepEnvNames is what start captures from the shell it runs in. PATH finds
// git and claude (a keeper that cannot list sessions changes nothing); HOME
// finds ~/.ssh/config and ~/.gitconfig; the rest is how git push
// authenticates: the ssh agent socket, or the GIT_SSH_COMMAND an agent
// launcher set. That is agent-ssh, which asks fnox's daemon for the key over a
// socket under TMPDIR, and launchd sets no TMPDIR of its own. No secret is
// captured: a plist is a plain file in the home directory.
var keepEnvNames = []string{"PATH", "HOME", "TMPDIR", "SSH_AUTH_SOCK", "GIT_SSH_COMMAND"}

// keepPlist is the job as a launchd property list.
func keepPlist(j keepstate.KeepJob) string {
	args := []string{j.Exe, "sync", "keep", "once", "--every", keepstate.FmtEvery(j.Every)}
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

// KeepStartOptions tunes Start.
type KeepStartOptions struct {
	Every  time.Duration
	NoPush bool
	// Getenv reads the shell's environment; nil is os.Getenv.
	Getenv func(string) string
	Now    func() time.Time
}

// ErrNoLaunchd is start's and stop's answer off macOS. The command exits 3
// with it: not a failure of the repository, a platform with no launchd.
var ErrNoLaunchd = errors.New("not supported on this platform; run wt sync keep once from cron")

// keepStarter is what every job a start installs shares: the interval, the
// wt binary and what it captured from the shell.
type keepStarter struct {
	every  time.Duration
	noPush bool
	exe    string
	env    map[string]string
	now    func() time.Time
}

// newKeepStarter checks the platform and the interval and captures the
// shell's environment, once for however many repositories follow.
func newKeepStarter(opts KeepStartOptions) (*keepStarter, error) {
	if keepstate.KeepGOOS != "darwin" {
		return nil, ErrNoLaunchd
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	s := &keepStarter{every: opts.Every, noPush: opts.NoPush, now: time.Now, env: map[string]string{}}
	if opts.Now != nil {
		s.now = opts.Now
	}
	if s.every <= 0 {
		s.every = keepstate.KeepDefaultInterval
	}
	if s.every < time.Minute {
		return nil, fmt.Errorf("--every %s is under a minute; a pass fetches trunk every time", s.every)
	}
	// The path as invoked, symlink and all: an install that points a stable
	// name at the current version keeps the job on the current version.
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	s.exe = exe
	for _, n := range keepEnvNames {
		if v := getenv(n); v != "" {
			s.env[n] = v
		}
	}
	return s, nil
}

// job is ctx's job as s would install it.
func (s *keepStarter) job(ctx *kit.Context) (keepstate.KeepJob, error) {
	job, err := keepstate.KeepJobFor(ctx)
	if err != nil {
		return keepstate.KeepJob{}, err
	}
	gitDir, _, _, err := keepstate.KeepPaths(ctx)
	if err != nil {
		return keepstate.KeepJob{}, err
	}
	job.Exe, job.GitDir, job.Every, job.NoPush, job.Env = s.exe, gitDir, s.every, s.noPush, s.env
	return job, nil
}

// install writes the job's plist, loads it, and records in ctx's state file
// when the first pass is due, which it returns.
func (s *keepStarter) install(ctx *kit.Context, job keepstate.KeepJob) (time.Time, error) {
	_, _, statePath, err := keepstate.KeepPaths(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if err := os.MkdirAll(filepath.Dir(job.PlistPath), 0o755); err != nil {
		return time.Time{}, err
	}
	// Owner-only: the plist carries the shell's GIT_SSH_COMMAND verbatim,
	// whatever that names. launchd asks only that a user agent's plist be
	// the user's own and not writable by anyone else.
	if err := os.WriteFile(job.PlistPath, []byte(keepPlist(job)), 0o600); err != nil {
		return time.Time{}, err
	}
	if err := keepstate.Launchctl.Load(job.PlistPath); err != nil {
		_ = os.Remove(job.PlistPath)
		return time.Time{}, err
	}
	// The state file says when the first pass is due, for the table line;
	// what an earlier keeper recorded is kept.
	st, _, err := keepstate.ReadKeepState(statePath)
	if err != nil {
		return time.Time{}, err
	}
	st.Interval = keepstate.FmtEvery(s.every)
	st.NextRun = s.now().Add(s.every)
	st.Job = job.Label
	if err := writeKeepState(statePath, st); err != nil {
		return time.Time{}, err
	}
	return st.NextRun, nil
}

// Start installs the keeper as a launchd job for this repository
// and loads it. It refuses when one is already installed.
func Start(ctx *kit.Context, opts KeepStartOptions, w io.Writer) error {
	s, err := newKeepStarter(opts)
	if err != nil {
		return err
	}
	job, err := s.job(ctx)
	if err != nil {
		return err
	}
	if _, err := os.Stat(job.PlistPath); err == nil {
		return fmt.Errorf("a keeper is already installed here: %s; wt sync keep status", job.PlistPath)
	}
	next, err := s.install(ctx, job)
	if err != nil {
		return err
	}
	_, logPath, _, err := keepstate.KeepPaths(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "installed %s\n", job.PlistPath)
	fmt.Fprintf(w, "runs wt sync keep once every %s in %s; first at %s\n", keepstate.FmtEvery(s.every), ctx.Repo.MainRoot, keepstate.KeepClock(next, s.now()))
	fmt.Fprintln(w, keepAuthLine(job))
	fmt.Fprintf(w, "log %s · wt sync keep status\n", logPath)
	return nil
}

// keepAuthLine says how the job's push authenticates, from what start
// captured: the ssh command an agent launcher set, the shell's agent
// socket, or neither.
func keepAuthLine(j keepstate.KeepJob) string {
	if j.NoPush {
		return "rebases only (--no-push); the push commands go to the log"
	}
	// An agent launcher's agent-ssh, which reads the agent key from fnox's
	// cache on every push.
	cmd := j.Env["GIT_SSH_COMMAND"]
	switch {
	case strings.HasSuffix(cmd, "/agent-ssh"):
		return "pushes through this shell's GIT_SSH_COMMAND, an agent launcher's agent-ssh: it works while fnox has the agent key cached, and after a reboot or a day unused a push asks for Touch ID, which this job cannot wait for; run wt sync keep start from your own shell to push through your ssh agent"
	case cmd != "":
		return "pushes through this shell's GIT_SSH_COMMAND"
	case j.Env["SSH_AUTH_SOCK"] != "":
		return "pushes through this shell's ssh agent (SSH_AUTH_SOCK captured; 1Password may ask to approve the key)"
	}
	return "pushes with no ssh agent: only a key ~/.ssh/config names with no passphrase will work"
}

// Stop unloads the keeper's job and removes its plist. The log and
// the state file stay, so wt sync still says when the repository was last
// kept.
func Stop(ctx *kit.Context, w io.Writer) error {
	if keepstate.KeepGOOS != "darwin" {
		return ErrNoLaunchd
	}
	job, err := keepstate.KeepJobFor(ctx)
	if err != nil {
		return err
	}
	installed, ok := keepJobPresent(job)
	if !ok {
		return errors.New("no keeper is installed here; wt sync keep start")
	}
	if err := stopKeepJob(job, installed); err != nil {
		return err
	}
	if !installed {
		fmt.Fprintf(w, "stopped %s; its plist was already gone\n", job.Label)
		return nil
	}
	fmt.Fprintf(w, "stopped %s; removed %s\n", job.Label, job.PlistPath)
	return nil
}

// keepJobPresent says whether the job's plist is there, and whether there is
// anything to stop at all: a plist, or a job launchd still has loaded
// because its plist was removed by hand (it stays until logout).
func keepJobPresent(job keepstate.KeepJob) (installed, present bool) {
	_, statErr := os.Stat(job.PlistPath)
	installed = statErr == nil
	return installed, installed || keepstate.Launchctl.Loaded(job.Label)
}

// stopKeepJob boots the job out by its label, plist or no plist, and
// removes the plist when there is one.
func stopKeepJob(job keepstate.KeepJob, installed bool) error {
	if err := keepstate.Launchctl.Unload(job.Label, job.PlistPath); err != nil {
		return err
	}
	if !installed {
		return nil
	}
	return os.Remove(job.PlistPath)
}
