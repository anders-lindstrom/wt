package keepstate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

// HasLaunchd says whether m runs launchd, which the keeper's
// start and stop need.
func HasLaunchd(m kit.Machine) bool { return m.Platform() == "darwin" }

// KeepJob is one repository's launchd job: everything the plist is made of.
type KeepJob struct {
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
	// Launchctl loads and unloads the job.
	Launchctl Launchctl
}

// KeepJobFor names the job for ctx's repository: a label from the
// repository's name and a hash of the main checkout's path, so two checkouts
// of one repository get two jobs, under ~/Library/LaunchAgents.
func KeepJobFor(ctx *kit.Context) (KeepJob, error) {
	home, err := ctx.Machine.HomeDir()
	if err != nil {
		return KeepJob{}, err
	}
	sum := sha256.Sum256([]byte(ctx.Repo.MainRoot))
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, ctx.Repo.Name)
	label := "se.wt.sync-keep." + name + "-" + hex.EncodeToString(sum[:4])
	return KeepJob{
		Label:     label,
		PlistPath: filepath.Join(home, "Library", "LaunchAgents", label+".plist"),
		MainRoot:  ctx.Repo.MainRoot,
		Launchctl: Launchctl{Exe: ctx.Machine.Launchctl},
	}, nil
}

// Launchctl runs launchctl: Exe, or the one on the PATH, looked up each
// time, when Exe is "".
type Launchctl struct {
	Exe string
}

func (d Launchctl) run(args ...string) (string, error) {
	exe := d.Exe
	if exe == "" {
		var err error
		if exe, err = exec.LookPath("launchctl"); err != nil {
			return "", errors.New("launchctl is not on the PATH")
		}
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			text = err.Error()
		}
		return "", errors.New(text)
	}
	return strings.TrimSpace(string(out)), nil
}

func (d Launchctl) domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// Loaded asks launchd whether the job is in the user's domain.
func (d Launchctl) Loaded(label string) bool {
	_, err := d.run("print", d.domain()+"/"+label)
	return err == nil
}

// Load bootstraps the plist into the user's domain, falling back to the
// older load verb where bootstrap is refused.
func (d Launchctl) Load(plist string) error {
	_, err := d.run("bootstrap", d.domain(), plist)
	if err == nil {
		return nil
	}
	if _, lerr := d.run("load", plist); lerr == nil {
		return nil
	}
	return fmt.Errorf("launchctl bootstrap: %w", err)
}

// Unload takes the job out of the user's domain; a job that is not there
// is not an error, the plist is going anyway.
func (d Launchctl) Unload(label, plist string) error {
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
	return fmt.Errorf("launchctl bootout: %w", err)
}
