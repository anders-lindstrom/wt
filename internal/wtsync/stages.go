package wtsync

import (
	"bytes"
	"fmt"
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
// appear, keeping git's own id for each stage, and marks the conflicts no
// strategy may see. A path is Incomplete when an entry is not a regular blob
// (a rename, a mode change, a submodule), or when a stage is missing: no base
// with both sides present is an add/add, anything else is a delete or a
// rename. The mode reason wins, because it describes the entry itself rather
// than the shape of the conflict.
func conflictsFrom(entries []stageEntry) []Conflict {
	index := map[string]int{}
	var out []Conflict
	for _, e := range entries {
		i, seen := index[e.Path]
		if !seen {
			i = len(out)
			index[e.Path] = i
			out = append(out, Conflict{Path: e.Path})
		}
		c := &out[i]
		switch e.Stage {
		case 1:
			c.BaseOID = e.OID
		case 2:
			c.TrunkOID = e.OID
		case 3:
			c.BranchOID = e.OID
		}
		if e.Mode != "100644" && e.Mode != "100755" {
			c.Incomplete = "not a regular file (mode " + e.Mode + ")"
		}
	}
	for i := range out {
		c := &out[i]
		switch {
		case c.Incomplete != "":
		case c.BaseOID != "" && c.TrunkOID != "" && c.BranchOID != "":
		case c.BaseOID == "" && c.TrunkOID != "" && c.BranchOID != "":
			c.Incomplete = "both sides added it"
		default:
			c.Incomplete = "one side deleted or renamed it"
		}
	}
	return out
}

// readBlobs fills in the three blobs of every complete conflict, with one
// git for the whole stop rather than one per stage. An Incomplete conflict is
// refused before any strategy sees it and nothing ever looks at its bytes, so
// it is not read.
func readBlobs(dir string, cs []Conflict) error {
	var want []string
	seen := map[string]bool{}
	for _, c := range cs {
		if c.Incomplete != "" {
			continue
		}
		for _, oid := range [3]string{c.BaseOID, c.TrunkOID, c.BranchOID} {
			if !seen[oid] {
				seen[oid] = true
				want = append(want, oid)
			}
		}
	}
	if len(want) == 0 {
		return nil
	}
	blobs, err := catFileBatch(dir, want)
	if err != nil {
		return err
	}
	for i := range cs {
		c := &cs[i]
		if c.Incomplete != "" {
			continue
		}
		c.Base, c.Trunk, c.Branch = blobs[c.BaseOID], blobs[c.TrunkOID], blobs[c.BranchOID]
	}
	return nil
}

// catFileBatch reads several blobs with one git. The request is one id per
// line on stdin; the answer is a "<oid> <type> <size>" line, then exactly
// size bytes, then a newline of git's own. The bytes come back as stored — a
// blob's trailing newline is content, so nothing here trims.
func catFileBatch(dir string, oids []string) (map[string][]byte, error) {
	var req strings.Builder
	for _, oid := range oids {
		req.WriteString(oid)
		req.WriteByte('\n')
	}
	out, err := runGit(dir, nil, strings.NewReader(req.String()), "cat-file", "--batch")
	if err != nil {
		return nil, fmt.Errorf("git cat-file --batch: %w", err)
	}
	blobs := make(map[string][]byte, len(oids))
	for rest := out; len(rest) > 0; {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return nil, fmt.Errorf("git cat-file --batch: header without a newline")
		}
		header := string(rest[:nl])
		rest = rest[nl+1:]
		// Three fields is an object; anything else is git saying it has none
		// ("<oid> missing"), which the caller asked for and must hear about.
		f := strings.Fields(header)
		if len(f) != 3 {
			return nil, fmt.Errorf("git cat-file --batch: %s", header)
		}
		size, serr := strconv.Atoi(f[2])
		if serr != nil || size > len(rest) {
			return nil, fmt.Errorf("git cat-file --batch: bad size in %q", header)
		}
		// Capped, so a later append to the blob cannot write into the next one.
		blobs[f[0]] = rest[:size:size]
		rest = rest[size:]
		if len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		}
	}
	return blobs, nil
}
