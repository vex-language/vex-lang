package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
)

// A stream function's C function packs its arguments and returns a new
// stream at once. Its body is a separate C function, run as a task by
// the stream's first read, that takes the stream as `vx_ys`.

// Writes the stream function [f]: its body, the entry point of the body's
// task, and the function that makes the stream.
func (g *gen) streamFunction(f *types.Func) {
	name := funcName(f)
	elem := f.Result.(*types.Stream).Elem
	g.result, g.yields = types.Void, elem
	params := append([]string{"vx_stream *vx_ys"}, g.params(f, true)...)
	g.line("")
	g.line("static void bd_%s(%s) {", name, strings.Join(params, ", "))
	g.ind++
	for _, s := range f.Decl.Body.Stmts {
		g.stmt(s)
	}
	g.ind--
	g.line("}")

	ts, ns := spawnParams(f)
	fields := []string{"vx_stream *s;"}
	args := []string{"a->s"}
	var sig []string
	for i := range ts {
		fields = append(fields, ts[i]+" "+ns[i]+";")
		args = append(args, "a->"+ns[i])
		sig = append(sig, ts[i]+" "+ns[i])
	}
	if len(sig) == 0 {
		sig = []string{"void"}
	}
	g.line("typedef struct { %s } ar_%s;", strings.Join(fields, " "), name)
	g.line("static void en_%s(void *p) {", name)
	g.line("    ar_%s *a = p;", name)
	g.line("    bd_%s(%s);", name, strings.Join(args, ", "))
	g.line("    vx_stream_close(a->s);")
	g.line("    vx_future_done(a->s->done, NULL);")
	g.line("}")
	g.line("static vx_stream *%s(%s) {", name, strings.Join(sig, ", "))
	g.line("    ar_%s *a = vx_alloc(sizeof(ar_%s));", name, name)
	for _, n := range ns {
		g.line("    a->%s = %s;", n, n)
	}
	g.line("    a->s = vx_stream_new(%s);", valueSize(elem))
	g.line("    vx_remember(a);")
	g.line("    vx_stream_start(a->s, en_%s, a);", name)
	g.line("    return a->s;")
	g.line("}")
}

// Generates `yield value`, which waits until the reader takes the value.
func (g *gen) yieldStmt(s *ast.YieldStmt) {
	x := g.temp()
	g.line("{ %s = %s; vx_stream_yield(vx_ys, &%s); }", decl(g.yields, x), g.expr(s.Value), x)
}

// Generates `for await x in stream`. Leaving the loop stops the stream.
func (g *gen) forAwait(s *ast.ForStmt) {
	elem := g.info.Defs[s.Elem]
	st := g.temp()
	g.line("{")
	g.ind++
	g.line("vx_stream *%s = %s;", st, g.expr(s.Iter))
	g.line("for (;;) {")
	g.ind++
	v := g.declare(elem)
	g.line("%s;", decl(elem.Type, v))
	g.line("if (!vx_stream_next(%s, &%s, %d)) break;", st, v, g.lineOf(s))
	if s.Pattern != nil {
		g.letBind(s.Pattern, g.varName(elem))
	}
	g.ind--
	g.streams = append(g.streams, st)
	g.body(s.Body)
	g.streams = g.streams[:len(g.streams)-1]
	g.line("}")
	g.line("vx_stream_stop(%s);", st)
	g.ind--
	g.line("}")
}

// Returns the C statements that stop the streams of the for await loops
// a return leaves.
func (g *gen) leaving() string {
	var b strings.Builder
	for i := len(g.streams) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "vx_stream_stop(%s); ", g.streams[i])
	}
	return b.String()
}
