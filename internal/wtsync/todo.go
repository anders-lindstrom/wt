package wtsync

import (
	"fmt"
	"io"
	"strings"
)

// todoPlan is what the run does to the sequencer's list: the lift's marks
// and drops (liftPlan), and checks, the picks after which it stops to run
// the each_commit steps.
type todoPlan struct {
	liftPlan
	checks []string
}

func (p todoPlan) edits() bool { return p.liftPlan.edits() || len(p.checks) > 0 }

// todoEditor writes the script a run hands git as its sequence editor:
// every pick of a commit in marks is preceded by a break and made an edit,
// so the run sees HEAD before the pick is applied and stops after it; every
// pick of a commit in drops is taken out, which is what git itself does
// with a commit trunk already carries when the list is not asked to keep
// them; and every pick of a commit in checks is followed by a break, after
// the edit when it has one, where the run checks the commit as it stays.
// The break before a pick is what tells a pick git dropped as already
// upstream from one it committed: at the edit stop HEAD either moved or did
// not. The list names each commit by the abbreviation the repository is
// configured for, so a pick is matched by its id being a prefix of the full
// one; the abbreviations of a list are unique in the repository, and so is
// the match. Every other line is left as git wrote it. POSIX sh only, since
// git runs it through the shell wherever the run is.
func todoEditor(w io.Writer, p todoPlan) {
	fmt.Fprintln(w, "#!/bin/sh")
	fmt.Fprintln(w, "# wt sync: stop before and after the picks that may need a version lift,")
	fmt.Fprintln(w, "# leave out the picks trunk already carries, and stop after the picks to check")
	fmt.Fprintf(w, "marks='%s'\n", strings.Join(p.marks, " "))
	fmt.Fprintf(w, "drops='%s'\n", strings.Join(p.drops, " "))
	fmt.Fprintf(w, "checks='%s'\n", strings.Join(p.checks, " "))
	fmt.Fprintln(w, `while IFS= read -r line || [ -n "$line" ]; do`)
	fmt.Fprintln(w, `  case "$line" in`)
	fmt.Fprintln(w, `    "pick "*)`)
	fmt.Fprintln(w, `      id=${line#pick }; id=${id%% *}; out=$line; check=`)
	fmt.Fprintln(w, `      for m in $marks; do case "$m" in "$id"*) printf 'break\n'; out="edit ${line#pick }"; break ;; esac; done`)
	fmt.Fprintln(w, `      for m in $drops; do case "$m" in "$id"*) out=""; break ;; esac; done`)
	fmt.Fprintln(w, `      for m in $checks; do case "$m" in "$id"*) check=1; break ;; esac; done`)
	fmt.Fprintf(w, "%s\n", `      if [ -n "$out" ]; then printf '%s\n' "$out"; [ -z "$check" ] || printf 'break\n'; fi ;;`)
	fmt.Fprintf(w, "%s\n", `    *) printf '%s\n' "$line" ;;`)
	fmt.Fprintln(w, `  esac`)
	fmt.Fprintln(w, `done < "$1" > "$1.wt" && mv "$1.wt" "$1"`)
}

// sequenceEditor is todoEditor for a list with nothing to check.
func sequenceEditor(w io.Writer, marks, drops []string) {
	todoEditor(w, todoPlan{liftPlan: liftPlan{marks: marks, drops: drops}})
}
