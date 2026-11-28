package types

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/diag"
	"github.com/vex-language/vex-lang/compiler/lexer"
)

// The maximum number of errors reported before checking stops.
const maxErrors = 30

// Stops checking once [maxErrors] is reached.
type tooMany struct{}

// Names of the built-in types.
var builtinTypes = map[string]Type{"int": Int, "float": Float, "bool": Bool, "string": String}

// Names of the built-in functions.
var builtinFuncs = map[string]bool{
	"print": true, "println": true, "input": true, "panic": true,
	"assert": true, "str": true, "int": true, "float": true, "expect": true,
}

// A block of local variables.
type scope struct {
	vars   map[string]*Var
	parent *scope

	// Names a type test showed to have a narrower type that couldn't be
	// narrowed because they can change, with the type tested for.
	tested map[string]Type
}

type checker struct {
	info *Info
	errs diag.List

	// Local variables in the order declared, with their files, to warn
	// about the ones never used.
	locals []local

	// The local functions of the blocks being checked that are declared
	// further down, by name.
	pending map[string]int

	// The names of the loops around the current statement, innermost
	// last, with the loop depth inside each.
	labels []label

	// Set inside a `defer` block, which can't return or throw.
	inDefer bool

	// The field whose type is being worked out from its default, or nil.
	inferring *Field

	// Methods of interface extensions whose signatures aren't resolved yet.
	pendingExts []*Func

	// Set while resolving a constraint or an interface list, where an
	// interface that can only be a constraint is allowed.
	asConstraint bool

	// The names of the closures that make local functions.
	localNames map[*ast.FuncLit]string

	// The `it` parameters given to blocks that name none.
	implicitIts map[ast.Node]bool

	// Set when checking a program built to run its tests.
	testing bool

	// The declarations of every module, and where each one lives.
	decls    []ast.Decl
	units    map[ast.Node]*unit
	files    []*unit
	root     *Module
	rootUnit *unit

	// The type a member access is expected to have, for tear-offs.
	tearWant Type

	// The struct or enum declaration each extension adds members to.
	extTargets map[*ast.ExtensionDecl]ast.Decl

	// The module and file being checked, the modules the file imports,
	// and the module's top-level names.
	unit    *unit
	mod     *Module
	file    *diag.File
	imports map[string]*Module
	tables

	// The type parameters in scope and the types they stand for: the
	// parameters themselves in generic code, concrete types in instances.
	env map[string]Type

	// Checks still to run, such as the bodies of instances created while
	// checking other code, and how deeply nested the current instance is.
	jobs    []func()
	depth   int
	tooDeep map[string]bool

	scope *scope

	// The function being checked, its `self` (nil outside instance
	// methods), and how many loops enclose the current statement.
	fn    *Func
	self  *Var
	loops int

	// Set while checking the target of an assignment, which a property
	// without a getter may be.
	writing bool

	// The closures enclosing the current expression, innermost last.
	lits []*litCtx

	// The call an await is about to check, which runs instead of starting
	// a task.
	awaited *ast.CallExpr

	// The try expressions whose calls are being checked, innermost last.
	tries []*tryCtx

	// The constant whose value is being checked, or nil.
	constOwner *Const
}

// Checks [file] and returns what was learned about it.
//
// On errors the returned error is a [diag.List]; the [Info] is still
// returned but may be incomplete.
func Check(f *diag.File, file *ast.File) (info *Info, err error) {
	return CheckProgram(singleFile(f, file))
}

func newChecker() *checker {
	return &checker{
		units: map[ast.Node]*unit{},

		extTargets: map[*ast.ExtensionDecl]ast.Decl{},
		info: &Info{
			Types:          map[ast.Expr]Type{},
			Uses:           map[*ast.Ident]any{},
			Defs:           map[*ast.Ident]*Var{},
			Calls:          map[*ast.CallExpr]*Call{},
			Members:        map[*ast.MemberExpr]*Member{},
			Selves:         map[*ast.FuncDecl]*Var{},
			Captures:       map[*ast.FuncLit][]*Var{},
			CaseValues:     map[ast.Expr]*Case{},
			Wraps:          map[ast.Expr]*Optional{},
			Converts:       map[ast.Expr]Type{},
			ThrowingRefs:   map[ast.Expr]bool{},
			Elements:       map[*ast.ExprStmt]ast.Expr{},
			IfValues:       map[*ast.ExprStmt]*ast.IfExpr{},
			Known:          map[ast.Expr]bool{},
			TypeTests:      map[*ast.TypeTest]Type{},
			PatternTests:   map[ast.Pattern]Type{},
			Observers:      map[*ast.AssignStmt]*Func{},
			Chains:         map[ast.Expr]bool{},
			TearOffs:       map[*ast.MemberExpr]ast.Expr{},
			Operators:      map[ast.Node]*Func{},
			IndexSets:      map[*ast.IndexExpr]*Func{},
			Matches:        map[*ast.MatchExpr]*Match{},
			PatternTypes:   map[ast.Pattern]Type{},
			PatternCases:   map[ast.Pattern]*Case{},
			PatternStructs: map[*ast.CasePattern]*Struct{},
			PatternVars:    map[ast.Pattern][]*Var{},
			FuncOf:         map[*ast.FuncDecl]*Func{},
			Throwing:       map[ast.Expr]Type{},
			Spawns:         map[*ast.CallExpr]bool{},
			Iterables:      map[*ast.ForStmt]*Interface{},
			Tries:          map[*ast.TryExpr]*Try{},
		},
	}
}

func (c *checker) run() (*Info, error) {
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(tooMany); !ok {
					panic(r)
				}
			}
		}()
		c.declareError()
		c.collect()
		c.exposures()
		c.bodies()
	}()
	c.fileTable()
	c.warnUnused(0)
	c.warnUnusedImports()
	if len(c.errs) > 0 {
		c.errs = dedupe(c.errs)
		c.errs.Sort()
		return c.info, c.errs
	}
	return c.info, nil
}

// --- Errors ---

// Records an error that underlines [n].
func (c *checker) errorAt(n ast.Node, hint, format string, args ...any) {
	start, end := n.Pos().Offset, n.EndOffset()
	length := 1
	if end > start && end <= len(c.file.Src) {
		length = utf8.RuneCountInString(c.file.Src[start:end])
	}
	c.errorAtPos(n.Pos(), length, hint, format, args...)
}

func (c *checker) errorAtPos(pos diag.Pos, length int, hint, format string, args ...any) {
	c.errs = append(c.errs, diag.Diagnostic{File: c.file, Pos: pos, Len: length, Message: fmt.Sprintf(format, args...), Hint: hint})
	if len(c.errs) >= maxErrors {
		panic(tooMany{})
	}
}

// Reports that [n] has type [got] where [want] is needed. [what] names the
// place, such as "argument 'name'", and may be empty.
func (c *checker) mismatch(n ast.Node, want, got Type, what string) {
	msg := fmt.Sprintf("expected %s, found %s", want, got)
	if what != "" {
		msg = what + ": " + msg
	}
	c.errorAt(n, conversionHint(want, got), "%s", msg)
}

// Suggests how to turn a [got] value into a [want] value.
func conversionHint(want, got Type) string {
	switch {
	case want == Float && got == Int:
		return "convert it with float(...)"
	case want == Int && got == Float:
		return "convert it with int(...), which drops the fraction"
	case want == Int && got == String:
		return "parse it with int(...)"
	case want == Float && got == String:
		return "parse it with float(...)"
	case want == String && (got == Int || got == Float || got == Bool):
		return `convert it with str(...) or "{...}"`
	}
	if o, ok := got.(*Optional); ok && Identical(o.Elem, want) && want != Invalid {
		return "it may be nil: unwrap it with if let, or give a default with ?? value"
	}
	if i, ok := want.(*Interface); ok {
		if s, ok := got.(*Struct); ok {
			return fmt.Sprintf("declare it: struct %s: %s", s.Name, i.Name)
		}
	}
	if w, ok := want.(*List); ok {
		if g, ok := got.(*List); ok {
			return elementHint(w.Elem, g.Elem, "list")
		}
	}
	if w, ok := want.(*Map); ok {
		if g, ok := got.(*Map); ok && Identical(w.Key, g.Key) {
			return elementHint(w.Value, g.Value, "map")
		}
	}
	return ""
}

// Suggests how to turn a [kind] of [got] elements into one of [want]
// elements, which happens implicitly only when each element converts.
func elementHint(want, got Type, kind string) string {
	if i, ok := got.(*Interface); ok && assignableType(i, want) {
		return fmt.Sprintf("a %s of %s may hold other types; test each element with as? %s", kind, i.Name, want)
	}
	if i, ok := want.(*Interface); ok {
		if s, ok := got.(*Struct); ok {
			return fmt.Sprintf("declare struct %s: %s, and the %s converts", s.Name, i.Name, kind)
		}
	}
	if kind == "list" {
		switch {
		case want == Float && got == Int:
			return "convert each element with .map(fn(x) => float(x))"
		case want == String && (got == Int || got == Float || got == Bool):
			return "convert each element with .map(fn(x) => str(x))"
		}
	}
	return ""
}

// Checks that a [got] value can be used where [want] is expected.
func (c *checker) assignable(want, got Type, n ast.Node, what string) {
	if got == Void {
		c.errorAt(n, "", "this expression has no value")
		return
	}
	if !assignableType(want, got) {
		c.mismatch(n, want, got, what)
	}
}

// --- Scopes ---

func (c *checker) push() { c.scope = &scope{vars: map[string]*Var{}, parent: c.scope} }

func (c *checker) pop() { c.scope = c.scope.parent }

// Declares a variable in the current scope.
func (c *checker) declare(id *ast.Ident, t Type, mutable bool, kind VarKind) *Var {
	v := &Var{Name: id.Name, Type: t, Mutable: mutable, Kind: kind, Decl: id, lit: c.curLit()}
	c.info.Defs[id] = v
	if kind == LocalVar || kind == FuncVar {
		c.locals = append(c.locals, local{v: v, file: c.file})
	}
	if c.reserved(id, "a variable") {
		return v
	}
	if prev, ok := c.scope.vars[id.Name]; ok {
		c.errorAt(id, "choose another name, or assign to it without let/var",
			"'%s' is already declared on line %d", id.Name, prev.Decl.Pos().Line)
		return v
	}
	c.scope.vars[id.Name] = v
	return v
}

// Reports and returns true if [id] uses a built-in or reserved name.
func (c *checker) reserved(id *ast.Ident, what string) bool {
	switch {
	case builtinTypes[id.Name] != nil || id.Name == "void":
		c.errorAt(id, "choose another name", "'%s' is a built-in type and can't be used as %s name", id.Name, what)
	case builtinFuncs[id.Name]:
		c.errorAt(id, "choose another name", "'%s' is a built-in function and can't be used as %s name", id.Name, what)
	case id.Name == "Error":
		c.errorAt(id, "choose another name", "'Error' is the built-in error type and can't be used as %s name", what)
	case id.Name == "self":
		c.errorAt(id, "choose another name", "'self' can't be used as %s name", what)
	case strings.HasPrefix(id.Name, "$") && id.Name != "$elem":
		c.errorAt(id, "choose a name that starts with a letter", "names starting with $ are the positional fields of records")
	case c.imported(id.Name) != nil:
		c.errorAt(id, "choose another name, or rename the import with as", "'%s' is an imported module and can't be used as %s name", id.Name, what)
	default:
		return false
	}
	return true
}

// Returns the local variable called [name], or nil.
func (c *checker) lookupVar(name string) *Var {
	for s := c.scope; s != nil; s = s.parent {
		if v, ok := s.vars[name]; ok {
			return v
		}
	}
	return nil
}

// --- Declarations ---

// Registers all structs and functions so bodies can refer to them in any order.
func (c *checker) collect() {
	each := func(f func(d ast.Decl)) {
		for _, d := range c.decls {
			back := c.enter(d)
			f(d)
			back()
		}
	}
	each(func(d ast.Decl) {
		switch d := d.(type) {
		case *ast.InterfaceDecl:
			c.declareInterface(d)
		case *ast.ExternTypeDecl:
			c.declareHandle(d)
		}
	})
	each(func(d ast.Decl) {
		if ad, ok := d.(*ast.TypeAliasDecl); ok {
			c.declareAlias(ad)
		}
	})
	each(func(d ast.Decl) {
		if sd, ok := d.(*ast.StructDecl); ok && len(sd.TypeParams) == 0 {
			c.declareStruct(sd)
		}
	})
	each(func(d ast.Decl) {
		if sd, ok := d.(*ast.StructDecl); ok && len(sd.TypeParams) > 0 {
			c.declareGenericStruct(sd)
		}
	})
	each(func(d ast.Decl) {
		if ed, ok := d.(*ast.EnumDecl); ok {
			if len(ed.TypeParams) > 0 {
				c.declareGenericEnum(ed)
			} else {
				c.declareEnum(ed)
			}
		}
	})
	each(func(d ast.Decl) {
		if xd, ok := d.(*ast.ExtensionDecl); ok {
			c.declareExtension(xd)
		}
	})
	each(func(d ast.Decl) {
		if kd, ok := d.(*ast.ConstDecl); ok {
			c.declareConst(kd)
		}
	})
	each(func(d ast.Decl) {
		if fd, ok := d.(*ast.FuncDecl); ok {
			if len(fd.TypeParams) > 0 {
				c.declareGenericFunc(fd)
			} else {
				c.declareFunc(fd)
			}
		}
	})
	c.queueGenericBodies()
	for _, i := range c.info.Interfaces {
		c.ifaceMembers(i)
	}
	c.ifaceExts()
	for _, s := range append([]*Struct(nil), c.info.Structs...) {
		if s != c.info.Error && s.generic == nil {
			c.structMembers(s)
		}
	}
	for _, e := range append([]*Enum(nil), c.info.Enums...) {
		if e.generic == nil {
			c.enumMembers(e)
		}
	}
	for _, f := range append([]*Func(nil), c.info.Funcs...) {
		if f.Owner == nil && f.Enum == nil && f.Iface == nil && f.TypeArgs == nil {
			c.signature(f)
		}
	}
	c.checkAliases()
	c.declareStaticConsts()
	for _, s := range append([]*Struct(nil), c.info.Structs...) {
		if s.untyped() {
			c.inferFields(s)
			c.conformances(s)
		}
	}
	c.checkStructCycles()
	c.checkDefaults()
	c.checkEquality()
	c.checkCodable()
	c.checkMain()
}

func (c *checker) declareStruct(sd *ast.StructDecl) {
	s := &Struct{Name: sd.Name.Name, Module: c.mod, Decl: sd}
	if c.reserved(sd.Name, "a struct") || c.ifaceTaken(sd.Name) {
		return
	}
	if g := c.genStructs[s.Name]; g != nil {
		c.errorAt(sd.Name, "", "struct '%s' is already declared on line %d", s.Name, g.decl.Pos().Line)
		return
	}
	if prev := c.structs[s.Name]; prev != nil {
		c.errorAt(sd.Name, "", "struct '%s' is already declared on line %d", s.Name, prev.Decl.Pos().Line)
		return
	}
	c.structs[s.Name] = s
	c.info.Structs = append(c.info.Structs, s)
}

func (c *checker) declareFunc(fd *ast.FuncDecl) {
	f := &Func{Name: fd.Name.Name, Module: c.mod, Decl: fd}
	c.info.FuncOf[fd] = f
	if c.reserved(fd.Name, "a function") || c.ifaceTaken(fd.Name) {
		return
	}
	switch {
	case c.funcs[f.Name] != nil:
		c.errorAt(fd.Name, "", "function '%s' is already declared on line %d", f.Name, c.funcs[f.Name].Decl.Pos().Line)
		return
	case c.genFuncs[f.Name] != nil:
		c.errorAt(fd.Name, "", "function '%s' is already declared on line %d", f.Name, c.genFuncs[f.Name].decl.Pos().Line)
		return
	case c.genStructs[f.Name] != nil:
		c.errorAt(fd.Name, "", "'%s' is already declared as a struct on line %d", f.Name, c.genStructs[f.Name].decl.Pos().Line)
		return
	case c.genEnums[f.Name] != nil:
		c.errorAt(fd.Name, "", "'%s' is already declared as an enum on line %d", f.Name, c.genEnums[f.Name].decl.Pos().Line)
		return
	case c.structs[f.Name] != nil:
		c.errorAt(fd.Name, "", "'%s' is already declared as a struct on line %d", f.Name, c.structs[f.Name].Decl.Pos().Line)
		return
	case c.enums[f.Name] != nil:
		c.errorAt(fd.Name, "", "'%s' is already declared as an enum on line %d", f.Name, c.enums[f.Name].Decl.Pos().Line)
		return
	}
	c.funcs[f.Name] = f
	c.info.Funcs = append(c.info.Funcs, f)
}

// Resolves the fields and method signatures of [s].
func (c *checker) structMembers(s *Struct) {
	defer c.enter(s.Decl)()
	for _, fd := range s.Decl.Fields {
		if c.reserved(fd.Name, "a field") {
			continue
		}
		if prev := s.Field(fd.Name.Name); prev != nil {
			c.errorAt(fd.Name, "", "field '%s' is already declared on line %d", fd.Name.Name, prev.Decl.Pos().Line)
			continue
		}
		f := &Field{Name: fd.Name.Name, Default: fd.Default, Owner: s, Decl: fd, Let: fd.Let, PrivateSet: fd.PrivateSet}
		switch {
		case fd.Type != nil:
			f.Type = c.resolveType(fd.Type)
		case s.generic != nil:
			c.errorAt(fd.Name, fmt.Sprintf("write its type: %s: T = ...", fd.Name.Name), "fields of a generic struct need a type")
			f.Type = Invalid
		}
		s.Fields = append(s.Fields, f)
	}
	for _, md := range s.Decl.Methods {
		m := &Func{Name: md.Name.Name, Decl: md, Owner: s, Static: md.Static, Ext: c.extModule(md)}
		if !s.abstract {
			c.info.FuncOf[md] = m
		}
		if md.Accessor == ast.Observer {
			if f := s.Field(m.Name); f != nil && f.Decl.Name.Start == md.Name.Start {
				f.DidSet = m
				c.addMethod(s, m)
			}
			continue
		}
		if c.reserved(md.Name, "a method") || c.memberTaken(s, md) {
			continue
		}
		if md.Name.Name == "init" && md.Accessor == ast.Method {
			c.errorAt(md.Name, "write init { ... } for code that runs at creation, or give the method another name", "'init' is reserved for the struct's init block")
			continue
		}
		c.addMethod(s, m)
	}
	c.initBlock(s)
	for _, m := range s.Methods {
		if m.Decl.Accessor == ast.Operator {
			c.checkOperator(s, m)
		}
		if m.Decl.Accessor != ast.Setter {
			continue
		}
		if g := s.member(m.Name, ast.Getter); g != nil && len(m.Params) == 1 && !Identical(g.Result, m.Params[0].Type) {
			c.errorAt(m.Decl.Params[0].Type, fmt.Sprintf("use %s, the type of the getter", g.Result), "setter '%s' takes %s, but its getter returns %s", m.Name, m.Params[0].Type, g.Result)
		}
	}
	if s.untyped() {
		// Checked once the field types are known.
		return
	}
	c.conformances(s)
}

func (c *checker) addMethod(s *Struct, m *Func) {
	s.Methods = append(s.Methods, m)
	if c.genericMethod(m, s.abstract, s.generic == nil || isTemplate(s.Args, s.generic.params)) {
		return
	}
	if !s.abstract {
		c.info.Funcs = append(c.info.Funcs, m)
	}
	c.signature(m)
}

// Reports, and returns true, if the name of the method or accessor [md]
// is already used in [s]. A getter and a setter may share a name.
func (c *checker) memberTaken(s *Struct, md *ast.FuncDecl) bool {
	name := md.Name.Name
	what := map[ast.Accessor]string{ast.Method: "method", ast.Getter: "getter", ast.Setter: "setter", ast.Operator: "operator"}
	if prev := s.member(name, md.Accessor); prev != nil {
		c.errorAt(md.Name, "", "%s '%s' is already declared on line %d", what[md.Accessor], name, prev.Decl.Pos().Line)
		return true
	}
	if f := s.Field(name); f != nil {
		c.errorAt(md.Name, "", "'%s' is already a field of %s", name, s.Name)
		return true
	}
	for _, other := range []ast.Accessor{ast.Method, ast.Getter, ast.Setter} {
		if other == md.Accessor || md.Accessor != ast.Method && other != ast.Method {
			continue
		}
		if prev := s.member(name, other); prev != nil {
			c.errorAt(md.Name, "", "'%s' is already a %s of %s, on line %d", name, what[other], s.Name, prev.Decl.Pos().Line)
			return true
		}
	}
	return false
}

// Reports, and returns true, if [id] names an interface.
func (c *checker) ifaceTaken(id *ast.Ident) bool {
	if i := c.interfaces[id.Name]; i != nil {
		c.errorAt(id, "", "'%s' is already declared as an interface on line %d", id.Name, i.Decl.Pos().Line)
		return true
	}
	if g := c.genIfaces[id.Name]; g != nil {
		c.errorAt(id, "", "'%s' is already declared as an interface on line %d", id.Name, g.decl.Pos().Line)
		return true
	}
	if h := c.handles[id.Name]; h != nil {
		c.errorAt(id, "", "'%s' is already declared as an extern type on line %d", id.Name, h.Decl.Pos().Line)
		return true
	}
	if a := c.aliases[id.Name]; a != nil {
		c.errorAt(id, "", "'%s' is already declared as a typedef on line %d", id.Name, a.decl.Pos().Line)
		return true
	}
	return false
}

// Resolves the parameter and result types of [f].
func (c *checker) signature(f *Func) {
	defer c.enter(f.Decl)()
	if f.Decl.Recv != nil && f.Owner == nil && f.Enum == nil && f.Iface == nil {
		f.Recv = c.resolveType(f.Decl.Recv)
	}
	if self := selfOf(f); self != nil {
		saved := c.env
		c.env = withSelf(c.env, self)
		defer func() { c.env = saved }()
	}
	if len(f.Decl.TypeParams) > 0 && f.Decl.Accessor != ast.Method {
		c.errorAt(f.Decl.TypeParams[0], "make it a method of a struct or enum", "accessors can't have their own type parameters")
		env := map[string]Type{}
		for n, t := range c.env {
			env[n] = t
		}
		for _, tp := range f.Decl.TypeParams {
			env[tp.Name.Name] = Invalid
		}
		saved := c.env
		c.env = env
		defer func() { c.env = saved }()
	}
	seen := map[string]bool{}
	native := f.Decl.Extern || f.Decl.Export
	for _, p := range f.Decl.Params {
		t, ct := c.nativeType(p.Type, native)
		param := &Param{Name: p.Name.Name, Type: t, Default: p.Default}
		f.Params = append(f.Params, param)
		if native {
			f.CParams = append(f.CParams, ct)
		}
		if c.reserved(p.Name, "a parameter") {
			continue
		}
		if seen[param.Name] {
			c.errorAt(p.Name, "", "parameter '%s' is declared twice", param.Name)
		}
		seen[param.Name] = true
	}
	f.Async = f.Decl.Async
	f.Result = Void
	if f.Decl.Result != nil {
		f.Result, f.CResult = c.nativeType(f.Decl.Result, native)
	}
	if f.Decl.Throws != nil {
		f.Throws = c.throwsType(f.Decl.Throws)
	}
	if native {
		c.nativeTypes(f)
	}
	if f.Name == "toString" && (f.Owner != nil || f.Enum != nil) && f.Decl.Accessor == ast.Method {
		c.checkToString(f)
	}
}

// Checks the `toString` method of a struct or enum, which printing calls:
// it must be `fn toString() -> string`.
func (c *checker) checkToString(f *Func) {
	const hint = "printing calls it: write fn toString() -> string"
	switch {
	case f.Static:
		c.errorAt(f.Decl.Name, hint, "toString can't be static")
	case len(f.Params) > 0:
		c.errorAt(f.Decl.Params[0], hint, "toString takes no parameters")
	case len(f.Decl.TypeParams) > 0:
		c.errorAt(f.Decl.TypeParams[0], hint, "toString can't have type parameters")
	case f.Async:
		c.errorAt(f.Decl.Name, hint, "toString can't be async")
	case f.Throws != nil:
		c.errorAt(f.Decl.Throws, hint, "toString can't throw")
	case f.Result != String && f.Result != Invalid:
		c.errorAt(f.Decl.Name, hint, "toString must return string, found %s", f.Result)
	}
}

// Returns the `toString` method that prints values of [t], a struct or
// enum, or nil.
func ToString(t Type) *Func {
	var m *Func
	switch t := t.(type) {
	case *Struct:
		m = t.Method("toString")
	case *Enum:
		m = t.Method("toString")
	}
	if m == nil || m.Static || len(m.Params) > 0 || m.Async || m.Throws != nil || m.Result != String || len(m.Decl.TypeParams) > 0 {
		return nil
	}
	return m
}

// Returns the type written as [te].
func (c *checker) resolveType(te ast.TypeExpr) Type {
	t := c.typeOf(te)
	if i, ok := t.(*Interface); ok && !c.asConstraint {
		if _, named := te.(*ast.NamedType); named && c.constraintOnly(i) {
			c.errorAt(te, fmt.Sprintf("use it as a constraint: fn f<T: %s>(x: T)", i.Name), "%s can only be a constraint, since it has %s", i.Name, constraintReason(i))
			return Invalid
		}
	}
	return t
}

// Resolves [te] for [checker.resolveType].
func (c *checker) typeOf(te ast.TypeExpr) Type {
	switch te := te.(type) {
	case *ast.ListType:
		return &List{Elem: c.resolveType(te.Elem)}
	case *ast.MapType:
		return c.mapType(te)
	case *ast.OptionalType:
		return optionalOf(c.resolveType(te.Elem))
	case *ast.FuncType:
		ft := &FuncType{Result: Void, Async: te.Async}
		for _, p := range te.Params {
			ft.Params = append(ft.Params, c.resolveType(p))
		}
		if te.Result != nil {
			ft.Result = c.resolveType(te.Result)
		}
		if te.Throws != nil {
			ft.Throws = c.throwsType(te.Throws)
		}
		return ft
	case *ast.RecordType:
		return c.recordType(te)
	case *ast.KnownType:
		return te.Type.(Type)
	case *ast.NamedType:
		if te.Module == "" && c.fromCore(te.Name) {
			te.Module = coreKey
		}
		if te.Module != "" {
			return c.qualifiedType(te)
		}
		if t, ok := c.env[te.Name]; ok && len(te.Args) == 0 {
			return t
		}
		if a := c.aliases[te.Name]; a != nil {
			if _, param := c.env[te.Name]; !param {
				return c.aliasType(te, a)
			}
		}
		if len(te.Args) > 0 {
			return c.genericType(te)
		}
		if t := builtinTypes[te.Name]; t != nil {
			return t
		}
		if te.Name == "Channel" && c.structs[te.Name] == nil && c.enums[te.Name] == nil && c.interfaces[te.Name] == nil && c.handles[te.Name] == nil {
			c.errorAt(te, "write its value type, e.g. Channel<int>", "Channel needs a type argument")
			return Invalid
		}
		if streamName(te.Name) && c.structs[te.Name] == nil && c.enums[te.Name] == nil && c.interfaces[te.Name] == nil && c.handles[te.Name] == nil {
			c.errorAt(te, "write its value type, e.g. "+te.Name+"<int>", "%s needs a type argument", te.Name)
			return Invalid
		}
		if te.Name == "Future" && c.structs[te.Name] == nil && c.enums[te.Name] == nil && c.interfaces[te.Name] == nil && c.handles[te.Name] == nil {
			c.errorAt(te, "write its value type, e.g. Future<int>, or Future<int, Error> when it can fail", "Future needs a type argument")
			return Invalid
		}
		if g := c.genStructs[te.Name]; g != nil {
			c.errorAt(te, fmt.Sprintf("write its type arguments, e.g. %s<%s>", te.Name, typeParamExample(g.params)), "%s is generic, so it needs type arguments", te.Name)
			return Invalid
		}
		if g := c.genEnums[te.Name]; g != nil {
			c.errorAt(te, fmt.Sprintf("write its type arguments, e.g. %s<%s>", te.Name, typeParamExample(g.params)), "%s is generic, so it needs type arguments", te.Name)
			return Invalid
		}
		if g := c.genIfaces[te.Name]; g != nil {
			c.errorAt(te, fmt.Sprintf("write its type arguments, e.g. %s<%s>", te.Name, typeParamExample(c.ifaceParams(g))), "%s is generic, so it needs type arguments", te.Name)
			return Invalid
		}
		if s := c.structs[te.Name]; s != nil {
			return s
		}
		if e := c.enums[te.Name]; e != nil {
			return e
		}
		if i := c.interfaces[te.Name]; i != nil {
			return i
		}
		if h := c.handles[te.Name]; h != nil {
			return h
		}
		if te.Name == "Range" {
			return Range
		}
		if cNumbers[te.Name] != "" {
			c.errorAt(te, "use int or float here; sized C numbers convert at the call", "%s is only for the signatures of extern fn and export fn", te.Name)
			return Invalid
		}
		if te.Name == "void" {
			c.errorAt(te, "leave out the return type for functions that return nothing", "'void' is not a type")
			return Invalid
		}
		names := []string{"int", "float", "bool", "string"}
		for n := range c.structs {
			names = append(names, n)
		}
		for n := range c.enums {
			names = append(names, n)
		}
		for n := range c.env {
			names = append(names, n)
		}
		if te.Name == "Self" {
			c.errorAt(te, "", "Self can only be used in the requirements of an interface and in the methods of a struct or enum")
			return Invalid
		}
		c.errorAt(te, didYouMean(te.Name, names), "unknown type '%s'", te.Name)
	}
	return Invalid
}

// Reports structs that contain themselves through fields, which could never
// be created.
func (c *checker) checkStructCycles() {
	state := map[*Struct]int{} // 0 unvisited, 1 visiting, 2 done
	var visit func(s *Struct, depth int) bool
	visit = func(s *Struct, depth int) bool {
		switch state[s] {
		case 1:
			return true
		case 2:
			return false
		}
		state[s] = 1
		for _, f := range s.Fields {
			if inner, ok := f.Type.(*Struct); ok && visit(inner, depth+1) {
				if inner == s || depth == 0 {
					defer c.enter(s.Decl)()
					var at ast.Node = f.Decl.Name
					if f.Decl.Type != nil {
						at = f.Decl.Type
					}
					c.errorAt(at, "use a list instead, e.g. children: ["+inner.Name+"]",
						"struct '%s' contains itself through field '%s', so it could never be created", s.Name, f.Name)
				}
				state[s] = 2
				return true
			}
		}
		state[s] = 2
		return false
	}
	all := append([]*Struct(nil), c.info.Structs...)
	for _, d := range c.decls {
		if sd, ok := d.(*ast.StructDecl); ok {
			back := c.enter(d)
			if g := c.genStructs[sd.Name.Name]; g != nil && g.decl == sd {
				all = append(all, c.structTemplate(g))
			}
			back()
		}
	}
	for _, s := range all {
		if state[s] == 0 && (s.generic == nil || s.abstract) {
			visit(s, 0)
		}
	}
}

// Checks default values of parameters and fields. Defaults can't refer to
// other parameters or fields, so they are checked without local variables.
func (c *checker) checkDefaults() {
	c.scope = &scope{vars: map[string]*Var{}}
	for _, s := range c.info.Structs {
		if s.generic != nil {
			continue
		}
		back := c.enter(s.Decl)
		for _, f := range s.Fields {
			if f.Default != nil && !f.Inferred {
				c.assignable(f.Type, c.expr(f.Default, f.Type), f.Default, "default of '"+f.Name+"'")
			}
		}
		back()
	}
	c.checkDefaultCycles()
	for _, f := range c.info.Funcs {
		if instanced(f) {
			continue
		}
		back := c.enter(f.Decl)
		for _, p := range f.Params {
			if p.Default != nil {
				c.assignable(p.Type, c.expr(p.Default, p.Type), p.Default, "default of '"+p.Name+"'")
			}
		}
		back()
	}
	c.scope = nil
}

func (c *checker) checkMain() {
	c.setUnit(c.rootUnit)
	if c.testing {
		c.checkTests()
		return
	}
	main := c.funcs["main"]
	if g := c.genFuncs["main"]; g != nil {
		defer c.enter(g.decl)()
		c.errorAt(g.decl.TypeParams[0], "", "main can't be generic")
		return
	}
	if main == nil {
		c.errorAtPos(diag.Pos{Line: 1, Col: 1}, 1, "add fn main() { ... } where the program starts", "no main function")
		return
	}
	defer c.enter(main.Decl)()
	if main.Decl.Extern {
		c.errorAt(main.Decl.Name, "", "main can't be extern")
		return
	}
	if len(main.Params) > 0 {
		c.errorAt(main.Decl.Name, "", "main can't take parameters")
	}
	if main.Result != Void {
		c.errorAt(main.Decl.Result, "", "main can't return a value")
	}
}

// --- Function bodies ---

func (c *checker) bodies() {
	for _, d := range c.decls {
		back := c.enter(d)
		switch d := d.(type) {
		case *ast.ConstDecl:
			if k := c.consts[d.Name.Name]; k != nil && k.Decl == d {
				c.checkConst(k)
			}
		case *ast.StructDecl:
			if s := c.structs[d.Name.Name]; s != nil && s.Decl == d {
				for _, k := range s.Consts {
					c.checkConst(k)
				}
			}
		case *ast.EnumDecl:
			if e := c.enums[d.Name.Name]; e != nil && e.Decl == d {
				for _, k := range e.Consts {
					c.checkConst(k)
				}
			}
		}
		back()
	}
	for _, f := range append([]*Func(nil), c.info.Funcs...) {
		if !instanced(f) {
			c.funcBody(f)
		}
	}
	for len(c.jobs) > 0 {
		job := c.jobs[0]
		c.jobs = c.jobs[1:]
		job()
	}
}

// Returns the type written as `Name<Args>`.
func (c *checker) genericType(te *ast.NamedType) Type {
	if te.Name == "Future" && c.genStructs[te.Name] == nil && c.genEnums[te.Name] == nil && c.genIfaces[te.Name] == nil {
		return c.futureType(te)
	}
	if te.Name == "Channel" && c.genStructs[te.Name] == nil && c.genEnums[te.Name] == nil && c.genIfaces[te.Name] == nil {
		return c.channelType(te, te.Args)
	}
	if streamName(te.Name) && c.genStructs[te.Name] == nil && c.genEnums[te.Name] == nil && c.genIfaces[te.Name] == nil {
		return c.streamType(te, te.Name, te.Args)
	}
	args := c.typeArgs(te)
	if g := c.genEnums[te.Name]; g != nil {
		return c.genericEnumInst(te, g, args)
	}
	if g := c.genIfaces[te.Name]; g != nil {
		return c.genericIfaceInst(te, g, args)
	}
	g := c.genStructs[te.Name]
	if g == nil {
		_, param := c.env[te.Name]
		if c.structs[te.Name] == nil && c.enums[te.Name] == nil && c.interfaces[te.Name] == nil && builtinTypes[te.Name] == nil &&
			c.handles[te.Name] == nil && cNumbers[te.Name] == "" && !param {
			c.errorAt(te, "", "unknown type '%s'", te.Name)
		} else {
			c.errorAt(te, "", "'%s' is not generic, so it takes no type arguments", te.Name)
		}
		return Invalid
	}
	return c.genericInst(te, g, args)
}

// Resolves the type arguments written in [te].
func (c *checker) typeArgs(te *ast.NamedType) []Type {
	var args []Type
	for _, a := range te.Args {
		args = append(args, c.resolveType(a))
	}
	return args
}

// Returns the instance of [g] for [args], written as [te].
func (c *checker) genericInst(te *ast.NamedType, g *genericStruct, args []Type) Type {
	if len(args) != len(g.params) {
		c.errorAt(te, "", "%s takes %s, found %d", te.Name, plural(len(g.params), "type argument"), len(args))
		return Invalid
	}
	for i, p := range g.params {
		if args[i] == Invalid {
			return Invalid
		}
		if !c.meetsConstraints(p, args[i], te.Args[i], te.Name, bindOf(g.params, args)) {
			return Invalid
		}
	}
	if s := c.instStruct(g, args, te); s != nil {
		return s
	}
	return Invalid
}

// Removes repeated diagnostics, which instances of generic code can
// report more than once.
func dedupe(errs diag.List) diag.List {
	type key struct {
		file *diag.File
		pos  diag.Pos
		msg  string
	}
	seen := map[key]bool{}
	var out diag.List
	for _, d := range errs {
		key := key{d.File, d.Pos, d.Message}
		if !seen[key] {
			seen[key] = true
			out = append(out, d)
		}
	}
	return out
}

func (c *checker) funcBody(f *Func) {
	if f.Decl.Extern {
		return
	}
	defer c.enter(f.Decl)()
	if f.Owner != nil || f.Enum != nil {
		saved := c.env
		c.env = withSelf(c.env, selfOf(f))
		defer func() { c.env = saved }()
	}
	c.fn, c.self, c.loops, c.lits = f, nil, 0, nil
	c.scope = nil
	c.push()
	if f.Owner != nil && !f.Static {
		c.self = &Var{Name: "self", Type: f.Owner, Kind: SelfVar, Decl: f.Decl.Name}
		c.info.Selves[f.Decl] = c.self
	}
	if f.Enum != nil && !f.Static {
		c.self = &Var{Name: "self", Type: f.Enum, Kind: SelfVar, Decl: f.Decl.Name}
		c.info.Selves[f.Decl] = c.self
	}
	if f.Iface != nil {
		c.self = &Var{Name: "self", Type: f.Iface, Kind: SelfVar, Decl: f.Decl.Name}
		c.info.Selves[f.Decl] = c.self
	}
	if f.Recv != nil {
		c.self = &Var{Name: "self", Type: f.Recv, Kind: SelfVar, Decl: f.Decl.Name}
		c.info.Selves[f.Decl] = c.self
	}
	for i, p := range f.Decl.Params {
		c.declare(p.Name, f.Params[i].Type, false, ParamVar)
	}
	if f.Generates() {
		if f.Throws != nil {
			c.errorAt(f.Decl.Throws, "catch errors inside it, or yield a result value", "a generator can't throw yet")
		}
		c.stmts(f.Decl.Body.Stmts)
	} else if f.Streams() {
		c.checkStreamFunc(f)
		if f.Decl.ExprBody != nil {
			c.expr(f.Decl.ExprBody, nil)
			c.errorAt(f.Decl.ExprBody, "write a body that gives values with yield", "a stream function needs a block body")
		} else {
			c.stmts(f.Decl.Body.Stmts)
		}
	} else if f.Decl.ExprBody != nil {
		var want Type
		if f.Result != Void {
			want = f.Result
		}
		t := c.expr(f.Decl.ExprBody, want)
		if f.Result != Void && !IsPanic(f.Decl.ExprBody) {
			c.assignable(f.Result, t, f.Decl.ExprBody, "result of "+f.Name)
		}
	} else {
		c.stmts(f.Decl.Body.Stmts)
		if f.Result != Void && f.Result != Invalid && !terminates(f.Decl.Body) {
			body := f.Decl.Body
			c.errorAtPos(c.file.PosOf(body.End-1), 1, "add a return statement before the closing }",
				"missing return: %s must return %s", f.Name, f.Result)
		}
	}
	if f.Owner != nil && f == f.Owner.Init {
		c.initOrder(f.Owner)
	}
	c.pop()
	c.fn, c.self = nil, nil
}

// Reports whether [s] always ends by returning or stopping the program.
func terminates(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.ReturnStmt, *ast.ThrowStmt:
		return true
	case *ast.Block:
		for _, st := range s.Stmts {
			if terminates(st) {
				return true
			}
		}
	case *ast.IfStmt:
		return s.Else != nil && terminates(s.Then) && terminates(s.Else)
	case *ast.WhileStmt:
		if s.Bind != nil || len(s.More) > 0 {
			return false
		}
		lit, ok := s.Cond.(*ast.BoolLit)
		return ok && lit.Value && !breaks(s.Body)
	case *ast.LabeledStmt:
		return terminates(s.Loop)
	case *ast.ExprStmt:
		if m, ok := s.X.(*ast.MatchExpr); ok {
			return matchTerminates(m)
		}
		return IsPanic(s.X)
	}
	return false
}

// Reports whether [s] contains a `break` for the enclosing loop, including
// one inside a match arm or a catch block, but not one in a nested loop or
// closure.
func breaks(s ast.Stmt) bool {
	found := false
	ast.Inspect(s, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BreakStmt:
			found = true
		case *ast.WhileStmt, *ast.ForStmt, *ast.RepeatStmt:
			if n != ast.Node(s) {
				// A nested loop's own breaks stay inside it, but a
				// labelled one may leave this loop too.
				found = found || labelledBreak(n)
				return false
			}
		case *ast.FuncLit, *ast.FuncDecl:
			return false
		}
		return !found
	})
	return found
}

// Reports whether [n] holds a `break` that names a loop.
func labelledBreak(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BreakStmt:
			found = found || n.Label != nil
		case *ast.FuncLit, *ast.FuncDecl:
			return false
		}
		return !found
	})
	return found
}

// --- Statements ---

func (c *checker) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.Block:
		c.push()
		c.stmts(s.Stmts)
		c.pop()
	case *ast.LetStmt:
		if s.Pattern != nil {
			t := c.expr(s.Value, nil)
			c.declPattern(s.Pattern, t, LocalVar, s.Mutable)
			break
		}
		var want Type
		if s.Type != nil {
			want = c.resolveType(s.Type)
		}
		t := c.expr(s.Value, want)
		if t == Void {
			c.errorAt(s.Value, "", "this expression has no value to store")
			t = Invalid
		}
		if want != nil {
			c.assignable(want, t, s.Value, "")
		} else {
			want = t
		}
		c.declare(s.Name, want, s.Mutable, LocalVar)
	case *ast.AssignStmt:
		c.assign(s)
	case *ast.ExprStmt:
		if m, ok := s.X.(*ast.MatchExpr); ok {
			c.info.Types[m] = c.match(m, nil, true)
			break
		}
		if tr, ok := s.X.(*ast.TryExpr); ok {
			c.info.Types[tr] = c.tryExpr(tr, nil, true)
			break
		}
		c.expr(s.X, nil)
		switch s.X.(type) {
		case *ast.CallExpr, *ast.AwaitExpr:
		default:
			c.errorAt(s.X, "store it with let, or remove it", "this value isn't used")
		}
	case *ast.IfStmt:
		if s.Bind != nil {
			elem := c.unwrapped(c.expr(s.Cond, nil), s.Cond, "if let")
			c.push()
			c.declare(s.Bind, elem, false, LocalVar)
			_, pushed := c.conds(s.More, "if")
			c.stmt(s.Then)
			c.popN(pushed)
			c.pop()
		} else if s.Case != nil {
			t := c.expr(s.Cond, nil)
			c.push()
			b := newBinder(PatternVar, false)
			c.pattern(s.Case, t, b, false, false)
			c.info.PatternVars[s.Case] = b.order
			if s.Guard != nil {
				c.cond(s.Guard, "when")
			}
			c.stmt(s.Then)
			c.pop()
		} else {
			c.cond(s.Cond, "if")
			c.pushNarrowed(s.Cond)
			_, pushed := c.conds(s.More, "if")
			c.stmt(s.Then)
			c.popN(pushed)
			c.pop()
		}
		if s.Else != nil {
			c.stmt(s.Else)
		}
	case *ast.GuardStmt:
		c.guardStmt(s)
	case *ast.WhileStmt:
		if s.Bind != nil {
			elem := c.unwrapped(c.expr(s.Cond, nil), s.Cond, "while let")
			c.push()
			c.declare(s.Bind, elem, false, LocalVar)
		} else {
			c.cond(s.Cond, "while")
			c.pushNarrowed(s.Cond)
		}
		_, pushed := c.conds(s.More, "while")
		c.loops++
		c.stmt(s.Body)
		c.loops--
		c.popN(pushed)
		c.pop()
	case *ast.ForStmt:
		c.forStmt(s)
	case *ast.ReturnStmt:
		c.returnStmt(s)
	case *ast.YieldStmt:
		c.yieldStmt(s)
	case *ast.LocalFuncStmt:
		c.localFunc(s)
	case *ast.ThrowStmt:
		c.throwStmt(s)
	case *ast.BreakStmt:
		c.jump(s.Label, "break", s)
	case *ast.ContinueStmt:
		c.jump(s.Label, "continue", s)
	case *ast.LabeledStmt:
		c.labeled(s)
	case *ast.DeferStmt:
		c.deferStmt(s)
	case *ast.RepeatStmt:
		c.push()
		c.loops++
		c.stmts(s.Body.Stmts)
		c.loops--
		c.pop()
		c.cond(s.Cond, "repeat ... while")
	}
}

// Checks that the condition of [keyword] is a bool.
func (c *checker) cond(e ast.Expr, keyword string) {
	t := c.expr(e, Bool)
	if Identical(t, Bool) {
		if v, ok := c.fold(e, Bool).(bool); ok && keyword == "if" {
			c.info.Known[e] = v
		}
		return
	}
	hint := ""
	switch t.(type) {
	case *List:
		hint = "check its length, e.g. xs.len > 0"
	default:
		switch t {
		case Int, Float:
			hint = "compare it, e.g. x != 0"
		case String:
			hint = `compare it, e.g. s != ""`
		}
	}
	c.errorAt(e, hint, "%s condition must be bool, found %s", keyword, t)
}

func (c *checker) assign(s *ast.AssignStmt) {
	var target Type
	switch x := s.Target.(type) {
	case *ast.Ident:
		c.writing = true
		target = c.expr(x, nil)
		c.writing = false
		c.assignMember(s, x, c.info.Uses[x])
		switch u := c.info.Uses[x].(type) {
		case *Const:
			c.constAssign(x, u)
		case *Func:
			c.errorAt(x, "declare a variable holding a function instead: var f = "+u.Name, "can't assign to function '%s'", u.Name)
		}
		if v, ok := c.info.Uses[x].(*Var); ok && !v.Mutable {
			switch v.Kind {
			case LocalVar:
				c.errorAt(x, fmt.Sprintf("declare it with var %s = ... to make it changeable", v.Name), "can't change '%s': it's declared with let", v.Name)
			case ParamVar:
				c.errorAt(x, fmt.Sprintf("copy it first: var my%s = %s", capitalize(v.Name), v.Name), "can't change parameter '%s'", v.Name)
			case LoopVar:
				c.errorAt(x, "", "can't change loop variable '%s'", v.Name)
			case FuncVar:
				c.errorAt(x, "declare a variable holding a function instead: var f = "+v.Name, "can't assign to local function '%s'", v.Name)
			case SelfVar:
				c.errorAt(x, "change its fields instead, e.g. self.name = ...", "can't assign to self")
			case PatternVar:
				c.errorAt(x, fmt.Sprintf("copy it first: var my%s = %s", capitalize(v.Name), v.Name), "can't change '%s': names bound by a match pattern can't be changed", v.Name)
			}
		}
	case *ast.MemberExpr:
		if x.Optional {
			c.errorAt(x, "unwrap it first with if let", "can't assign through ?.")
		}
		c.writing = true
		target = c.expr(x, nil)
		c.writing = false
		if cs := c.info.CaseValues[x]; cs != nil {
			c.errorAt(x, "", "can't assign to enum case '%s'", cs.Name)
		} else if m := c.info.Members[x]; m != nil && m.Builtin != "" {
			c.errorAt(x.Name, "", "%s can't be changed", m.Builtin)
		} else if m != nil && m.Const != nil {
			c.constAssign(x, m.Const)
		} else if m != nil && m.Property != nil {
			c.assignMember(s, x.Name, m.Property)
		} else if m != nil {
			c.assignMember(s, x.Name, m.Field)
		}
	case *ast.IndexExpr:
		xt := c.expr(x.X, nil)
		switch xt.(type) {
		case *Map:
			c.constChange(x.X, "map")
		case *List:
			c.constChange(x.X, "list")
		}
		if m, ok := xt.(*Map); ok {
			target = c.mapTarget(x, m, s)
			if target == nil {
				return
			}
			break
		}
		if st, ok := xt.(*Struct); ok {
			target = c.indexSet(s, x, st)
			if target == nil {
				return
			}
			break
		}
		target = c.indexOf(x, xt)
		c.info.Types[x] = target
		if c.info.Types[x.Index] == Range {
			c.expr(s.Value, nil)
			c.errorAt(x.Index, "assign each element, or build a new list", "a slice is a copy, so it can't be assigned to")
			return
		}
	default:
		target = c.expr(s.Target, nil)
	}
	if st, ok := target.(*Struct); ok && s.Op != lexer.Assign {
		c.compoundOperator(s, st)
		return
	}
	value := c.expr(s.Value, target)

	if s.Op == lexer.Assign {
		c.assignable(target, value, s.Value, "")
		return
	}
	op := strings.Trim(s.Op.String(), "'")
	ok := false
	switch s.Op {
	case lexer.PlusAssign:
		ok = numeric(target) || target == String
	case lexer.PercentAssign, lexer.AmpPlusAssign, lexer.AmpMinusAssign, lexer.AmpStarAssign,
		lexer.AmpAssign, lexer.PipeAssign, lexer.CaretAssign, lexer.ShlAssign, lexer.ShrAssign:
		ok = target == Int || target == Invalid
	default:
		ok = numeric(target)
	}
	if !ok {
		c.errorAt(s.Target, "", "%s can't be used with %s", op, target)
		return
	}
	c.assignable(target, value, s.Value, "")
}

// Checks the assignment [s] to the field or property [m], named at [n]:
// a property needs a setter, and a compound assignment a getter too. An
// assignment to a field with `didSet` runs it, except inside that block.
func (c *checker) assignMember(s *ast.AssignStmt, n ast.Node, m any) {
	switch m := m.(type) {
	case *Field:
		if m == nil {
			return
		}
		c.fieldWrite(n, m)
		if m.DidSet != nil && c.fn != m.DidSet {
			c.info.Observers[s] = m.DidSet
		}
	case *Property:
		switch {
		case m.Set == nil:
			name := m.Get.Name
			c.errorAt(n, "add a setter: set "+name+"(value: ...) { ... }", "can't assign to '%s': it has a getter but no setter", name)
		case m.Get == nil && s.Op != lexer.Assign:
			name := m.Set.Name
			c.errorAt(n, "add a getter: get "+name+" -> ...", "'%s' has a setter but no getter, so %s can't read it", name, strings.Trim(s.Op.String(), "'"))
		default:
			c.methodPrivate(n, m.Set)
		}
	}
}

func (c *checker) forStmt(s *ast.ForStmt) {
	c.forLoop(s, func() { c.stmt(s.Body) })
}

// Checks the header of the loop [s], declaring its variables, and then its
// body with [body].
func (c *checker) forLoop(s *ast.ForStmt, body func()) {
	c.push()
	defer c.pop()
	var elem Type = Invalid
	if s.Await {
		elem = c.forAwait(s)
	} else if r, ok := s.Iter.(*ast.RangeExpr); ok {
		c.rangeBounds(r, false)
		c.info.Types[r] = Int
		elem = Int
		if s.Index != nil {
			c.errorAt(s.Index, "write for i in a..b", "a range has no separate index")
		}
	} else if t := c.expr(s.Iter, nil); t == Range {
		elem = Int
		if s.Index != nil {
			c.errorAt(s.Index, "write for i in r", "a range has no separate index")
		}
	} else {
		switch t := t.(type) {
		case *List:
			elem = t.Elem
		case *Map:
			if s.Pattern != nil {
				c.errorAt(s.Pattern, "write for key, value in map", "a map loop takes the key and the value as two names, not a pattern")
			}
			if s.Index != nil {
				c.declare(s.Index, t.Key, false, LoopVar)
				c.declare(s.Elem, t.Value, false, LoopVar)
			} else {
				c.declare(s.Elem, t.Key, false, LoopVar)
			}
			c.loops++
			body()
			c.loops--
			return
		default:
			if it := c.iterableOf(t); it != nil {
				c.info.Iterables[s] = it
				elem = it.Args[0]
			} else if t != Invalid {
				c.errorAt(s.Iter, "loop over a list, a map, a range like 0..10 or an Iterable", "can't loop over a value of type %s", t)
			}
		}
	}
	if s.Index != nil {
		c.declare(s.Index, Int, false, LoopVar)
	}
	c.declare(s.Elem, elem, false, LoopVar)
	if s.Pattern != nil {
		c.declPattern(s.Pattern, elem, LoopVar, false)
	}
	c.loops++
	body()
	c.loops--
}

func (c *checker) returnStmt(s *ast.ReturnStmt) {
	if c.inDefer {
		if s.Value != nil {
			c.expr(s.Value, nil)
		}
		c.errorAt(s, "a defer block runs as its block ends, so it can't return", "return can't be used in a defer block")
		return
	}
	if len(c.lits) > 0 {
		c.closureReturn(s, c.lits[len(c.lits)-1])
		return
	}
	f := c.fn
	switch {
	case f.Streams() && s.Value != nil:
		c.expr(s.Value, nil)
		c.errorAt(s.Value, "give values with yield, and end the stream with a bare return", "a stream function returns no value")
	case f.Generates() && s.Value != nil:
		c.expr(s.Value, nil)
		c.errorAt(s.Value, "give values with yield, and end the generator with a bare return", "a generator returns no value")
	case f.Streams() || f.Generates():
	case s.Value == nil && f.Result != Void:
		c.errorAt(s, fmt.Sprintf("return a value, e.g. return %s", zeroExample(f.Result)), "missing return value: %s must return %s", f.Name, f.Result)
	case s.Value != nil && f.Result == Void:
		t := c.expr(s.Value, nil)
		c.errorAt(s.Value, fmt.Sprintf("declare the result type: fn %s(...) -> %s", f.Name, t), "%s doesn't return a value", f.Name)
	case s.Value != nil:
		c.assignable(f.Result, c.expr(s.Value, f.Result), s.Value, "")
	}
}

// Returns a sample value of [t] for hints.
func zeroExample(t Type) string {
	switch t {
	case Int:
		return "0"
	case Float:
		return "0.0"
	case Bool:
		return "false"
	case String:
		return `""`
	}
	if _, ok := t.(*List); ok {
		return "[]"
	}
	return "..."
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Reports field defaults that create their own struct again, such as
// `struct S { a: S? = S() }`, which would never finish.
func (c *checker) checkDefaultCycles() {
	type slot struct {
		s *Struct
		i int
	}
	// Each default leads to the defaults its constructor calls leave out.
	next := func(at slot) []slot {
		var out []slot
		ast.Inspect(at.s.Fields[at.i].Default, func(n ast.Node) bool {
			e, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			call := c.info.Calls[e]
			if call == nil || call.Kind != ConstructorCall || call.Struct == nil {
				return true
			}
			for j, a := range call.Args {
				if j < len(call.Struct.Fields) && a != nil && a == call.Struct.Fields[j].Default {
					out = append(out, slot{call.Struct, j})
				}
			}
			return true
		})
		return out
	}
	const (
		visiting = 1
		done     = 2
	)
	state := map[slot]int{}
	var visit func(at slot) bool
	visit = func(at slot) bool {
		switch state[at] {
		case visiting:
			return true
		case done:
			return false
		}
		state[at] = visiting
		cyclic := false
		for _, n := range next(at) {
			if visit(n) {
				cyclic = true
				break
			}
		}
		state[at] = done
		return cyclic
	}
	for _, s := range c.info.Structs {
		if s.generic != nil {
			continue
		}
		for i, f := range s.Fields {
			if f.Default != nil && state[slot{s, i}] == 0 && visit(slot{s, i}) {
				back := c.enter(s.Decl)
				c.errorAt(f.Default, "pass that field explicitly, or default it to nil", "the default of '%s' creates another %s that uses the same default, so it never ends", f.Name, s.Name)
				back()
			}
		}
	}
}

// Checks the tests of a program built to run them: their names must
// differ. Such a program needs no main.
func (c *checker) checkTests() {
	seen := map[string]*ast.FuncDecl{}
	for _, t := range c.info.Tests {
		if prev := seen[t.Test]; prev != nil {
			back := c.enter(t)
			c.errorAtPos(t.Name.Pos(), len(t.Test)+2, "give each test its own name", "a test named '%s' is already on line %d", t.Test, prev.Pos().Line)
			back()
			continue
		}
		seen[t.Test] = t
	}
}

// --- Warnings ---

// A local variable and the file it is declared in.
type local struct {
	v    *Var
	file *diag.File
	hint string
}

// Records a warning that underlines [n], once for each place.
func (c *checker) warnAt(n ast.Node, hint, format string, args ...any) {
	start, end := n.Pos().Offset, n.EndOffset()
	length := 1
	if end > start && end <= len(c.file.Src) {
		length = utf8.RuneCountInString(c.file.Src[start:end])
	}
	d := diag.Diagnostic{File: c.file, Pos: n.Pos(), Len: length, Message: fmt.Sprintf(format, args...), Hint: hint, Warning: true}
	for _, w := range c.info.Warnings {
		if w.File == d.File && w.Pos == d.Pos && w.Message == d.Message {
			return
		}
	}
	c.info.Warnings = append(c.info.Warnings, d)
}

// Checks the statements of a block, warning at the first one that can't
// run because the one before it always leaves.
func (c *checker) stmts(list []ast.Stmt) {
	var locals []string
	for _, s := range list {
		if l, ok := s.(*ast.LocalFuncStmt); ok {
			locals = append(locals, l.Decl.Name.Name)
		}
	}
	if len(locals) > 0 {
		if c.pending == nil {
			c.pending = map[string]int{}
		}
		for _, n := range locals {
			c.pending[n]++
		}
		defer func() {
			for _, n := range locals {
				c.pending[n]--
			}
		}()
	}
	warned := false
	for i, s := range list {
		if i > 0 && !warned && jumps(list[i-1]) {
			c.warnAt(s, "remove it, or move it above the "+jumpWord(list[i-1]), "this code is never reached")
			warned = true
		}
		c.stmt(s)
	}
}

// Reports whether the statements after [s] in its block can't run.
func jumps(s ast.Stmt) bool {
	switch s.(type) {
	case *ast.BreakStmt, *ast.ContinueStmt:
		return true
	}
	return terminates(s)
}

// Names what [s], a statement that [jumps], does, for hints.
func jumpWord(s ast.Stmt) string {
	switch s := s.(type) {
	case *ast.ReturnStmt:
		return "return"
	case *ast.ThrowStmt:
		return "throw"
	case *ast.BreakStmt:
		return "break"
	case *ast.ContinueStmt:
		return "continue"
	case *ast.ExprStmt:
		if IsPanic(s.X) {
			return "panic"
		}
	case *ast.WhileStmt:
		return "loop"
	}
	return "statement before it"
}

// Warns about the local variables from index [start] on that are never
// used. A name starting with _ marks one that is meant to be unused.
func (c *checker) warnUnused(start int) {
	used := map[*Var]bool{}
	for _, u := range c.info.Uses {
		for v, ok := u.(*Var); ok && v != nil; v = v.Narrows {
			used[v] = true
		}
	}
	saved := c.file
	for _, l := range c.locals[start:] {
		if used[l.v] || strings.HasPrefix(l.v.Name, "_") || l.file == nil {
			continue
		}
		hint := l.hint
		if hint == "" {
			hint = "use it, remove it, or name it _" + l.v.Name
		}
		c.file = l.file
		c.warnAt(l.v.Decl, hint, "'%s' is never used", l.v.Name)
	}
	c.file = saved
}
