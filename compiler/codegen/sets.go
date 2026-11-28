package codegen

import (
	"fmt"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
)

// Sets are maps whose entries have keys and no values.

// Returns C code for a new, empty set of type [s].
func (g *gen) newSet(s *types.Set) string {
	return g.newTable(s.Elem, "0, 0")
}

// Generates a list literal that makes a set: the list is built as usual,
// and its items are added in order, so repeats keep their first place.
func (g *gen) setLit(e *ast.ListLit, s *types.Set) string {
	g.info.Types[e] = &types.List{Elem: s.Elem}
	items := g.value(e)
	g.info.Types[e] = s
	r := fmt.Sprintf("vx_set_add_list(%s, %s)", g.newSet(s), items)
	if g.constMode {
		return "vx_map_freeze(" + r + ")"
	}
	return r
}

// Returns C code for [code], the operand [x] of a set operation on sets of
// type [s], as a set: a list becomes a new set.
func (g *gen) setOperand(x ast.Expr, code string, s *types.Set) string {
	if _, ok := g.info.Types[x].(*types.List); ok {
		return fmt.Sprintf("vx_set_add_list(%s, %s)", g.newSet(s), code)
	}
	return code
}

// Generates `Set<T>()` or `Set(items)`, which always makes a new set.
func (g *gen) newSetCall(e *ast.CallExpr, c *types.Call) string {
	s := g.info.Types[e].(*types.Set)
	if len(c.Args) == 0 {
		return g.newSet(s)
	}
	x := c.Args[0]
	if _, ok := g.info.Types[x].(*types.List); ok {
		return g.setOperand(x, g.expr(x), s)
	}
	return "vx_map_copy(" + g.expr(x) + ")"
}

// Generates a call of the set member [c] on [s], with the receiver and
// arguments already in [cs].
func (g *gen) setCall(e *ast.CallExpr, c *types.Call, s *types.Set, cs []string, line int) string {
	switch c.Builtin {
	case "contains", "insert", "remove":
		k := g.temp()
		var call string
		switch c.Builtin {
		case "contains":
			call = fmt.Sprintf("vx_map_find(%s, &%s) != NULL", cs[0], k)
		case "insert":
			call = fmt.Sprintf("vx_set_insert(vx_set_mut(%s, %d), &%s)", cs[0], line, k)
		default:
			call = fmt.Sprintf("vx_set_remove(vx_set_mut(%s, %d), &%s)", cs[0], line, k)
		}
		return fmt.Sprintf("({ %s = %s; %s; })", decl(s.Elem, k), cs[1], call)
	case "union":
		return fmt.Sprintf("vx_set_union(%s, %s)", cs[0], g.setOperand(c.Args[0], cs[1], s))
	case "intersection", "subtracting":
		return fmt.Sprintf("vx_set_filter(%s, %s, %t)", cs[0], g.setOperand(c.Args[0], cs[1], s), c.Builtin == "intersection")
	case "isSubset":
		return fmt.Sprintf("vx_set_subset(%s, %s)", cs[0], g.setOperand(c.Args[0], cs[1], s))
	case "toList":
		return "vx_map_list(" + cs[0] + ", false)"
	case "copy":
		return "vx_map_copy(" + cs[0] + ")"
	}
	panic("codegen: unknown set member " + c.Builtin)
}

// Generates `for x in set`, which visits the items that were there when
// the loop started, in insertion order.
func (g *gen) setLoop(s *ast.ForStmt, set *types.Set) {
	sv, it, e := g.temp(), g.temp(), g.temp()
	g.line("{")
	g.ind++
	g.line("vx_map *%s = %s;", sv, g.expr(s.Iter))
	g.line("vx_map_iter %s = vx_map_iter_new(%s);", it, sv)
	g.line("for (char *%s; (%s = vx_map_next(%s, &%s));) {", e, e, sv, it)
	g.ind++
	elem := g.info.Defs[s.Elem]
	g.line("%s = *(%s*)(%s + VX_MAP_KEY);", decl(set.Elem, g.declare(elem)), ctype(set.Elem), e)
	if s.Pattern != nil {
		g.letBind(s.Pattern, g.varName(elem))
	}
	g.ind--
	g.body(s.Body)
	g.line("}")
	g.ind--
	g.line("}")
}

// Writes the body of the function showing a set, which looks like a list
// of its items in insertion order.
func (g *gen) setRepr(t *types.Set) {
	g.line("    vx_buf_add(b, VX_LIT(\"[\"));")
	g.line("    vx_map_iter it = vx_map_iter_new(v);")
	g.line("    bool first = true;")
	g.line("    for (char *e; (e = vx_map_next(v, &it)); first = false) {")
	g.line("        if (!first) vx_buf_add(b, VX_LIT(\", \"));")
	g.line("        %s", write(t.Elem, "b", fmt.Sprintf("*(%s*)(e + VX_MAP_KEY)", ctype(t.Elem))))
	g.line("    }")
	g.line("    vx_buf_add(b, VX_LIT(\"]\"));")
}
