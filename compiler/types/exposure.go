package types

import (
	"fmt"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// Reports the pub declarations whose signatures name a private type of
// their own module: other modules could reach them but never write the
// type down.
func (c *checker) exposures() {
	for _, d := range c.decls {
		back := c.enter(d)
		c.declExposes(d)
		back()
	}
}

// Reports the private types that the public parts of [d] name.
func (c *checker) declExposes(d ast.Decl) {
	switch d := d.(type) {
	case *ast.FuncDecl:
		if d.Pub {
			what := fmt.Sprintf("function '%s'", d.Name.Name)
			c.funcExposes(d, nil, "pub "+what, what)
		}
	case *ast.StructDecl:
		if !d.Pub {
			return
		}
		what := fmt.Sprintf("struct '%s'", d.Name.Name)
		c.paramsExpose(d.TypeParams, nil, "pub "+what, what)
		for _, f := range d.Fields {
			if f.Pub {
				field := fmt.Sprintf("field '%s'", f.Name.Name)
				c.exposes(f.Type, d.TypeParams, fmt.Sprintf("pub %s of %s", field, d.Name.Name), field)
			}
		}
		c.membersExpose(d.Name.Name, d.TypeParams, d.Methods, d.Consts)
	case *ast.EnumDecl:
		if !d.Pub {
			return
		}
		what := fmt.Sprintf("enum '%s'", d.Name.Name)
		c.paramsExpose(d.TypeParams, nil, "pub "+what, what)
		for _, k := range d.Cases {
			for _, f := range k.Fields {
				c.exposes(f.Type, d.TypeParams, fmt.Sprintf("case '%s' of pub enum %s", k.Name.Name, d.Name.Name), what)
			}
		}
		c.membersExpose(d.Name.Name, d.TypeParams, d.Methods, d.Consts)
	case *ast.InterfaceDecl:
		if !d.Pub {
			return
		}
		what := fmt.Sprintf("interface '%s'", d.Name.Name)
		c.paramsExpose(d.TypeParams, nil, "pub "+what, what)
		for _, s := range d.Supers {
			c.exposes(s, d.TypeParams, "pub "+what, what)
		}
		for _, m := range d.Methods {
			c.funcExposes(m, d.TypeParams, fmt.Sprintf("%s of pub interface %s", memberKind(m), d.Name.Name), what)
		}
	case *ast.TypeAliasDecl:
		if d.Pub {
			what := fmt.Sprintf("typedef '%s'", d.Name.Name)
			c.exposes(d.Type, d.TypeParams, "pub "+what, what)
		}
	case *ast.ConstDecl:
		if d.Pub {
			what := fmt.Sprintf("'%s'", d.Name.Name)
			c.exposes(d.Type, nil, "pub "+what, what)
		}
	case *ast.ExtensionDecl:
		if nt, ok := d.Type.(*ast.NamedType); ok && nt.Module == "" && c.privateKind(nt.Name) != "" {
			return
		}
		for _, m := range d.Methods {
			if m.Pub {
				c.funcExposes(m, d.TypeParams, "pub "+memberKind(m)+" of the extension", memberKind(m))
			}
		}
		for _, k := range d.Consts {
			if k.Pub {
				what := fmt.Sprintf("'%s'", k.Name.Name)
				c.exposes(k.Type, d.TypeParams, "pub "+what+" of the extension", what)
			}
		}
	}
}

// Reports the private types named by the pub methods and constants of
// struct or enum [owner].
func (c *checker) membersExpose(owner string, params []*ast.TypeParam, methods []*ast.FuncDecl, consts []*ast.ConstDecl) {
	for _, m := range methods {
		if m.Pub {
			c.funcExposes(m, params, fmt.Sprintf("pub %s of %s", memberKind(m), owner), memberKind(m))
		}
	}
	for _, k := range consts {
		if k.Pub {
			what := fmt.Sprintf("'%s'", k.Name.Name)
			c.exposes(k.Type, params, fmt.Sprintf("pub %s of %s", what, owner), what)
		}
	}
}

// Names member [m] for messages, such as `method 'area'`.
func memberKind(m *ast.FuncDecl) string {
	kind := map[ast.Accessor]string{ast.Getter: "getter", ast.Setter: "setter", ast.Operator: "operator"}[m.Accessor]
	if kind == "" {
		kind = "method"
	}
	return fmt.Sprintf("%s '%s'", kind, m.Name.Name)
}

// Reports the private types in the signature of [f], whose enclosing
// type has type parameters [outer].
func (c *checker) funcExposes(f *ast.FuncDecl, outer []*ast.TypeParam, what, holder string) {
	params := append(append([]*ast.TypeParam(nil), outer...), f.TypeParams...)
	c.paramsExpose(f.TypeParams, params, what, holder)
	for _, p := range f.Params {
		c.exposes(p.Type, params, what, holder)
	}
	c.exposes(f.Result, params, what, holder)
	if f.Throws != nil {
		c.exposes(f.Throws.Type, params, what, holder)
	}
}

// Reports the private interfaces that constrain type parameters [list].
func (c *checker) paramsExpose(list, scope []*ast.TypeParam, what, holder string) {
	if scope == nil {
		scope = list
	}
	for _, p := range list {
		for _, k := range p.Constraints {
			c.exposes(k, scope, what, holder)
		}
	}
}

// Reports, once each, the private types of this module named in [te],
// part of the public declaration [what]. [holder] is what drops its pub
// to fix it, and names in [params] are type parameters.
func (c *checker) exposes(te ast.TypeExpr, params []*ast.TypeParam, what, holder string) {
	if te == nil {
		return
	}
	seen := map[string]bool{}
	ast.Inspect(te, func(n ast.Node) bool {
		nt, ok := n.(*ast.NamedType)
		if !ok || nt.Module != "" || seen[nt.Name] {
			return true
		}
		for _, p := range params {
			if p.Name.Name == nt.Name {
				return true
			}
		}
		if kind := c.privateKind(nt.Name); kind != "" {
			seen[nt.Name] = true
			c.errorAt(nt, fmt.Sprintf("mark %s '%s' pub, or remove pub from %s", kind, nt.Name, holder),
				"%s exposes private %s '%s'", what, kind, nt.Name)
		}
		return true
	})
}

// Returns what [name] is, such as "struct", when it names a type of this
// module that isn't pub; otherwise "".
func (c *checker) privateKind(name string) string {
	switch {
	case c.structs[name] != nil && c.structs[name].Decl != nil:
		if !c.structs[name].Decl.Pub {
			return "struct"
		}
	case c.genStructs[name] != nil:
		if !c.genStructs[name].decl.Pub {
			return "struct"
		}
	case c.enums[name] != nil && c.enums[name].Decl != nil:
		if !c.enums[name].Decl.Pub {
			return "enum"
		}
	case c.genEnums[name] != nil:
		if !c.genEnums[name].decl.Pub {
			return "enum"
		}
	case c.interfaces[name] != nil && c.interfaces[name].Decl != nil:
		if !c.interfaces[name].Decl.Pub {
			return "interface"
		}
	case c.genIfaces[name] != nil:
		if !c.genIfaces[name].decl.Pub {
			return "interface"
		}
	case c.aliases[name] != nil:
		if !c.aliases[name].decl.Pub {
			return "typedef"
		}
	case c.handles[name] != nil && c.handles[name].Decl != nil:
		if !c.handles[name].Decl.Pub {
			return "extern type"
		}
	}
	return ""
}
