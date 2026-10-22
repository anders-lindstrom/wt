package synccmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
)

// KeepGOOS is the platform the keeper's start and stop check for launchd;
// tests set it.
var KeepGOOS = runtime.GOOS

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
}

// KeepJobFor names the job for ctx's repository: a label from the
// repository's name and a hash of the main checkout's path, so two checkouts
// of one repository get two jobs, under ~/Library/LaunchAgents.
func KeepJobFor(ctx *kit.Context) (KeepJob, error) {
	home, err := os.UserHomeDir()
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
	}, nil
}

// launchctlDriver runs launchctl, found on the PATH each time so a test can
// stand in for it.
type launchctlDriver struct{}

// Launchctl is the launchctl wt drives; tests stand in for it.
var Launchctl launchctlDriver

func (launchctlDriver) run(args ...string) (string, error) {
	exe, err := exec.LookPath("launchctl")
	if err != nil {
		return "", errors.New("launchctl is not on the PATH")
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

func (d launchctlDriver) domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// Loaded asks launchd whether the job is in the user's domain.
func (d launchctlDriver) Loaded(label string) bool {
	_, err := d.run("print", d.domain()+"/"+label)
	return err == nil
}

// Load bootstraps the plist into the user's domain, falling back to the
// older load verb where bootstrap is refused.
func (d launchctlDriver) Load(plist string) error {
	_, err := d.run("bootstrap", d.domain(), plist)
	if err == nil {
		return nil
	}
	if _, lerr := d.run("load", plist); lerr == nil {
		return nil
	}
	return fmt.Errorf("launchctl bootstrap: %v", err)
}

// Unload takes the job out of the user's domain; a job that is not there
// is not an error, the plist is going anyway.
func (d launchctlDriver) Unload(label, plist string) error {
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
