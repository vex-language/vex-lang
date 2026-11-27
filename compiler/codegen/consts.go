package codegen

import (
	"math"
	"strconv"
	"strings"

	"github.com/vex-language/vex-lang/compiler/types"
)

// Returns the C field holding [k] inside `vx_k`.
func constField(k *types.Const) string {
	switch {
	case k.Owner != nil:
		return "k_" + structIdent(k.Owner) + "_" + cid(k.Name)
	case k.Enum != nil:
		return "k_" + enumIdent(k.Enum) + "_" + cid(k.Name)
	}
	return "k_" + modTag(k.Module) + cid(k.Name)
}

// Returns C code reading [k]: its value when the compiler worked it out,
// otherwise the global holding it.
func (g *gen) constRef(k *types.Const) string {
	switch v := k.Value.(type) {
	case int64:
		if v == -1<<63 {
			return "INT64_MIN"
		}
		return "INT64_C(" + strconv.FormatInt(v, 10) + ")"
	case float64:
		switch {
		case math.IsNaN(v):
			return "((double)NAN)"
		case math.IsInf(v, 1):
			return "((double)INFINITY)"
		case math.IsInf(v, -1):
			return "(-(double)INFINITY)"
		}
		s := strconv.FormatFloat(v, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eEn") {
			s += ".0"
		}
		return s
	case bool:
		return strconv.FormatBool(v)
	case string:
		return "VX_LIT(" + cQuote(v) + ")"
	}
	if k.Lazy {
		return "(*v" + constField(k) + "())"
	}
	return "vx_k." + constField(k)
}

// Reports whether [k] is kept in a global rather than written inline.
func global(k *types.Const) bool { return k.Value == nil && !k.Lazy }

// Returns the variables declared outside functions, in the order checked.
func lazyVars(info *types.Info) []*types.Const {
	var out []*types.Const
	for _, k := range info.Consts {
		if k.Lazy {
			out = append(out, k)
		}
	}
	return out
}

// Writes the storage of the variables declared outside functions and the
// functions reaching them. Each isolate has its own copy, built on first
// use: the state of each is 0 before, 1 while its value is worked out and
// 2 after.
func (g *gen) varsStruct(info *types.Info) {
	vars := lazyVars(info)
	if len(vars) == 0 {
		return
	}
	g.line("static VX_TLS struct {")
	for _, k := range vars {
		g.line("    %s;", decl(k.Type, constField(k)))
	}
	g.line("} vx_v;")
	g.line("static VX_TLS uint8_t vx_vs[%d];", len(vars))
	for i, k := range vars {
		f := constField(k)
		g.line("static void vi%s(void);", f)
		g.line("static inline %s *v%s(void) { if (__builtin_expect(vx_vs[%d] != 2, 0)) vi%s(); return &vx_v.%s; }", ctype(k.Type), f, i, f, f)
	}
	g.line("")
}

// Returns the functions that build the variables declared outside
// functions, or "" when there are none.
func (g *gen) varsInit(info *types.Info) string {
	vars := lazyVars(info)
	if len(vars) == 0 {
		return ""
	}
	var b strings.Builder
	for i, k := range vars {
		g.enter(&b)
		g.file = info.FileOf[k.Decl]
		g.rng, g.result, g.throws, g.tries = nil, nil, nil, nil
		f := constField(k)
		g.line("static void vi%s(void) {", f)
		g.ind++
		g.line("if (vx_vs[%d] == 1) vx_fail(%d, \"'%s' is used before its starting value is ready\");", i, g.lineOf(k.Decl.Name), k.String())
		g.line("vx_vars_lo = (const char *)&vx_v;")
		g.line("vx_vars_hi = (const char *)(&vx_v + 1);")
		g.line("vx_vs[%d] = 1;", i)
		g.line("%s = %s;", decl(k.Type, "v"), g.expr(k.Decl.Value))
		g.line("vx_v.%s = v;", f)
		g.line("vx_vs[%d] = 2;", i)
		g.ind--
		g.line("}")
		g.line("")
	}
	return b.String()
}

// Writes the struct of globals holding the constants built at startup.
func (g *gen) constsStruct(info *types.Info) {
	var fields []string
	for _, k := range info.Consts {
		if global(k) {
			fields = append(fields, "    "+decl(k.Type, constField(k))+";")
		}
	}
	if len(fields) == 0 {
		return
	}
	g.line("static struct {")
	for _, f := range fields {
		g.line("%s", f)
	}
	g.line("} vx_k;")
	g.line("")
}

// Returns the function that builds the constants kept in globals, each
// after the ones it uses, or "" when there are none. Their lists and maps
// are frozen, and the globals stay reachable for the collector.
func (g *gen) constsInit(info *types.Info) string {
	any := false
	for _, k := range info.Consts {
		any = any || global(k)
	}
	if !any {
		return ""
	}
	var b strings.Builder
	g.enter(&b)
	g.line("static void vx_consts_init(void) {")
	g.ind++
	g.line("vx_roots_lo = (const char *)&vx_k;")
	g.line("vx_roots_hi = (const char *)(&vx_k + 1);")
	g.constMode = true
	for _, k := range info.Consts {
		if global(k) {
			g.file = info.FileOf[k.Decl]
			g.line("vx_k.%s = %s;", constField(k), g.expr(k.Decl.Value))
		}
	}
	g.constMode = false
	g.ind--
	g.line("}")
	g.line("")
	return b.String()
}
