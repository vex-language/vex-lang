package types

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/lexer"
	"github.com/vex-language/vex-lang/compiler/modules"
)

// Checks [e] and returns its type. [want] is the type the context expects,
// or nil; it lets `[]` and int literals take their type from the context.
func (c *checker) expr(e ast.Expr, want Type) Type {
	o, opt := want.(*Optional)
	hint := want
	if opt {
		switch e.(type) {
		case *ast.NilLit, *ast.MatchExpr, *ast.IfExpr, *ast.TryExpr:
		default:
			// Literals and closures take their type from the value inside.
			hint = o.Elem
		}
	}
	t := c.exprType(e, hint)
	t = c.throwingRef(e, t, hint)
	c.info.Types[e] = t
	if opt && t != Invalid && t != Void {
		if _, isOpt := t.(*Optional); !isOpt && assignableType(o.Elem, t) {
			c.info.Wraps[e] = o
			return o
		}
	}
	return t
}

func (c *checker) exprType(e ast.Expr, want Type) Type {
	switch e := e.(type) {
	case *ast.KnownExpr:
		return e.Type.(Type)
	case *ast.IntLit:
		if want == Float {
			return Float
		}
		if _, err := strconv.ParseInt(e.Value, 0, 64); err != nil {
			c.errorAt(e, "the largest int is 9223372036854775807", "number %s is too big for int", e.Value)
		}
		return Int
	case *ast.FloatLit:
		v, err := strconv.ParseFloat(e.Value, 64)
		mantissa, _, _ := strings.Cut(strings.ToLower(e.Value), "e")
		switch {
		case err != nil:
			c.errorAt(e, "the largest float is about 1.8e308", "number %s is too big for float", e.Value)
		case v == 0 && strings.ContainsAny(mantissa, "123456789"):
			c.errorAt(e, "the smallest float above 0 is about 5e-324; write 0.0 for zero", "number %s is too small for float, so it would be 0", e.Value)
		}
		return Float
	case *ast.BoolLit:
		return Bool
	case *ast.StringLit:
		return String
	case *ast.InterpolatedString:
		for _, x := range e.Exprs {
			if t := c.expr(x, nil); !printable(t) {
				c.errorAt(x, "", "this expression has no value to put in a string")
			}
		}
		return String
	case *ast.Ident:
		if g := c.genFuncOf(e); g != nil {
			return c.genericFuncValue(e, g, want)
		}
		return c.ident(e)
	case *ast.MapLit:
		if e.Body != nil {
			return c.collection(e, e.Body, want, true)
		}
		return c.mapLit(e, want)
	case *ast.ListLit:
		if e.Body != nil {
			return c.collection(e, e.Body, want, false)
		}
		return c.listLit(e, want)
	case *ast.TypeArgsExpr:
		return c.typeArgsValue(e, want)
	case *ast.SpreadExpr:
		c.expr(e.X, nil)
		c.errorAt(e, "use it inside a list or map literal, e.g. [...xs, 4]", "'...' can only be used in a list or map literal")
		return Invalid
	case *ast.MapEntry:
		c.expr(e.Key, nil)
		c.expr(e.Value, nil)
		c.errorAt(e, "", "unexpected 'key: value' outside a map literal")
		return Invalid
	case *ast.UnaryExpr:
		return c.unary(e, want)
	case *ast.BinaryExpr:
		return c.binary(e, want)
	case *ast.RangeExpr:
		c.errorAt(e, "for i in 0..10 { ... }", "ranges can only be used in for loops")
		return Invalid
	case *ast.CallExpr:
		t := c.call(e, want)
		if m, ok := e.Fn.(*ast.MemberExpr); ok {
			return c.chain(e, m, t)
		}
		return t
	case *ast.NilLit:
		return c.nilLit(e, want)
	case *ast.IndexExpr:
		return c.index(e)
	case *ast.MemberExpr:
		if c.typeChain(e) {
			return Invalid
		}
		if g := c.genEnumRef(e.X); g != nil {
			return c.genericCaseValue(e, g, e.Name, want)
		}
		saved := c.tearWant
		c.tearWant = want
		t := c.member(e)
		c.tearWant = saved
		return c.chain(e, e, t)
	case *ast.FuncLit:
		return c.funcLit(e, want)
	case *ast.ImplicitMember:
		if en := c.implicitEnum(e, want); en != nil {
			return c.caseValue(e, en, e.Name, "."+e.Name.Name)
		}
		return Invalid
	case *ast.IfExpr:
		return c.ifValue(e, want)
	case *ast.TypeTest:
		return c.typeTest(e)
	case *ast.MatchExpr:
		return c.match(e, want, false)
	case *ast.TryExpr:
		return c.tryExpr(e, want, false)
	case *ast.AwaitExpr:
		return c.awaitExpr(e, want)
	case *ast.RecordLit:
		return c.recordLit(e, want)
	}
	return Invalid
}

// Resolves a name used as a value.
func (c *checker) ident(id *ast.Ident) Type {
	if id.Module != "" {
		return c.moduleIdent(id)
	}
	if v := c.lookupVar(id.Name); v != nil {
		c.info.Uses[id] = v
		if v.Narrows != nil {
			c.use(v.Narrows)
		} else {
			c.use(v)
		}
		return v.Type
	}
	if id.Name != "self" && c.selfMember(id.Name) {
		m := selfAccess(id)
		c.info.Uses[id] = &SelfMember{Expr: m}
		return c.expr(m, nil)
	}
	if id.Name == "self" {
		if c.self == nil {
			c.errorAt(id, "", "'self' can only be used inside a method")
			return Invalid
		}
		c.info.Uses[id] = c.self
		c.use(c.self)
		return c.self.Type
	}
	if owner := c.ownStruct(); owner != nil {
		if f := owner.Field(id.Name); f != nil {
			if c.self == nil {
				c.errorAt(id, "static methods have no instance; pass the value in as a parameter",
					"static method %s can't use field '%s'", c.fn.Name, id.Name)
				return Invalid
			}
			if c.fieldPrivate(id, f) {
				return Invalid
			}
			c.info.Uses[id] = f
			c.use(c.self)
			return f.Type
		}
		if p := owner.Property(id.Name); p != nil {
			if p.Get != nil && c.methodPrivate(id, p.Get) {
				return Invalid
			}
			if c.self == nil {
				c.errorAt(id, "static methods have no instance; pass the value in as a parameter",
					"static method %s can't use property '%s'", c.fn.Name, id.Name)
				return Invalid
			}
			c.info.Uses[id] = p
			c.use(c.self)
			return c.readProperty(id, id.Name, p)
		}
		if m := owner.Method(id.Name); m != nil {
			// A method named alone is torn off `self`, or off the type
			// when static.
			var x *ast.MemberExpr
			switch {
			case !m.Static && c.self != nil:
				x = selfAccess(id)
			case m.Static && owner.generic == nil:
				x = &ast.MemberExpr{Span: id.Span, X: &ast.Ident{Span: id.Span, Name: owner.Name}, Name: &ast.Ident{Span: id.Span, Name: id.Name}}
			default:
				c.errorAt(id, fmt.Sprintf("call it in a closure: fn(...) => %s(...)", id.Name), "method '%s' can't be used as a value here", id.Name)
				return Invalid
			}
			c.info.Uses[id] = &SelfMember{Expr: x}
			return c.expr(x, nil)
		}
	}
	if c.self != nil {
		var p *Property
		switch t := c.self.Type.(type) {
		case *Enum:
			p = t.Property(id.Name)
		case *Interface:
			p = ifaceProperty(t, id.Name)
		}
		if p != nil {
			c.info.Uses[id] = p
			c.use(c.self)
			return c.readProperty(id, id.Name, p)
		}
	}
	if k := c.constIdent(id); k != nil {
		return k.Type
	}
	if f := c.funcs[id.Name]; f != nil {
		c.info.Uses[id] = f
		return funcValue(f)
	}
	if s := c.structs[id.Name]; s != nil {
		c.errorAt(id, fmt.Sprintf("create one with %s(...)", s.Name), "'%s' is a type, not a value", s.Name)
		return Invalid
	}
	if g := c.genStructs[id.Name]; g != nil {
		c.errorAt(id, fmt.Sprintf("create one with %s(...)", id.Name), "'%s' is a type, not a value", id.Name)
		return Invalid
	}
	if i := c.interfaces[id.Name]; i != nil {
		c.errorAt(id, "", "'%s' is a type, not a value", i.Name)
		return Invalid
	}
	if c.genIfaces[id.Name] != nil || c.handles[id.Name] != nil {
		c.errorAt(id, "", "'%s' is a type, not a value", id.Name)
		return Invalid
	}
	if en := c.enums[id.Name]; en != nil {
		c.errorAt(id, caseHint("use one of its cases, e.g. ", en, true), "'%s' is a type, not a value", en.Name)
		return Invalid
	}
	if builtinTypes[id.Name] != nil {
		c.errorAt(id, "", "'%s' is a type, not a value", id.Name)
		return Invalid
	}
	if builtinFuncs[id.Name] {
		c.errorAt(id, fmt.Sprintf("call it: %s(...)", id.Name), "built-in function '%s' must be called", id.Name)
		return Invalid
	}
	if c.imported(id.Name) != nil {
		c.errorAt(id, fmt.Sprintf("use a name from it, e.g. %s.name", id.Name), "'%s' is a module, not a value", id.Name)
		return Invalid
	}
	hint := didYouMean(id.Name, c.visibleNames())
	if modules.IsStd(id.Name) {
		hint = fmt.Sprintf("import it at the top of the file: import \"vex:%s\"", id.Name)
	}
	if c.pending[id.Name] > 0 {
		c.errorAt(id, "move the declaration of "+id.Name+" above this line", "local function '%s' is used before it is declared", id.Name)
		return Invalid
	}
	c.errorAt(id, hint, "unknown name '%s'", id.Name)
	return Invalid
}

// Returns the type of function [f] used as a value.
// Returns the type of [e], whose own type is [t], where [want] is expected.
// A function that doesn't throw can be used where a throwing one is
// expected: `apply(twice)` with `apply(f: fn(int) throws -> int)`.
func (c *checker) throwingRef(e ast.Expr, t, want Type) Type {
	ft, ok := t.(*FuncType)
	wf, wok := want.(*FuncType)
	if !ok || !wok || ft.Throws != nil || wf.Throws == nil {
		return t
	}
	id, ok := e.(*ast.Ident)
	if !ok {
		return t
	}
	if _, ok := c.info.Uses[id].(*Func); !ok {
		return t
	}
	if !Identical(ft, &FuncType{Params: wf.Params, Result: wf.Result}) {
		return t
	}
	c.info.ThrowingRefs[e] = true
	return &FuncType{Params: ft.Params, Result: ft.Result, Throws: wf.Throws}
}

func funcValue(f *Func) *FuncType {
	ft := &FuncType{Result: f.Result, Throws: f.Throws, Async: f.Async}
	for _, p := range f.Params {
		ft.Params = append(ft.Params, p.Type)
	}
	return ft
}

// Returns the struct whose method is being checked, or nil.
func (c *checker) ownStruct() *Struct {
	if c.fn == nil {
		return nil
	}
	return c.fn.Owner
}

// Returns every name usable as a value here, for suggestions.
func (c *checker) visibleNames() []string {
	var names []string
	for s := c.scope; s != nil; s = s.parent {
		for n := range s.vars {
			names = append(names, n)
		}
	}
	if owner := c.ownStruct(); owner != nil && c.self != nil {
		for _, f := range owner.Fields {
			names = append(names, f.Name)
		}
	}
	return names
}

func (c *checker) listLit(e *ast.ListLit, want Type) Type {
	var elem Type
	if l, ok := want.(*List); ok {
		elem = l.Elem
	}
	if len(e.Elems) == 0 {
		if elem == nil {
			c.errorAt(e, "give the type, e.g. let xs: [int] = []", "can't tell the type of an empty list")
			return Invalid
		}
		return &List{Elem: elem}
	}
	// Without a written type, a nil among the items makes the list
	// optional: [1, nil] is an [int?].
	inferred := elem == nil
	var leadingNils []ast.Expr
	for i, x := range e.Elems {
		if inferred && isNil(x) {
			if elem == nil {
				leadingNils = append(leadingNils, x)
				continue
			}
			if _, ok := elem.(*Optional); !ok && elem != Invalid {
				elem = c.makeOptional(e.Elems[:i], elem)
			}
		}
		t := c.expr(x, elem)
		if t == Void {
			c.errorAt(x, "", "this expression has no value")
			continue
		}
		if elem == nil || elem == Invalid {
			elem = t
			if len(leadingNils) > 0 && t != Invalid {
				if _, ok := t.(*Optional); !ok {
					elem = c.makeOptional(e.Elems[i:i+1], t)
				}
				for _, n := range leadingNils {
					c.expr(n, elem)
				}
				leadingNils = nil
			}
			continue
		}
		if !assignableType(elem, t) {
			c.mismatch(x, elem, t, fmt.Sprintf("list item %d", i+1))
		}
	}
	if len(leadingNils) > 0 {
		c.expr(leadingNils[0], nil)
		return Invalid
	}
	if elem == nil {
		// Every item had no value.
		return Invalid
	}
	return &List{Elem: elem}
}

// Returns [t] made optional, storing the already checked [items] of type
// [t] as present values.
func (c *checker) makeOptional(items []ast.Expr, t Type) *Optional {
	o := &Optional{Elem: t}
	for _, x := range items {
		if Identical(c.info.Types[x], t) && c.info.Types[x] != Invalid {
			c.info.Wraps[x] = o
		}
	}
	return o
}

func (c *checker) unary(e *ast.UnaryExpr, want Type) Type {
	if e.Op == lexer.Not {
		t := c.expr(e.X, Bool)
		if !Identical(t, Bool) {
			c.errorAt(e.X, "", "'!' needs a bool, found %s", t)
			return Invalid
		}
		return Bool
	}
	if e.Op == lexer.Tilde {
		t := c.expr(e.X, Int)
		if t != Int && t != Invalid {
			c.errorAt(e.X, "", "'~' needs an int, found %s", t)
			return Invalid
		}
		return t
	}
	if IsMinInt(e) && want != Float {
		c.info.Types[e.X] = Int
		return Int
	}
	t := c.expr(e.X, want)
	if s, ok := t.(*Struct); ok {
		f := s.Operator("unary -")
		if f == nil {
			c.errorAt(e.X, fmt.Sprintf("define it in %s: operator -() -> %s", s.Name, s.Name), "'-' can't be used with %s", s)
			return Invalid
		}
		c.methodPrivate(e, f)
		c.info.Operators[e] = f
		return f.Result
	}
	if !numeric(t) {
		c.errorAt(e.X, "", "'-' needs a number, found %s", t)
		return Invalid
	}
	return t
}

// Reports whether [e] is the smallest int written as a literal,
// `-9223372036854775808`, whose digits alone don't fit in an int.
func IsMinInt(e ast.Expr) bool {
	u, ok := e.(*ast.UnaryExpr)
	if !ok || u.Op != lexer.Minus {
		return false
	}
	lit, ok := u.X.(*ast.IntLit)
	if !ok {
		return false
	}
	v, err := strconv.ParseUint(lit.Value, 0, 64)
	return err == nil && v == 1<<63
}

// Reports whether [e] is an int literal, possibly negated.
func isIntConst(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.IntLit:
		return true
	case *ast.UnaryExpr:
		return e.Op == lexer.Minus && isIntConst(e.X)
	}
	return false
}

// Retypes the int literal [e] as float, as in `price * 2`.
func (c *checker) toFloat(e ast.Expr) {
	c.info.Types[e] = Float
	if u, ok := e.(*ast.UnaryExpr); ok {
		c.toFloat(u.X)
	}
}

func (c *checker) binary(e *ast.BinaryExpr, want Type) Type {
	op := strings.Trim(e.Op.String(), "'")
	switch e.Op {
	case lexer.QuestionQuestion:
		return c.coalesce(e)
	case lexer.Eq, lexer.NotEq:
		if isNil(e.X) || isNil(e.Y) {
			return c.nilComparison(e)
		}
	case lexer.EqEq, lexer.NotEqEq:
		return c.identity(e)
	case lexer.AndAnd, lexer.OrOr:
		for i, x := range []ast.Expr{e.X, e.Y} {
			if i == 1 && e.Op == lexer.AndAnd {
				// The right side runs only when the left holds.
				c.pushNarrowed(e.X)
				defer c.pop()
			}
			if t := c.expr(x, Bool); !Identical(t, Bool) {
				c.errorAt(x, "", "'%s' needs bool on both sides, found %s", op, t)
			}
		}
		return Bool
	}

	var operandWant Type
	if want == Float && e.Op != lexer.Eq && e.Op != lexer.NotEq && precedenceOf(e.Op) >= 5 {
		operandWant = Float
	}
	x := c.expr(e.X, operandWant)
	equality := e.Op == lexer.Eq || e.Op == lexer.NotEq
	if s, ok := x.(*Struct); ok && !(equality && s.Operator("==") == nil && s.Equatable()) {
		return c.binaryOperator(e, s)
	}
	switch x.(type) {
	case *Struct, *List, *Map, *Optional:
		if equality {
			operandWant = x
		}
	}
	if operandWant == nil && numeric(x) && x != Invalid {
		operandWant = x
	}
	if en, ok := x.(*Enum); ok {
		operandWant = en
	}
	if r, ok := x.(*Record); ok && (e.Op == lexer.Eq || e.Op == lexer.NotEq) {
		operandWant = r
	}
	y := c.expr(e.Y, operandWant)
	if x == Int && y == Float && isIntConst(e.X) {
		c.toFloat(e.X)
		x = Float
	}
	if x == Invalid || y == Invalid {
		if comparison(e.Op) {
			return Bool
		}
		return Invalid
	}
	if x == Void || y == Void {
		c.errorAt(e, "", "'%s' can't be used on an expression with no value", op)
		return Invalid
	}

	switch e.Op {
	case lexer.Eq, lexer.NotEq:
		if !Identical(x, y) {
			c.errorAt(e, conversionHint(x, y), "can't compare %s with %s", x, y)
		} else if !comparable(x) {
			hint := ""
			if _, ok := x.(*Optional); ok {
				hint = "compare with nil, or unwrap it first with if let"
			}
			if tp, ok := x.(*TypeParam); ok {
				hint = fmt.Sprintf("require it: <%s: Equatable>", tp.Name)
			}
			c.errorAt(e, hint, "values of type %s can't be compared with %s", x, op)
		}
		return Bool
	case lexer.Lt, lexer.LtEq, lexer.Gt, lexer.GtEq:
		if !Identical(x, y) {
			c.errorAt(e, conversionHint(x, y), "can't compare %s with %s", x, y)
		} else if !ordered(x) {
			hint := ""
			if tp, ok := x.(*TypeParam); ok {
				hint = fmt.Sprintf("require it: <%s: Comparable>", tp.Name)
			}
			c.errorAt(e, hint, "values of type %s can't be ordered with %s", x, op)
		}
		return Bool
	}

	if (wrapping(e.Op) || bitwise(e.Op)) && (x != Int || y != Int) {
		hint := ""
		switch {
		case x == Bool && y == Bool && e.Op == lexer.Amp:
			hint = "use && for logical and"
		case x == Bool && y == Bool && e.Op == lexer.Pipe:
			hint = "use || for logical or"
		}
		if bitwise(e.Op) {
			// An int literal beside a float has already become a float, so
			// only the operand that isn't an int is named.
			bad := x
			if x == Int {
				bad = y
			}
			c.errorAt(e, hint, "'%s' works only on int, found %s", op, bad)
			return Invalid
		}
		c.errorAt(e, hint, "'%s' works only on int, found %s and %s", op, x, y)
		return Invalid
	}
	if Identical(x, y) {
		switch {
		case e.Op == lexer.Plus && (numeric(x) || x == String):
			return x
		case e.Op == lexer.Percent && x == Int:
			return Int
		case e.Op != lexer.Plus && e.Op != lexer.Percent && numeric(x):
			return x
		}
		c.errorAt(e, "", "'%s' can't be used with %s", op, x)
		return Invalid
	}

	hint := ""
	switch {
	case e.Op == lexer.Plus && (x == String || y == String):
		hint = `put the value inside the string instead, e.g. "{count} items"`
		if fix := interpolationFix(e); fix != "" {
			hint = "put the value inside the string instead: " + fix
		}
	case x == Int && y == Float:
		hint = "convert the int with float(...)"
	case x == Float && y == Int:
		hint = "convert the int with float(...)"
	}
	c.errorAt(e, hint, "can't use '%s' with %s and %s", op, x, y)
	return Invalid
}

// Returns the interpolated string that replaces `"text" + name` or
// `name + "text"`, or "" if [e] isn't that shape.
func interpolationFix(e *ast.BinaryExpr) string {
	lit, id := e.X, e.Y
	if _, ok := lit.(*ast.StringLit); !ok {
		lit, id = id, lit
	}
	s, ok := lit.(*ast.StringLit)
	name, ok2 := id.(*ast.Ident)
	if !ok || !ok2 || strings.ContainsAny(s.Value, "\"\\{}\n\t\r") {
		return ""
	}
	if lit == e.X {
		return `"` + s.Value + "{" + name.Name + `}"`
	}
	return `"{` + name.Name + "}" + s.Value + `"`
}

func comparison(k lexer.Kind) bool {
	switch k {
	case lexer.Eq, lexer.NotEq, lexer.Lt, lexer.LtEq, lexer.Gt, lexer.GtEq:
		return true
	}
	return false
}

// Returns 5 for additive and 6 for multiplicative operators, 0 otherwise.
func precedenceOf(k lexer.Kind) int {
	switch k {
	case lexer.Plus, lexer.Minus:
		return 5
	case lexer.Star, lexer.Slash, lexer.Percent:
		return 6
	}
	return 0
}

// Reports whether [k] is an operator that wraps around on overflow instead
// of stopping the program: `&+`, `&-`, `&*` and their assignments.
func wrapping(k lexer.Kind) bool {
	switch k {
	case lexer.AmpPlus, lexer.AmpMinus, lexer.AmpStar, lexer.AmpPlusAssign, lexer.AmpMinusAssign, lexer.AmpStarAssign:
		return true
	}
	return false
}

// Reports whether [k] is a bitwise operator: `&`, `|`, `^`, `<<` or `>>`.
func bitwise(k lexer.Kind) bool {
	switch k {
	case lexer.Amp, lexer.Pipe, lexer.Caret, lexer.Shl, lexer.Shr:
		return true
	}
	return false
}

// Reports whether values of type [t] can be ordered with `<`. Enums whose
// cases carry no values are ordered as their cases are declared.
func ordered(t Type) bool {
	switch t := t.(type) {
	case *TypeParam:
		return t.Has("Comparable")
	case *Struct:
		return t.Operator("<") != nil && t.Operator("==") != nil
	case *Enum:
		return t.Simple()
	}
	return t == Int || t == Float || t == String
}

// Reports whether values of type [t] can be compared with `==`.
func comparable(t Type) bool {
	switch t := t.(type) {
	case *Enum:
		return t.Simple() || t.Equatable()
	case *Struct:
		return t.Operator("==") != nil || t.Equatable()
	case *List:
		return comparable(t.Elem)
	case *Map:
		return comparable(t.Value)
	case *Optional:
		return comparable(t.Elem)
	case *TypeParam:
		return t.Has("Equatable")
	case *Record:
		for _, f := range t.FieldTypes() {
			if !comparable(f) {
				return false
			}
		}
		return true
	}
	if _, ok := t.(*Handle); ok {
		return true
	}
	return t == Int || t == Float || t == Bool || t == String
}

func (c *checker) index(e *ast.IndexExpr) Type {
	return c.indexOf(e, c.expr(e.X, nil))
}

// Checks the index of [e], whose indexed value has type [x].
func (c *checker) indexOf(e *ast.IndexExpr, x Type) Type {
	if m, ok := x.(*Map); ok {
		c.mapKey(e.Index, m)
		return &Optional{Elem: m.Value}
	}
	if s, ok := x.(*Struct); ok {
		f := s.Operator("[]")
		if f == nil {
			c.expr(e.Index, nil)
			c.errorAt(e.X, fmt.Sprintf("define it in %s: operator [](index: int) -> ...", s.Name), "can't index a value of type %s", s)
			return Invalid
		}
		c.methodPrivate(e, f)
		c.assignable(f.Params[0].Type, c.expr(e.Index, f.Params[0].Type), e.Index, "index")
		c.info.Operators[e] = f
		return f.Result
	}
	if t := c.expr(e.Index, Int); !Identical(t, Int) {
		c.errorAt(e.Index, conversionHint(Int, t), "index must be int, found %s", t)
	}
	switch x := x.(type) {
	case *List:
		return x.Elem
	}
	if x == Invalid {
		return Invalid
	}
	c.errorAt(e.X, "", "can't index a value of type %s", x)
	return Invalid
}

// Returns the struct named by [e] when it is used as a type, as in
// `User.guest()`, or nil.
func (c *checker) structRef(e ast.Expr) *Struct {
	id, ok := e.(*ast.Ident)
	if ok && id.Module != "" {
		s, _ := c.moduleRef(id)
		return s
	}
	if !ok || c.lookupVar(id.Name) != nil {
		return nil
	}
	if owner := c.ownStruct(); owner != nil && owner.Field(id.Name) != nil {
		return nil
	}
	if s := c.structs[id.Name]; s != nil {
		return s
	}
	_, t := c.aliasedNominal(c.mod, id.Name)
	s, _ := t.(*Struct)
	return s
}

func (c *checker) member(e *ast.MemberExpr) Type {
	name := e.Name.Name
	writing := c.writing
	c.writing = false
	want := c.tearWant
	c.tearWant = nil
	at := e.Name.Span
	called := func(x ast.Expr) ast.Expr { return memberAccess(x, name, at) }
	if en := c.enumRef(e.X); en != nil {
		if k := c.staticConst(e, nil, en); k != nil {
			return k.Type
		}
		return c.caseValue(e, en, e.Name, en.Name+"."+name)
	}
	if s := c.structRef(e.X); s != nil {
		if k := c.staticConst(e, s, nil); k != nil {
			return k.Type
		}
		switch m := s.Method(name); {
		case m != nil && !m.Static:
			c.errorAt(e.Name, fmt.Sprintf("tear it off a value instead: value.%s", name), "method '%s' isn't static, so it needs a value of type %s", name, s.Name)
		case m != nil:
			if c.methodPrivate(e.Name, m) {
				return Invalid
			}
			if sig := c.funcSig(e.Name, m); sig != nil {
				return c.tearOff(e, sig, nil, called, want)
			}
		case name == "new":
			return c.tearOff(e, fieldsSig(s.Fields, s), nil, func(x ast.Expr) ast.Expr { return x }, want)
		default:
			c.errorAt(e.Name, "", "%s has no static method '%s'", s.Name, name)
		}
		return Invalid
	}
	if t := basicRef(e.X); t != nil {
		return c.basicStatic(e, t, writing, want)
	}
	if g := c.genStructRef(e.X); g != nil && name == "new" {
		var names []string
		for _, f := range c.structTemplate(g).Fields {
			names = append(names, f.Name)
		}
		return c.genericTearOff(e, names, want, written(e.X)+".new", func(x ast.Expr) ast.Expr { return x })
	}
	x := c.recv(e)
	switch x := x.(type) {
	case *Record:
		ft := x.Field(name)
		if ft == nil {
			hint := didYouMean(name, x.FieldNames())
			if strings.HasPrefix(name, "$") && hint == "" {
				hint = "positional fields count from $1"
				if i, err := strconv.Atoi(name[1:]); err == nil && x.Field(fmt.Sprintf("$%d", i)) != nil {
					hint = fmt.Sprintf("write $%d", i)
				}
			}
			c.errorAt(e.Name, hint, "%s has no field '%s'", x, name)
			return Invalid
		}
		if writing {
			c.errorAt(e.Name, "make a new record instead", "record fields can't be changed")
		}
		c.info.Members[e] = &Member{RecordField: name}
		return ft
	case *Enum:
		if p := x.Property(name); p != nil {
			c.methodPrivate(e.Name, p.Get)
			c.info.Members[e] = &Member{Property: p}
			return p.Type()
		}
		if m := x.Method(name); m != nil {
			if c.methodPrivate(e.Name, m) {
				return Invalid
			}
			if sig := c.funcSig(e.Name, m); sig != nil {
				return c.tearOff(e, sig, x, called, want)
			}
		} else {
			c.errorAt(e.Name, "read the values a case carries with match, e.g. match v { circle(r) => r, ... }", "%s has no member '%s'", x.Name, name)
		}
		return Invalid
	case *Struct:
		if f := x.Field(name); f != nil {
			c.fieldPrivate(e.Name, f)
			c.info.Members[e] = &Member{Field: f}
			return f.Type
		}
		if p := x.Property(name); p != nil {
			if f := p.Get; f != nil && !writing {
				c.methodPrivate(e.Name, f)
			}
			c.info.Members[e] = &Member{Property: p}
			c.writing = writing
			defer func() { c.writing = false }()
			return c.readProperty(e.Name, name, p)
		}
		if m := x.Method(name); m != nil {
			if c.methodPrivate(e.Name, m) {
				return Invalid
			}
			if sig := c.funcSig(e.Name, m); sig != nil {
				return c.tearOff(e, sig, x, called, want)
			}
			return Invalid
		}
		c.errorAt(e.Name, didYouMean(name, memberNames(x)), "%s has no field '%s'", x.Name, name)
		return Invalid
	case *Future:
		if name == "isCancelled" {
			c.info.Members[e] = &Member{Builtin: name}
			return Bool
		}
	case *Stream:
		switch {
		case x.Controller && name == "stream":
			c.info.Members[e] = &Member{Builtin: "stream"}
			return &Stream{Elem: x.Elem}
		case x.Controller && name == "isClosed":
			c.info.Members[e] = &Member{Builtin: "isClosed"}
			return Bool
		}
	case *List:
		switch name {
		case "len":
			c.info.Members[e] = &Member{Builtin: "len"}
			return Int
		case "isEmpty":
			c.info.Members[e] = &Member{Builtin: "isEmpty"}
			return Bool
		case "first", "last":
			c.info.Members[e] = &Member{Builtin: name}
			return &Optional{Elem: x.Elem}
		}
	case *Map:
		switch name {
		case "len":
			c.info.Members[e] = &Member{Builtin: "len"}
			return Int
		case "keys":
			c.info.Members[e] = &Member{Builtin: "keys"}
			return &List{Elem: x.Key}
		case "values":
			c.info.Members[e] = &Member{Builtin: "values"}
			return &List{Elem: x.Value}
		}
	}
	if x == String && name == "len" {
		c.info.Members[e] = &Member{Builtin: "len"}
		return Int
	}
	if x == Float && (name == "isNaN" || name == "isFinite") {
		c.info.Members[e] = &Member{Builtin: name}
		return Bool
	}
	if x == Invalid {
		return Invalid
	}
	switch x.(type) {
	case *Interface, *TypeParam:
		if p := ifaceProperty(x, name); p != nil {
			c.info.Members[e] = &Member{Property: p}
			c.writing = writing
			defer func() { c.writing = false }()
			return c.readProperty(e.Name, name, p)
		}
		if _, m := ifaceMethod(x, name); m != nil {
			if sig := c.funcSig(e.Name, m); sig != nil {
				return c.tearOff(e, sig, x, called, want)
			}
		} else {
			c.errorAt(e.Name, c.narrowHint(e.X, didYouMean(name, ifaceNames(x))), "%s has no member '%s'", x, name)
		}
		return Invalid
	}
	if t := c.recvGetter(e, x); t != nil {
		return t
	}
	if sig := builtinMember(x, name); sig != nil {
		return c.tearOff(e, memberSigOf(sig), x, called, want)
	}
	if _, ok := x.(*List); ok && (name == "map" || name == "flatMap" || name == "reduce") {
		c.errorAt(e.Name, "call it in a closure: fn(x) => ...", "'%s' can't be used as a value", name)
		return Invalid
	}
	if r, b := c.findRecv(x, name, ast.Method, e.Name); r != nil {
		f := r.plain
		if f == nil {
			args := make([]Type, len(r.gen.params))
			for i, p := range r.gen.params {
				if args[i] = b.types[p]; args[i] == nil {
					c.errorAt(e.Name, fmt.Sprintf("call it in a closure: fn(...) => %s(...)", name), "generic method '%s' can't be used as a value", name)
					return Invalid
				}
			}
			if f = c.instFunc(r.gen, args, e); f == nil {
				return Invalid
			}
		}
		if sig := c.funcSig(e.Name, f); sig != nil {
			return c.tearOff(e, sig, x, called, want)
		}
		return Invalid
	}
	if c.recvUnavailable(e.Name, x, name) {
		return Invalid
	}
	c.errorAt(e.Name, didYouMean(name, builtinMemberNames(x)), "%s has no member '%s'", x, name)
	return Invalid
}

func memberNames(s *Struct) []string {
	var names []string
	for _, f := range s.Fields {
		names = append(names, f.Name)
	}
	for _, m := range s.Methods {
		if m.Decl.Accessor != ast.Observer && m.Decl.Accessor != ast.Operator {
			names = append(names, m.Name)
		}
	}
	return names
}

// Returns the type of property [p] read at [n], reporting an error when
// it has no getter. An assignment target is only written, unless it is a
// compound assignment, which the assignment checks.
func (c *checker) readProperty(n ast.Node, name string, p *Property) Type {
	if p.Get == nil && !c.writing {
		c.errorAt(n, "add a getter: get "+name+" -> ...", "'%s' has a setter but no getter, so it can't be read", name)
		return Invalid
	}
	return p.Type()
}

// Returns a "did you mean" hint naming the closest of [names] to [name],
// or "" if none is close.
func didYouMean(name string, names []string) string {
	sort.Strings(names)
	best, bestDist := "", 3
	for _, n := range names {
		if n == name {
			continue
		}
		limit := max(1, min(2, (len(name)+1)/3))
		if d := editDistance(strings.ToLower(name), strings.ToLower(n)); d <= limit && d < bestDist {
			best, bestDist = n, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf("did you mean '%s'?", best)
}

// Returns the edit distance between [a] and [b], counting a swap of two
// neighbouring letters as one edit.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
