package types

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/lexer"
)

// Checks [e] and returns its type. [want] is the type the context expects,
// or nil; it lets `[]` and int literals take their type from the context.
func (c *checker) expr(e ast.Expr, want Type) Type {
	t := c.exprType(e, want)
	c.info.Types[e] = t
	return t
}

func (c *checker) exprType(e ast.Expr, want Type) Type {
	switch e := e.(type) {
	case *ast.IntLit:
		if want == Float {
			return Float
		}
		if _, err := strconv.ParseInt(e.Value, 0, 64); err != nil {
			c.errorAt(e, "the largest int is 9223372036854775807", "number %s is too big for int", e.Value)
		}
		return Int
	case *ast.FloatLit:
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
		return c.ident(e)
	case *ast.ListLit:
		return c.listLit(e, want)
	case *ast.UnaryExpr:
		return c.unary(e, want)
	case *ast.BinaryExpr:
		return c.binary(e, want)
	case *ast.RangeExpr:
		c.errorAt(e, "for i in 0..10 { ... }", "ranges can only be used in for loops")
		return Invalid
	case *ast.CallExpr:
		return c.call(e)
	case *ast.IndexExpr:
		return c.index(e)
	case *ast.MemberExpr:
		return c.member(e)
	case *ast.FuncLit:
		return c.funcLit(e, want)
	}
	return Invalid
}

// Resolves a name used as a value.
func (c *checker) ident(id *ast.Ident) Type {
	if v := c.lookupVar(id.Name); v != nil {
		c.info.Uses[id] = v
		c.use(v)
		return v.Type
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
			c.info.Uses[id] = f
			c.use(c.self)
			return f.Type
		}
		if owner.Method(id.Name) != nil {
			c.errorAt(id, fmt.Sprintf("call it: %s()", id.Name), "method '%s' must be called", id.Name)
			return Invalid
		}
	}
	if f := c.funcs[id.Name]; f != nil {
		c.info.Uses[id] = f
		ft := &FuncType{Result: f.Result}
		for _, p := range f.Params {
			ft.Params = append(ft.Params, p.Type)
		}
		return ft
	}
	if s := c.structs[id.Name]; s != nil {
		c.errorAt(id, fmt.Sprintf("create one with %s(...)", s.Name), "'%s' is a type, not a value", s.Name)
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
	c.errorAt(id, didYouMean(id.Name, c.visibleNames()), "unknown name '%s'", id.Name)
	return Invalid
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
	for i, x := range e.Elems {
		t := c.expr(x, elem)
		if t == Void {
			c.errorAt(x, "", "this expression has no value")
			continue
		}
		if elem == nil || elem == Invalid {
			elem = t
			continue
		}
		if !Identical(elem, t) {
			c.mismatch(x, elem, t, fmt.Sprintf("list item %d", i+1))
		}
	}
	return &List{Elem: elem}
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
	t := c.expr(e.X, want)
	if !numeric(t) {
		c.errorAt(e.X, "", "'-' needs a number, found %s", t)
		return Invalid
	}
	return t
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
	case lexer.AndAnd, lexer.OrOr:
		for _, x := range []ast.Expr{e.X, e.Y} {
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
	if operandWant == nil && numeric(x) && x != Invalid {
		operandWant = x
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
			c.errorAt(e, "", "values of type %s can't be compared with %s", x, op)
		}
		return Bool
	case lexer.Lt, lexer.LtEq, lexer.Gt, lexer.GtEq:
		if !Identical(x, y) {
			c.errorAt(e, conversionHint(x, y), "can't compare %s with %s", x, y)
		} else if x != Int && x != Float && x != String {
			c.errorAt(e, "", "values of type %s can't be ordered with %s", x, op)
		}
		return Bool
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

// Reports whether values of type [t] can be compared with `==`.
func comparable(t Type) bool { return t == Int || t == Float || t == Bool || t == String }

func (c *checker) index(e *ast.IndexExpr) Type {
	x := c.expr(e.X, nil)
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
	if !ok || c.lookupVar(id.Name) != nil {
		return nil
	}
	if owner := c.ownStruct(); owner != nil && owner.Field(id.Name) != nil {
		return nil
	}
	return c.structs[id.Name]
}

func (c *checker) member(e *ast.MemberExpr) Type {
	name := e.Name.Name
	if s := c.structRef(e.X); s != nil {
		if s.Method(name) != nil {
			c.errorAt(e.Name, fmt.Sprintf("call it: %s.%s()", s.Name, name), "method '%s' must be called", name)
		} else {
			c.errorAt(e.Name, "", "%s has no static method '%s'", s.Name, name)
		}
		return Invalid
	}
	x := c.expr(e.X, nil)
	switch x := x.(type) {
	case *Struct:
		if f := x.Field(name); f != nil {
			c.info.Members[e] = &Member{Field: f}
			return f.Type
		}
		if x.Method(name) != nil {
			c.errorAt(e.Name, fmt.Sprintf("call it: %s()", name), "method '%s' must be called", name)
			return Invalid
		}
		c.errorAt(e.Name, didYouMean(name, memberNames(x)), "%s has no field '%s'", x.Name, name)
		return Invalid
	case *List:
		if name == "len" {
			c.info.Members[e] = &Member{Builtin: "len"}
			return Int
		}
	}
	if x == String && name == "len" {
		c.info.Members[e] = &Member{Builtin: "len"}
		return Int
	}
	if x == Invalid {
		return Invalid
	}
	if sig := builtinMember(x, name); sig != nil {
		c.errorAt(e.Name, fmt.Sprintf("call it: %s()", name), "'%s' must be called", name)
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
		names = append(names, m.Name)
	}
	return names
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
