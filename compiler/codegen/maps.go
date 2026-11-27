package codegen

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/types"
)

// Returns the runtime constant for how keys of type [t] are hashed, or ""
// for keys that need generated functions.
func keyKind(t types.Type) string {
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
