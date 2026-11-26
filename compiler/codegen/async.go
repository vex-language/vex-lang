package codegen

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/types"
)

// An async function runs as a plain C function on the stack of the task
// that calls it, so `await f()` is a direct call, and an await inside it
// switches stacks in the runtime. A call without await goes through the
// function's spawn wrapper, which starts it as a new task and returns its
// future.

// Returns the C parameters of the spawn wrapper of [f]: those of [f]
// itself, without the error.
func spawnParams(f *types.Func) (cts, names []string) {
	if f.Owner != nil && !f.Static {
		cts, names = append(cts, structName(f.Owner)+" *"), append(names, "self")
	}
	if f.Enum != nil && !f.Static {
		cts, names = append(cts, enumName(f.Enum)+" *"), append(names, "self")
	}
	if f.Recv != nil {
		cts, names = append(cts, ctype(f.Recv)), append(names, "self")
	}
	for i, p := range f.Params {
		cts, names = append(cts, ctype(p.Type)), append(names, fmt.Sprintf("a%d", i))
	}
	return cts, names
}

func spawnSig(f *types.Func) string {
	ts, ns := spawnParams(f)
	params := make([]string, len(ts))
	for i := range ts {
		params[i] = ts[i] + " " + ns[i]
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	return "vx_future *sp_" + funcName(f) + "(" + strings.Join(params, ", ") + ")"
}

// Returns the size of the value a future of [t] holds.
func valueSize(t types.Type) string {
	if t == types.Void {
		return "0"
	}
	return "sizeof(" + ctype(t) + ")"
}

// Writes the spawn wrapper of async function [f], with the entry point
// of its task.
func (g *gen) spawnWrapper(f *types.Func) {
	name := funcName(f)
	ts, ns := spawnParams(f)
	var fields []string
	for i := range ts {
		fields = append(fields, ts[i]+" "+ns[i]+";")
	}
	g.line("")
	g.line("typedef struct { vx_future *f; %s } ar_%s;", strings.Join(fields, " "), name)
	g.line("static void en_%s(void *p) {", name)
	g.line("    ar_%s *a = p;", name)
	var args []string
	for _, n := range ns {
		args = append(args, "a->"+n)
	}
	if f.Throws != nil {
		g.line("    void *err = NULL;")
		args = append(args, "&err")
	}
	call := name + "(" + strings.Join(args, ", ") + ")"
	value := "NULL"
	if f.Result != types.Void {
		g.line("    %s = %s;", decl(f.Result, "r"), call)
		value = "&r"
	} else {
		g.line("    %s;", call)
	}
	if f.Throws != nil {
		g.line("    if (err) { vx_future_fail(a->f, err); return; }")
	}
	g.line("    vx_future_done(a->f, %s);", value)
	g.line("}")
	g.line("static %s {", spawnSig(f))
	g.line("    ar_%s *a = vx_alloc(sizeof(ar_%s));", name, name)
	g.line("    a->f = vx_future_new(%s);", valueSize(f.Result))
	for _, n := range ns {
		g.line("    a->%s = %s;", n, n)
	}
	g.line("    vx_remember(a);")
	g.line("    vx_go(en_%s, a);", name)
	g.line("    return a->f;")
	g.line("}")
}

// Generates `await x`.
func (g *gen) await(e *ast.AwaitExpr) string {
	if call, ok := e.X.(*ast.CallExpr); ok {
		if c := g.info.Calls[call]; c != nil && c.Func != nil && c.Func.Async {
			return g.expr(e.X)
		}
	}
	return g.settle(e, g.expr(e.X), "vx_await(%s)")
}

// Returns code that waits for the future [x] with [wait], a call of the
// runtime that settles it, and gives its value, passing its error to the
// enclosing try when [e] throws.
func (g *gen) settle(e ast.Expr, x, wait string) string {
	t := g.info.Types[e]
	f := g.temp()
	value := ""
	if t != types.Void && t != types.Invalid {
		value = fmt.Sprintf(" *(%s *)%s->value;", ctype(t), f)
	}
	thrown := g.info.Throwing[e]
	if thrown == nil {
		return fmt.Sprintf("({ vx_future *%s = %s; %s;%s })", f, x, fmt.Sprintf(wait, f), value)
	}
	code := fmt.Sprintf("({ vx_future *%s = %s; %s = %s;%s })", f, x, g.tries[len(g.tries)-1].err, fmt.Sprintf(wait, f), value)
	return g.throwingCall(e, code, thrown)
}

// Generates a call of all, any or run.
func (g *gen) asyncBuiltin(e *ast.CallExpr, c *types.Call) string {
	arg := g.expr(c.Args[0])
	switch c.Builtin {
	case "run":
		return g.settle(e, arg, fmt.Sprintf("vx_run(%%s, %d)", g.lineOf(e)))
	case "all":
		elem := g.info.Types[c.Args[0]].(*types.List).Elem.(*types.Future).Elem
		return fmt.Sprintf("vx_all(%s, %s)", arg, valueSize(elem))
	}
	elem := g.info.Types[c.Args[0]].(*types.List).Elem.(*types.Future).Elem
	return fmt.Sprintf("vx_any(%s, %s, %d)", arg, valueSize(elem), g.lineOf(e))
}

// Returns the C function the call [e] of [f] runs: [f] itself, or its
// spawn wrapper when an async call isn't awaited.
func (g *gen) callee(e *ast.CallExpr, f *types.Func) string {
	if g.info.Spawns[e] {
		return "sp_" + funcName(f)
	}
	return funcName(f)
}
