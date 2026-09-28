package quarantine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestMoveRenamesADirectoryWithWhatIsInIt(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "a"), filepath.Join(root, "b")
	mkdir(t, filepath.Join(from, "sub"))
	if err := os.WriteFile(filepath.Join(from, "sub", "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := Identify(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := Move(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(to, "sub", "f")); err != nil {
		t.Error("the contents must move with it")
	}
	after, _ := Identify(to)
	if after != before {
		t.Errorf("a rename keeps the inode: %+v then %+v", before, after)
	}
}

// rename(2) replaces an empty directory without a word; a move must not.
func TestMoveNeverReplacesWhatIsThere(t *testing.T) {
	root := t.TempDir()
	from, to := filepath.Join(root, "a"), filepath.Join(root, "b")
	mkdir(t, from)
	mkdir(t, to)
	if err := Move(from, to); err == nil {
		t.Fatal("want a refusal: the destination is there")
	}
	if _, err := os.Stat(from); err != nil {
		t.Error("the source must stay where it was")
	}
}

func TestCheckRefusesAFolderThatExists(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mkdir(t, src)
	err := Check(root, src)
	if err == nil || !strings.Contains(err.Error(), "is there already") {
		t.Errorf("want a refusal naming the folder: %v", err)
	}
}

func TestCheckRefusesAMissingParent(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	mkdir(t, src)
	if err := Check(filepath.Join(root, "nope", "q"), src); err == nil {
		t.Error("want a refusal: the parent is not there")
	}
}

// Only a rename moves nothing but names; across volumes it would be a copy.
func TestCheckRefusesAnotherVolume(t *testing.T) {
	root := t.TempDir()
	src, other := filepath.Join(root, "src"), filepath.Join(root, "other")
	mkdir(t, src)
	mkdir(t, other)
	orig := DeviceOf
	t.Cleanup(func() { DeviceOf = orig })
	DeviceOf = func(p string) (uint64, error) {
		if p == other {
			return 99, nil
		}
		return orig(p)
	}
	err := Check(filepath.Join(other, "q"), src)
	if err == nil || !strings.Contains(err.Error(), "volume") {
		t.Errorf("want a volume refusal: %v", err)
	}
	if err := Check(filepath.Join(root, "q"), src); err != nil {
		t.Errorf("same volume: %v", err)
	}
}

// A volume is where a path leads: a parent that is a symlink onto another
// volume is on that volume.
func TestDeviceOfFollowsASymlink(t *testing.T) {
	link := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink("/dev", link); err != nil {
		t.Fatal(err)
	}
	got, err := DeviceOf(link)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := DeviceOf("/dev")
	if got != want {
		t.Errorf("device %d, want /dev's %d", got, want)
	}
}
