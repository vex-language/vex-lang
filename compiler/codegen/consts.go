package codegen

import (
	"math"
	"strconv"
	"strings"

	"github.com/irshadio/vex-lang/compiler/types"
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
	return "vx_k." + constField(k)
}

// Reports whether [k] is kept in a global rather than written inline.
func global(k *types.Const) bool { return k.Value == nil }

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
