package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
)

// Generates `x.copy(...)`: the constructor called with the given
// arguments and x's other fields, so init runs on the copy too.
func (g *gen) copyCall(c *types.Call, rank []int) string {
	var given []ast.Expr
	var givenRank []int
	for i, a := range c.Args {
		if !c.Keep[i] {
			given = append(given, a)
			givenRank = append(givenRank, rank[i])
		}
	}
	if wrapped(c.Struct) != nil {
		if c.Keep[0] {
			return g.expr(c.Recv)
		}
		return g.orderedBy([]ast.Expr{c.Recv, given[0]}, []int{-1, givenRank[0]}, func(cs []string) string { return cs[1] })
	}
	return g.orderedBy(append([]ast.Expr{c.Recv}, given...), append([]int{-1}, givenRank...), func(cs []string) string {
		src := g.temp()
		var args []string
		next := 1
		i := 0
		for _, f := range c.Struct.Fields {
			if f.ByInit {
				continue
			}
			if c.Keep[i] {
				args = append(args, fmt.Sprintf("%s->f_%s", src, f.Name))
			} else {
				args = append(args, cs[next])
				next++
			}
			i++
		}
		name := structName(c.Struct)
		return fmt.Sprintf("({ %s *%s = %s; new_%s(%s); })", name, src, cs[0], name, strings.Join(args, ", "))
	})
}
