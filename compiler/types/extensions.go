package types

import (
	"fmt"
	"slices"

	"github.com/irshadio/vex-lang/compiler/ast"
)

// A method or getter that an extension adds to a built-in type such as
// `string` or `[T]`. It is compiled as a function that takes the value as
// `self`.
type recvFunc struct {
	decl *ast.FuncDecl

	// Set when neither the extension nor the method has type parameters.
	plain *Func

	// Set otherwise; the extension's type parameters come first.
	gen *genericFunc
}

// The types an extension can add to, besides structs and enums.
func extendable(t Type) bool {
	switch t.(type) {
	case *List, *Map:
		return true
	}
	return t == Int || t == Float || t == Bool || t == String
}

// Declares the members of extension [d]. Those of a struct or enum join
// its own, so they work like members declared in it.
func (c *checker) declareExtension(d *ast.ExtensionDecl) {
	if target := c.extensionTarget(d); target != nil {
		if len(d.TypeParams) > 0 {
			// `extension<T> Box<T>` is reported once, at the type.
			if nt, ok := d.Type.(*ast.NamedType); !ok || len(nt.Args) == 0 {
				c.errorAt(d.TypeParams[0], fmt.Sprintf("write extension %s { ... }; its members can use the type's own type parameters", ast.TypeString(d.Type)), "an extension of a struct or enum takes no type parameters")
			}
			return
		}
		u := c.units[d]
		switch t := target.(type) {
		case *ast.StructDecl:
			t.Methods = append(t.Methods, d.Methods...)
		case *ast.EnumDecl:
			for _, m := range d.Methods {
				if m.Accessor == ast.Operator {
					c.errorAt(m.Name, "use a method, or a struct that holds the enum", "enums can't define operators yet")
				} else {
					t.Methods = append(t.Methods, m)
				}
			}
		}
		for _, m := range d.Methods {
			c.units[m] = u
		}
		c.extTargets[d] = target
		return
	}
	var params []*TypeParam
	if len(d.TypeParams) > 0 {
		params = c.typeParams(d.TypeParams)
	}
	var recv Type
	c.withEnv(envOf(params, paramTypes(params)), func() { recv = c.resolveType(d.Type) })
	switch {
	case recv == Invalid:
		return
	case !extendable(recv):
		hint := "extend a struct, an enum, int, float, bool, string, a list or a map"
		if _, ok := recv.(*Interface); ok {
			hint = "give the interface a default method instead"
		}
		c.errorAt(d.Type, hint, "%s can't be extended", recv)
		return
	}
	if len(d.Consts) > 0 {
		c.errorAt(d.Consts[0].Name, "move it to the top level", "only extensions of structs and enums can add constants")
	}
	for _, md := range d.Methods {
		c.declareRecvFunc(d, md, params, recv)
	}
}

// Returns the declaration of the struct or enum that [d] extends, or nil
// when it extends another type.
func (c *checker) extensionTarget(d *ast.ExtensionDecl) ast.Decl {
	nt, ok := d.Type.(*ast.NamedType)
	if !ok {
		return nil
	}
	tables := c.tables
	if nt.Module != "" {
		m := c.imported(nt.Module)
		if m == nil {
			return nil
		}
		tables = m.tables
	}
	var generic ast.Decl
	if g := tables.genStructs[nt.Name]; g != nil {
		generic = g.decl
	} else if g := tables.genEnums[nt.Name]; g != nil {
		generic = g.decl
	}
	if generic != nil && len(nt.Args) > 0 {
		name := nt.Name
		if nt.Module != "" {
			name = nt.Module + "." + name
		}
		c.errorAt(nt, fmt.Sprintf("write extension %s { ... }; its members can use the type's own type parameters", name), "an extension of a generic struct or enum names it without type arguments")
		return generic
	}
	if generic != nil {
		return generic
	}
	if len(nt.Args) > 0 {
		return nil
	}
	var t Type
	c.scratch(false, func() { t = c.resolveType(nt) })
	switch t := t.(type) {
	case *Struct:
		if t.Decl != nil {
			return t.Decl
		}
	case *Enum:
		if t.Decl != nil {
			return t.Decl
		}
	}
	return nil
}

// Declares [md], a member of extension [d] of the built-in type [recv].
func (c *checker) declareRecvFunc(d *ast.ExtensionDecl, md *ast.FuncDecl, params []*TypeParam, recv Type) {
	name := md.Name.Name
	switch {
	case md.Static:
		c.errorAt(md.Name, "make it a top-level function", "only extensions of structs and enums can add static members")
		return
	case md.Accessor == ast.Setter:
		c.errorAt(md.Name, "make it a method", "only extensions of structs and enums can add setters")
		return
	case md.Accessor == ast.Operator:
		c.errorAt(md.Name, "make it a method", "only extensions of structs can add operators")
		return
	case c.reserved(md.Name, "a method"):
		return
	case slices.Contains(builtinMemberNames(recv), name) || recv == String && name == "len":
		c.errorAt(md.Name, "choose another name", "%s already has a member '%s'", recv, name)
		return
	}
	for _, r := range c.recvs[name] {
		if r.decl.Accessor == md.Accessor && r.decl.Recv != nil && ast.TypeString(r.decl.Recv) == ast.TypeString(d.Type) {
			c.errorAt(md.Name, "", "'%s' is already declared for %s on line %d", name, recv, r.decl.Pos().Line)
			return
		}
	}
	r := &recvFunc{decl: md}
	if len(params) == 0 && len(md.TypeParams) == 0 {
		r.plain = &Func{Name: name, Module: c.mod, Decl: md, Ext: c.mod}
		c.info.FuncOf[md] = r.plain
		c.info.Funcs = append(c.info.Funcs, r.plain)
	} else {
		all := params
		if len(md.TypeParams) > 0 {
			c.withEnv(envOf(params, paramTypes(params)), func() { all = append(slices.Clone(params), c.typeParams(md.TypeParams)...) })
		}
		r.gen = &genericFunc{decl: md, params: all, insts: map[string]*Func{}}
		g := r.gen
		f := c.funcTemplate(g)
		c.jobs = append(c.jobs, func() {
			n := len(c.errs)
			c.scratch(true, func() {
				c.withEnv(envOf(g.params, paramTypes(g.params)), func() {
					c.paramDefaults(f)
					c.funcBody(f)
				})
			})
			g.failed = len(c.errs) > n
		})
	}
	c.recvs[name] = append(c.recvs[name], r)
}

// Returns the members called [name] of extensions of built-in types that
// can be used here: those of this module, and the public ones of the
// modules this file imports.
func (c *checker) visibleRecvs(name string) []*recvFunc {
	out := slices.Clone(c.recvs[name])
	for _, m := range c.imports {
		if m == c.mod {
			continue
		}
		for _, r := range m.recvs[name] {
			if r.decl.Pub {
				out = append(out, r)
			}
		}
	}
	return out
}

// Returns the extension member [name] of kind [kind] for a value of type
// [recv], with the type arguments its extension takes for it, or nil.
func (c *checker) findRecv(recv Type, name string, kind ast.Accessor, at ast.Node) (*recvFunc, *binding) {
	var found []*recvFunc
	var binds []*binding
	for _, r := range c.visibleRecvs(name) {
		if r.decl.Accessor != kind {
			continue
		}
		if r.plain != nil {
			if r.plain.Recv != nil && Identical(r.plain.Recv, recv) {
				// A plain extension is more specific than a generic one.
				return r, nil
			}
			continue
		}
		tmpl := c.funcTemplate(r.gen)
		if tmpl.Recv == nil {
			continue
		}
		b := &binding{params: r.gen.params, types: map[*TypeParam]Type{}}
		b.unify(tmpl.Recv, recv)
		if Identical(c.substType(tmpl.Recv, b.types), recv) {
			found = append(found, r)
			binds = append(binds, b)
		}
	}
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
		return found[0], binds[0]
	}
	c.errorAt(at, "", "'%s' of %s is declared by more than one extension, on lines %d and %d", name, recv, found[0].decl.Pos().Line, found[1].decl.Pos().Line)
	return found[0], binds[0]
}

// Checks `x.name(...)`, a call of an extension method on [recv], a value
// of a built-in type. It returns nil when no extension has the method.
func (c *checker) recvCall(e *ast.CallExpr, m *ast.MemberExpr, recv Type, want Type) Type {
	r, b := c.findRecv(recv, m.Name.Name, ast.Method, m.Name)
	if r == nil {
		return nil
	}
	f := r.plain
	if f == nil {
		args := c.inferBound(e, b, paramSlots(c.funcTemplate(r.gen)), c.funcTemplate(r.gen).Result, want, m.Name.Name)
		if args == nil {
			return Invalid
		}
		if f = c.instFunc(r.gen, args, e); f == nil {
			c.checkArgs(e)
			return Invalid
		}
	}
	c.info.Calls[e] = &Call{Kind: MethodCall, Func: f, Recv: m.X, Args: c.bind(e, paramSlots(f), f.Name)}
	return f.Result
}

// Checks `x.name`, a getter that an extension adds to [recv], a built-in
// type. It returns nil when no extension has the getter.
func (c *checker) recvGetter(e *ast.MemberExpr, recv Type) Type {
	r, b := c.findRecv(recv, e.Name.Name, ast.Getter, e.Name)
	if r == nil {
		return nil
	}
	f := r.plain
	if f == nil {
		args := make([]Type, len(r.gen.params))
		for i, p := range r.gen.params {
			if args[i] = b.types[p]; args[i] == nil || !c.meetsConstraints(p, args[i], e.Name, e.Name.Name, b.types) {
				return Invalid
			}
		}
		if f = c.instFunc(r.gen, args, e); f == nil {
			return Invalid
		}
	}
	c.info.Members[e] = &Member{Property: &Property{Get: f}}
	return f.Result
}

// Reports whether [name], written alone inside a member of an extension
// of a built-in type, means a member of `self`.
func (c *checker) selfMember(name string) bool {
	if c.fn == nil || c.fn.Recv == nil || c.self == nil || c.lookupVar(name) != nil {
		return false
	}
	if slices.Contains(builtinMemberNames(c.self.Type), name) {
		return true
	}
	for _, r := range c.visibleRecvs(name) {
		if r.plain != nil && r.plain.Recv != nil && Identical(r.plain.Recv, c.self.Type) {
			return true
		}
		if r.gen != nil {
			if tmpl := c.funcTemplate(r.gen); tmpl.Recv != nil {
				b := &binding{params: r.gen.params, types: map[*TypeParam]Type{}}
				b.unify(tmpl.Recv, c.self.Type)
				if Identical(c.substType(tmpl.Recv, b.types), c.self.Type) {
					return true
				}
			}
		}
	}
	return false
}

// Returns `self.name`, spelled where [id] is.
func selfAccess(id *ast.Ident) *ast.MemberExpr {
	return &ast.MemberExpr{Span: id.Span, X: &ast.Ident{Span: id.Span, Name: "self"}, Name: &ast.Ident{Span: id.Span, Name: id.Name}}
}

// Reports, and returns true, when [m], a member an extension in another
// module adds, can't be used here because that module isn't imported.
func (c *checker) extensionHidden(at ast.Node, m *Func) bool {
	if m.Ext == nil || m.Ext == c.mod {
		return false
	}
	for _, imp := range c.imports {
		if imp == m.Ext {
			return false
		}
	}
	c.errorAt(at, "import that module to use it", "'%s' comes from an extension in module %s", m.Name, m.Ext.Name)
	return true
}

// Reports, and returns true, when an extension of [recv] in a module that
// isn't imported here, or a private one of another module, has [name].
func (c *checker) recvUnavailable(at ast.Node, recv Type, name string) bool {
	for _, m := range c.info.Modules {
		if m == c.mod {
			continue
		}
		for _, r := range m.recvs[name] {
			f := r.plain
			if f == nil {
				f = c.funcTemplate(r.gen)
				b := &binding{params: r.gen.params, types: map[*TypeParam]Type{}}
				if f.Recv == nil {
					continue
				}
				b.unify(f.Recv, recv)
				if !Identical(c.substType(f.Recv, b.types), recv) {
					continue
				}
			} else if f.Recv == nil || !Identical(f.Recv, recv) {
				continue
			}
			imported := false
			for _, imp := range c.imports {
				imported = imported || imp == m
			}
			if !imported {
				c.errorAt(at, "import that module to use it", "'%s' comes from an extension in module %s", name, m.Name)
			} else {
				kind := map[ast.Accessor]string{ast.Method: "method", ast.Getter: "getter"}[r.decl.Accessor]
				c.private(at, m, false, fmt.Sprintf("%s '%s' of the extension", kind, name))
			}
			return true
		}
	}
	return false
}
