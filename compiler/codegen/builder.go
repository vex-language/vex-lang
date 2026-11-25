package codegen

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/types"
)

// Generates a list or map literal whose elements are the statements of
// [body], or a builder block, as a statement expression that fills a new
// collection of type [t].
func (g *gen) collection(lit ast.Expr, body *ast.Block, t types.Type) string {
	r := g.temp()
	g.builds[lit] = r
	var init string
	switch t := t.(type) {
	case *types.List:
		init = fmt.Sprintf("vx_list *%s = vx_list_new(sizeof(%s), 0, NULL);", r, ctype(t.Elem))
	case *types.Map:
		init = fmt.Sprintf("vx_map *%s = %s;", r, newMap(t))
	}
	outer, ind := g.b, g.ind
	var b strings.Builder
	g.b, g.ind = &b, ind+1
	for _, s := range body.Stmts {
		g.stmt(s)
	}
	g.b, g.ind = outer, ind
	return fmt.Sprintf("({ %s\n%s%s%s; })", init, b.String(), strings.Repeat("    ", ind+1), r)
}

// Writes the code that adds the element [x] to the collection built for
// [lit].
func (g *gen) element(x ast.Expr, lit ast.Expr) {
	r := g.builds[lit]
	switch t := g.info.Types[lit].(type) {
	case *types.List:
		if s, ok := x.(*ast.SpreadExpr); ok {
			g.spread(s, func(src string, st types.Type) {
				from := st.(*types.List).Elem
				o, wrap := t.Elem.(*types.Optional)
				if _, opt := from.(*types.Optional); !wrap || opt {
					g.line("vx_list_append(%s, %s);", r, src)
					return
				}
				l, i, v := g.temp(), g.temp(), g.temp()
				g.line("vx_list *%s = %s;", l, src)
				g.line("for (int64_t %s = 0; %s < %s->len; %s++) {", i, i, l, i)
				g.line("    %s = {true, ((%s*)%s->data)[%s]};", decl(o, v), ctype(from), l, i)
				g.line("    vx_list_push(%s, &%s);", r, v)
				g.line("}")
			})
			return
		}
		v := g.temp()
		g.line("{ %s = %s; vx_list_push(%s, &%s); }", decl(t.Elem, v), g.expr(x), r, v)
	case *types.Map:
		if s, ok := x.(*ast.SpreadExpr); ok {
			g.spread(s, func(src string, _ types.Type) { g.line("vx_map_merge(%s, %s);", r, src) })
			return
		}
		e := x.(*ast.MapEntry)
		k, v := g.temp(), g.temp()
		g.line("{ %s = %s; %s = %s; *(%s*)vx_map_set(%s, &%s) = %s; }",
			decl(t.Key, k), g.expr(e.Key), decl(t.Value, v), g.expr(e.Value), ctype(t.Value), r, k, v)
	}
}

// Writes `...xs` or `...?xs`, calling [add] with the C code of the
// collection to add and its type.
func (g *gen) spread(s *ast.SpreadExpr, add func(src string, t types.Type)) {
	t := g.info.Types[s]
	g.line("{")
	g.ind++
	if o, ok := t.(*types.Optional); ok {
		v := g.temp()
		g.line("%s = %s;", decl(o, v), g.expr(s.X))
		g.line("if (%s.has) {", v)
		g.ind++
		add(v+".v", o.Elem)
		g.ind--
		g.line("}")
	} else {
		add(g.expr(s.X), t)
	}
	g.ind--
	g.line("}")
}
