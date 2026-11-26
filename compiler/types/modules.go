package types

import (
	"fmt"
	"sort"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/diag"
	"github.com/irshadio/vex-lang/compiler/modules"
)

// A module as the checker sees it: the top-level names of one folder.
type Module struct {
	// How it is imported, such as `vex:math` or `models`; empty for the
	// root module.
	Path string
	Name string

	// The module's place in the program; 0 for the root module.
	Index int

	tables
}

// Returns the module as written in messages, such as `vex:math`.
func (m *Module) String() string {
	if m.Path == "" {
		return "main"
	}
	return m.Path
}

// The top-level names of a module.
type tables struct {
	structs    map[string]*Struct
	enums      map[string]*Enum
	funcs      map[string]*Func
	interfaces map[string]*Interface
	genFuncs   map[string]*genericFunc
	genStructs map[string]*genericStruct
	genEnums   map[string]*genericEnum
	genIfaces  map[string]*genericIface
	handles    map[string]*Handle
	consts     map[string]*Const
	aliases    map[string]*typeAlias
	recvs      map[string][]*recvFunc
}

func newTables() tables {
	return tables{
		structs:    map[string]*Struct{},
		consts:     map[string]*Const{},
		enums:      map[string]*Enum{},
		funcs:      map[string]*Func{},
		interfaces: map[string]*Interface{},
		genFuncs:   map[string]*genericFunc{},
		genStructs: map[string]*genericStruct{},
		genEnums:   map[string]*genericEnum{},
		genIfaces:  map[string]*genericIface{},
		handles:    map[string]*Handle{},
		aliases:    map[string]*typeAlias{},
		recvs:      map[string][]*recvFunc{},
	}
}

// Where a declaration lives: its module, its file, and the modules that
// file imports.
type unit struct {
	mod     *Module
	file    *diag.File
	imports map[string]*Module
}

// Records the units of [clone], a copy of the struct or enum [orig] made
// for an instance: those of [orig], method by method, as members that
// extensions add live in the extension's module.
func (c *checker) registerClone(clone, orig ast.Decl) {
	c.register(clone, c.units[orig])
	var from, to []*ast.FuncDecl
	switch o := orig.(type) {
	case *ast.StructDecl:
		from, to = o.Methods, clone.(*ast.StructDecl).Methods
	case *ast.EnumDecl:
		from, to = o.Methods, clone.(*ast.EnumDecl).Methods
	}
	for i, m := range from {
		c.units[to[i]] = c.units[m]
	}
}

// Returns the module of the extension that declares [md], or nil when a
// struct or enum declares it.
func (c *checker) extModule(md *ast.FuncDecl) *Module {
	if md.Recv == nil {
		return nil
	}
	if u := c.units[md]; u != nil {
		return u.mod
	}
	return nil
}

// Records that [d], and the methods inside it, live in [u].
func (c *checker) register(d ast.Decl, u *unit) {
	c.units[d] = u
	switch d := d.(type) {
	case *ast.StructDecl:
		for _, m := range d.Methods {
			c.units[m] = u
		}
	case *ast.EnumDecl:
		for _, m := range d.Methods {
			c.units[m] = u
		}
	case *ast.InterfaceDecl:
		for _, m := range d.Methods {
			c.units[m] = u
		}
	case *ast.ExtensionDecl:
		for _, m := range d.Methods {
			c.units[m] = u
		}
	}
}

// Switches to the module and file of declaration [n] and returns a func
// that switches back.
func (c *checker) enter(n ast.Node) func() {
	u := c.units[n]
	if u == nil || u == c.unit {
		return func() {}
	}
	saved := c.unit
	c.setUnit(u)
	return func() { c.setUnit(saved) }
}

func (c *checker) setUnit(u *unit) {
	c.unit = u
	c.mod = u.mod
	c.file = u.file
	c.imports = u.imports
	c.tables = u.mod.tables
}

// Returns the module of an imported name such as `math` in `math.sqrt`.
func (c *checker) imported(name string) *Module { return c.imports[name] }

// Reports, and returns true, when [what] in module [m] can't be used here
// because it isn't marked pub.
func (c *checker) private(at ast.Node, m *Module, pub bool, what string) bool {
	if pub || m == nil || m == c.mod {
		return false
	}
	c.errorAt(at, fmt.Sprintf("mark it pub in %s to use it from other modules", m), "%s is private to %s", what, m)
	return true
}

// Returns the names module [m] makes public, for suggestions.
func (m *Module) publicNames() []string {
	var names []string
	for n, f := range m.funcs {
		if f.Decl != nil && f.Decl.Pub {
			names = append(names, n)
		}
	}
	for n, g := range m.genFuncs {
		if g.decl.Pub {
			names = append(names, n)
		}
	}
	for n, s := range m.structs {
		if s.Decl != nil && s.Decl.Pub {
			names = append(names, n)
		}
	}
	for n, g := range m.genStructs {
		if g.decl.Pub {
			names = append(names, n)
		}
	}
	for n, e := range m.enums {
		if e.Decl.Pub {
			names = append(names, n)
		}
	}
	for n, g := range m.genEnums {
		if g.decl.Pub {
			names = append(names, n)
		}
	}
	for n, g := range m.genIfaces {
		if g.decl.Pub {
			names = append(names, n)
		}
	}
	for n, h := range m.handles {
		if h.Decl.Pub {
			names = append(names, n)
		}
	}
	for n, i := range m.interfaces {
		if i.Decl.Pub {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// Reports that module [m] has no public [name].
func (c *checker) unknownIn(at ast.Node, m *Module, name string) {
	c.errorAt(at, didYouMean(name, m.publicNames()), "%s has no '%s'", m, name)
}

// Reports whether a top-level declaration is public.
func declPub(d ast.Decl) bool {
	switch d := d.(type) {
	case *ast.FuncDecl:
		return d.Pub
	case *ast.StructDecl:
		return d.Pub
	case *ast.EnumDecl:
		return d.Pub
	case *ast.InterfaceDecl:
		return d.Pub
	case *ast.ExternTypeDecl:
		return d.Pub
	case *ast.TypeAliasDecl:
		return d.Pub
	}
	return false
}

// Turns `mod.name`, where `mod` is a module imported by the file, into a
// single name that remembers its module.
func qualify(tree *ast.File, imports map[string]*Module) {
	if len(imports) == 0 {
		return
	}
	ast.Rewrite(tree, func(e ast.Expr) ast.Expr {
		m, ok := e.(*ast.MemberExpr)
		if !ok {
			return e
		}
		id, ok := m.X.(*ast.Ident)
		if !ok || id.Module != "" || imports[id.Name] == nil {
			return e
		}
		return &ast.Ident{Span: m.Span, Name: m.Name.Name, Module: id.Name}
	})
}

// Checks a whole program and returns what was learned about it.
//
// On errors the returned error is a [diag.List] whose diagnostics name
// their files; the [Info] is still returned but may be incomplete.
func CheckProgram(prog *modules.Program) (*Info, error) {
	c := newChecker()
	mods := map[*modules.Module]*Module{}
	for i, pm := range prog.Modules {
		mods[pm] = &Module{Path: pm.Path, Name: pm.Name, Index: i, tables: newTables()}
	}
	root := mods[prog.Root]
	c.root = root
	for _, pm := range prog.Modules {
		m := mods[pm]
		c.info.Modules = append(c.info.Modules, m)
		for i, f := range pm.Files {
			u := &unit{mod: m, file: f, imports: map[string]*Module{}}
			for name, imp := range pm.Imports[f] {
				u.imports[name] = mods[imp.Module]
			}
			tree := pm.Trees[i]
			qualify(tree, u.imports)
			for _, d := range tree.Decls {
				c.register(d, u)
				c.decls = append(c.decls, d)
			}
			if m == root && c.rootUnit == nil {
				c.rootUnit = u
			}
		}
	}
	if c.rootUnit == nil {
		c.rootUnit = &unit{mod: root, imports: map[string]*Module{}}
	}
	c.setUnit(c.rootUnit)
	return c.run()
}

// Builds a program of a single file without imports.
func singleFile(f *diag.File, file *ast.File) *modules.Program {
	m := &modules.Module{
		Name:    "main",
		Root:    true,
		Files:   []*diag.File{f},
		Trees:   []*ast.File{file},
		Imports: map[*diag.File]map[string]*modules.Import{f: {}},
	}
	return &modules.Program{Modules: []*modules.Module{m}, Root: m}
}

// Reports, and returns true, when field [f] is private to another module.
func (c *checker) fieldPrivate(at ast.Node, f *Field) bool {
	if f.Owner == nil || f.Decl == nil {
		return false
	}
	return c.private(at, f.Owner.Module, f.Decl.Pub, fmt.Sprintf("field '%s' of %s", f.Name, f.Owner))
}

// Reports, and returns true, when method [m] is private to another
// module. Methods an interface requires are public with the interface.
func (c *checker) methodPrivate(at ast.Node, m *Func) bool {
	if c.extensionHidden(at, m) {
		return true
	}
	if m.Decl == nil || m.Decl.Pub || m.Iface != nil {
		return false
	}
	if m.Ext != nil {
		kind := map[ast.Accessor]string{ast.Method: "method", ast.Getter: "getter", ast.Setter: "setter"}[m.Decl.Accessor]
		return c.private(at, m.Ext, false, fmt.Sprintf("%s '%s' of the extension", kind, m.Name))
	}
	var mod *Module
	owner := ""
	switch {
	case m.Owner != nil:
		mod, owner = m.Owner.Module, m.Owner.String()
		if requiredBy(m.Owner.Interfaces, m) {
			return false
		}
	case m.Enum != nil:
		mod, owner = m.Enum.Module, m.Enum.String()
		if requiredBy(m.Enum.Interfaces, m) {
			return false
		}
	default:
		return false
	}
	kind := map[ast.Accessor]string{ast.Method: "method", ast.Getter: "getter", ast.Setter: "setter"}[m.Decl.Accessor]
	return c.private(at, mod, false, fmt.Sprintf("%s '%s' of %s", kind, m.Name, owner))
}

// Reports whether one of [ifaces] requires [m], which makes it public.
func requiredBy(ifaces []*Interface, m *Func) bool {
	for _, i := range ifaces {
		if memberOf(i.Methods, m.Name, m.Decl.Accessor) != nil {
			return true
		}
	}
	return false
}

// Reports when struct [s] of another module can't be created here because
// of private fields: one is given, or one has no default.
func (c *checker) constructPrivate(e *ast.CallExpr, s *Struct) bool {
	if s.Module == nil || s.Module == c.mod {
		return false
	}
	for _, f := range s.Fields {
		if f.Decl == nil || f.Decl.Pub {
			continue
		}
		for _, a := range e.Args {
			if a.Name != nil && a.Name.Name == f.Name {
				c.errorAt(a.Name, fmt.Sprintf("mark it pub in %s to set it from other modules", s.Module), "field '%s' of %s is private to %s", f.Name, s, s.Module)
				return true
			}
		}
		if f.Default == nil {
			c.errorAt(e.Fn, fmt.Sprintf("give '%s' a default or mark it pub, or add a pub static fn that creates it", f.Name),
				"%s can't be created outside %s because field '%s' is private", s, s.Module, f.Name)
			return true
		}
	}
	return false
}

// Returns the type written as `mod.Name`.
func (c *checker) qualifiedType(te *ast.NamedType) Type {
	m := c.imported(te.Module)
	if m == nil {
		c.errorAt(te, fmt.Sprintf("import it at the top of the file, e.g. import \"%s\"", te.Module), "unknown module '%s'", te.Module)
		return Invalid
	}
	if g := m.genStructs[te.Name]; g != nil {
		if c.private(te, m, g.decl.Pub, "struct '"+te.Name+"'") {
			return Invalid
		}
		if len(te.Args) == 0 {
			c.errorAt(te, fmt.Sprintf("write its type arguments, e.g. %s.%s<%s>", te.Module, te.Name, typeParamExample(g.params)), "%s.%s is generic, so it needs type arguments", te.Module, te.Name)
			return Invalid
		}
		return c.genericInst(te, g, c.typeArgs(te))
	}
	if g := m.genEnums[te.Name]; g != nil {
		if c.private(te, m, g.decl.Pub, "enum '"+te.Name+"'") {
			return Invalid
		}
		if len(te.Args) == 0 {
			c.errorAt(te, fmt.Sprintf("write its type arguments, e.g. %s.%s<%s>", te.Module, te.Name, typeParamExample(g.params)), "%s.%s is generic, so it needs type arguments", te.Module, te.Name)
			return Invalid
		}
		return c.genericEnumInst(te, g, c.typeArgs(te))
	}
	if g := m.genIfaces[te.Name]; g != nil {
		if c.private(te, m, g.decl.Pub, "interface '"+te.Name+"'") {
			return Invalid
		}
		if len(te.Args) == 0 {
			c.errorAt(te, fmt.Sprintf("write its type arguments, e.g. %s.%s<%s>", te.Module, te.Name, typeParamExample(c.ifaceParams(g))), "%s.%s is generic, so it needs type arguments", te.Module, te.Name)
			return Invalid
		}
		return c.genericIfaceInst(te, g, c.typeArgs(te))
	}
	if a := m.aliases[te.Name]; a != nil {
		if c.private(te, m, a.decl.Pub, "typedef '"+te.Name+"'") {
			return Invalid
		}
		return c.aliasType(te, a)
	}
	var t Type
	pub := false
	switch {
	case m.structs[te.Name] != nil && m.structs[te.Name].Decl != nil:
		s := m.structs[te.Name]
		t, pub = s, s.Decl.Pub
	case m.enums[te.Name] != nil:
		e := m.enums[te.Name]
		t, pub = e, e.Decl.Pub
	case m.interfaces[te.Name] != nil:
		i := m.interfaces[te.Name]
		t, pub = i, i.Decl.Pub
	case m.handles[te.Name] != nil:
		h := m.handles[te.Name]
		t, pub = h, h.Decl.Pub
	default:
		c.unknownIn(te, m, te.Name)
		return Invalid
	}
	if len(te.Args) > 0 {
		c.errorAt(te, "", "'%s.%s' is not generic, so it takes no type arguments", te.Module, te.Name)
		return Invalid
	}
	if c.private(te, m, pub, fmt.Sprintf("'%s'", te.Name)) {
		return Invalid
	}
	return t
}

// Checks a call of a name from an imported module, as in `math.sqrt(x)`.
func (c *checker) moduleCall(e *ast.CallExpr, id *ast.Ident, want Type) Type {
	m := c.imported(id.Module)
	name := id.Name
	fail := func() Type {
		c.checkArgs(e)
		return Invalid
	}
	if m.Path == "vex:isolate" && (name == "run" || name == "spawn") {
		return c.isolateCall(e, name, want)
	}
	if len(e.TypeArgs) > 0 && m.genFuncs[name] == nil && m.genStructs[name] == nil {
		c.errorAt(e.TypeArgs[0], "remove the <...>", "'%s.%s' is not generic, so it takes no type arguments", id.Module, name)
	}
	switch {
	case m.funcs[name] != nil:
		f := m.funcs[name]
		if c.private(id, m, f.Decl.Pub, "function '"+name+"'") {
			return fail()
		}
		c.info.Calls[e] = &Call{Kind: FuncCall, Func: f, Args: c.bind(e, paramSlots(f), id.Module+"."+name)}
		return f.Result
	case m.genFuncs[name] != nil:
		g := m.genFuncs[name]
		if c.private(id, m, g.decl.Pub, "function '"+name+"'") {
			return fail()
		}
		return c.genericFuncCall(e, g, want)
	case m.genStructs[name] != nil:
		g := m.genStructs[name]
		if c.private(id, m, g.decl.Pub, "struct '"+name+"'") {
			return fail()
		}
		return c.genericStructCall(e, g, want)
	case m.structs[name] != nil && m.structs[name].Decl != nil:
		s := m.structs[name]
		if c.private(id, m, s.Decl.Pub, "struct '"+name+"'") || c.constructPrivate(e, s) {
			return fail()
		}
		return c.construct(e, s)
	case m.enums[name] != nil:
		c.errorAt(id, caseHint("create it from a case, e.g. "+id.Module+".", m.enums[name], true), "enums are created from one of their cases")
	case m.interfaces[name] != nil:
		c.errorAt(id, "", "%s.%s is an interface, so it can't be created", id.Module, name)
	case m.aliases[name] != nil:
		a := m.aliases[name]
		if c.private(id, m, a.decl.Pub, "typedef '"+name+"'") {
			return fail()
		}
		switch t := c.aliasTarget(a).(type) {
		case *Struct:
			if len(a.params) > 0 {
				c.errorAt(id, "create it with the struct's own name", "%s.%s is a generic typedef, so it can't be called", id.Module, name)
			} else if !c.constructPrivate(e, t) {
				return c.construct(e, t)
			}
		case *Enum:
			c.errorAt(id, caseHint("create it from a case, e.g. "+id.Module+".", t, true), "enums are created from one of their cases")
		default:
			c.errorAt(id, "", "%s.%s names %s, so it can't be called", id.Module, name, t)
		}
	default:
		c.unknownIn(id, m, name)
	}
	return fail()
}

// Checks a name from an imported module used as a value, as in
// `list.map(math.abs)`.
func (c *checker) moduleIdent(id *ast.Ident) Type {
	m := c.imported(id.Module)
	name := id.Name
	if k := m.consts[name]; k != nil {
		if c.private(id, m, k.Decl.Pub, "constant '"+name+"'") {
			return Invalid
		}
		c.checkConst(k)
		c.info.Uses[id] = k
		return k.Type
	}
	if f := m.funcs[name]; f != nil {
		if c.private(id, m, f.Decl.Pub, "function '"+name+"'") || c.asyncValue(id, f) {
			return Invalid
		}
		c.info.Uses[id] = f
		return funcValue(f)
	}
	switch {
	case m.structs[name] != nil && m.structs[name].Decl != nil, m.genStructs[name] != nil, m.genEnums[name] != nil, m.interfaces[name] != nil, m.genIfaces[name] != nil, m.handles[name] != nil, m.aliases[name] != nil:
		c.errorAt(id, "", "'%s.%s' is a type, not a value", id.Module, name)
	case m.enums[name] != nil:
		c.errorAt(id, caseHint("use one of its cases, e.g. "+id.Module+".", m.enums[name], true), "'%s.%s' is a type, not a value", id.Module, name)
	default:
		c.unknownIn(id, m, name)
	}
	return Invalid
}

// Returns the struct or enum of another module named by [id], reporting
// it if private.
func (c *checker) moduleRef(id *ast.Ident) (*Struct, *Enum) {
	m := c.imported(id.Module)
	if s := m.structs[id.Name]; s != nil && s.Decl != nil {
		c.private(id, m, s.Decl.Pub, "struct '"+id.Name+"'")
		return s, nil
	}
	if e := m.enums[id.Name]; e != nil {
		c.private(id, m, e.Decl.Pub, "enum '"+id.Name+"'")
		return nil, e
	}
	if a, t := c.aliasedNominal(m, id.Name); a != nil {
		c.private(id, m, a.decl.Pub, "typedef '"+id.Name+"'")
		s, _ := t.(*Struct)
		e, _ := t.(*Enum)
		return s, e
	}
	return nil, nil
}

// Returns the interface written as `mod.Name`, or nil after reporting why
// not, unless [quiet].
func (c *checker) qualifiedIface(te *ast.NamedType, quiet bool) *Interface {
	var t Type
	if quiet {
		c.scratch(false, func() { t = c.qualifiedType(te) })
		i, _ := t.(*Interface)
		return i
	}
	switch t := c.qualifiedType(te).(type) {
	case *Interface:
		return t
	case *Struct, *Enum:
		c.errorAt(te, "", "'%s' is not an interface", t)
	}
	return nil
}

// Records the file of each declaration and default value, so runtime
// errors can name it.
func (c *checker) fileTable() {
	index := map[*diag.File]int{}
	for _, u := range c.units {
		if _, ok := index[u.file]; !ok && u.file != nil {
			index[u.file] = 0
		}
	}
	if len(index) < 2 {
		return
	}
	files := make([]*diag.File, 0, len(index))
	for f := range index {
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool {
		if a, b := files[i] == c.rootUnit.file, files[j] == c.rootUnit.file; a != b {
			return a
		}
		return files[i].Name < files[j].Name
	})
	for i, f := range files {
		index[f] = i
	}
	c.info.Files = files
	c.info.FileOf = map[ast.Node]int{}
	for n, u := range c.units {
		c.info.FileOf[n] = index[u.file]
	}
	for _, k := range c.info.Consts {
		c.info.FileOf[k.Decl] = index[k.unit.file]
	}
	for _, f := range c.info.Funcs {
		for _, p := range f.Params {
			if p.Default != nil {
				c.info.FileOf[p.Default] = c.info.FileOf[f.Decl]
			}
		}
	}
	for _, s := range c.info.Structs {
		for _, f := range s.Fields {
			if f.Default != nil && s.Decl != nil {
				c.info.FileOf[f.Default] = c.info.FileOf[s.Decl]
			}
		}
	}
}
