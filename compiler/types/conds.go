package types

import (
	"github.com/vex-language/vex-lang/compiler/ast"
)

// Checks the conditions after the first in the header of [keyword], in
// order: each binding is visible to the conditions after it. Returns the
// variables bound and how many scopes were pushed, for the caller to pop.
func (c *checker) conds(list []*ast.Cond, keyword string) (vars []*Var, pushed int) {
	for _, cd := range list {
		if cd.Bind != nil {
			elem := c.unwrapped(c.expr(cd.Value, nil), cd.Value, keyword+" let")
			vars = append(vars, c.declare(cd.Bind, elem, false, LocalVar))
			continue
		}
		c.cond(cd.Value, keyword)
		c.pushNarrowed(cd.Value)
		pushed++
	}
	return vars, pushed
}

// Pops [n] scopes.
func (c *checker) popN(n int) {
	for range n {
		c.pop()
	}
}

// Checks `guard`: its conditions, then the else block, which must leave,
// and makes the names it binds visible after it.
func (c *checker) guardStmt(s *ast.GuardStmt) {
	c.push()
	var vars []*Var
	if s.Bind != nil {
		elem := c.unwrapped(c.expr(s.Value, nil), s.Value, "guard let")
		vars = append(vars, c.declare(s.Bind, elem, false, LocalVar))
	} else {
		c.cond(s.Value, "guard")
	}
	more, pushed := c.conds(s.More, "guard")
	vars = append(vars, more...)
	c.popN(pushed)
	c.pop()
	c.stmt(s.Else)
	if !leaves(s.Else) {
		what := "the condition holds"
		if len(vars) == 1 {
			what = "'" + vars[0].Name + "' always has a value"
		} else if len(vars) > 1 {
			what = "its names always have values"
		}
		c.errorAtPos(s.Else.Pos(), 1, "end it with return, break, continue or panic(...)", "the else block of guard must leave, so %s after it", what)
	}
	for _, v := range vars {
		if prev, ok := c.scope.vars[v.Name]; ok && prev.Decl != nil {
			c.errorAt(v.Decl, "", "'%s' is already declared on line %d", v.Name, prev.Decl.Pos().Line)
			continue
		}
		c.scope.vars[v.Name] = v
	}
}

// Checks `x ?? return`, `?? throw ...`, `?? break` or `?? continue`, the
// right side of `??` that leaves when the left side is nil. [elem] is the
// type of the value when it isn't.
func (c *checker) jumpExpr(e *ast.JumpExpr, elem Type) Type {
	c.info.Types[e] = elem
	if c.fn == nil && len(c.lits) == 0 {
		c.errorAt(e, "use a default value instead: x ?? 0", "%s after ?? only works inside a function", jumpName(e.Stmt))
		return elem
	}
	c.stmt(e.Stmt)
	return elem
}

// Returns the keyword that starts the statement [s].
func jumpName(s ast.Stmt) string {
	switch s.(type) {
	case *ast.ReturnStmt:
		return "return"
	case *ast.ThrowStmt:
		return "throw"
	case *ast.BreakStmt:
		return "break"
	}
	return "continue"
}
