package types

import (
	"fmt"
	"path/filepath"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// Declares the methods of `extension Name { ... }` for interface [i]. Their
// signatures are resolved once the interfaces' own members are.
func (c *checker) declareIfaceExt(d *ast.ExtensionDecl, i *Interface) {
	if i.generic != nil || i.Args != nil {
		name := i.Base
		if name == "" {
			name = i.Name
		}
		c.errorAt(d.Type, fmt.Sprintf("write extension %s { ... }; its methods can use the interface's own type parameters", name), "an extension of a generic interface names it without type arguments")
		return
	}
	if len(d.Consts) > 0 {
		c.errorAt(d.Consts[0].Name, "move it to the top level", "an extension of an interface can't add %s", staticKind(d.Consts[0]))
	}
	for _, md := range d.Methods {
		switch {
		case md.Static:
			c.errorAt(md.Name, "move it to the top level", "an extension of an interface can't add static methods")
			continue
		case md.Accessor != ast.Method:
			c.errorAt(md.Name, "write a method instead: fn "+md.Name.Name+"() -> ...", "an extension of an interface can only add methods")
			continue
		case len(md.TypeParams) > 0:
			c.errorAt(md.TypeParams[0], "write it as a generic top-level function", "methods added to an interface can't have their own type parameters yet")
			continue
		}
		c.units[md] = c.units[d]
		c.pendingExts = append(c.pendingExts, &Func{Name: md.Name.Name, Module: c.mod, Decl: md, Iface: i, Ext: c.mod})
	}
}

// Resolves the methods added to interfaces by extensions.
func (c *checker) ifaceExts() {
	pending := c.pendingExts
	c.pendingExts = nil
	for _, f := range pending {
		i := f.Iface
		c.ifaceMembers(i)
		back := c.enter(f.Decl)
		switch {
		case c.reserved(f.Decl.Name, "a method"):
		case c.extClash(i, f):
		default:
			c.signature(f)
			i.Exts = append(i.Exts, f)
			c.info.FuncOf[f.Decl] = f
			c.info.Funcs = append(c.info.Funcs, f)
		}
		back()
	}
}

// Reports, and returns true, when [f] can't be added to [i]: the name is
// required by it, added by another extension, or already a method of a
// type that implements it.
func (c *checker) extClash(i *Interface, f *Func) bool {
	if j, m := ifaceMethod(i, f.Name); m != nil {
		c.errorAt(f.Decl.Name, "give it another name", "'%s' is already required by %s", f.Name, j.Name)
		return true
	}
	if prev := extOf(i, f.Name); prev != nil {
		where := fmt.Sprintf("on line %d", prev.Decl.Pos().Line)
		if u := c.units[prev.Decl]; u != c.units[f.Decl] {
			where += " of " + filepath.Base(u.file.Name)
		}
		c.errorAt(f.Decl.Name, "give it another name", "'%s' is already added to %s %s", f.Name, i.Name, where)
		return true
	}
	for _, s := range c.info.Structs {
		if implements(s, i) && s.Method(f.Name) != nil {
			c.errorAt(f.Decl.Name, "give it another name", "%s implements %s and already has a method '%s'", s.Name, i.Name, f.Name)
			return true
		}
	}
	return false
}

// Returns the method called [name] that an extension adds to [i] or an
// interface it extends, or nil.
func extOf(i *Interface, name string) *Func {
	for _, j := range i.All() {
		for _, f := range j.Exts {
			if f.Name == name {
				return f
			}
		}
	}
	return nil
}

// Returns the interfaces whose extensions apply to a value of type [t].
func extIfaces(t Type) []*Interface {
	switch t := t.(type) {
	case *Struct:
		return t.Interfaces
	case *Enum:
		return t.Interfaces
	}
	return ifacesOf(t)
}

// Checks a call of [name] on a value of type [t], [recv] (nil for self),
// when it is a method an interface extension adds. Reports false when
// none applies.
func (c *checker) extCall(e *ast.CallExpr, recv ast.Expr, at ast.Node, t Type, name string) (Type, bool) {
	var f *Func
	for _, i := range extIfaces(t) {
		if f = extOf(i, name); f != nil {
			break
		}
	}
	if f == nil {
		return nil, false
	}
	if c.methodPrivate(at, f) {
		c.checkArgs(e)
		return Invalid, true
	}
	c.info.Calls[e] = &Call{Kind: MethodCall, Func: f, Recv: recv, Args: c.bind(e, paramSlots(f), name)}
	return f.Result, true
}

// Reports the methods of a type, among [methods], that clash with ones
// extensions add to the interfaces it implements, [ifaces].
func (c *checker) extClashes(ifaces []*Interface, methods []*Func, owns func(*Func) bool) {
	for _, i := range ifaces {
		for _, j := range i.All() {
			for _, f := range j.Exts {
				for _, m := range methods {
					if m.Name == f.Name && owns(m) {
						c.errorAt(m.Decl.Name, "give it another name", "'%s' is already added to every %s by an extension", m.Name, j.Name)
					}
				}
			}
		}
	}
}
