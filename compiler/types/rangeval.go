package types

import "github.com/vex-language/vex-lang/compiler/ast"

// Checks the bounds of the range [r], which must be ints. Either may be
// left out only when [open], inside `[ ]`.
func (c *checker) rangeBounds(r *ast.RangeExpr, open bool) {
	for _, bound := range []ast.Expr{r.Lo, r.Hi} {
		if bound == nil {
			if !open {
				c.errorAt(r, "write both ends, e.g. 0..10; only xs[a..] and xs[..b] leave one out", "a range needs both ends")
			}
			continue
		}
		if t := c.expr(bound, Int); !Identical(t, Int) {
			c.errorAt(bound, conversionHint(Int, t), "range bounds must be int, found %s", t)
		}
	}
}
