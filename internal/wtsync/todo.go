package wtsync

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// todoPlan is what the run does to the sequencer's list: the lift's marks
// and drops (liftPlan), and checks, the picks after which it stops to run
// the each_commit steps.
type todoPlan struct {
	liftPlan
	checks []string
	stack  stackMarks
}

// stackMarks is where a run rebasing a stack held on one line puts each
// layer's marker: an update-ref line for prefix plus the layer's number,
// after the last line of the layers up to it. layerOf numbers every commit
// of the line by its layer, 1 the bottom, top+1 the line's own commits
// above every layer. The zero value marks nothing.
type stackMarks struct {
	prefix  string
	top     int
	layerOf map[string]int
	// copy is where the editor leaves a copy of the list it wrote, for the
	// run to read the plan from; empty leaves none.
	copy string
}

func (p todoPlan) edits() bool { return p.liftPlan.edits() || len(p.checks) > 0 || p.stack.top > 0 }

// todoEditor writes the script a run hands git as its sequence editor:
// every pick of a commit in marks is preceded by a break and made an edit,
// so the run sees HEAD before the pick is applied and stops after it; every
// pick of a commit in drops is taken out, which is what git itself does
// with a commit trunk already carries when the list is not asked to keep
// them; and every pick of a commit in checks is followed by a break, after
// the edit when it has one and after the fixup and squash lines folded into
// it, where the run checks the commit as it stays; its break comes before a
// layer marker already in the list, and a group is checked when its pick or
// any fixup or squash folded into it is to be checked. With stack marks, each
// layer's update-ref line goes after the last group of the layers up to it,
// that group's check break included, and before the next group's own lines.
// The break before a pick is what tells a pick git dropped as already
// upstream from one it committed: at the edit stop HEAD either moved or did
// not. The list names each commit by the abbreviation the repository is
// configured for, so a pick is matched by its id being a prefix of the full
// one; the abbreviations of a list are unique in the repository, and so is
// the match. Every other line is left as git wrote it. POSIX sh only, since
// git runs it through the shell wherever the run is.
func todoEditor(w io.Writer, p todoPlan) {
	var layers []string
	for sha, n := range p.stack.layerOf {
		layers = append(layers, fmt.Sprintf("%s=%d", sha, n))
	}
	sort.Strings(layers)
	fmt.Fprintln(w, "#!/bin/sh")
	fmt.Fprintln(w, "# wt sync: stop before and after the picks that may need a version lift,")
	fmt.Fprintln(w, "# leave out the picks trunk already carries, stop after the picks to check,")
	fmt.Fprintln(w, "# and mark where each layer of a stack ends")
	fmt.Fprintf(w, "marks='%s'\n", strings.Join(p.marks, " "))
	fmt.Fprintf(w, "drops='%s'\n", strings.Join(p.drops, " "))
	fmt.Fprintf(w, "checks='%s'\n", strings.Join(p.checks, " "))
	fmt.Fprintf(w, "layers='%s'\n", strings.Join(layers, " "))
	fmt.Fprintf(w, "top=%d\n", p.stack.top)
	fmt.Fprintf(w, "mapref=%s\n", shellQuote(p.stack.prefix))
	fmt.Fprintf(w, "copy=%s\n", shellQuote(p.stack.copy))
	fmt.Fprintln(w, `pend=; open=; marked=0`)
	fmt.Fprintf(w, "%s\n", `close_group() { if [ -n "$pend" ]; then printf 'break\n'; pend=; fi; }`)
	fmt.Fprintf(w, "%s\n", `flush() { while [ "$marked" -lt "$1" ]; do marked=$((marked+1)); printf 'update-ref %s%s\n' "$mapref" "$marked"; done; }`)
	fmt.Fprintln(w, `{`)
	fmt.Fprintln(w, `while IFS= read -r line || [ -n "$line" ]; do`)
	fmt.Fprintln(w, `  case "$line" in`)
	fmt.Fprintln(w, `    "pick "*)`)
	fmt.Fprintln(w, `      id=${line#pick }; id=${id%% *}; out=$line; check=; pre=`)
	fmt.Fprintln(w, `      for m in $marks; do case "$m" in "$id"*) pre=1; out="edit ${line#pick }"; break ;; esac; done`)
	fmt.Fprintln(w, `      for m in $drops; do case "$m" in "$id"*) out=""; break ;; esac; done`)
	fmt.Fprintln(w, `      for m in $checks; do case "$m" in "$id"*) check=1; break ;; esac; done`)
	fmt.Fprintln(w, `      if [ -n "$out" ]; then`)
	fmt.Fprintln(w, `        layer=$((top+1))`)
	fmt.Fprintln(w, `        for l in $layers; do case "${l%%=*}" in "$id"*) layer=${l#*=}; break ;; esac; done`)
	fmt.Fprintln(w, `        close_group; flush $((layer-1))`)
	fmt.Fprintf(w, "%s\n", `        [ -z "$pre" ] || printf 'break\n'`)
	fmt.Fprintf(w, "%s\n", `        printf '%s\n' "$out"; pend=$check; open=1`)
	fmt.Fprintln(w, `      fi ;;`)
	fmt.Fprintln(w, `    "fixup "*|"squash "*)`)
	fmt.Fprintln(w, `      id=${line#* }; case "$id" in -[Cc]\ *) id=${id#* } ;; esac; id=${id%% *}`)
	fmt.Fprintln(w, `      for m in $checks; do case "$m" in "$id"*) [ -z "$open" ] || pend=1; break ;; esac; done`)
	fmt.Fprintf(w, "%s\n", `      printf '%s\n' "$line" ;;`)
	fmt.Fprintf(w, "%s\n", `    "update-ref "*) close_group; printf '%s\n' "$line" ;;`)
	fmt.Fprintf(w, "%s\n", `    *) printf '%s\n' "$line" ;;`)
	fmt.Fprintln(w, `  esac`)
	fmt.Fprintln(w, `done < "$1"`)
	fmt.Fprintln(w, `close_group; flush "$top"`)
	fmt.Fprintln(w, `} > "$1.wt" && mv "$1.wt" "$1" && { [ -z "$copy" ] || cp "$1" "$copy"; }`)
}

// sequenceEditor is todoEditor for a list with nothing to check.
func sequenceEditor(w io.Writer, marks, drops []string) {
	todoEditor(w, todoPlan{liftPlan: liftPlan{marks: marks, drops: drops}})
}
