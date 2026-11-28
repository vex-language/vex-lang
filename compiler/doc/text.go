package doc

import (
	"fmt"
	"io"
	"strings"
)

// Returns the declaration [name] refers to, such as `Point`,
// `Point.x` or `models.User`, looking in the modules in order, or nil.
func (s *Set) Find(name string) *Decl {
	parts := strings.Split(name, ".")
	for _, m := range s.Modules {
		if d := lookupPath(m, parts); d != nil {
			return d
		}
	}
	if len(parts) > 1 {
		for _, m := range s.Modules {
			if m.Name == parts[0] || m.Path == parts[0] {
				if d := lookupPath(m, parts[1:]); d != nil {
					return d
				}
			}
		}
	}
	return nil
}

// Writes the signature and doc comment of [d] as plain text, followed by
// those of its members.
func WriteText(w io.Writer, d *Decl) {
	fmt.Fprintln(w, d.Signature)
	if d.Doc != "" {
		fmt.Fprintln(w)
		writeIndented(w, d.Doc, "    ")
	}
	if len(d.Members) == 0 {
		return
	}
	fmt.Fprintln(w)
	for i, m := range d.Members {
		// A member with a doc comment is set off by blank lines.
		if m.Doc != "" && i > 0 && d.Members[i-1].Doc == "" {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, "    "+m.Signature)
		if m.Doc != "" {
			writeIndented(w, m.Doc, "        ")
			if i+1 < len(d.Members) {
				fmt.Fprintln(w)
			}
		}
	}
}

// Writes an overview of [m] as plain text: its doc comment and the
// signatures of its declarations.
func WriteModuleText(w io.Writer, m *Module) {
	if m.Path != "" {
		fmt.Fprintf(w, "module %s // import %s\n", m.Name, quote(m.Path))
	} else {
		fmt.Fprintf(w, "module %s\n", m.Name)
	}
	if m.Doc != "" {
		fmt.Fprintln(w)
		writeIndented(w, m.Doc, "    ")
	}
	for _, g := range groups {
		decls := g.of(m)
		if len(decls) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s:\n", g.title)
		for _, d := range decls {
			fmt.Fprintln(w, "    "+d.Signature)
		}
	}
}

// Writes [text] with every non-blank line indented by [indent].
func writeIndented(w io.Writer, text, indent string) {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			fmt.Fprintln(w)
		} else {
			fmt.Fprintln(w, indent+line)
		}
	}
}
