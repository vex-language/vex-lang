package codegen

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/types"
)

// Returns C code for an `if` used as a value: the if statement inside a
// statement expression, each branch storing its value in a result.
func (g *gen) ifValue(e *ast.IfExpr) string {
	t := g.info.Types[e]
	r, init := "", ""
	if t != types.Void {
		r = g.temp()
		g.ifResults[e] = r
		init = decl(t, r) + ";"
	}
	outer, ind := g.b, g.ind
	var b strings.Builder
	g.b, g.ind = &b, ind+1
	g.ifStmt(e.If)
	g.b, g.ind = outer, ind
	end := strings.Repeat("    ", ind+1)
	if r != "" {
		end += r + "; "
	}
	return fmt.Sprintf("({ %s\n%s%s})", init, b.String(), end)
}
