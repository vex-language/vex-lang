package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
)

// Returns the runtime constant for how keys of type [t] are hashed, or ""
// for keys that need generated functions.
func keyKind(t types.Type) string {
	if s, ok := t.(*types.Struct); ok && s.Underlying() != nil && s.Operator("==") == nil && s.HashMethod() == nil {
		return keyKind(s.Underlying())
	}
	switch t {
	case types.Int:
		return "VX_KEY_INT"
	case types.Bool:
		return "VX_KEY_BOOL"
	case types.String:
		return "VX_KEY_STR"
	}
	if e, ok := t.(*types.Enum); ok && e.Simple() {
		return "VX_KEY_ENUM"
	}
	return ""
}

// Generates a map literal. Keys and values run in source order, and a later
// value for the same key replaces the earlier one.
func (g *gen) mapLit(e *ast.MapLit, m *types.Map) string {
	if len(e.Keys) == 0 {
		return g.newMap(m)
	}
	r := g.temp()
	var b strings.Builder
	fmt.Fprintf(&b, "({ vx_map *%s = %s; ", r, g.newMap(m))
	for i, k := range e.Keys {
		kt, vt := g.temp(), g.temp()
		fmt.Fprintf(&b, "%s = %s; %s = %s; *(%s*)vx_map_set(%s, &%s) = %s; ",
			decl(m.Key, kt), g.expr(k), decl(m.Value, vt), g.expr(e.Values[i]), ctype(m.Value), r, kt, vt)
	}
	b.WriteString(r + "; })")
	return b.String()
}

// Generates `for k in m` or `for k, v in m`, which visit the entries that
// existed when the loop started, in insertion order.
func (g *gen) mapLoop(s *ast.ForStmt, m *types.Map) {
	mv, it, e := g.temp(), g.temp(), g.temp()
	g.line("{")
	g.ind++
	g.line("vx_map *%s = %s;", mv, g.expr(s.Iter))
	g.line("vx_map_iter %s = vx_map_iter_new(%s);", it, mv)
	g.line("for (char *%s; (%s = vx_map_next(%s, &%s));) {", e, e, mv, it)
	g.ind++
	key := g.info.Defs[s.Elem]
	if s.Index != nil {
		key = g.info.Defs[s.Index]
		value := g.info.Defs[s.Elem]
		g.line("%s = *(%s*)(%s + VX_MAP_KEY);", decl(m.Key, g.declare(key)), ctype(m.Key), e)
		g.line("%s = *(%s*)(%s + %s->voff);", decl(m.Value, g.declare(value)), ctype(m.Value), e, mv)
	} else {
		g.line("%s = *(%s*)(%s + VX_MAP_KEY);", decl(m.Key, g.declare(key)), ctype(m.Key), e)
	}
	g.ind--
	g.body(s.Body)
	g.line("}")
	g.ind--
	g.line("}")
}

// Returns C code for `m.entries`, a list of `(key:, value:)` records of
// the entries of [x], a map of [m], in order.
func (g *gen) mapEntries(m *types.Map, x string) string {
	mv, out, i, e := g.temp(), g.temp(), g.temp(), g.temp()
	r := &types.Record{Named: []types.RecordField{{Name: "key", Type: m.Key}, {Name: "value", Type: m.Value}}}
	entry := fmt.Sprintf("((%s){.n_key = *(%s*)(%s + VX_MAP_KEY), .n_value = *(%s*)(%s + %s->voff)})",
		ctype(r), ctype(m.Key), e, ctype(m.Value), e, mv)
	return fmt.Sprintf("({ vx_map *%s = %s; vx_list *%s = %s; for (int64_t %s = 0; %s < %s->used; %s++) { "+
		"char *%s = VX_MAP_ENTRY(%s, %s); if (VX_MAP_SEQ(%s) != 0) %s; } %s; })",
		mv, x, out, newList(r, "0", mv+"->len", "NULL"), i, i, mv, i, e, mv, i, e, push(r, out, entry), out)
}

// Returns C code for the built-in map member [c] of [e] on a map of [m],
// with [cs] the receiver and arguments, or "" if [c] isn't one of these.
// Members that call a function for each entry visit the entries there
// were at the start, as a for loop does.
func (g *gen) mapMember(e *ast.CallExpr, c *types.Call, m *types.Map, cs []string, line int) string {
	mv, f, k, v := g.temp(), g.temp(), g.temp(), g.temp()
	call := func(arg ast.Expr, args ...string) string {
		s := fmt.Sprintf("((%s)%s.fn)(%s.env", fnPtrType(g.info.Types[arg].(*types.FuncType)), f, f)
		for _, a := range args {
			s += ", " + a
		}
		return s + ")"
	}
	// A loop over the entries of mv that reads each into k and v.
	loop := func() string {
		it, en := g.temp(), g.temp()
		return fmt.Sprintf("vx_map_iter %s = vx_map_iter_new(%s); for (char *%s; (%s = vx_map_next(%s, &%s));) { "+
			"%s = *(%s*)(%s + VX_MAP_KEY); %s = *(%s*)(%s + %s->voff); ",
			it, mv, en, en, mv, it, decl(m.Key, k), ctype(m.Key), en, decl(m.Value, v), ctype(m.Value), en, mv)
	}
	switch c.Builtin {
	case "getOrPut":
		r, p := g.temp(), g.temp()
		return fmt.Sprintf("({ vx_map *%s = vx_map_mut(%s, %d); %s = %s; vx_fn %s = %s; %s; %s = vx_map_find(%s, &%s); "+
			"if (%s) %s = *%s; else { %s = %s; *(%s*)vx_map_set(%s, &%s) = %s; } %s; })",
			mv, cs[0], line, decl(m.Key, k), cs[1], f, cs[2], decl(m.Value, r), declPtr(m.Value, p), mv, k,
			p, r, p, r, call(c.Args[1]), ctype(m.Value), mv, k, r, r)
	case "update":
		r, p, o := g.temp(), g.temp(), g.optOf(e)
		return fmt.Sprintf("({ vx_map *%s = vx_map_mut(%s, %d); %s = %s; vx_fn %s = %s; %s %s = {0}; %s = vx_map_find(%s, &%s); "+
			"if (%s) { %s = %s; *(%s*)vx_map_set(%s, &%s) = %s; %s = %s; } %s; })",
			mv, cs[0], line, decl(m.Key, k), cs[1], f, cs[2], ctype(o), r, declPtr(m.Value, p), mv, k,
			p, decl(m.Value, v), call(c.Args[1], "*"+p), ctype(m.Value), mv, k, v, r, wrapOpt(o, v), r)
	case "filter":
		out := g.temp()
		return fmt.Sprintf("({ vx_map *%s = %s; vx_fn %s = %s; vx_map *%s = %s; %sif (%s) *(%s*)vx_map_set(%s, &%s) = %s; } %s; })",
			mv, cs[0], f, cs[1], out, g.newMap(m), loop(), call(c.Args[0], k, v), ctype(m.Value), out, k, v, out)
	case "mapValues":
		out, u := g.temp(), g.temp()
		res := g.info.Types[e].(*types.Map)
		return fmt.Sprintf("({ vx_map *%s = %s; vx_fn %s = %s; vx_map *%s = %s; %s%s = %s; *(%s*)vx_map_set(%s, &%s) = %s; } %s; })",
			mv, cs[0], f, cs[1], out, g.newMap(res), loop(), decl(res.Value, u), call(c.Args[0], v), ctype(res.Value), out, k, u, out)
	}
	return ""
}
