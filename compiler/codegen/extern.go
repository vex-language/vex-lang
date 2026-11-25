package codegen

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/types"
)

// C math functions on doubles that the C compiler knows: calls to them
// compile to single instructions where the CPU has one.
var mathBuiltins = map[string]int{
	"sqrt": 1, "cbrt": 1, "sin": 1, "cos": 1, "tan": 1, "asin": 1, "acos": 1, "atan": 1,
	"sinh": 1, "cosh": 1, "tanh": 1, "exp": 1, "exp2": 1, "log": 1, "log2": 1, "log10": 1,
	"floor": 1, "ceil": 1, "round": 1, "trunc": 1, "fabs": 1,
	"pow": 2, "atan2": 2, "hypot": 2, "fmod": 2, "fmin": 2, "fmax": 2,
}

// Returns the C type an extern function uses for [t].
func externType(t types.Type) string {
	switch t {
	case types.Int:
		return "int64_t"
	case types.Float:
		return "double"
	case types.Bool:
		return "bool"
	case types.Void:
		return "void"
	}
	return "const char *"
}

// Writes the Vex side of extern function [f]: a wrapper with a Vex
// signature that converts strings and calls the C function.
func (g *gen) externFunction(f *types.Func) {
	cname := f.Decl.CName
	if cname == "" {
		cname = f.Name
	}
	var params, cparams, args []string
	floats := f.Result == types.Float
	for i, p := range f.Params {
		name := fmt.Sprintf("a%d", i)
		params = append(params, decl(p.Type, name))
		cparams = append(cparams, externType(p.Type))
		floats = floats && p.Type == types.Float
		switch {
		case p.Type == types.String:
			args = append(args, "vx_cstr("+name+")")
		case p.Type != types.Int && p.Type != types.Float && p.Type != types.Bool:
			args = append(args, fmt.Sprintf("(%s.has ? vx_cstr(%s.v) : NULL)", name, name))
		default:
			args = append(args, name)
		}
	}
	if len(params) == 0 {
		params, cparams = []string{"void"}, []string{"void"}
	}
	callee := cname
	switch {
	case floats && mathBuiltins[cname] == len(f.Params):
		callee = "__builtin_" + cname
	case strings.HasPrefix(cname, "vx_"):
	default:
		callee = "vx_c_" + funcName(f)
		g.line("")
		g.line("extern %s %s(%s) __asm__(VX_SYM(%s));", externType(f.Result), callee, strings.Join(cparams, ", "), cQuote(cname))
	}
	call := callee + "(" + strings.Join(args, ", ") + ")"
	g.line("")
	g.line("static inline %s {", decl(f.Result, funcName(f)+"("+strings.Join(params, ", ")+")"))
	switch r := f.Result; {
	case r == types.Void:
		g.line("    %s;", call)
	case r == types.String:
		g.line("    return vx_str_from_c(%s);", call)
	case r == types.Int || r == types.Float || r == types.Bool:
		g.line("    return %s;", call)
	default:
		g.line("    const char *r = %s;", call)
		g.line("    return r ? (%s){true, vx_str_from_c(r)} : (%s){false};", ctype(r), ctype(r))
	}
	g.line("}")
}
