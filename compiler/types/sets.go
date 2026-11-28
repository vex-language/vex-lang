package types

import (
	"fmt"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// A set type such as `Set<int>`: Hashable values without repeats, kept in
// the order they were added. Sets are reference types.
type Set struct{ Elem Type }

// Returns the type in source form, such as `Set<int>`.
func (s *Set) String() string { return "Set<" + s.Elem.String() + ">" }

// The members of every set, for suggestions.
var setMemberNames = []string{"len", "isEmpty", "contains", "insert", "remove", "union", "intersection",
	"subtracting", "isSubset", "toList", "copy"}

// Reports whether `Set` means the built-in set type, which a declaration
// of that name hides.
func (c *checker) builtinSet() bool {
	const name = "Set"
	return c.structs[name] == nil && c.enums[name] == nil && c.interfaces[name] == nil && c.handles[name] == nil &&
		c.genStructs[name] == nil && c.genEnums[name] == nil && c.genIfaces[name] == nil && c.aliases[name] == nil
}

// Returns the type written as `Set<T>`, or created by `Set<T>()`.
func (c *checker) setType(at ast.Node, args []ast.TypeExpr) Type {
	if len(args) != 1 {
		c.errorAt(at, "write Set<int>", "Set takes 1 type argument, found %d", len(args))
		return Invalid
	}
	return c.setOf(c.resolveType(args[0]), args[0])
}

// Returns `Set<elem>`, reporting at [at] when [elem] can't be in a set.
func (c *checker) setOf(elem Type, at ast.Node) Type {
	if elem == Invalid {
		return Invalid
	}
	if !mapKeyType(elem) {
		c.errorAt(at, keyHint(elem), "%s can't be in a set", elem)
		elem = Invalid
	}
	return &Set{Elem: elem}
}

// Checks the list literal [e] where the set [s] is expected, which makes
// it a set literal.
func (c *checker) setLit(e *ast.ListLit, s *Set) Type {
	list := &List{Elem: s.Elem}
	var t Type
	if e.Body != nil {
		t = c.collection(e, e.Body, list, false)
	} else {
		t = c.listLit(e, list)
		seen := map[string]int{}
		for _, x := range e.Elems {
			if lit := literalKey(x); lit != "" {
				if line, dup := seen[lit]; dup {
					c.errorAt(x, "", "value %s is already in this set on line %d", c.source(x), line)
				}
				seen[lit] = x.Pos().Line
			}
		}
	}
	if t == Invalid || s.Elem == Invalid {
		return Invalid
	}
	return s
}

// Checks `Set<T>()`, which makes an empty set, and `Set(items)`, which
// makes one from a list or another set.
func (c *checker) newSet(e *ast.CallExpr, want Type) Type {
	call := &Call{Kind: BuiltinCall, Builtin: "Set"}
	c.info.Calls[e] = call
	if len(e.Args) > 1 || e.Trailing != nil || len(e.Args) == 1 && e.Args[0].Name != nil {
		c.checkArgs(e)
		c.errorAt(e.Fn, "write Set<int>() for an empty set, or Set(list) for one with items", "Set takes one list without a label")
		return Invalid
	}
	var set *Set
	if len(e.TypeArgs) > 0 {
		t := c.setType(e.Fn, e.TypeArgs)
		if t == Invalid {
			c.checkArgs(e)
			return Invalid
		}
		set = t.(*Set)
	} else if s, ok := want.(*Set); ok {
		set = s
	}
	if len(e.Args) == 0 {
		if set != nil {
			return set
		}
		if want != Invalid {
			c.errorAt(e.Fn, "write its element type: Set<int>()", "can't tell what the set holds")
		}
		return Invalid
	}
	arg := e.Args[0].Value
	call.Args = []ast.Expr{arg}
	var argWant Type
	if set != nil {
		argWant = set
	}
	return c.setOperand(arg, c.expr(arg, argWant), set, "Set")
}

// Checks [x], of type [t], as the items of a set operation [what]: a set
// or a list. Returns the set type, which must be [want] when it is given.
func (c *checker) setOperand(x ast.Expr, t Type, want *Set, what string) Type {
	var elem Type
	switch t := t.(type) {
	case *Set:
		elem = t.Elem
	case *List:
		elem = t.Elem
	default:
		if t != Invalid {
			c.errorAt(x, "", "%s needs a set or a list, found %s", what, t)
		}
		return Invalid
	}
	if want == nil {
		return c.setOf(elem, x)
	}
	if !Identical(want.Elem, elem) {
		c.errorAt(x, "", "%s needs items of type %s, found %s", what, want.Elem, t)
		return Invalid
	}
	return want
}

// Returns the signature of the set member [name], or nil.
func setMember(s *Set, name string) *memberSig {
	switch name {
	case "contains", "insert", "remove":
		return &memberSig{[]slot{{name: "value", typ: s.Elem}}, Bool}
	case "union", "intersection", "subtracting":
		return &memberSig{[]slot{{name: "other", typ: s}}, s}
	case "isSubset":
		return &memberSig{[]slot{{name: "of", typ: s}}, Bool}
	case "toList":
		return &memberSig{nil, &List{Elem: s.Elem}}
	case "copy":
		return &memberSig{nil, s}
	}
	return nil
}

// Checks a call of the member [name] of the set [s]. Reports false for a
// name that isn't a set operation, which the caller then checks as usual.
func (c *checker) setCall(e *ast.CallExpr, m *ast.MemberExpr, s *Set, name string) (Type, bool) {
	switch name {
	case "insert", "remove":
		c.constChange(m.X, "set")
		return nil, false
	case "union", "intersection", "subtracting", "isSubset":
	default:
		if hint := setMethodHint(name); hint != "" {
			c.checkArgs(e)
			c.errorAt(m.Name, hint, "%s has no method '%s'", s, name)
			return Invalid, true
		}
		return nil, false
	}
	label := ""
	if name == "isSubset" {
		label = "of"
	}
	example := fmt.Sprintf("e.g. a.%s(b)", name)
	if label != "" {
		example = fmt.Sprintf("e.g. a.%s(%s: b)", name, label)
	}
	if len(e.Args) != 1 || e.Trailing != nil {
		c.checkArgs(e)
		c.errorAt(m.Name, example, "%s takes one set or list", name)
		return Invalid, true
	}
	a := e.Args[0]
	switch {
	case label != "" && (a.Name == nil || a.Name.Name != label):
		c.expr(a.Value, nil)
		c.errorAt(a, example, "%s needs the label '%s:'", name, label)
		return Invalid, true
	case label == "" && a.Name != nil:
		c.expr(a.Value, nil)
		c.errorAt(a.Name, example, "%s has no parameter '%s'", name, a.Name.Name)
		return Invalid, true
	}
	if c.setOperand(a.Value, c.expr(a.Value, s), s, name) == Invalid {
		return Invalid, true
	}
	c.info.Calls[e] = &Call{Kind: MemberBuiltinCall, Builtin: name, Recv: m.X, Args: []ast.Expr{a.Value}}
	if name == "isSubset" {
		return Bool, true
	}
	return s, true
}

// Returns a hint for a method other languages give sets, or "".
func setMethodHint(name string) string {
	switch name {
	case "add", "put":
		return "use .insert(value)"
	case "has", "includes":
		return "use .contains(value)"
	case "delete", "discard":
		return "use .remove(value)"
	case "difference", "minus", "subtract":
		return "use .subtracting(other)"
	case "intersect":
		return "use .intersection(other)"
	case "isSubsetOf", "isSubSet":
		return "use .isSubset(of: other)"
	case "size", "count", "length":
		return "read .len"
	}
	return ""
}
