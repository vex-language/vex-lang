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
	var init, done string
	switch t := t.(type) {
	case *types.List:
		n := g.presize(body)
		init = fmt.Sprintf("vx_list *%s = %s;", r, newList(t.Elem, "0", n, "NULL"))
		if n != "0" {
			f := fill{g.temp(), g.temp(), g.temp()}
			g.fills[lit] = f
			init += fmt.Sprintf(" int64_t %s = 0, %s = %s->cap; %s *%s = %s->data;", f.len, f.cap, r, ctype(t.Elem), f.data, r)
			done = fmt.Sprintf("%s->len = %s; ", r, f.len)
		}
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
	return fmt.Sprintf("({ %s\n%s%s%s%s; })", init, b.String(), strings.Repeat("    ", ind+1), done, r)
}

// The length, capacity and data of a presized list being filled, kept in
// locals so each element is a single store. The list's own length is
// stale until the fill ends.
type fill struct{ len, cap, data string }

// Returns C code for the number of elements [body] gives when it is a
// single `for` over a range or a list that adds one element each time, or
// "0". The bounds are read again by the loop, so they must not run calls.
func (g *gen) presize(body *ast.Block) string {
	if len(body.Stmts) != 1 {
		return "0"
	}
	fs, ok := body.Stmts[0].(*ast.ForStmt)
	if !ok || len(fs.Body.Stmts) != 1 {
		return "0"
	}
	es, ok := fs.Body.Stmts[0].(*ast.ExprStmt)
	if !ok || g.info.Elements[es] == nil {
		return "0"
	}
	if _, spread := g.info.Elements[es].(*ast.SpreadExpr); spread {
		return "0"
	}
	if r, ok := fs.Iter.(*ast.RangeExpr); ok {
		if g.hasCall(r.Lo) || g.hasCall(r.Hi) {
			return "0"
		}
		return fmt.Sprintf("vx_range_len(%s, %s, %t)", g.expr(r.Lo), g.expr(r.Hi), r.Inclusive)
	}
	if _, ok := g.info.Types[fs.Iter].(*types.List); ok && !g.hasCall(fs.Iter) {
		return "(" + g.expr(fs.Iter) + ")->len"
	}
	return "0"
}

// Returns C code creating a list of [t] with [n] elements from [items] and
// room for [capacity]. A list whose elements hold no pointers isn't
// scanned by the collector.
func newList(t types.Type, n, capacity, items string) string {
	if capacity == "0" {
		f := "vx_list_new"
		if flat(t) {
			f = "vx_list_flat"
		}
		return fmt.Sprintf("%s(sizeof(%s), %s, %s)", f, ctype(t), n, items)
	}
	return fmt.Sprintf("vx_list_make(sizeof(%s), %s, %s, %s, %t)", ctype(t), n, capacity, items, flat(t))
}

// Reports whether values of [t] never hold a pointer the collector must
// follow.
func flat(t types.Type) bool {
	switch t := t.(type) {
	case *types.Optional:
		return flat(t.Elem)
	case *types.Record:
		for _, f := range t.FieldTypes() {
			if !flat(f) {
				return false
			}
		}
		return true
	}
	return t == types.Int || t == types.Float || t == types.Bool
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
				g.line("    VX_PUSH(%s, %s, %s);", ctype(o), r, v)
				g.line("}")
			})
			return
		}
		f, ok := g.fills[lit]
		if !ok {
			g.line("VX_PUSH(%s, %s, %s);", ctype(t.Elem), r, g.expr(x))
			return
		}
		v := g.temp()
		g.line("%s = %s;", decl(t.Elem, v), g.expr(x))
		g.line("if (__builtin_expect(%s < %s, 1)) %s[%s++] = %s;", f.len, f.cap, f.data, f.len, v)
		g.line("else { %s->len = %s; vx_list_push(%s, &%s); %s = %s->len; %s = %s->cap; %s = %s->data; }",
			r, f.len, r, v, f.len, r, f.cap, r, f.data, r)
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
