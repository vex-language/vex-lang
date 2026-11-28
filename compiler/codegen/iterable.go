package codegen

import (
	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
)

// Generates `for x in items` over [it], an `Iterable<T>`: it asks for an
// iterator once, then calls its next method until that gives nil.
func (g *gen) iterableLoop(s *ast.ForStmt, it *types.Interface) {
	iter := types.IteratorOf(it)
	next := iter.Method("next")
	opt := next.Result.(*types.Optional)
	elem := g.info.Defs[s.Elem]
	itv, o, k := g.temp(), g.temp(), g.temp()
	g.line("{")
	g.ind++
	g.line("void *%s = %s;", itv, g.atLine(g.lineOf(s), g.memberCall(it.Method("iterator"), it, g.expr(s.Iter))))
	g.line("for (int64_t %s = 0;; %s++) {", k, k)
	g.ind++
	g.line("%s = %s;", decl(opt, o), g.atLine(g.lineOf(s), g.memberCall(next, iter, itv)))
	g.line("if (!%s) break;", present(opt, o))
	if s.Index != nil {
		g.line("int64_t %s = %s;", g.declare(g.info.Defs[s.Index]), k)
	}
	g.line("%s = %s;", decl(elem.Type, g.declare(elem)), unwrap(opt, o))
	if s.Pattern != nil {
		g.letBind(s.Pattern, g.varName(elem))
	}
	g.ind--
	g.body(s.Body)
	g.line("}")
	g.ind--
	g.line("}")
}
