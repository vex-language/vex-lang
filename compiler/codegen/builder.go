package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
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
			f := fill{g.temp(), g.temp()}
			g.fills[lit] = f
			c := ctype(t.Elem)
			init += fmt.Sprintf(" int64_t %s = 0; %s *%s = (%s *)%s->data;", f.len, c, f.data, c, r)
			done = fmt.Sprintf("%s->len = %s; ", r, f.len)
		}
	case *types.Map:
		init = fmt.Sprintf("vx_map *%s = %s;", r, g.newMap(t))
	}
	outer, ind := g.b, g.ind
	var b strings.Builder
	g.b, g.ind = &b, ind+1
	g.stmtsIn(body.Stmts, nil)
	g.b, g.ind = outer, ind
	return fmt.Sprintf("({ %s\n%s%s%s%s; })", init, b.String(), strings.Repeat("    ", ind+1), done, r)
}

// The length and data of a presized list being filled, kept in locals so
// each element is a single store. The list's own length is stale until the
// fill ends. The list is presized for every pass of its one loop, which adds
// one element each pass, so the elements always fit.
type fill struct{ len, data string }

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
	switch g.info.Elements[es].(type) {
	case *ast.SpreadExpr, *ast.OptionalElem:
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
		if flat(t) {
			return fmt.Sprintf("vx_list_flat(sizeof(%s), %s, %s)", ctype(t), n, items)
		}
		return fmt.Sprintf("vx_list_new(sizeof(%s), %s, %s, %s)", ctype(t), layoutOf(t), n, items)
	}
	mask := "0"
	if !flat(t) {
		mask = layoutOf(t)
	}
	return fmt.Sprintf("vx_list_make(sizeof(%s), %s, %s, %s, %s)", ctype(t), n, capacity, items, mask)
}

// Returns C code adding [v] to the end of list [l] of [t]. A value with
// pointers goes through the write barrier.
func push(t types.Type, l, v string) string {
	if flat(t) {
		return fmt.Sprintf("VX_PUSH(%s, %s, %s)", ctype(t), l, v)
	}
	return fmt.Sprintf("VX_PUSH_REF(%s, %s, %s)", ctype(t), l, v)
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
				g.line("    %s = %s;", decl(o, v), wrapOpt(o, fmt.Sprintf("((%s*)%s->data)[%s]", ctype(from), l, i)))
				g.line("    %s;", push(o, r, v))
				g.line("}")
			})
			return
		}
		if _, ok := x.(*ast.OptionalElem); ok {
			v := g.temp()
			g.line("{")
			g.ind++
			n := g.elemValue(x, t.Elem, v)
			g.line("%s;", push(t.Elem, r, v))
			g.closeBlocks(n)
			g.ind--
			g.line("}")
			return
		}
		f, ok := g.fills[lit]
		if !ok {
			g.line("%s;", push(t.Elem, r, g.expr(x)))
			return
		}
		v := g.temp()
		g.line("%s = %s;", decl(t.Elem, v), g.expr(x))
		barrier := ""
		if !flat(t.Elem) {
			barrier = fmt.Sprintf(" vx_remember(%s);", f.data)
		}
		g.line("%s[%s++] = %s;%s", f.data, f.len, v, barrier)
	case *types.Map:
		if s, ok := x.(*ast.SpreadExpr); ok {
			g.spread(s, func(src string, _ types.Type) { g.line("vx_map_merge(%s, %s);", r, src) })
			return
		}
		e := x.(*ast.MapEntry)
		k, v := g.temp(), g.temp()
		if plainElem(e.Key) && plainElem(e.Value) {
			g.line("{ %s = %s; %s = %s; *(%s*)vx_map_set(%s, &%s) = %s; }",
				decl(t.Key, k), g.expr(e.Key), decl(t.Value, v), g.expr(e.Value), ctype(t.Value), r, k, v)
			return
		}
		g.line("{")
		g.ind++
		n := g.elemValue(e.Key, t.Key, k)
		n += g.elemValue(e.Value, t.Value, v)
		g.line("*(%s*)vx_map_set(%s, &%s) = %s;", ctype(t.Value), r, k, v)
		g.closeBlocks(n)
		g.ind--
		g.line("}")
	}
}

// Reports whether [x] isn't a `?v` element.
func plainElem(x ast.Expr) bool {
	_, opt := x.(*ast.OptionalElem)
	return !opt
}

// Writes the declaration of [name] of type [t] holding the element value
// [x]. For `?v`, it opens a block that runs only when v isn't nil, and
// returns the number of blocks opened.
func (g *gen) elemValue(x ast.Expr, t types.Type, name string) int {
	e, ok := x.(*ast.OptionalElem)
	if !ok {
		g.line("%s = %s;", decl(t, name), g.expr(x))
		return 0
	}
	o, tmp := g.optOf(e.X), g.temp()
	g.line("%s = %s;", decl(o, tmp), g.expr(e.X))
	g.line("if (%s) {", present(o, tmp))
	g.ind++
	v := unwrap(o, tmp)
	if want, ok := t.(*types.Optional); ok {
		v = wrapOpt(want, v)
	}
	g.line("%s = %s;", decl(t, name), v)
	return 1
}

// Closes [n] blocks opened by [gen.elemValue].
func (g *gen) closeBlocks(n int) {
	for range n {
		g.ind--
		g.line("}")
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
		g.line("if (%s) {", present(o, v))
		g.ind++
		add(unwrap(o, v), o.Elem)
		g.ind--
		g.line("}")
	} else {
		add(g.expr(s.X), t)
	}
	g.ind--
	g.line("}")
}
