package types

import "github.com/irshadio/vex-lang/compiler/ast"

// Checks an `if` used as a value against [want], returning its type: the
// type the branches share.
func (c *checker) ifValue(e *ast.IfExpr, want Type) Type {
	var result Type
	if want != Void {
		result = want
	}
	given := result != nil
	for s := e.If; ; {
		c.push()
		switch {
		case s.Bind != nil:
			elem := c.unwrapped(c.expr(s.Cond, nil), s.Cond, "if let")
			c.declare(s.Bind, elem, false, LocalVar)
		case s.Case != nil:
			t := c.expr(s.Cond, nil)
			b := newBinder(PatternVar, false)
			c.pattern(s.Case, t, b, false, false)
			c.info.PatternVars[s.Case] = b.order
			if s.Guard != nil {
				c.cond(s.Guard, "when")
			}
		default:
			c.cond(s.Cond, "if")
		}
		c.pushNarrowed(s.Cond)
		result = c.branchValue(e, s.Then, result, given)
		c.pop()
		c.pop()
		switch els := s.Else.(type) {
		case *ast.IfStmt:
			s = els
			continue
		case *ast.Block:
			c.push()
			result = c.branchValue(e, els, result, given)
			c.pop()
		default:
			c.errorAtPos(s.Pos(), 2, "add one: if ok { a } else { b }", "an if used as a value needs an else")
			return Invalid
		}
		break
	}
	if result == nil {
		if want != nil {
			return want
		}
		return Void
	}
	return result
}

// Checks [b], a branch of [e], which must hold a single expression, and
// returns the type of [e] so far, given [result]: the type the context
// asks for when [given], else the type of the branches before it, or nil.
func (c *checker) branchValue(e *ast.IfExpr, b *ast.Block, result Type, given bool) Type {
	var s *ast.ExprStmt
	if len(b.Stmts) == 1 {
		s, _ = b.Stmts[0].(*ast.ExprStmt)
	}
	if s == nil {
		c.errorAt(b, "compute the value before the if, or use an if statement", "each branch of an if used as a value must be a single expression")
		return Invalid
	}
	if IsPanic(s.X) {
		c.expr(s.X, nil)
		return result
	}
	c.info.IfValues[s] = e
	t := c.expr(s.X, result)
	switch {
	case result == nil || result == Invalid:
		return t
	case !assignableType(result, t) && given:
		c.errorAt(s.X, conversionHint(result, t), "expected %s, found %s", result, t)
	case !assignableType(result, t):
		c.errorAt(s.X, "make every branch give the same type", "the branches of an if must have the same type: the first gives %s, this one %s", result, t)
	}
	return result
}
