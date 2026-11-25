// Package diag reports problems found in Vex source code.
package diag

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// A location in a source file.
//
// [Line] and [Col] are 1-based; [Col] counts characters, not bytes.
type Pos struct {
	Offset int
	Line   int
	Col    int
}

// Returns the position as `line:col`.
func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// A single problem in a source file, such as a syntax or type error.
type Diagnostic struct {
	Pos Pos

	// The file the problem is in, when a program has several; nil means
	// the file being reported on.
	File *File

	// The number of characters to underline, starting at [Pos].
	Len int

	Message string

	// An optional suggestion shown next to the underline.
	Hint string

	// Set for a warning, which doesn't stop the program from building.
	Warning bool
}

// Returns the diagnostic as `line:col: message`.
func (d Diagnostic) Error() string { return d.Pos.String() + ": " + d.Message }

// A list of diagnostics that can be returned as a single error.
type List []Diagnostic

// Returns the first diagnostic and how many more follow.
func (l List) Error() string {
	switch len(l) {
	case 0:
		return "no errors"
	case 1:
		return l[0].Error()
	}
	return fmt.Sprintf("%s (and %d more errors)", l[0].Error(), len(l)-1)
}

// Sorts the list by file, then by position in the file.
func (l List) Sort() {
	sort.SliceStable(l, func(i, j int) bool {
		if a, b := l[i].File, l[j].File; a != b && a != nil && b != nil {
			return a.Name < b.Name
		}
		return l[i].Pos.Offset < l[j].Pos.Offset
	})
}

// A named source file with fast access to its lines.
type File struct {
	Name string
	Src  string

	lineStarts []int
}

// Creates a [File] from its name and contents.
func NewFile(name, src string) *File {
	starts := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &File{Name: name, Src: src, lineStarts: starts}
}

// Returns the text of line [n] (1-based) without its line ending.
func (f *File) Line(n int) string {
	if n < 1 || n > len(f.lineStarts) {
		return ""
	}
	start := f.lineStarts[n-1]
	end := len(f.Src)
	if n < len(f.lineStarts) {
		end = f.lineStarts[n] - 1
	}
	return strings.TrimSuffix(f.Src[start:end], "\r")
}

// Returns the [Pos] for a byte offset in the file.
func (f *File) PosOf(offset int) Pos {
	line := sort.Search(len(f.lineStarts), func(i int) bool { return f.lineStarts[i] > offset })
	start := f.lineStarts[line-1]
	return Pos{Offset: offset, Line: line, Col: utf8.RuneCountInString(f.Src[start:offset]) + 1}
}

// Terminal colours used by [Format].
type style struct{ err, warn, accent, hint, reset string }

var (
	plain   = style{}
	colored = style{err: "\x1b[1;31m", warn: "\x1b[1;33m", accent: "\x1b[1;34m", hint: "\x1b[36m", reset: "\x1b[0m"}
)

// Formats [d] with the offending source line and an underline:
//
//	error: unterminated string
//	  --> main.vx:3:13
//	   |
//	 3 |     let s = "abc
//	   |             ^^^^ add a closing "
func Format(f *File, d Diagnostic, color bool) string {
	s := plain
	if color {
		s = colored
	}
	if d.File != nil {
		f = d.File
	}
	if f == nil {
		return fmt.Sprintf("%serror%s: %s\n", s.err, s.reset, d.Message)
	}
	var b strings.Builder
	if d.Warning {
		fmt.Fprintf(&b, "%swarning%s: %s\n", s.warn, s.reset, d.Message)
	} else {
		fmt.Fprintf(&b, "%serror%s: %s\n", s.err, s.reset, d.Message)
	}

	num := fmt.Sprint(d.Pos.Line)
	pad := strings.Repeat(" ", len(num))
	fmt.Fprintf(&b, "%s%s-->%s %s:%s\n", pad, s.accent, s.reset, f.Name, d.Pos)

	line := f.Line(d.Pos.Line)
	if line == "" && d.Pos.Line > len(f.lineStarts) {
		return b.String()
	}
	fmt.Fprintf(&b, "%s %s|%s\n", pad, s.accent, s.reset)
	fmt.Fprintf(&b, "%s%s |%s %s\n", s.accent, num, s.reset, line)

	// Mirror tabs from the source line so the underline stays aligned.
	var lead strings.Builder
	col := 1
	for _, r := range line {
		if col >= d.Pos.Col {
			break
		}
		if r == '\t' {
			lead.WriteByte('\t')
		} else {
			lead.WriteByte(' ')
		}
		col++
	}
	n := d.Len
	if rest := utf8.RuneCountInString(line) - (d.Pos.Col - 1); n > rest {
		n = rest
	}
	if n < 1 {
		n = 1
	}
	fmt.Fprintf(&b, "%s %s|%s %s%s%s%s", pad, s.accent, s.reset, lead.String(), s.err, strings.Repeat("^", n), s.reset)
	if d.Hint != "" {
		fmt.Fprintf(&b, " %s%s%s", s.hint, d.Hint, s.reset)
	}
	b.WriteByte('\n')
	return b.String()
}
