package main

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/anders-lindstrom/wt/internal/textwrap"
)

// maxHelpWidth is the widest a help line gets, however wide the terminal is.
const maxHelpWidth = 180

// helpWidth is the column count help wraps to on w: COLUMNS when it is set,
// else the size of the terminal w is on, never more than maxHelpWidth. It is 0
// when w is not a terminal, and 0 means do not wrap at all: a pipe, a file or
// an agent's tool call gets each paragraph and each description on one line.
func helpWidth(w io.Writer) int {
	if f, ok := w.(*os.File); !ok || !isTerminal(f) {
		return 0
	}
	size := terminalWidth(w)
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		size = n
	}
	if size <= 0 {
		size = 80 // a terminal that will not say how wide it is
	}
	return min(size, maxHelpWidth)
}

// renderWidth is the width the usage being rendered right now wraps to, or -1
// when none is being rendered through help. UsageString points the command's
// writers at a buffer, so the template cannot ask the writer: help works the
// width out from the real one first and leaves it here.
var renderWidth = -1

// templateWidth is the width a usage template wraps to for c.
func templateWidth(c *cobra.Command) int {
	if renderWidth >= 0 {
		return renderWidth
	}
	return helpWidth(c.OutOrStderr())
}

// installHelp makes root's help follow the terminal: the description is
// reflowed, and the flag list and the command list keep their columns, a
// description that does not fit wrapping under its own first line.
func installHelp(root *cobra.Command) {
	cobra.AddTemplateFunc("flagUsages", func(fs *pflag.FlagSet, c *cobra.Command) string {
		return fs.FlagUsagesWrapped(templateWidth(c))
	})
	cobra.AddTemplateFunc("commandSummary", func(summary string, padding int, c *cobra.Command) string {
		const margin = 2 // the template indents every command by two
		return textwrap.Wrap(margin+padding+1, summary, templateWidth(c), strings.Repeat(" ", margin+padding+1))
	})
	t := root.UsageTemplate()
	t = strings.ReplaceAll(t, "{{.LocalFlags.FlagUsages |", "{{flagUsages .LocalFlags . |")
	t = strings.ReplaceAll(t, "{{.InheritedFlags.FlagUsages |", "{{flagUsages .InheritedFlags . |")
	t = strings.ReplaceAll(t, "{{rpad .Name .NamePadding }} {{.Short}}", "{{rpad .Name .NamePadding }} {{commandSummary .Short .NamePadding $}}")
	root.SetUsageTemplate(t)
	root.SetHelpFunc(func(c *cobra.Command, _ []string) {
		out := c.OutOrStdout()
		text := c.Long
		if text == "" {
			text = c.Short
		}
		if text = strings.TrimRight(text, " \t\r\n"); text != "" {
			_, _ = io.WriteString(out, reflow(text, helpWidth(out))+"\n\n")
		}
		if c.Runnable() || c.HasSubCommands() {
			renderWidth = helpWidth(out)
			usage := c.UsageString()
			renderWidth = -1
			if colourOn(out) {
				usage = paintUsage(usage)
			}
			_, _ = io.WriteString(out, usage)
		}
	})
}

// gap is the run of spaces between two cells of a hand-laid table.
var gap = regexp.MustCompile(`\S {2,}\S`)

// reflow lays a hand-written description out for width columns (0 for no
// wrapping). A run of lines that start at the left margin is one paragraph:
// it is joined and wrapped. A line that starts with a space is laid out as
// written, a list item or a table row, and only when it is too long for the
// terminal does it wrap. A table row (a cell, a gap of two spaces or more,
// then another) hangs its wrapped text under its own cell, and the lines an
// author hand-wrapped under that cell are joined to it first, so the row
// wraps to the terminal and not to the width it was written for. Any other
// indented line hangs two spaces in.
func reflow(text string, width int) string {
	var out []string
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, textwrap.Wrap(0, strings.Join(para, " "), width, ""))
			para = nil
		}
	}
	type row struct {
		lead, rest string
		cell       int // the column the row's last cell starts at; 0 with none
	}
	var rows []row
	flushRows := func() {
		for _, r := range rows {
			hang := len(r.lead) + 2
			if r.cell > 0 && (width <= 0 || r.cell < width-10) {
				hang = r.cell
			}
			out = append(out, r.lead+textwrap.Wrap(len(r.lead), r.rest, width, strings.Repeat(" ", hang)))
		}
		rows = nil
	}
	tableCol := 0
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			flush()
			flushRows()
			tableCol = 0
			out = append(out, "")
		case line[0] == ' ' || line[0] == '\t':
			flush()
			rest := strings.TrimLeft(line, " \t")
			lead := line[:len(line)-len(rest)]
			if n := len(rows); n > 0 && rows[n-1].cell == len(lead) {
				rows[n-1].rest += " " + rest
				continue
			}
			r := row{lead: lead, rest: rest}
			if g := gap.FindAllStringIndex(rest, -1); len(g) > 0 {
				// The match ends past the first character of the last cell.
				end := g[len(g)-1][1]
				_, first := utf8.DecodeLastRuneInString(rest[:end])
				r.cell = len(lead) + textwrap.Width(rest[:end-first])
				tableCol = r.cell
			} else if len(lead) == tableCol && tableCol > 0 {
				// The cell of a row whose first column stood alone above it.
				r.cell = len(lead)
			}
			rows = append(rows, r)
		default:
			flushRows()
			tableCol = 0
			para = append(para, line)
		}
	}
	flush()
	flushRows()
	return strings.Join(out, "\n")
}
