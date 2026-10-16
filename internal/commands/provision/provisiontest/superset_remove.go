package provisiontest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anders-lindstrom/wt/internal/commands/kit"
	"github.com/anders-lindstrom/wt/internal/superset"
)

// SupersetAt writes a `superset` whose one live worktree workspace, w1, is
// at path, beside the main checkout's own "local" one, and points
// provision.ProbeSuperset at it. deleteBody is what `ws delete` does; before it runs
// the fake notes in the log whether path still exists and whether git still
// lists it, so a test can prove the delete came after both were gone.
func SupersetAt(t *testing.T, ctx *kit.Context, path, deleteBody string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	exe := filepath.Join(dir, "superset")
	list := `[{"id":"w0","type":"local","worktreePath":"` + ctx.Repo.MainRoot + `","archivedAt":null},` +
		`{"id":"w1","type":"worktree","branch":"b","worktreePath":"` + path + `","archivedAt":null}]`
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + log + "\n" +
		"case \"$1 $2\" in\n" +
		"  'ws list') echo '" + list + "' ;;\n" +
		"  'ws delete')\n" +
		"    [ -e '" + path + "' ] && echo 'DIR STILL THERE' >> " + log + "\n" +
		"    git -C '" + ctx.Repo.MainRoot + "' worktree list --porcelain | grep -q '/" + filepath.Base(path) + "$' && echo 'STILL LISTED' >> " + log + "\n" +
		"    " + deleteBody + " ;;\n" +
		"  'projects list') echo '[{\"id\":\"p1\",\"name\":\"demo\",\"path\":\"" + ctx.Repo.MainRoot + "\"}]' ;;\n" +
		"  'ws create') echo '{\"workspace\":{\"id\":\"w2\"},\"alreadyExists\":false}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	Stub(t, superset.Status{Exe: exe, Running: true})
	return log
}
