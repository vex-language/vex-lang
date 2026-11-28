// Package doc builds API documentation for Vex modules from their `///`
// doc comments, as HTML pages or as plain text.
package doc

import (
	"path"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/modules"
)

// A documented module.
type Module struct {
	// The name the module is shown with, such as `json` or the program's
	// name.
	Name string

	// How the module is imported, such as `vex:json` or `models`; empty
	// for the root module.
	Path string

	// The `///` comment at the top of the module's files.
	Doc string

	Decls []*Decl
}

// The kind of a documented declaration.
type Kind string

const (
	Struct     Kind = "struct"
	Enum       Kind = "enum"
	Interface  Kind = "interface"
	TypeAlias  Kind = "typedef"
	ExternType Kind = "extern type"
	Extension  Kind = "extension"
	Func       Kind = "fn"
	Const      Kind = "const"
	Variable   Kind = "var"
	Case       Kind = "case"
	Field      Kind = "field"
	Method     Kind = "method"
	Property   Kind = "property"
	Operator   Kind = "operator"
)

// A documented declaration or member, with its signature and doc comment.
type Decl struct {
	Kind Kind

	// The declared name; for an extension, the type it extends.
	Name string

	// The declaration in source form without its body, such as
	// `fn add(a: int, b: int) -> int`.
	Signature string

	// The `///` comment, without the slashes.
	Doc string

	// The cases, fields, properties, methods and constants of a type or
	// extension.
	Members []*Decl

	// The type or extension holding a member, or nil.
	Parent *Decl

	Module *Module

	// The names of the parameters and type parameters, which doc comments
	// may refer to.
	Params []string

	// The file and line the declaration starts on.
	File string
	Line int
}

// Returns the fragment that identifies [d] on its module's page, such as
// `Point` or `Point.x`.
func (d *Decl) Anchor() string {
	name := d.Name
	switch {
	case d.Kind == Extension:
		name = "extension-" + anchorText(d.Name)
	case d.Kind == Property && strings.HasPrefix(d.Signature, "set "):
		// A setter shares its name with the getter.
		name = "set-" + name
	}
	if d.Parent != nil {
		return d.Parent.Anchor() + "." + anchorText(name)
	}
	return name
}

// Returns [s] with characters that don't belong in a fragment replaced.
func anchorText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r == '+':
			b.WriteString("plus")
		case r == '-':
			b.WriteString("minus")
		case r == '*':
			b.WriteString("times")
		case r == '/':
			b.WriteString("div")
		case r == '%':
			b.WriteString("mod")
		case r == '=':
			b.WriteString("eq")
		case r == '<':
			b.WriteString("lt")
		case r == '>':
			b.WriteString("gt")
		case r == '[':
			b.WriteString("index")
		case r == '!':
			b.WriteString("not")
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Returns the name of the HTML page of [m].
func (m *Module) Page() string {
	if m.Path == "" {
		return fileText(m.Name) + ".html"
	}
	return fileText(m.Path) + ".html"
}

// Returns [s] with characters that don't belong in a file name replaced
// by `-`.
func fileText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// Returns the declaration called [name] in [m], or nil.
func (m *Module) Lookup(name string) *Decl {
	for _, d := range m.Decls {
		if d.Kind != Extension && d.Name == name {
			return d
		}
	}
	return nil
}

// Returns the member called [name] of [d], or of an extension of the type
// [d] declares, or nil.
func (d *Decl) Member(name string) *Decl {
	for _, m := range d.Members {
		if m.Name == name {
			return m
		}
	}
	if d.Module != nil && d.Kind != Extension {
		for _, e := range d.Module.Decls {
			if e.Kind == Extension && baseName(e.Name) == d.Name {
				if m := e.Member(name); m != nil {
					return m
				}
			}
		}
	}
	return nil
}

// Returns a written type without its type arguments: `Box` for `Box<T>`.
func baseName(t string) string {
	if i := strings.IndexByte(t, '<'); i >= 0 {
		return t[:i]
	}
	return t
}

// Returns the documentation of the modules of [prog] that aren't part of
// the standard library, the root first.
//
// A root module without any `pub` declaration, such as a program of one
// file, has all its declarations documented; otherwise only `pub` ones.
func FromProgram(prog *modules.Program) []*Module {
	var out []*Module
	for _, m := range prog.Modules {
		if m.Std() {
			continue
		}
		dm := FromModule(m, m.Root && !hasPub(m))
		if m.Root {
			dm.Name = rootName(prog)
		}
		out = append(out, dm)
	}
	return out
}

// Returns the name shown for the root module of [prog]: the project's
// name, or the file's name without `.vx`.
func rootName(prog *modules.Program) string {
	if prog.Name != "" {
		return prog.Name
	}
	if len(prog.Root.Files) > 0 {
		return strings.TrimSuffix(path.Base(strings.ReplaceAll(prog.Root.Files[0].Name, "\\", "/")), ".vx")
	}
	return "main"
}

// Reports whether a file of [m] has a `pub` declaration.
func hasPub(m *modules.Module) bool {
	for _, tree := range m.Trees {
		for _, d := range tree.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Pub {
					return true
				}
			case *ast.StructDecl:
				if d.Pub {
					return true
				}
			case *ast.EnumDecl:
				if d.Pub {
					return true
				}
			case *ast.InterfaceDecl:
				if d.Pub {
					return true
				}
			case *ast.ConstDecl:
				if d.Pub {
					return true
				}
			case *ast.TypeAliasDecl:
				if d.Pub {
					return true
				}
			case *ast.ExternTypeDecl:
				if d.Pub {
					return true
				}
			case *ast.ExtensionDecl:
				if d.Pub {
					return true
				}
			}
		}
	}
	return false
}

// Returns the documentation of [m]: its `pub` declarations and members,
// or every declaration when [all] is set.
func FromModule(m *modules.Module, all bool) *Module {
	dm := &Module{Name: m.Name, Path: m.Path}
	b := &builder{mod: dm, all: all}
	for i, tree := range m.Trees {
		if dm.Doc == "" {
			dm.Doc = tree.Doc
		}
		b.file = m.Files[i].Name
		for _, d := range tree.Decls {
			if x := b.decl(d); x != nil {
				dm.Decls = append(dm.Decls, x)
			}
		}
	}
	return dm
}

// Turns syntax tree declarations into [Decl]s.
type builder struct {
	mod  *Module
	file string
	all  bool
}

func (b *builder) new(kind Kind, name string, n ast.Node, doc string) *Decl {
	return &Decl{Kind: kind, Name: name, Doc: doc, Module: b.mod, File: b.file, Line: n.Pos().Line}
}

func (b *builder) decl(d ast.Decl) *Decl {
	switch d := d.(type) {
	case *ast.FuncDecl:
		if !b.all && !d.Pub || d.Test != "" {
			return nil
		}
		return b.fn(d, nil)
	case *ast.StructDecl:
		if !b.all && !d.Pub {
			return nil
		}
		x := b.new(Struct, d.Name.Name, d, d.Doc)
		x.Signature = "struct " + d.Name.Name + typeParamsString(d.TypeParams) + interfacesString(d.Interfaces)
		x.Params = typeParamNames(d.TypeParams)
		for _, f := range d.Fields {
			if b.all || f.Pub {
				m := b.new(Field, f.Name.Name, f, f.Doc)
				m.Signature = fieldString(f)
				b.add(x, m)
			}
		}
		b.members(x, d.Methods, d.Consts)
		return x
	case *ast.EnumDecl:
		if !b.all && !d.Pub {
			return nil
		}
		x := b.new(Enum, d.Name.Name, d, d.Doc)
		x.Signature = "enum " + d.Name.Name + typeParamsString(d.TypeParams) + interfacesString(d.Interfaces)
		x.Params = typeParamNames(d.TypeParams)
		for _, c := range d.Cases {
			m := b.new(Case, c.Name.Name, c, c.Doc)
			m.Signature = caseString(c)
			for _, f := range c.Fields {
				m.Params = append(m.Params, f.Name.Name)
			}
			b.add(x, m)
		}
		b.members(x, d.Methods, d.Consts)
		return x
	case *ast.InterfaceDecl:
		if !b.all && !d.Pub {
			return nil
		}
		x := b.new(Interface, d.Name.Name, d, d.Doc)
		x.Signature = "interface " + d.Name.Name + typeParamsString(d.TypeParams) + interfacesString(d.Supers)
		x.Params = typeParamNames(d.TypeParams)
		for _, f := range d.Methods {
			b.add(x, b.fn(f, x))
		}
		return x
	case *ast.ExtensionDecl:
		x := b.new(Extension, ast.TypeString(d.Type), d, d.Doc)
		x.Signature = "extension" + typeParamsString(d.TypeParams) + " " + x.Name
		x.Params = typeParamNames(d.TypeParams)
		b.members(x, d.Methods, d.Consts)
		if len(x.Members) == 0 {
			return nil
		}
		return x
	case *ast.ConstDecl:
		if !b.all && !d.Pub {
			return nil
		}
		return b.constant(d, false)
	case *ast.TypeAliasDecl:
		if !b.all && !d.Pub {
			return nil
		}
		x := b.new(TypeAlias, d.Name.Name, d, d.Doc)
		x.Signature = "typedef " + d.Name.Name + typeParamsString(d.TypeParams) + " = " + ast.TypeString(d.Type)
		x.Params = typeParamNames(d.TypeParams)
		return x
	case *ast.ExternTypeDecl:
		if !b.all && !d.Pub {
			return nil
		}
		x := b.new(ExternType, d.Name.Name, d, d.Doc)
		x.Signature = "extern type " + d.Name.Name
		return x
	}
	return nil
}

// Adds member [m] to [parent].
func (b *builder) add(parent, m *Decl) {
	m.Parent = parent
	parent.Members = append(parent.Members, m)
}

// Adds the visible methods and constants of a type or extension to [x].
func (b *builder) members(x *Decl, fns []*ast.FuncDecl, consts []*ast.ConstDecl) {
	for _, k := range consts {
		if b.all || k.Pub {
			b.add(x, b.constant(k, true))
		}
	}
	for _, f := range fns {
		if f.Accessor == ast.Observer || !b.all && !f.Pub {
			continue
		}
		b.add(x, b.fn(f, x))
	}
}

func (b *builder) fn(f *ast.FuncDecl, parent *Decl) *Decl {
	kind := Func
	switch {
	case f.Accessor == ast.Getter || f.Accessor == ast.Setter:
		kind = Property
	case f.Accessor == ast.Operator:
		kind = Operator
	case parent != nil:
		kind = Method
	}
	name := f.Name.Name
	if kind == Operator {
		name = strings.TrimPrefix(name, "unary ")
	}
	x := b.new(kind, name, f, f.Doc)
	x.Signature = funcString(f)
	x.Params = typeParamNames(f.TypeParams)
	for _, p := range f.Params {
		x.Params = append(x.Params, p.Name.Name)
	}
	return x
}

func (b *builder) constant(k *ast.ConstDecl, static bool) *Decl {
	kind := Const
	if k.Lazy {
		kind = Variable
	}
	x := b.new(kind, k.Name.Name, k, k.Doc)
	x.Signature = constString(k, static)
	return x
}

func typeParamNames(tps []*ast.TypeParam) []string {
	var names []string
	for _, tp := range tps {
		names = append(names, tp.Name.Name)
	}
	return names
}
