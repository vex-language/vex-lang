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
	file    *diag.File
	info    *Info
	errs    diag.List
	structs map[string]*Struct
	enums   map[string]*Enum
	funcs   map[string]*Func

	scope *scope

	// The function being checked, its `self` (nil outside instance
	// methods), and how many loops enclose the current statement.
	fn    *Func
	self  *Var
	loops int

	// The closures enclosing the current expression, innermost last.
	lits []*litCtx

	// The try expressions whose calls are being checked, innermost last.
	tries []*tryCtx
}

// Checks [file] and returns what was learned about it.
//
// On errors the returned error is a [diag.List]; the [Info] is still
// returned but may be incomplete.
func Check(f *diag.File, file *ast.File) (info *Info, err error) {
	c := &checker{
		file: f,
		info: &Info{
			Types:      map[ast.Expr]Type{},
			Uses:       map[*ast.Ident]any{},
			Defs:       map[*ast.Ident]*Var{},
			Calls:      map[*ast.CallExpr]*Call{},
			Members:    map[*ast.MemberExpr]*Member{},
			Selves:     map[*ast.FuncDecl]*Var{},
			Captures:   map[*ast.FuncLit][]*Var{},
			CaseValues: map[ast.Expr]*Case{},
			Wraps:      map[ast.Expr]*Optional{},
			Chains:     map[ast.Expr]bool{},
			Matches:    map[*ast.MatchExpr]*Match{},
			FuncOf:     map[*ast.FuncDecl]*Func{},
			Throwing:   map[*ast.CallExpr]Type{},
			Tries:      map[*ast.TryExpr]*Try{},
		},
		structs: map[string]*Struct{},
		enums:   map[string]*Enum{},
		funcs:   map[string]*Func{},
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(tooMany); !ok {
					panic(r)
				}
			}
		}()
		c.declareError()
		c.collect(file)
		c.bodies()
	}()
	if len(c.errs) > 0 {
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
	c.errs = append(c.errs, diag.Diagnostic{Pos: pos, Len: length, Message: fmt.Sprintf(format, args...), Hint: hint})
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
	return ""
}

// Checks that a [got] value can be used where [want] is expected.
func (c *checker) assignable(want, got Type, n ast.Node, what string) {
	if got == Void {
		c.errorAt(n, "", "this expression has no value")
		return
	}
	if !Identical(want, got) {
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
func (c *checker) collect(file *ast.File) {
	for _, d := range file.Decls {
		if sd, ok := d.(*ast.StructDecl); ok {
			c.declareStruct(sd)
		}
	}
	for _, d := range file.Decls {
		if ed, ok := d.(*ast.EnumDecl); ok {
			c.declareEnum(ed)
		}
	}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			c.declareFunc(fd)
		}
	}
	for _, s := range c.info.Structs {
		if s != c.info.Error {
			c.structMembers(s)
		}
	}
	for _, e := range c.info.Enums {
		c.enumMembers(e)
	}
	for _, f := range c.info.Funcs {
		if f.Owner == nil && f.Enum == nil {
			c.signature(f)
		}
	}
	c.checkStructCycles()
	c.checkDefaults()
	c.checkMain()
}

func (c *checker) declareStruct(sd *ast.StructDecl) {
	s := &Struct{Name: sd.Name.Name, Decl: sd}
	if c.reserved(sd.Name, "a struct") {
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
	f := &Func{Name: fd.Name.Name, Decl: fd}
	c.info.FuncOf[fd] = f
	if c.reserved(fd.Name, "a function") {
		return
	}
	switch {
	case c.funcs[f.Name] != nil:
		c.errorAt(fd.Name, "", "function '%s' is already declared on line %d", f.Name, c.funcs[f.Name].Decl.Pos().Line)
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
		c.info.FuncOf[md] = m
		if c.reserved(md.Name, "a method") {
			continue
		}
		if prev := s.Method(m.Name); prev != nil {
			c.errorAt(md.Name, "", "method '%s' is already declared on line %d", m.Name, prev.Decl.Pos().Line)
			continue
		}
		if f := s.Field(m.Name); f != nil {
			c.errorAt(md.Name, "", "'%s' is already a field of %s", m.Name, s.Name)
			continue
		}
		s.Methods = append(s.Methods, m)
		c.info.Funcs = append(c.info.Funcs, m)
		c.signature(m)
	}
}

// Resolves the parameter and result types of [f].
func (c *checker) signature(f *Func) {
	seen := map[string]bool{}
	for _, p := range f.Decl.Params {
		param := &Param{Name: p.Name.Name, Type: c.resolveType(p.Type), Default: p.Default}
		f.Params = append(f.Params, param)
		if c.reserved(p.Name, "a parameter") {
			continue
		}
		if seen[param.Name] {
			c.errorAt(p.Name, "", "parameter '%s' is declared twice", param.Name)
		}
		seen[param.Name] = true
	}
	f.Result = Void
	if f.Decl.Result != nil {
		f.Result = c.resolveType(f.Decl.Result)
	}
	if f.Decl.Throws != nil {
		f.Throws = c.throwsType(f.Decl.Throws)
	}
}

// Returns the type written as [te].
func (c *checker) resolveType(te ast.TypeExpr) Type {
	switch te := te.(type) {
	case *ast.ListType:
		return &List{Elem: c.resolveType(te.Elem)}
	case *ast.OptionalType:
		return &Optional{Elem: c.resolveType(te.Elem)}
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
	case *ast.NamedType:
		if t := builtinTypes[te.Name]; t != nil {
			return t
		}
		if s := c.structs[te.Name]; s != nil {
			return s
		}
		if e := c.enums[te.Name]; e != nil {
			return e
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
	for _, s := range c.info.Structs {
		if state[s] == 0 {
			visit(s, 0)
		}
	}
}

// Checks default values of parameters and fields. Defaults can't refer to
// other parameters or fields, so they are checked without local variables.
func (c *checker) checkDefaults() {
	c.scope = &scope{vars: map[string]*Var{}}
	for _, s := range c.info.Structs {
		for _, f := range s.Fields {
			if f.Default != nil {
				c.assignable(f.Type, c.expr(f.Default, f.Type), f.Default, "default of '"+f.Name+"'")
			}
		}
	}
	for _, f := range c.info.Funcs {
		for _, p := range f.Params {
			if p.Default != nil {
				c.assignable(p.Type, c.expr(p.Default, p.Type), p.Default, "default of '"+p.Name+"'")
			}
		}
	}
	c.scope = nil
}

func (c *checker) checkMain() {
	main := c.funcs["main"]
	if main == nil {
		c.errorAtPos(diag.Pos{Line: 1, Col: 1}, 1, "add fn main() { ... } where the program starts", "no main function")
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
	for _, f := range c.info.Funcs {
		c.funcBody(f)
	}
}

func (c *checker) funcBody(f *Func) {
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

// Reports whether [s] contains a `break` for the enclosing loop.
func breaks(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.BreakStmt:
		return true
	case *ast.Block:
		for _, st := range s.Stmts {
			if breaks(st) {
				return true
			}
		}
	case *ast.IfStmt:
		return breaks(s.Then) || (s.Else != nil && breaks(s.Else))
	case *ast.GuardStmt:
		return breaks(s.Else)
	case *ast.ExprStmt:
		if m, ok := s.X.(*ast.MatchExpr); ok {
			for _, a := range m.Arms {
				if a.Block != nil && breaks(a.Block) {
					return true
				}
			}
		}
	}
	return false
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
		if _, ok := s.X.(*ast.CallExpr); !ok {
			c.errorAt(s.X, "store it with let, or remove it", "this value isn't used")
		}
	case *ast.IfStmt:
		if s.Bind != nil {
			elem := c.unwrapped(c.expr(s.Cond, nil), s.Cond, "if let")
			c.push()
			c.declare(s.Bind, elem, false, LocalVar)
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
		c.cond(s.Cond, "while")
		c.loops++
		c.stmt(s.Body)
		c.loops--
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
		target = c.expr(x, nil)
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
		target = c.expr(x, nil)
		if m := c.info.Members[x]; m != nil && m.Builtin != "" {
			c.errorAt(x.Name, "", "%s can't be changed", m.Builtin)
		}
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

func (c *checker) forStmt(s *ast.ForStmt) {
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
		default:
			if t != Invalid {
				c.errorAt(s.Iter, "loop over a list or a range like 0..10", "can't loop over a value of type %s", t)
			}
		}
	}
	if s.Index != nil {
		c.declare(s.Index, Int, false, LoopVar)
	}
	c.declare(s.Elem, elem, false, LoopVar)
	c.loops++
	c.stmt(s.Body)
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
