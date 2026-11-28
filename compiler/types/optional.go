package types

import (
	"fmt"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// Returns the type inside the optional [t], which [what] unwraps,
// reporting an error if [t] isn't optional.
func (c *checker) unwrapped(t Type, n ast.Node, what string) Type {
	if o, ok := t.(*Optional); ok {
		return o.Elem
	}
	if t != Invalid {
		c.errorAt(n, "it always has a value, so a plain let works", "%s needs an optional value, but this is %s", what, t)
	}
	return Invalid
}

func (c *checker) nilLit(e *ast.NilLit, want Type) Type {
	switch want.(type) {
	case *Optional:
		return want
	case nil:
		c.errorAt(e, "write the type, e.g. let user: User? = nil", "can't tell the type of nil")
		return Invalid
	}
	if want != Invalid {
		c.errorAt(e, fmt.Sprintf("make the type optional: %s?", want), "nil can't be used as %s, which always has a value", want)
	}
	return Invalid
}

// Reports whether [s] always ends by returning, stopping, or jumping out of
// the current loop iteration.
func leaves(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.BreakStmt, *ast.ContinueStmt:
		return true
	case *ast.Block:
		return len(s.Stmts) > 0 && leaves(s.Stmts[len(s.Stmts)-1]) || terminates(s)
	case *ast.IfStmt:
		return s.Else != nil && leaves(s.Then) && leaves(s.Else) || terminates(s)
	}
	return terminates(s)
}

func isNil(e ast.Expr) bool {
	_, ok := e.(*ast.NilLit)
	return ok
}

// Checks `x == nil` or `x != nil`.
func (c *checker) nilComparison(e *ast.BinaryExpr) Type {
	value, nilSide := e.X, e.Y
	if isNil(value) {
		value, nilSide = e.Y, e.X
	}
	t := c.expr(value, nil)
	if _, ok := t.(*Optional); ok {
		c.expr(nilSide, t)
		return Bool
	}
	c.info.Types[nilSide] = Invalid
	if t != Invalid {
		c.errorAt(e, "", "can't compare %s with nil: only optional values can be nil", t)
	}
	return Bool
}

// Checks `x ?? fallback`: the value inside [x], or [fallback] when it is
// nil.
func (c *checker) coalesce(e *ast.BinaryExpr) Type {
	x := c.expr(e.X, nil)
	o, ok := x.(*Optional)
	if !ok {
		if j, isJump := e.Y.(*ast.JumpExpr); isJump {
			c.jumpExpr(j, x)
		} else {
			c.expr(e.Y, x)
		}
		if x != Invalid {
			c.errorAt(e.X, "it always has a value, so ?? isn't needed", "?? needs an optional on the left, but this is %s", x)
		}
		return x
	}
	if j, ok := e.Y.(*ast.JumpExpr); ok {
		return c.jumpExpr(j, o.Elem)
	}
	y := c.expr(e.Y, o.Elem)
	switch {
	case y == Invalid, assignableType(o.Elem, y):
		return o.Elem
	case Identical(y, o):
		return o
	}
	c.errorAt(e.Y, conversionHint(o.Elem, y), "the default after ?? must be %s, found %s", o.Elem, y)
	return o.Elem
}

// Returns the type whose members [m] reads: for `x?.name`, the type inside
// the optional.
func (c *checker) recv(m *ast.MemberExpr) Type {
	x := c.expr(m.X, nil)
	o, isOpt := x.(*Optional)
	switch {
	case m.Optional && isOpt:
		return o.Elem
	case m.Optional:
		if x != Invalid {
			c.errorAt(m.Name, "use . instead", "?. is only for optional values, but this is %s", x)
		}
	case isOpt:
		c.errorAt(m.Name, fmt.Sprintf("use ?.%s, or unwrap it first with if let", m.Name.Name),
			"can't read '%s' from a %s: it may be nil", m.Name.Name, x)
		return Invalid
	}
	return x
}

// Reports `T?.name`, which uses ?. on a type rather than an optional value.
func (c *checker) typeChain(m *ast.MemberExpr) bool {
	if !m.Optional || (c.enumRef(m.X) == nil && c.structRef(m.X) == nil && c.genEnumRef(m.X) == nil && c.genStructRef(m.X) == nil) {
		return false
	}
	c.errorAt(m.Name, "use . instead", "?. is only for optional values, not types")
	return true
}

// Returns the type of [e], which reads member [m] whose own type is [t]:
// for `x?.name`, [t] made optional.
func (c *checker) chain(e ast.Expr, m *ast.MemberExpr, t Type) Type {
	return c.optChain(e, m.Optional, t)
}

// Returns the type of [e], whose own type is [t], made optional when
// [optional] marks it as a step of an optional chain.
func (c *checker) optChain(e ast.Expr, optional bool, t Type) Type {
	if !optional || t == Invalid {
		return t
	}
	if _, ok := t.(*Optional); ok || t == Void {
		c.info.Chains[e] = false
		return t
	}
	c.info.Chains[e] = true
	return &Optional{Elem: t}
}

// Returns the optional of [t]. Optionals don't nest, so `T?` for an
// optional T, such as `int?`, is [t] itself.
func optionalOf(t Type) Type {
	if _, ok := t.(*Optional); ok {
		return t
	}
	return &Optional{Elem: t}
}
