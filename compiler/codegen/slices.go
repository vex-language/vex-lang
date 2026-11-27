package codegen

import (
	"fmt"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
)

// Generates `xs[a..b]` and `s[a..b]`, which copy the elements or
// characters at the positions of the range. A left-out start is 0, and a
// left-out end is the length.
func (g *gen) slice(e *ast.IndexExpr) string {
	f, length := "vx_list_slice_range", "%s->len"
	if g.info.Types[e.X] == types.String {
		f, length = "vx_str_slice_range", "vx_str_len(%s)"
	}
	line := g.lineOf(e)
	r, ok := e.Index.(*ast.RangeExpr)
	if !ok || r.Lo != nil && r.Hi != nil {
		return g.ordered([]ast.Expr{e.X, e.Index}, func(cs []string) string {
			return fmt.Sprintf("%s(%s, %s, %d)", f, cs[0], cs[1], line)
		})
	}
	x := g.temp()
	return g.ordered([]ast.Expr{e.X, r.Lo, r.Hi}, func(cs []string) string {
		lo, hi, incl := cs[1], cs[2], r.Inclusive
		if r.Lo == nil {
			lo = "0"
		}
		if r.Hi == nil {
			hi, incl = fmt.Sprintf(length, x), false
		}
		return fmt.Sprintf("({ %s = %s; %s(%s, vx_range_new(%s, %s, %t), %d); })", decl(g.info.Types[e.X], x), cs[0], f, x, lo, hi, incl, line)
	})
}
