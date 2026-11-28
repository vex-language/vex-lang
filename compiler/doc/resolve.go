package doc

import (
	"fmt"
	"io"
	"strings"

	"github.com/vex-language/vex-lang/compiler/modules"
)

// The names every module can use without declaring them: built-in types
// and interfaces, and a few keywords that doc comments mention.
var builtinNames = map[string]bool{
	"int": true, "float": true, "bool": true, "string": true, "Error": true, "Range": true,
	"Future": true, "Codable": true, "Comparable": true, "Equatable": true, "Hashable": true,
	"Self": true, "self": true, "nil": true, "true": true, "false": true,
}

// A set of documented modules, whose doc comments may refer to each
// other's declarations.
type Set struct {
	Modules []*Module

	// The names that are known without a page: [builtinNames] and the
	// public declarations of the core module, unless it is documented.
	known map[string]bool

	// Where unresolved references are reported, or nil.
	warn io.Writer
	seen map[string]bool
}

// Returns a set of [mods] that reports references nothing has the name
// of to [warn], which may be nil.
func NewSet(mods []*Module, warn io.Writer) *Set {
	s := &Set{Modules: mods, known: map[string]bool{}, warn: warn, seen: map[string]bool{}}
	for name := range builtinNames {
		s.known[name] = true
	}
	documented := false
	for _, m := range mods {
		documented = documented || m.Path == "vex:"+modules.CoreName
	}
	if core, err := modules.LoadStd(modules.CoreName); err == nil && !documented {
		for _, d := range FromModule(core, false).Decls {
			if d.Kind != Extension {
				s.known[d.Name] = true
			}
		}
	}
	return s
}

// Returns the link to [d] from the page of module [from].
func (s *Set) href(d *Decl, from *Module) string {
	if d.Module == from {
		return "#" + d.Anchor()
	}
	return d.Module.Page() + "#" + d.Anchor()
}

// Returns the [Resolver] for the doc comment of [d] in module [m], with
// links relative to the page of [m]. [d] is nil for the module's own doc.
func (s *Set) Resolver(m *Module, d *Decl) Resolver { return s.resolverOn(m, d, m) }

// Returns the [Resolver] for the doc comment of [d] in module [m], with
// links relative to the page of module [page], or to the index when
// [page] is nil.
func (s *Set) resolverOn(m *Module, d *Decl, page *Module) Resolver {
	return func(ref string) (string, bool) {
		if t := s.find(m, d, ref); t != nil {
			return s.href(t, page), true
		}
		parts := strings.Split(ref, ".")
		for x := d; x != nil; x = x.Parent {
			if len(parts) == 1 && contains(x.Params, ref) {
				return "", true
			}
		}
		return "", s.known[parts[0]]
	}
}

// Returns the declaration that [ref] names in the doc comment of [d] in
// module [m], or nil.
func (s *Set) find(m *Module, d *Decl, ref string) *Decl {
	parts := strings.Split(ref, ".")
	if len(parts) == 1 {
		for x := d; x != nil; x = x.Parent {
			if x.isType() {
				if mem := x.Member(ref); mem != nil {
					return mem
				}
			}
		}
	}
	if t := lookupPath(m, parts); t != nil {
		return t
	}
	if len(parts) > 1 {
		for _, other := range s.Modules {
			if other != m && other.Name == parts[0] {
				return lookupPath(other, parts[1:])
			}
		}
	}
	return nil
}

// Returns the declaration in [m] that [parts] name: a declaration, or a
// member of a type or of an extension of it.
func lookupPath(m *Module, parts []string) *Decl {
	switch len(parts) {
	case 1:
		return m.Lookup(parts[0])
	case 2:
		if t := m.Lookup(parts[0]); t != nil {
			return t.Member(parts[1])
		}
		for _, e := range m.Decls {
			if e.Kind == Extension && baseName(e.Name) == parts[0] {
				if mem := e.Member(parts[1]); mem != nil {
					return mem
				}
			}
		}
	}
	return nil
}

// Reports whether [d] declares a type or extends one.
func (d *Decl) isType() bool {
	switch d.Kind {
	case Struct, Enum, Interface, Extension, ExternType, TypeAlias:
		return true
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Reports the references in the doc comment of [d] that nothing has the
// name of, once each.
func (s *Set) report(m *Module, d *Decl, unresolved []string) {
	if s.warn == nil {
		return
	}
	for _, ref := range unresolved {
		where, what := m.Name, "module "+m.Name
		if d != nil {
			where = fmt.Sprintf("%s:%d", d.File, d.Line)
			what = qualifiedName(d)
		}
		key := where + "\x00" + ref
		if s.seen[key] {
			continue
		}
		s.seen[key] = true
		fmt.Fprintf(s.warn, "%s: warning: [%s] in the doc of %s doesn't name a declaration\n", where, ref, what)
	}
}

// Returns the name of [d] with its type, such as `Point.x`.
func qualifiedName(d *Decl) string {
	if d.Parent != nil {
		return qualifiedName(d.Parent) + "." + d.Name
	}
	if d.Kind == Extension {
		return "extension " + d.Name
	}
	return d.Name
}

// Returns the HTML of the doc comment of [d] in [m], or of the module's
// own doc when [d] is nil, reporting unresolved references.
func (s *Set) docHTML(m *Module, d *Decl) string {
	text := m.Doc
	if d != nil {
		text = d.Doc
	}
	out, bad := Markdown(text, s.Resolver(m, d))
	s.report(m, d, bad)
	return out
}

// Returns the HTML of the first sentence of the doc comment of [d] in
// [m], or of the module's own doc when [d] is nil, with links relative to
// the page of [page], or to the index when it is nil. Unresolved
// references are reported with the full doc comment.
func (s *Set) summaryHTML(m *Module, d *Decl, page *Module) string {
	text := m.Doc
	if d != nil {
		text = d.Doc
	}
	out, _ := Summary(text, s.resolverOn(m, d, page))
	return out
}
