package types

import (
	"fmt"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/modules"
)

// The name under which every file imports the core module. It can't be
// written in code, so the module's names are only reached unqualified.
const coreKey = "\x00core"

// The names the core module gives every file.
var coreNames = map[string]bool{"Iterable": true, "Iterator": true}

// Reports whether [name], written alone, means the core module's
// declaration: nothing in scope here declares it.
func (c *checker) fromCore(name string) bool {
	if !coreNames[name] || c.imports[coreKey] == nil {
		return false
	}
	if _, ok := c.env[name]; ok {
		return false
	}
	return c.structs[name] == nil && c.enums[name] == nil && c.interfaces[name] == nil &&
		c.genStructs[name] == nil && c.genEnums[name] == nil && c.genIfaces[name] == nil &&
		c.aliases[name] == nil && c.handles[name] == nil
}

// Reports whether [i] is an instance of the core module's interface
// called [name], such as `Iterable<int>`.
func coreIface(i *Interface, name string) bool {
	return i.Base == name && i.Module != nil && i.Module.Path == "vex:"+modules.CoreName
}

// Returns the `Iterable<T>` that a value of type [t] can be looped over
// as, or nil.
func (c *checker) iterableOf(t Type) *Interface {
	for _, f := range c.recvForms(t) {
		if i, ok := f.(*Interface); ok && coreIface(i, "Iterable") {
			c.ifaceMembers(i)
			return i
		}
	}
	return nil
}

// Returns the `Iterator<T>` that the iterator method of [it], an
// `Iterable<T>`, returns.
func IteratorOf(it *Interface) *Interface {
	return it.Method("iterator").Result.(*Interface)
}

// Reports whether [body] has a yield statement of its own, outside the
// closures and local functions inside it.
func hasYield(body *ast.Block) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.YieldStmt:
			found = true
		case *ast.FuncLit, *ast.FuncDecl:
			return false
		}
		return !found
	})
	return found
}

// Reports, and returns false, when [args] holds an optional for the core
// module's Iterable or Iterator: next gives nil when no values are left,
// so a nil value couldn't be told apart from the end.
func (c *checker) coreArgsOK(g *genericIface, args []Type, at ast.Node) bool {
	u := c.units[g.decl]
	if u == nil || u.mod.Path != "vex:"+modules.CoreName || !coreNames[g.decl.Name.Name] || len(args) != 1 {
		return true
	}
	o, ok := args[0].(*Optional)
	if !ok {
		return true
	}
	c.errorAt(at, fmt.Sprintf("give values of %s, or wrap each one in an enum or a struct", o.Elem), "%s<%s> can't hold optionals, since nil from next marks the end", g.decl.Name.Name, o)
	return false
}
