package main

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/anders-lindstrom/wt/internal/colour"
)

// colorValue is the value of --color: auto, always or never. It refuses
// anything else when the flag is parsed, so `wt --color=bogus --help` is a
// usage error too: help runs before any command's own checks.
type colorValue string

var colorMode colorValue = "auto"

func (c *colorValue) String() string { return string(*c) }
func (c *colorValue) Type() string   { return "string" }
func (c *colorValue) Set(s string) error {
	switch s {
	case "auto", "always", "never":
		*c = colorValue(s)
		return nil
	}
	return fmt.Errorf("--color takes auto, always or never, not %q", s)
}

func colourOn(w io.Writer) bool { return colour.Enabled(w, string(colorMode)) }

var (
	flagLine    = regexp.MustCompile(`^( {1,8})(-\w, )?(--[\w-]+)( [^\s]+)?(\s{2,}|$)(.*)$`)
	defaultNote = regexp.MustCompile(`\((default|deprecated)[^)]*\)`)
)

// paintUsage colours the generated part of help, which is the same text with
// escapes added: bold headings, a cyan usage line and command names, green
// flag names, dim value types, notes and example comments. Plain descriptions
// stay plain. Nothing is added or removed besides escapes, so the columns
// line up as they did.
func paintUsage(usage string) string {
	var out []string
	section := ""
	for _, line := range strings.Split(usage, "\n") {
		switch {
		case line != "" && line[0] != ' ' && strings.HasSuffix(line, ":"):
			section = line
			out = append(out, colour.Paint(true, colour.Bold, line))
		case line == "" || line[0] != ' ':
			out = append(out, line)
		case section == "Usage:" || section == "Aliases:":
			out = append(out, colour.Paint(true, colour.Cyan, line))
		case section == "Examples:":
			out = append(out, paintExample(line))
		case section == "Flags:" || section == "Global Flags:":
			out = append(out, paintFlag(line))
		default:
			out = append(out, paintCommand(line))
		}
	}
	return strings.Join(out, "\n")
}

func paintExample(line string) string {
	if i := strings.Index(line, "  # "); i >= 0 {
		return line[:i] + colour.Paint(true, colour.Dim, line[i:])
	}
	return line
}

func paintFlag(line string) string {
	m := flagLine.FindStringSubmatch(line)
	if m == nil {
		return line
	}
	lead, short, long, typ, gap, rest := m[1], m[2], m[3], m[4], m[5], m[6]
	if short != "" {
		short = colour.Paint(true, colour.Green, strings.TrimSuffix(short, " ")) + " "
	}
	rest = defaultNote.ReplaceAllStringFunc(rest, func(s string) string { return colour.Paint(true, colour.Dim, s) })
	return lead + short + colour.Paint(true, colour.Green, long) + colour.Paint(true, colour.Dim, typ) + gap + rest
}

func paintCommand(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	lead := line[:len(line)-len(trimmed)]
	name, rest, found := strings.Cut(trimmed, " ")
	if found {
		rest = " " + rest
	}
	return lead + colour.Paint(true, colour.Cyan, name) + rest
}

// statusWriter paints the status marks of the lines written through it: a
// leading ✓ green and a leading ! red (doctor's mark for a problem). It passes everything else through,
// and only ever sits in front of a terminal.
type statusWriter struct {
	w   io.Writer
	buf []byte
}

func (s *statusWriter) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := strings.IndexByte(string(s.buf), '\n')
		if i < 0 {
			return len(p), nil
		}
		line := string(s.buf[:i+1])
		s.buf = s.buf[i+1:]
		if _, err := io.WriteString(s.w, paintStatus(line)); err != nil {
			return 0, err
		}
	}
}

// Flush writes whatever is left without a newline.
func (s *statusWriter) Flush() {
	if len(s.buf) > 0 {
		_, _ = io.WriteString(s.w, paintStatus(string(s.buf)))
		s.buf = nil
	}
}

func paintStatus(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	lead := line[:len(line)-len(trimmed)]
	switch {
	case strings.HasPrefix(trimmed, "✓"):
		return lead + colour.Paint(true, colour.Green, "✓") + strings.TrimPrefix(trimmed, "✓")
	case strings.HasPrefix(trimmed, "! "):
		return lead + colour.Paint(true, colour.Red, "!") + strings.TrimPrefix(trimmed, "!")
	}
	return line
}

// errorLine is the line Execute prints for an error: the tool's name in red
// when stderr is a person's terminal.
func errorLine(w io.Writer, err error) string {
	return fmt.Sprintf("%s %v\n", colour.Paint(colourOn(w), colour.Red, "wt:"), err)
}
