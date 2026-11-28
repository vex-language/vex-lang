package codegen

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/vex-language/vex-lang/compiler/types"
)

// C math functions on doubles that the C compiler knows: calls to them
// compile to single instructions where the CPU has one.
var mathBuiltins = map[string]int{
	"sqrt": 1, "cbrt": 1, "sin": 1, "cos": 1, "tan": 1, "asin": 1, "acos": 1, "atan": 1,
	"sinh": 1, "cosh": 1, "tanh": 1, "exp": 1, "exp2": 1, "log": 1, "log2": 1, "log10": 1,
	"floor": 1, "ceil": 1, "round": 1, "trunc": 1, "fabs": 1,
	"pow": 2, "atan2": 2, "hypot": 2, "fmod": 2, "fmin": 2, "fmax": 2,
}

// Returns the C type an extern or export function uses for [t], or
// [sized] when the signature names a sized number such as int32.
func externType(t types.Type, sized string) string {
	if sized != "" {
		return sized
	}
	switch t := t.(type) {
	case *types.Handle:
		return "void *"
	case *types.Optional:
		if _, ok := t.Elem.(*types.Handle); ok {
			return "void *"
		}
		if t.Elem == types.Bytes {
			return "vx_list *"
		}
	}
	switch t {
	case types.Bytes:
		return "vx_list *"
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

// Returns C code giving the Vex value [x] of type [t] as C wants it.
func toC(t types.Type, x string) string {
	switch t := t.(type) {
	case *types.Optional:
		if _, ok := t.Elem.(*types.Handle); ok || t.Elem == types.Bytes {
			return fmt.Sprintf("(%s.has ? %s.v : NULL)", x, x)
		}
		return fmt.Sprintf("(%s.has ? vx_cstr(%s.v) : NULL)", x, x)
	}
	if t == types.String {
		return "vx_cstr(" + x + ")"
	}
	return x
}

// Returns C code giving the C value [x] as a Vex value of type [t].
func fromC(t types.Type, x string) string {
	switch t := t.(type) {
	case *types.Optional:
		return fmt.Sprintf("({ %s r = %s; r ? (%s){true, %s} : (%s){false}; })", externType(t, ""), x, ctype(t), fromC(t.Elem, "r"), ctype(t))
	}
	switch t {
	case types.String:
		return "vx_str_from_c(" + x + ")"
	case types.Int, types.Float:
		return "(" + ctype(t) + ")" + x
	}
	return x
}

// Returns the C function pointer type that C code calls the callback of
// type [ft] through: its context first, then its parameters.
func callbackPtr(ft *types.FuncType) string {
	params := []string{"void *"}
	for _, p := range ft.Params {
		params = append(params, externType(p, ""))
	}
	return externType(resultOf(ft), "") + " (*)(" + strings.Join(params, ", ") + ")"
}

func resultOf(ft *types.FuncType) types.Type {
	if ft.Result == nil {
		return types.Void
	}
	return ft.Result
}

// Writes the function C calls for callback parameter [i] of extern
// function [f], which calls the Vex closure kept in its context.
func (g *gen) trampoline(f *types.Func, i int, ft *types.FuncType) string {
	name := fmt.Sprintf("vx_cb_%s_%d", funcName(f), i)
	params, args := []string{"void *ctx"}, []string{"cb.env"}
	for n, p := range ft.Params {
		a := fmt.Sprintf("a%d", n)
		params = append(params, externType(p, "")+" "+a)
		args = append(args, fromC(p, a))
	}
	r := resultOf(ft)
	call := fmt.Sprintf("((%s)cb.fn)(%s)", fnPtrType(ft), strings.Join(args, ", "))
	if r != types.Void {
		call = "return " + toC(r, call)
	}
	g.line("")
	g.line("static %s %s(%s) { vx_fn cb = *(vx_fn *)ctx; %s; }", externType(r, ""), name, strings.Join(params, ", "), call)
	return name
}

// Writes the Vex side of extern function [f]: a wrapper with a Vex
// signature that converts strings and calls the C function.
func (g *gen) externFunction(f *types.Func) {
	cname := f.Decl.CName
	if cname == "" {
		cname = f.Name
	}
	var params, cparams, args []string
	floats := f.Result == types.Float && f.CResult == ""
	native := runtimeSigs[cname]
	for i, p := range f.Params {
		name := fmt.Sprintf("a%d", i)
		params = append(params, decl(p.Type, name))
		if ft, ok := p.Type.(*types.FuncType); ok {
			cparams = append(cparams, callbackPtr(ft), "void *")
			args = append(args, g.trampoline(f, i, ft), "vx_keep_fn("+name+")")
			floats = false
			continue
		}
		cparams = append(cparams, externType(p.Type, f.CParams[i]))
		floats = floats && p.Type == types.Float && f.CParams[i] == ""
		arg := toC(p.Type, name)
		if p.Type == types.String && i < len(native.strs) && native.strs[i] {
			arg = name
		}
		if f.CParams[i] != "" {
			arg = "(" + f.CParams[i] + ")" + arg
		}
		args = append(args, arg)
	}
	if len(params) == 0 {
		params, cparams = []string{"void"}, []string{"void"}
	}
	callee := cname
	switch {
	case floats && mathBuiltins[cname] == len(f.Params):
		callee = "__builtin_" + cname
	case native.result != "":
	default:
		callee = "vx_c_" + funcName(f)
		g.line("")
		g.line("extern %s %s(%s) __asm__(VX_SYM(%s));", externType(f.Result, f.CResult), callee, strings.Join(cparams, ", "), cQuote(cname))
	}
	call := callee + "(" + strings.Join(args, ", ") + ")"
	result := fromC(f.Result, call)
	if native.result == "vx_str" {
		switch {
		case f.Result == types.String:
			result = call
		case types.Identical(f.Result, &types.Optional{Elem: types.String}):
			// A runtime function gives no string with a null pointer.
			result = fmt.Sprintf("({ vx_str r = %s; r.ptr ? (%s){true, r} : (%s){false}; })", call, ctype(f.Result), ctype(f.Result))
		}
	}
	g.line("")
	g.line("static inline %s {", decl(f.Result, funcName(f)+"("+strings.Join(params, ", ")+")"))
	if f.Result == types.Void {
		g.line("    %s;", call)
	} else {
		g.line("    return %s;", result)
	}
	g.line("}")
}

// Writes the C function that export function [f] gives C code to call,
// under [f]'s own name, converting to and from Vex values.
func (g *gen) exportFunction(f *types.Func) {
	var params, args []string
	for i, p := range f.Params {
		name := fmt.Sprintf("a%d", i)
		params = append(params, externType(p.Type, f.CParams[i])+" "+name)
		args = append(args, fromC(p.Type, name))
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	call := funcName(f) + "(" + strings.Join(args, ", ") + ")"
	g.line("")
	g.line("%s %s(%s) {", externType(f.Result, f.CResult), f.Name, strings.Join(params, ", "))
	if f.Result == types.Void {
		g.line("    %s;", call)
	} else {
		g.line("    return (%s)%s;", externType(f.Result, f.CResult), toC(f.Result, call))
	}
	g.line("}")
}

// The C signature of a runtime function.
type runtimeSig struct {
	result string

	// Whether each parameter is a `vx_str`.
	strs []bool
}

// The signatures of the runtime functions. A runtime function that takes
// or gives `vx_str` gets Vex strings as they are, with their length, so
// text holding NUL bytes passes through whole.
var runtimeSigs = func() map[string]runtimeSig {
	m := map[string]runtimeSig{}
	re := regexp.MustCompile(`(?m)^static (?:inline )?([A-Za-z_][A-Za-z0-9_ ]*?[ *]+)(vx_[A-Za-z0-9_]+)\(([^)]*)\)`)
	for _, sm := range re.FindAllStringSubmatch(runtime, -1) {
		sig := runtimeSig{result: strings.TrimSpace(sm[1])}
		for _, p := range strings.Split(sm[3], ",") {
			sig.strs = append(sig.strs, strings.HasPrefix(strings.TrimSpace(p), "vx_str "))
		}
		m[sm[2]] = sig
	}
	return m
}()
