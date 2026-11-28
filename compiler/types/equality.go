package types

import (
	"fmt"
	"maps"
	"slices"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/lexer"
)

// Reports whether [list] names the built-in [marker], `Equatable` or
// `Hashable`, which a struct or enum lists to get == or hashing.
func declares(list []*ast.NamedType, marker string) bool {
	for _, te := range list {
		if te.Module == "" && len(te.Args) == 0 && te.Name == marker {
			return true
		}
	}
	return false
}

// Reports whether [te] names one of the built-in markers.
func isMarker(te *ast.NamedType) bool {
	return te.Module == "" && len(te.Args) == 0 && (te.Name == "Equatable" || te.Name == "Hashable" || te.Name == "Codable")
}

// Reports whether the struct asked for == by listing `Equatable` or
// `Hashable`.
func (s *Struct) Equatable() bool {
	return s.Decl != nil && (declares(s.Decl.Interfaces, "Equatable") || declares(s.Decl.Interfaces, "Hashable"))
}

// Reports whether the struct listed `Hashable`.
func (s *Struct) Hashable() bool { return s.Decl != nil && declares(s.Decl.Interfaces, "Hashable") }

// Returns the struct's own `fn hash() -> int`, or nil.
func (s *Struct) HashMethod() *Func { return hashMethod(s.Methods) }

// Reports whether the enum asked for == by listing `Equatable` or
// `Hashable`. Enums whose cases carry no values always have ==.
func (e *Enum) Equatable() bool {
	return e.Decl != nil && (declares(e.Decl.Interfaces, "Equatable") || declares(e.Decl.Interfaces, "Hashable"))
}

// Reports whether the enum listed `Hashable`.
func (e *Enum) Hashable() bool { return e.Decl != nil && declares(e.Decl.Interfaces, "Hashable") }

// Returns the enum's own `fn hash() -> int`, or nil.
func (e *Enum) HashMethod() *Func { return hashMethod(e.Methods) }

func hashMethod(ms []*Func) *Func {
	m := memberOf(ms, "hash", ast.Method)
	if m == nil || m.Static || len(m.Params) != 0 || m.Result != Int {
		return nil
	}
	return m
}

// Reports whether [t] can be a field of a struct that hashes its fields:
// a map key type, or a float.
func hashableField(t Type) bool { return t == Float || mapKeyType(t) }

// Checks the structs and enums that list `Equatable` or `Hashable`: their
// fields must have == and hash, unless they write their own.
// Generic types are checked once, on their templates, whose constraints
// then hold for every instance.
func (c *checker) checkEquality() {
	structs := c.info.Structs
	for _, name := range slices.Sorted(maps.Keys(c.genStructs)) {
		if t := c.genStructs[name].tmpl; t != nil {
			structs = append(structs, t)
		}
	}
	for _, s := range structs {
		if s == c.info.Error || s.generic != nil && !s.abstract || !s.Equatable() {
			continue
		}
		c.checkEqualFields(s.Decl.Name.Name, s.Decl.Name, s.Fields, s.Operator("==") != nil, s.Hashable(), s.Methods)
	}
	enums := c.info.Enums
	for _, name := range slices.Sorted(maps.Keys(c.genEnums)) {
		if t := c.genEnums[name].tmpl; t != nil {
			enums = append(enums, t)
		}
	}
	for _, e := range enums {
		if e.generic != nil && !e.abstract || !e.Equatable() {
			continue
		}
		var fields []*Field
		for _, cs := range e.Cases {
			fields = append(fields, cs.Fields...)
		}
		c.checkEqualFields(e.Decl.Name.Name, e.Decl.Name, fields, e.Operator("==") != nil, e.Hashable(), e.Methods)
	}
}

// Checks the [fields] of the type [name], declared at [at], that asked for
// == and, when [hashable], hashing. [ownEq] tells whether it writes its
// own ==, and [methods] are its methods.
func (c *checker) checkEqualFields(name string, at ast.Node, fields []*Field, ownEq, hashable bool, methods []*Func) {
	own := memberOf(methods, "hash", ast.Method)
	if own != nil && hashMethod(methods) == nil {
		c.errorAt(own.Decl.Name, "write fn hash() -> int", "hash must take no parameters and return int, so maps can use it")
		return
	}
	if hashable && ownEq && own == nil {
		c.errorAt(at, "add fn hash() -> int, e.g. => hash(field)", "%s writes its own ==, so it needs a matching fn hash() -> int to be Hashable", name)
	}
	for _, f := range fields {
		if f.Type == Invalid {
			continue
		}
		where := ast.Node(f.Decl.Name)
		if f.Decl.Type != nil {
			where = f.Decl.Type
		}
		switch {
		case !ownEq && !comparable(f.Type):
			c.errorAt(where, equalHint(f.Type, "Equatable"), "field '%s' is %s, which has no ==, so %s can't be Equatable", f.Name, f.Type, name)
		case hashable && !ownEq && own == nil && !hashableField(f.Type):
			c.errorAt(where, equalHint(f.Type, "Hashable"), "field '%s' is %s, which can't be hashed, so %s can't be Hashable", f.Name, f.Type, name)
		}
	}
}

// Returns how a field of type [t] could get what [marker] needs.
func equalHint(t Type, marker string) string {
	switch t := t.(type) {
	case *Struct:
		return fmt.Sprintf("list %s on %s too", marker, t.Name)
	case *Enum:
		return fmt.Sprintf("list %s on %s too", marker, t.Name)
	case *TypeParam:
		return fmt.Sprintf("require it: <%s: %s>", t.Name, marker)
	case *Interface:
		return "write your own operator ==, comparing it with ==="
	}
	return "write your own operator ==" + map[string]string{"Hashable": " and fn hash() -> int"}[marker]
}

// Returns how a map could use keys of type [t].
func keyHint(t Type) string {
	switch t := t.(type) {
	case *Struct:
		return fmt.Sprintf("list Hashable on it: struct %s: Hashable", t.Name)
	case *Enum:
		return fmt.Sprintf("list Hashable on it: enum %s: Hashable", t.Name)
	}
	return "use int, string, bool, a record of those, or a struct or enum that lists Hashable"
}

// Reports whether the struct or enum whose method is being checked has a
// method `hash` that takes parameters, which a call `hash(x)` then means.
func (c *checker) ownHashWithParams() bool {
	if c.fn == nil {
		return false
	}
	var ms []*Func
	switch {
	case c.fn.Owner != nil:
		ms = c.fn.Owner.Methods
	case c.fn.Enum != nil:
		ms = c.fn.Enum.Methods
	}
	m := memberOf(ms, "hash", ast.Method)
	return m != nil && len(m.Params) > 0
}

// Reports whether values of [t] are references, which `===` can compare.
func reference(t Type) bool {
	switch t := t.(type) {
	case *Struct, *List, *Map, *Interface, *Handle:
		return true
	case *Enum:
		return !t.Simple()
	}
	return false
}

// Checks `a === b` or `a !== b`.
func (c *checker) identity(e *ast.BinaryExpr) Type {
	op := "==="
	if e.Op == lexer.NotEqEq {
		op = "!=="
	}
	x := c.expr(e.X, nil)
	y := c.expr(e.Y, x)
	if x == Invalid || y == Invalid {
		return Bool
	}
	for _, t := range []Type{x, y} {
		if !reference(t) {
			c.errorAt(e, "use == to compare values", "'%s' compares references, but %s is a value", op, article(t))
			return Bool
		}
	}
	if !assignableType(x, y) && !assignableType(y, x) {
		c.errorAt(e, "", "can't compare %s and %s with '%s'", x, y, op)
	}
	return Bool
}

// Checks `hash(a, b, ...)`, which combines the hashes of its arguments.
func (c *checker) hashCall(e *ast.CallExpr) Type {
	call := &Call{Kind: BuiltinCall, Builtin: "hash"}
	c.info.Calls[e] = call
	for _, a := range e.Args {
		if a.Name != nil {
			c.errorAt(a.Name, "", "hash doesn't take named arguments")
		}
		t := c.expr(a.Value, nil)
		if t != Invalid && !hashableField(t) {
			c.errorAt(a.Value, equalHint(t, "Hashable"), "%s can't be hashed", t)
		}
		call.Args = append(call.Args, a.Value)
	}
	if e.Trailing != nil {
		c.errorAt(e.Trailing, "", "hash doesn't take a trailing block")
	}
	return Int
}
