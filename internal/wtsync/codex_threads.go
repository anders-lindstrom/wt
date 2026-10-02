package wtsync

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// turnState is where a Codex thread's last turn stands, read from the turn
// markers in its rollout file.
type turnState int

const (
	// turnNone is a thread nobody has given a prompt yet.
	turnNone turnState = iota
	// turnOpen is a turn that started and has neither completed nor been
	// aborted: in flight, waiting on an approval, or cut off by a crash.
	turnOpen
	turnDone
	// turnUnknown is a rollout with content and none of the markers: another
	// Codex version, or a file that could not be read.
	turnUnknown
)

// codexThread is one Codex session as Codex records it: a rollout file under
// $CODEX_HOME/sessions whose first line names the thread and its directory.
type codexThread struct {
	// Path is the rollout file. Unplaced is one that could not be opened or
	// whose first line names no thread and directory: it is somebody's
	// session, in a directory nobody can tell.
	Path       string
	Unplaced   bool
	ID         string
	Cwd        string
	Originator string
	// Source is who runs the thread: "exec" and "cli" are a codex process of
	// its own, anything else an app-server. Parent is set on a thread
	// another thread spawned.
	Source   string
	Parent   string
	Modified time.Time
	Turn     turnState
}

// codexHome is where Codex keeps its state.
func codexHome() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// loadCodexThreads reads the rollouts under home written at or after since.
// One written before full is returned only when its turn is not known to be
// over, which is read from the end of the file without parsing the rest. A
// rollout that cannot be read comes back Unplaced rather than not at all; no
// sessions directory is no threads.
func loadCodexThreads(home string, since, full time.Time) ([]codexThread, error) {
	var threads []codexThread
	root := filepath.Join(home, "sessions")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		name := d.Name()
		if d.IsDir() || !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.ModTime().Before(since) {
			// Too old to belong to anything alive.
			return nil
		}
		if t, ok := readCodexThread(path, info, full); ok {
			threads = append(threads, t)
		}
		return nil
	})
	return threads, err
}

// readCodexThread reads one rollout. False is a thread nobody needs: one
// from before full whose turn is over, or a file removed meanwhile.
func readCodexThread(path string, info fs.FileInfo, full time.Time) (codexThread, bool) {
	t := codexThread{Path: path, Modified: info.ModTime(), Turn: turnUnknown, Unplaced: true}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return t, false
	}
	if err != nil {
		return t, true
	}
	defer f.Close()
	if turn, err := lastTurn(f, info.Size()); err == nil {
		t.Turn = turn
	}
	if t.Modified.Before(full) && (t.Turn == turnDone || t.Turn == turnNone) {
		return t, false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return t, true
	}
	line, _ := bufio.NewReaderSize(f, 64<<10).ReadBytes('\n')
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			ID         string          `json:"id"`
			Cwd        string          `json:"cwd"`
			Originator string          `json:"originator"`
			Source     json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &meta) != nil || meta.Type != "session_meta" || meta.Payload.Cwd == "" || meta.Payload.ID == "" {
		return t, true
	}
	t.Unplaced = false
	t.ID, t.Cwd, t.Originator = meta.Payload.ID, meta.Payload.Cwd, meta.Payload.Originator
	t.Source, t.Parent = codexSource(meta.Payload.Source)
	return t, true
}

// codexSource reads session_meta's source: a string, or for a thread that
// another one started an object, {"subagent": {"thread_spawn":
// {"parent_thread_id": …}}} or {"subagent": "review"}.
func codexSource(raw json.RawMessage) (source, parent string) {
	if json.Unmarshal(raw, &source) == nil {
		return source, ""
	}
	var spawned struct {
		Subagent json.RawMessage `json:"subagent"`
	}
	if json.Unmarshal(raw, &spawned) != nil || spawned.Subagent == nil {
		return "", ""
	}
	var how struct {
		Spawn struct {
			Parent string `json:"parent_thread_id"`
		} `json:"thread_spawn"`
	}
	_ = json.Unmarshal(spawned.Subagent, &how)
	return "subagent", how.Spawn.Parent
}

var turnMarkers = map[string]turnState{
	"task_started":  turnOpen,
	"task_complete": turnDone,
	"turn_aborted":  turnDone,
}

// lastTurn finds the last turn marker in a rollout, reading backwards from
// the end so a finished thread costs one read however long it is. A file of
// session_meta lines alone has had no turn; anything else without a marker
// is not known. Nor is a file whose last line is cut short: that is a
// record being written, and it may be the start of a turn.
func lastTurn(f io.ReaderAt, size int64) (turnState, error) {
	var head []byte // the start of a line whose end has been read
	other, last := false, true
	for pos, chunk := size, int64(64<<10); pos > 0; chunk = min(chunk*2, 8<<20) {
		n := min(chunk, pos)
		pos -= n
		buf := make([]byte, n, n+int64(len(head)))
		if _, err := f.ReadAt(buf, pos); err != nil && !errors.Is(err, io.EOF) {
			return turnUnknown, err
		}
		buf = append(buf, head...)
		whole := buf
		if pos > 0 {
			i := bytes.IndexByte(buf, '\n')
			if i < 0 {
				head = buf
				continue
			}
			head, whole = buf[:i+1], buf[i+1:]
		}
		lines := bytes.Split(whole, []byte("\n"))
		for i := len(lines) - 1; i >= 0; i-- {
			line := lines[i]
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			if last && !json.Valid(line) {
				return turnUnknown, nil
			}
			last = false
			if state, ok := turnMarker(line); ok {
				return state, nil
			}
			if !bytes.Contains(line, []byte(`"type":"session_meta"`)) {
				other = true
			}
		}
	}
	if other {
		return turnUnknown, nil
	}
	return turnNone, nil
}

// turnMarker reports whether line is one of the three turn events. A quote
// inside a JSON string is escaped, so the cheap test cannot match text a
// session merely wrote about; the parse confirms where the words sit.
func turnMarker(line []byte) (turnState, bool) {
	if !bytes.Contains(line, []byte(`"task_started"`)) && !bytes.Contains(line, []byte(`"task_complete"`)) &&
		!bytes.Contains(line, []byte(`"turn_aborted"`)) {
		return 0, false
	}
	var event struct {
		Type    string `json:"type"`
		Payload struct {
			Type string `json:"type"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &event) != nil || event.Type != "event_msg" {
		return 0, false
	}
	state, ok := turnMarkers[event.Payload.Type]
	return state, ok
}
