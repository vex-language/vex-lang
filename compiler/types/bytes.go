package types

import (
	"github.com/vex-language/vex-lang/compiler/ast"
)

// Reports whether [name] means the built-in [Bytes] here: it is `Bytes`
// and nothing declared hides it.
func (c *checker) builtinBytes(name string) bool {
	return name == "Bytes" && c.lookupVar(name) == nil && c.structs[name] == nil && c.genStructs[name] == nil &&
		c.enums[name] == nil && c.genEnums[name] == nil && c.interfaces[name] == nil && c.genIfaces[name] == nil &&
		c.handles[name] == nil && c.aliases[name] == nil && c.funcs[name] == nil && c.genFuncs[name] == nil
}

const bytesExample = "e.g. Bytes(), Bytes(count: 16) or Bytes([0x48, 0x69])"

// Checks `Bytes()`, `Bytes(count: n)` and `Bytes(list)`.
func (c *checker) newBytes(e *ast.CallExpr) Type {
	call := &Call{Kind: BuiltinCall, Builtin: "Bytes"}
	c.info.Calls[e] = call
	if e.Trailing != nil {
		c.errorAt(e.Trailing, "", "Bytes doesn't take a trailing block")
	}
	switch {
	case len(e.Args) == 0:
		return Bytes
	case len(e.Args) > 1:
		c.checkArgs(e)
		c.errorAt(e.Fn, bytesExample, "Bytes takes at most 1 argument, found %d", len(e.Args))
		return Bytes
	}
	a := e.Args[0]
	if a.Name != nil {
		if a.Name.Name != "count" {
			c.expr(a.Value, nil)
			c.errorAt(a.Name, bytesExample, "Bytes has no argument '%s'", a.Name.Name)
			return Bytes
		}
		call.Builtin = "Bytes.count"
		call.Args = []ast.Expr{a.Value}
		c.assignable(Int, c.expr(a.Value, Int), a.Value, "argument 'count'")
		return Bytes
	}
	call.Builtin = "Bytes.list"
	call.Args = []ast.Expr{a.Value}
	want := &List{Elem: Int}
	if t := c.expr(a.Value, want); t != Invalid && !Identical(t, want) {
		hint := bytesExample
		if t == String {
			hint = "use s.toBytes() for the UTF-8 bytes of a string"
		}
		c.errorAt(a.Value, hint, "Bytes needs a list of ints, found %s", t)
	}
	return Bytes
}

// Checks a call of the static member [m] of the built-in [Bytes].
func (c *checker) bytesStatic(e *ast.CallExpr, m *ast.MemberExpr) Type {
	name := m.Name.Name
	if name != "fromHex" {
		c.checkArgs(e)
		c.errorAt(m.Name, didYouMean(name, []string{"fromHex"}), "Bytes has no static method '%s'", name)
		return Invalid
	}
	result := &Optional{Elem: Bytes}
	c.info.Calls[e] = &Call{Kind: BuiltinCall, Builtin: "Bytes.fromHex", Args: c.bind(e, []slot{{name: "text", typ: String}}, "Bytes.fromHex")}
	return result
}
