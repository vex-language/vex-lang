package types

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// Extension types, `extension type UserId(value: int)`, are structs with
// one field that compile to that field's type alone: creating one and
// reading its value cost nothing, and its methods take the value itself.
// They have no type tag, so they can't become interface values; they list
// only the built-in markers, which they get from the value they wrap.

// Returns the type an extension type wraps, or nil for other structs.
func (s *Struct) Underlying() Type {
	if s == nil || s.Decl == nil || !s.Decl.Extension || len(s.Fields) != 1 {
		return nil
	}
	return s.Fields[0].Type
}

// Reports whether the extension type listed `Comparable`, which orders its
// values as the values they wrap are ordered.
func (s *Struct) DerivesOrder() bool {
	return s.Decl != nil && s.Decl.Extension && declares(s.Decl.Interfaces, "Comparable")
}

// Returns what [d] declares: "struct" or "extension type".
func kindName(d *ast.StructDecl) string {
	if d.Extension {
		return "extension type"
	}
	return "struct"
}

// Returns the extension type's header as written, such as
// `extension type UserId(value: int)`.
func (s *Struct) header() string {
	f := s.Decl.Fields[0]
	return fmt.Sprintf("extension type %s(%s: %s)", s.Decl.Name.Name, f.Name.Name, ast.TypeString(f.Type))
}

// Checks the interfaces an extension type lists: only the built-in
// markers, since its values can't be boxed into interface values.
func (c *checker) extensionMarkers(s *Struct) {
	if s.generic != nil && !s.abstract {
		return
	}
	for _, te := range s.Decl.Interfaces {
		if isMarker(te) || te.Module == "" && len(te.Args) == 0 && te.Name == "Comparable" {
			continue
		}
		c.errorAt(te, "use a struct to implement "+te.Name, "an extension type can only list Equatable, Hashable, Comparable and Codable, which it gets from the value it wraps")
	}
}

// Checks every extension type: it can't wrap itself, and a `Comparable`
// one must wrap a value that can be ordered.
func (c *checker) checkExtensionTypes() {
	structs := c.info.Structs
	for _, name := range slices.Sorted(maps.Keys(c.genStructs)) {
		if t := c.genStructs[name].tmpl; t != nil {
			structs = append(structs, t)
		}
	}
	for _, s := range structs {
		u := s.Underlying()
		if u == nil || s.generic != nil && !s.abstract || u == Invalid {
			continue
		}
		f := s.Fields[0]
		back := c.enter(s.Decl)
		if wrapsItself(u, s.Decl, map[Type]bool{}) {
			c.errorAt(f.Decl.Type, fmt.Sprintf("wrap a list or a struct instead, e.g. %s: [%s]", f.Name, s.Name), "extension type '%s' wraps itself, so it could never be created", s.Decl.Name.Name)
		} else if s.DerivesOrder() && !ordered(u) {
			hint := ""
			if tp, ok := u.(*TypeParam); ok {
				hint = fmt.Sprintf("require it: <%s: Comparable>", tp.Name)
			}
			c.errorAt(f.Decl.Type, hint, "'%s' is %s, which can't be ordered, so %s can't be Comparable", f.Name, u, s.Decl.Name.Name)
		}
		back()
	}
}

// Reports whether a value of [t] holds, without a reference in between,
// a value of the extension type declared by [d].
func wrapsItself(t Type, d *ast.StructDecl, seen map[Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t := t.(type) {
	case *Struct:
		if u := t.Underlying(); u != nil {
			return t.Decl == d || wrapsItself(u, d, seen)
		}
	case *Optional:
		return wrapsItself(t.Elem, d, seen)
	case *Record:
		for _, f := range t.FieldTypes() {
			if wrapsItself(f, d, seen) {
				return true
			}
		}
	}
	return false
}

// Suggests how to turn the expression [n], of type [got], into the
// extension type [want] or back, or returns "".
func (c *checker) wrapHint(n ast.Node, want, got Type) string {
	text := "..."
	if e, ok := n.(ast.Expr); ok {
		if s := c.source(e); s != "" && len(s) <= 30 && !strings.ContainsAny(s, "\n") {
			text = s
		}
	}
	if s, ok := want.(*Struct); ok && s.Underlying() != nil && Identical(s.Underlying(), got) && got != Invalid {
		return fmt.Sprintf("wrap it: %s(%s: %s)", s.Name, s.Fields[0].Name, text)
	}
	if s, ok := got.(*Struct); ok && s.Underlying() != nil && Identical(s.Underlying(), want) && want != Invalid {
		if id, ok := n.(*ast.Ident); ok {
			return fmt.Sprintf("use %s.%s", id.Name, s.Fields[0].Name)
		}
		return "use ." + s.Fields[0].Name
	}
	return ""
}
