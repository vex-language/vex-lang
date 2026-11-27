package codegen

import (
	"fmt"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
)

// Returns a C condition testing whether the interface value [x], read
// more than once, is a [target]: a struct or enum compares its
// descriptor, and an interface looks for its method table.
func typeCheck(x string, target types.Type) string {
	desc := fmt.Sprintf("(*(const vx_desc *const *)(%s))", x)
	switch t := target.(type) {
	case *types.Struct:
		return fmt.Sprintf("(%s == &desc_%s)", desc, structIdent(t))
	case *types.Enum:
		return fmt.Sprintf("(%s == &desc_%s)", desc, enumName(t))
	case *types.Interface:
		return fmt.Sprintf("(%s->itabs[%d] != NULL)", desc, t.Index)
	}
	panic("type test for " + target.String())
}

// Returns C code for `x is T` or `x as? T`.
func (g *gen) typeTest(e *ast.TypeTest) string {
	target := g.info.TypeTests[e]
	v := g.temp()
	if !e.Cast {
		return fmt.Sprintf("({ void *%s = %s; %s; })", v, g.expr(e.X), typeCheck(v, target))
	}
	o := g.optOf(e)
	return fmt.Sprintf("({ void *%s = %s; %s ? %s : (%s){0}; })",
		v, g.expr(e.X), typeCheck(v, target), wrapOpt(o, fmt.Sprintf("((%s)%s)", ctype(target), v)), ctype(o))
}
