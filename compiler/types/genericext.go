package types

import (
	"github.com/vex-language/vex-lang/compiler/ast"
)

// Returns the generic interface that [nt], written without type
// arguments, names, or nil.
func (c *checker) genIfaceNamed(nt *ast.NamedType) *genericIface {
	if nt.Module == "" && c.fromCore(nt.Name) {
		nt.Module = coreKey
	}
	if nt.Module == "" {
		if _, ok := c.env[nt.Name]; ok {
			return nil
		}
		return c.genIfaces[nt.Name]
	}
	m := c.imported(nt.Module)
	if m == nil {
		return nil
	}
	return m.genIfaces[nt.Name]
}

// Declares the members of [d], an extension of the generic interface [g]
// written as [nt]. They work like those of an extension of a built-in
// generic type: each takes the value as `self`, and can use the
// interface's type parameters.
func (c *checker) declareGenericIfaceExt(d *ast.ExtensionDecl, nt *ast.NamedType, g *genericIface) {
	if len(d.Consts) > 0 {
		c.errorAt(d.Consts[0].Name, "move it to the top level", "an extension of an interface can't add %s", staticKind(d.Consts[0]))
	}
	var params []*TypeParam
	func() {
		defer c.enter(g.decl)()
		c.withEnv(nil, func() { params = c.typeParams(g.decl.TypeParams) })
	}()
	recvTE := &ast.NamedType{Span: nt.Span, Name: nt.Name, Module: nt.Module}
	for _, p := range params {
		recvTE.Args = append(recvTE.Args, &ast.NamedType{Span: nt.Span, Name: p.Name})
	}
	var recv Type
	c.withEnv(envOf(params, paramTypes(params)), func() { recv = c.resolveType(recvTE) })
	if recv == Invalid {
		return
	}
	for _, md := range d.Methods {
		switch {
		case md.Static:
			c.errorAt(md.Name, "move it to the top level", "an extension of an interface can't add static methods")
		case md.Accessor != ast.Method:
			c.errorAt(md.Name, "write a method instead: fn "+md.Name.Name+"() -> ...", "an extension of an interface can only add methods")
		default:
			md.Recv = recvTE
			c.declareRecvFunc(d, md, params, recv)
		}
	}
}

// Returns the types an extension member can be found through for a value
// of type [t]: [t] itself and, for a struct, enum or interface, the
// interfaces it implements or extends.
func (c *checker) recvForms(t Type) []Type {
	out := []Type{t}
	var ifaces []*Interface
	switch t := t.(type) {
	case *Struct:
		ifaces = t.Interfaces
	case *Enum:
		ifaces = t.Interfaces
	case *Interface:
		c.ifaceMembers(t)
		ifaces = t.Supers
	}
	for _, i := range ifaces {
		for _, j := range i.All() {
			out = append(out, j)
		}
	}
	return out
}
