package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/lexer"
	"github.com/vex-language/vex-lang/compiler/types"
)

// Writes a wrapper for each test of a program built to run its tests,
// which reports an error passed up out of it.
func (g *gen) testWrappers() {
	for i, d := range g.info.Tests {
		f := g.info.FuncOf[d]
		if f.Throws != nil {
			g.line("static void tw_%d(void) { void *err = NULL; %s(&err); if (err) vx_test_error(%s); }", i+1, funcName(f), errorMessage(g.info, f.Throws, "err"))
		} else {
			g.line("static void tw_%d(void) { %s(); }", i+1, funcName(f))
		}
	}
	g.line("")
}

// Writes the end of main for a program built to run its tests.
func (g *gen) testMain() {
	if len(g.info.Tests) == 0 {
		g.line("    return vx_test_run(0, NULL, NULL);")
		return
	}
	names := make([]string, len(g.info.Tests))
	fns := make([]string, len(g.info.Tests))
	for i, d := range g.info.Tests {
		names[i] = cQuote(d.Test)
		fns[i] = fmt.Sprintf("tw_%d", i+1)
	}
	g.line("    static const char *const names[] = {%s};", strings.Join(names, ", "))
	g.line("    static void (*const fns[])(void) = {%s};", strings.Join(fns, ", "))
	g.line("    return vx_test_run(%d, names, fns);", len(g.info.Tests))
}

// Returns `expect(cond)`. When the condition compares two values that can
// be shown, each side is evaluated once into a temporary, so a failure
// shows both.
func (g *gen) expect(e *ast.CallExpr, c *types.Call) string {
	line := g.lineOf(e)
	msg := `VX_LIT("")`
	if len(c.Args) > 1 && c.Args[1] != nil {
		msg = g.expr(c.Args[1])
	}
	text := cQuote(c.Text)
	if b, ok := sides(c.Args[0]); ok {
		lt, rt := g.sideType(b.X), g.sideType(b.Y)
		if lt != types.Void && rt != types.Void {
			l, r := g.temp(), g.temp()
			pre := fmt.Sprintf("%s = %s; %s = %s;", decl(lt, l), g.expr(b.X), decl(rt, r), g.expr(b.Y))
			g.recvs[b.X], g.recvs[b.Y] = l, r
			test := g.expr(b)
			delete(g.recvs, b.X)
			delete(g.recvs, b.Y)
			return fmt.Sprintf("({ %s if (!(%s)) vx_expect_fail(%d, %s, %s, true, %s, %s); })",
				pre, test, line, text, msg, repr(lt, l), repr(rt, r))
		}
	}
	return fmt.Sprintf("({ if (!(%s)) vx_expect_fail(%d, %s, %s, false, (vx_str){0}, (vx_str){0}); })",
		g.expr(c.Args[0]), line, text, msg)
}

// Returns [e] as a comparison whose two sides a failed expect can show.
func sides(e ast.Expr) (*ast.BinaryExpr, bool) {
	b, ok := e.(*ast.BinaryExpr)
	if !ok {
		return nil, false
	}
	switch b.Op {
	case lexer.Eq, lexer.NotEq, lexer.Lt, lexer.LtEq, lexer.Gt, lexer.GtEq:
	default:
		return nil, false
	}
	_, xNil := b.X.(*ast.NilLit)
	_, yNil := b.Y.(*ast.NilLit)
	return b, !xNil && !yNil
}

// Returns the type of the C value of [e], an optional when it is wrapped
// into one.
func (g *gen) sideType(e ast.Expr) types.Type {
	if o := g.info.Wraps[e]; o != nil {
		return o
	}
	return g.info.Types[e]
}
