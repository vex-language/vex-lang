package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
)

// Headers with several conditions are generated as nested C ifs, one per
// condition, so each binding is in scope for the conditions after it.

// Returns the conditions of a header: its first, then [more].
func headerConds(bind *ast.Ident, value ast.Expr, more []*ast.Cond) []*ast.Cond {
	return append([]*ast.Cond{{Bind: bind, Value: value}}, more...)
}

// Writes nested checks of [conds] around [inner], which runs when all of
// them hold. A binding declares its name when [declared] is false;
// otherwise it assigns the name declared before.
func (g *gen) condChain(conds []*ast.Cond, declared bool, inner func()) {
	if len(conds) == 0 {
		inner()
		return
	}
	c := conds[0]
	if c.Bind == nil {
		g.line("if (%s) {", cond(g.expr(c.Value)))
		g.ind++
		g.condChain(conds[1:], declared, inner)
		g.ind--
		g.line("}")
		return
	}
	tmp := g.temp()
	o := g.optOf(c.Value)
	g.line("{")
	g.ind++
	g.line("%s = %s;", decl(o, tmp), g.expr(c.Value))
	g.line("if (%s) {", present(o, tmp))
	g.ind++
	v := g.info.Defs[c.Bind]
	if declared {
		g.line("%s = %s;", g.varName(v), unwrap(o, tmp))
	} else {
		g.line("%s = %s;", decl(v.Type, g.declare(v)), unwrap(o, tmp))
	}
	g.condChain(conds[1:], declared, inner)
	g.ind--
	g.line("}")
	g.ind--
	g.line("}")
}

// Generates an `if` with several conditions. A flag records whether the
// then block ran, so the else branch is written once.
func (g *gen) ifConds(s *ast.IfStmt) {
	ok := g.temp()
	g.line("{")
	g.ind++
	g.line("bool %s = false;", ok)
	g.condChain(headerConds(s.Bind, s.Cond, s.More), false, func() {
		g.line("%s = true;", ok)
		g.stmtsIn(s.Then.Stmts, nil)
	})
	switch e := s.Else.(type) {
	case *ast.IfStmt:
		g.line("if (!%s) {", ok)
		g.ind++
		g.ifStmt(e)
		g.ind--
		g.line("}")
	case *ast.Block:
		g.block("if (!"+ok+")", e)
	}
	g.ind--
	g.line("}")
}

// Generates a `while` with several conditions: the loop ends at the first
// that fails.
func (g *gen) whileConds(s *ast.WhileStmt) {
	g.line("for (;;) {")
	g.ind++
	for _, c := range headerConds(s.Bind, s.Cond, s.More) {
		if c.Bind == nil {
			g.line("if (!(%s)) break;", g.expr(c.Value))
			continue
		}
		tmp := g.temp()
		o := g.optOf(c.Value)
		g.line("%s = %s;", decl(o, tmp), g.expr(c.Value))
		g.line("if (!%s) break;", present(o, tmp))
		v := g.info.Defs[c.Bind]
		g.line("%s = %s;", decl(v.Type, g.declare(v)), unwrap(o, tmp))
	}
	g.ind--
	g.body(s.Body)
	g.line("}")
}

// Generates `guard`. Its names are declared first, since they stay in
// scope after it, and set as each condition holds.
func (g *gen) guardStmt(s *ast.GuardStmt) {
	conds := headerConds(s.Bind, s.Value, s.More)
	for _, c := range conds {
		if c.Bind != nil {
			v := g.info.Defs[c.Bind]
			g.line("%s;", decl(v.Type, g.declare(v)))
		}
	}
	ok := g.temp()
	g.line("bool %s = false;", ok)
	g.condChain(conds, true, func() { g.line("%s = true;", ok) })
	g.block("if (!"+ok+")", s.Else)
}

// Returns C code for `x ?? return` and the like: the statement runs inside
// the expression, and a zero value of [t] stands for the value it never
// gives.
func (g *gen) jumpExpr(e *ast.JumpExpr, t types.Type) string {
	outer, ind := g.b, g.ind
	var b strings.Builder
	g.b, g.ind = &b, 0
	g.stmt(e.Stmt)
	g.b, g.ind = outer, ind
	return fmt.Sprintf("({ %s (%s){0}; })", strings.TrimSpace(b.String()), ctype(t))
}
