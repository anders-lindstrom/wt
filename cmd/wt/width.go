package main

import (
	"io"
	"os"
	"strconv"
	"strings"

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

// installHelp makes root's help follow the terminal: the description is
// reflowed, and the flag list and the command list keep their columns, a
// description that does not fit wrapping under its own first line.
func installHelp(root *cobra.Command) {
	cobra.AddTemplateFunc("flagUsages", func(fs *pflag.FlagSet, c *cobra.Command) string {
		return fs.FlagUsagesWrapped(helpWidth(c.OutOrStdout()))
	})
	cobra.AddTemplateFunc("commandSummary", func(summary string, padding int, c *cobra.Command) string {
		const margin = 2 // the template indents every command by two
		return textwrap.Wrap(margin+padding+1, summary, helpWidth(c.OutOrStdout()), strings.Repeat(" ", margin+padding+1))
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
			_, _ = io.WriteString(out, c.UsageString())
		}
	})
}

// reflow lays a hand-written description out for width columns (0 for no
// wrapping). A run of lines that start at the left margin is one paragraph:
// it is joined and wrapped. A line that starts with a space is laid out as
// written, a list or a table row, and only when it is too long for the
// terminal does it wrap, under its own text.
func reflow(text string, width int) string {
	var out []string
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, textwrap.Wrap(0, strings.Join(para, " "), width, ""))
			para = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			flush()
			out = append(out, "")
		case line[0] == ' ' || line[0] == '\t':
			flush()
			rest := strings.TrimLeft(line, " \t")
			lead := line[:len(line)-len(rest)]
			out = append(out, lead+textwrap.Wrap(len(lead), rest, width, lead+"  "))
		default:
			para = append(para, line)
		}
	}
	flush()
	return strings.Join(out, "\n")
}
