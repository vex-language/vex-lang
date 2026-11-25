package types

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/diag"
	"github.com/irshadio/vex-lang/compiler/lexer"
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
	"assert": true, "str": true, "int": true, "float": true,
}

// A block of local variables.
type scope struct {
	vars   map[string]*Var
	parent *scope
}

type checker struct {
	info *Info
	errs diag.List

	// The declarations of every module, and where each one lives.
	decls    []ast.Decl
	units    map[ast.Node]*unit
	root     *Module
	rootUnit *unit

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
			Elements:       map[*ast.ExprStmt]ast.Expr{},
			Observers:      map[*ast.AssignStmt]*Func{},
			Chains:         map[ast.Expr]bool{},
			Matches:        map[*ast.MatchExpr]*Match{},
			PatternTypes:   map[ast.Pattern]Type{},
			PatternCases:   map[ast.Pattern]*Case{},
			PatternStructs: map[*ast.CasePattern]*Struct{},
			PatternVars:    map[ast.Pattern][]*Var{},
			FuncOf:         map[*ast.FuncDecl]*Func{},
			Throwing:       map[ast.Expr]Type{},
			Spawns:         map[*ast.CallExpr]bool{},
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
		c.bodies()
	}()
	c.fileTable()
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
	c.declareStaticConsts()
	c.checkStructCycles()
	c.checkDefaults()
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
		s.Fields = append(s.Fields, &Field{Name: fd.Name.Name, Type: c.resolveType(fd.Type), Default: fd.Default, Owner: s, Decl: fd})
	}
	for _, md := range s.Decl.Methods {
		m := &Func{Name: md.Name.Name, Decl: md, Owner: s, Static: md.Static}
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
		c.addMethod(s, m)
	}
	for _, m := range s.Methods {
		if m.Decl.Accessor != ast.Setter {
			continue
		}
		if g := s.member(m.Name, ast.Getter); g != nil && len(m.Params) == 1 && !Identical(g.Result, m.Params[0].Type) {
			c.errorAt(m.Decl.Params[0].Type, fmt.Sprintf("use %s, the type of the getter", g.Result), "setter '%s' takes %s, but its getter returns %s", m.Name, m.Params[0].Type, g.Result)
		}
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
	what := map[ast.Accessor]string{ast.Method: "method", ast.Getter: "getter", ast.Setter: "setter"}
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
	return false
}

// Resolves the parameter and result types of [f].
func (c *checker) signature(f *Func) {
	defer c.enter(f.Decl)()
	if len(f.Decl.TypeParams) > 0 && (f.Iface != nil || f.Decl.Accessor != ast.Method) {
		c.errorAt(f.Decl.TypeParams[0], "make it a method of a struct or enum", "interface methods and accessors can't have their own type parameters yet")
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
}

// Returns the type written as [te].
func (c *checker) resolveType(te ast.TypeExpr) Type {
	switch te := te.(type) {
	case *ast.ListType:
		return &List{Elem: c.resolveType(te.Elem)}
	case *ast.MapType:
		return c.mapType(te)
	case *ast.OptionalType:
		return optionalOf(c.resolveType(te.Elem))
	case *ast.FuncType:
		ft := &FuncType{Result: Void}
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
	case *ast.NamedType:
		if te.Module != "" {
			return c.qualifiedType(te)
		}
		if len(te.Args) > 0 {
			return c.genericType(te)
		}
		if t, ok := c.env[te.Name]; ok {
			return t
		}
		if t := builtinTypes[te.Name]; t != nil {
			return t
		}
		if te.Name == "Channel" && c.structs[te.Name] == nil && c.enums[te.Name] == nil && c.interfaces[te.Name] == nil && c.handles[te.Name] == nil {
			c.errorAt(te, "write its value type, e.g. Channel<int>", "Channel needs a type argument")
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
					c.errorAt(f.Decl.Type, "use a list instead, e.g. children: ["+inner.Name+"]",
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
			if f.Default != nil {
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
	args := c.typeArgs(te)
	if g := c.genEnums[te.Name]; g != nil {
		return c.genericEnumInst(te, g, args)
	}
	if g := c.genIfaces[te.Name]; g != nil {
		return c.genericIfaceInst(te, g, args)
	}
	g := c.genStructs[te.Name]
	if g == nil {
		if c.structs[te.Name] == nil && c.enums[te.Name] == nil && c.interfaces[te.Name] == nil && builtinTypes[te.Name] == nil {
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
	seen := map[string]bool{}
	var out diag.List
	for _, d := range errs {
		key := fmt.Sprintf("%d:%d:%s", d.Pos.Line, d.Pos.Col, d.Message)
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
	for i, p := range f.Decl.Params {
		c.declare(p.Name, f.Params[i].Type, false, ParamVar)
	}
	if f.Decl.ExprBody != nil {
		var want Type
		if f.Result != Void {
			want = f.Result
		}
		t := c.expr(f.Decl.ExprBody, want)
		if f.Result != Void {
			c.assignable(f.Result, t, f.Decl.ExprBody, "result of "+f.Name)
		}
	} else {
		for _, s := range f.Decl.Body.Stmts {
			c.stmt(s)
		}
		if f.Result != Void && !terminates(f.Decl.Body) {
			body := f.Decl.Body
			c.errorAtPos(c.file.PosOf(body.End-1), 1, "add a return statement before the closing }",
				"missing return: %s must return %s", f.Name, f.Result)
		}
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
		if s.Bind != nil {
			return false
		}
		lit, ok := s.Cond.(*ast.BoolLit)
		return ok && lit.Value && !breaks(s.Body)
	case *ast.ExprStmt:
		if m, ok := s.X.(*ast.MatchExpr); ok {
			return matchTerminates(m)
		}
		return isPanic(s.X)
	}
	return false
}

// Reports whether [s] contains a `break` for the enclosing loop, including
// one inside a match arm or a catch block, but not one in a nested loop or
// closure.
func breaks(s ast.Stmt) bool {
	found := false
	ast.Inspect(s, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.BreakStmt:
			found = true
		case *ast.WhileStmt, *ast.ForStmt, *ast.FuncLit:
			return n == ast.Node(s)
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
		for _, st := range s.Stmts {
			c.stmt(st)
		}
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
			c.stmt(s.Then)
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
			c.stmt(s.Then)
		}
		if s.Else != nil {
			c.stmt(s.Else)
		}
	case *ast.GuardStmt:
		elem := c.unwrapped(c.expr(s.Value, nil), s.Value, "guard let")
		c.stmt(s.Else)
		if !leaves(s.Else) {
			c.errorAtPos(s.Else.Pos(), 1, "end it with return, break, continue or panic(...)", "the else block of guard must leave, so '%s' always has a value after it", s.Bind.Name)
		}
		c.declare(s.Bind, elem, false, LocalVar)
	case *ast.WhileStmt:
		if s.Bind != nil {
			elem := c.unwrapped(c.expr(s.Cond, nil), s.Cond, "while let")
			c.push()
			c.declare(s.Bind, elem, false, LocalVar)
		} else {
			c.cond(s.Cond, "while")
		}
		c.loops++
		c.stmt(s.Body)
		c.loops--
		if s.Bind != nil {
			c.pop()
		}
	case *ast.ForStmt:
		c.forStmt(s)
	case *ast.ReturnStmt:
		c.returnStmt(s)
	case *ast.ThrowStmt:
		c.throwStmt(s)
	case *ast.BreakStmt:
		if c.loops == 0 {
			c.errorAt(s, "", "break can only be used inside a loop")
		}
	case *ast.ContinueStmt:
		if c.loops == 0 {
			c.errorAt(s, "", "continue can only be used inside a loop")
		}
	}
}

// Checks that the condition of [keyword] is a bool.
func (c *checker) cond(e ast.Expr, keyword string) {
	t := c.expr(e, Bool)
	if Identical(t, Bool) {
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
			c.errorAt(x, "", "can't change constant '%s'", u)
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
			c.errorAt(x, "", "can't change constant '%s'", m.Const)
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
		target = c.indexOf(x, xt)
		c.info.Types[x] = target
	default:
		target = c.expr(s.Target, nil)
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
	case lexer.PercentAssign, lexer.AmpPlusAssign, lexer.AmpMinusAssign, lexer.AmpStarAssign:
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
		if m != nil && m.DidSet != nil && c.fn != m.DidSet {
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
	if r, ok := s.Iter.(*ast.RangeExpr); ok {
		for _, bound := range []ast.Expr{r.Lo, r.Hi} {
			if t := c.expr(bound, Int); !Identical(t, Int) {
				c.errorAt(bound, conversionHint(Int, t), "range bounds must be int, found %s", t)
			}
		}
		c.info.Types[r] = Int
		elem = Int
		if s.Index != nil {
			c.errorAt(s.Index, "write for i in a..b", "a range has no separate index")
		}
	} else {
		t := c.expr(s.Iter, nil)
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
			if t != Invalid {
				c.errorAt(s.Iter, "loop over a list, a map or a range like 0..10", "can't loop over a value of type %s", t)
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
	if len(c.lits) > 0 {
		c.closureReturn(s, c.lits[len(c.lits)-1])
		return
	}
	f := c.fn
	switch {
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
