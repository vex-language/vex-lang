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
	g.line("    vx_go_f(en_%s, a, a->f);", name)
	g.line("    return a->f;")
	g.line("}")
}

// Generates `await x`. A call of an async function or function value runs
// it here, on this task's stack.
func (g *gen) await(e *ast.AwaitExpr) string {
	if call, ok := e.X.(*ast.CallExpr); ok && g.asyncCallee(g.info.Calls[call]) {
		return g.expr(e.X)
	}
	return g.settle(e, g.expr(e.X), "vx_await(%s)")
}

// Reports whether [c] calls an async function, method or function value.
func (g *gen) asyncCallee(c *types.Call) bool {
	switch {
	case c == nil:
		return false
	case c.Func != nil:
		return c.Func.Async && !c.Func.Streams()
	case c.Kind == types.ValueCall:
		ft, _ := g.info.Types[c.Callee].(*types.FuncType)
		return ft != nil && ft.Async && !ft.Streams()
	}
	return false
}

// Returns the name of the C function that starts a function value of type
// [ft] as a new task and returns its future. It takes the value and then
// the arguments; each function type gets one, made on first use.
func (g *gen) valueSpawner(ft *types.FuncType) string {
	sync := *ft
	sync.Async = false
	m := mangle(&sync)
	name := "spv_" + m
	if g.codecs[name] {
		return name
	}
	g.codecs[name] = true
	var fields, params, args, sets []string
	for i, p := range ft.Params {
		n := fmt.Sprintf("a%d", i)
		fields = append(fields, decl(p, n)+";")
		params = append(params, decl(p, n))
		args = append(args, "a->"+n)
		sets = append(sets, fmt.Sprintf("a->%s = %s;", n, n))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "typedef struct { vx_future *f; vx_fn fn; %s } arv_%s;\n", strings.Join(fields, " "), m)
	fmt.Fprintf(&b, "static void env_%s(void *p) { arv_%s *a = p; ", m, m)
	call := fmt.Sprintf("((%s)a->fn.fn)(%s)", fnPtrType(ft), strings.Join(append([]string{"a->fn.env"}, args...), ", "))
	if ft.Throws != nil {
		b.WriteString("void *err = NULL; ")
		call = call[:len(call)-1] + ", &err)"
	}
	value := "NULL"
	if ft.Result != types.Void {
		fmt.Fprintf(&b, "%s = %s; ", decl(ft.Result, "r"), call)
		value = "&r"
	} else {
		b.WriteString(call + "; ")
	}
	if ft.Throws != nil {
		b.WriteString("if (err) { vx_future_fail(a->f, err); return; } ")
	}
	fmt.Fprintf(&b, "vx_future_done(a->f, %s); }\n", value)
	fmt.Fprintf(&b, "static vx_future *%s(%s) { arv_%s *a = vx_alloc(sizeof(arv_%s)); a->f = vx_future_new(%s); a->fn = fn; %s vx_remember(a); vx_go_f(env_%s, a, a->f); return a->f; }\n",
		name, strings.Join(append([]string{"vx_fn fn"}, params...), ", "), m, m, valueSize(ft.Result), strings.Join(sets, " "), m)
	g.codecDefs.WriteString(b.String())
	return name
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

// Generates `time.timeout(future, ms)`: a task that waits for the future
// or the timer, whichever settles first, and cancels the other.
func (g *gen) timeoutCall(e *ast.CallExpr, c *types.Call) string {
	src := g.info.Types[c.Args[0]].(*types.Future)
	res := g.info.Types[e].(*types.Future)
	m := mangle(res.Elem)
	name := "tmo_" + m
	if !g.codecs[name] {
		g.codecs[name] = true
		var value string
		if o, ok := res.Elem.(*types.Optional); ok {
			value = fmt.Sprintf("%s r = %s;", ctype(o), wrapOpt(o, fmt.Sprintf("*(%s *)a->src->value", ctype(src.Elem))))
		} else {
			value = "bool r = true;"
		}
		fmt.Fprintf(&g.codecDefs, "typedef struct { vx_future *f; vx_future *src; int64_t ms; } artmo_%s;\n", m)
		fmt.Fprintf(&g.codecDefs, "static void entmo_%s(void *p) { artmo_%s *a = p; vx_future *timer = vx_sleep(a->ms); vx_wait_settled(vx_first(a->src, timer)); "+
			"switch (a->src->state) { case 0: { %s r = (%s){0}; vx_cancel(a->src); vx_future_done(a->f, &r); break; } "+
			"case 1: { %s vx_future_done(a->f, &r); break; } case 2: vx_future_fail(a->f, a->src->err); break; default: vx_future_settle(a->f, 3); } vx_cancel(timer); }\n",
			m, m, ctype(res.Elem), ctype(res.Elem), value)
		fmt.Fprintf(&g.codecDefs, "static vx_future *%s(vx_future *src, int64_t ms) { artmo_%s *a = vx_alloc(sizeof(artmo_%s)); a->f = vx_future_new(sizeof(%s)); a->src = src; a->ms = ms; vx_remember(a); vx_go_f(entmo_%s, a, a->f); return a->f; }\n",
			name, m, m, ctype(res.Elem), m)
	}
	return g.ordered(c.Args, func(cs []string) string {
		return fmt.Sprintf("%s(%s, %s)", name, cs[0], cs[1])
	})
}
