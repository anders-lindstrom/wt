package wtsync

import (
	"strconv"
	"strings"
)

// stageEntry is one record of git's unmerged index, in the form both
// `ls-files -u -z` and `merge-tree --write-tree -z` write:
// "<mode> <oid> <stage>\t<path>".
type stageEntry struct {
	Mode  string
	OID   string
	Stage int
	Path  string
}

// stagedConflict is one conflicted path: the Conflict as far as the index
// describes it, and the blob id of each stage, indexed 1 to 3, empty where
// the stage is absent.
type stagedConflict struct {
	Conflict Conflict
	OID      [4]string
}

// parseStages reads the unmerged index records in records, stopping at the
// first empty record, which is what ends the section in merge-tree's output;
// next is the index just past it, where merge-tree's messages begin. A record
// that is not three fields and a path is skipped rather than guessed at.
func parseStages(records []string) (entries []stageEntry, next int) {
	for i, rec := range records {
		if rec == "" {
			return entries, i + 1
		}
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 {
			continue
		}
		stage, _ := strconv.Atoi(f[2])
		entries = append(entries, stageEntry{Mode: f[0], OID: f[1], Stage: stage, Path: path})
	}
	return entries, len(records)
}

// conflictsFrom groups stage entries by path, in the order the paths first
// appear, and marks the conflicts no strategy may see. A path is Incomplete
// when an entry is not a regular blob (a rename, a mode change, a submodule),
// or when a stage is missing: no base with both sides present is an add/add,
// anything else is a delete or a rename. The mode reason wins, because it
// describes the entry itself rather than the shape of the conflict.
func conflictsFrom(entries []stageEntry) []stagedConflict {
	index := map[string]int{}
	var out []stagedConflict
	for _, e := range entries {
		i, seen := index[e.Path]
		if !seen {
			i = len(out)
			index[e.Path] = i
			out = append(out, stagedConflict{Conflict: Conflict{Path: e.Path}})
		}
		sc := &out[i]
		if e.Stage >= 1 && e.Stage <= 3 {
			sc.OID[e.Stage] = e.OID
		}
		if e.Mode != "100644" && e.Mode != "100755" {
			sc.Conflict.Incomplete = "not a regular file (mode " + e.Mode + ")"
		}
	}
	for i := range out {
		sc := &out[i]
		switch {
		case sc.Conflict.Incomplete != "":
		case sc.OID[1] != "" && sc.OID[2] != "" && sc.OID[3] != "":
		case sc.OID[1] == "" && sc.OID[2] != "" && sc.OID[3] != "":
			sc.Conflict.Incomplete = "both sides added it"
		default:
			sc.Conflict.Incomplete = "one side deleted or renamed it"
		}
	}
	return out
}

// readStages fills a complete conflict's three blobs. Only a complete
// conflict is read: an Incomplete one is refused before any strategy sees it,
// and nothing ever looks at its bytes.
func readStages(dir string, c *Conflict, oid [4]string) error {
	var err error
	if c.Base, err = catFileRaw(dir, oid[1]); err != nil {
		return err
	}
	if c.Trunk, err = catFileRaw(dir, oid[2]); err != nil {
		return err
	}
	c.Branch, err = catFileRaw(dir, oid[3])
	return err
}
