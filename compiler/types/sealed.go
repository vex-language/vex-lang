package types

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// Returns the structs and enums that implement [t] when it is a sealed
// interface, structs first, each in declaration order. Returns nil for
// other types, and when a generic type implements it, since a match can't
// list every instance of one.
func (c *checker) sealedMembers(t Type) []Type {
	i, ok := t.(*Interface)
	if !ok || !i.Sealed {
		return nil
	}
	if genericMember(i) {
		return nil
	}
	var out []Type
	for _, s := range c.info.Structs {
		if s.generic == nil && s.Module == i.Module && implements(s, i) {
			out = append(out, s)
		}
	}
	for _, e := range c.info.Enums {
		if e.generic == nil && e.Module == i.Module && implements(e, i) {
			out = append(out, e)
		}
	}
	return out
}

// Reports whether a generic struct or enum may implement [i].
func genericMember(i *Interface) bool {
	mod := i.Module
	for _, g := range mod.genStructs {
		var listed []*Interface
		if g.tmpl != nil {
			listed = g.tmpl.Interfaces
		}
		if mayImplement(g.decl.Interfaces, listed, i) {
			return true
		}
	}
	for _, g := range mod.genEnums {
		var listed []*Interface
		if g.tmpl != nil {
			listed = g.tmpl.Interfaces
		}
		if mayImplement(g.decl.Interfaces, listed, i) {
			return true
		}
	}
	return false
}

// Reports whether a generic type that lists [decls], resolved as [listed]
// when known, may implement [i].
func mayImplement(decls []*ast.NamedType, listed []*Interface, i *Interface) bool {
	if listed != nil {
		for _, j := range listed {
			if j == i || j.generic != nil && j.generic == i.generic {
				return true
			}
		}
		return false
	}
	for _, te := range decls {
		if te.Module == "" && (te.Name == i.Name || te.Name == i.Base) {
			return true
		}
	}
	return false
}

// Reduces [p], which tests a value of a sealed interface for [target],
// for coverage checks. Each member of the interface is a constructor
// whose one argument is the value as that member.
func (c *checker) lowerSealed(p ast.Pattern, target Type, members []Type) *spat {
	if j, ok := target.(*Interface); ok {
		s := &spat{kind: sOr}
		for _, m := range members {
			if implements(m, j) {
				s.alts = append(s.alts, &spat{kind: sCtor, key: memberKey(m), args: []*spat{wild}})
			}
		}
		if len(s.alts) == 0 {
			return &spat{kind: sOpaque, key: fmt.Sprintf("type %p", p)}
		}
		return s
	}
	inner := wild
	if cp, ok := p.(*ast.CasePattern); ok {
		if st := c.info.PatternStructs[cp]; st != nil {
			inner = c.lowerStruct(cp, st)
		}
	}
	return &spat{kind: sCtor, key: memberKey(target), args: []*spat{inner}}
}

// Returns the constructor key of member [t] of a sealed interface.
func memberKey(t Type) string { return "type " + t.String() }

// Reports whether [s] matches every value of one member of a sealed
// interface.
func wholeMember(s *spat) bool {
	if s.kind != sCtor || !strings.HasPrefix(s.key, "type ") || len(s.args) != 1 {
		return false
	}
	in := s.args[0]
	if in.kind == sCtor && in.key == "struct" {
		return allWildSpats(in.args)
	}
	return in.kind == sWild
}

func allWildSpats(ss []*spat) bool {
	for _, s := range ss {
		if s.kind != sWild {
			return false
		}
	}
	return true
}

// Returns an arm pattern that matches every [t], such as `l: Loading`.
func memberArm(t Type) string {
	name := t.String()
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	r, _ := utf8.DecodeRuneInString(name)
	return string(unicode.ToLower(r)) + ": " + t.String()
}

// Reports whether a pattern in the current module can name the struct or
// enum [t].
func (c *checker) visibleType(t Type) bool {
	switch t := t.(type) {
	case *Struct:
		return t.Module == c.mod || t.Decl.Pub
	case *Enum:
		return t.Module == c.mod || t.Decl.Pub
	}
	return true
}

// Returns the module that declares the struct or enum [t].
func typeModule(t Type) *Module {
	switch t := t.(type) {
	case *Struct:
		return t.Module
	case *Enum:
		return t.Module
	}
	return nil
}
