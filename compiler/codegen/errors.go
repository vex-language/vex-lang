package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
)

// A try being generated: the variable that receives the error, the label
// its handler starts at, and the type the error is held as (nil for try?).
type tryGen struct {
	err, label string
	target     types.Type
}

func mainFunc(info *types.Info) *types.Func {
	for _, f := range info.Funcs {
		if f.Name == "main" && f.Owner == nil && f.Enum == nil {
			return f
		}
	}
	return nil
}

// Returns C code for the message of the error [err], whose type is [t].
func errorMessage(info *types.Info, t types.Type, err string) string {
	if en, ok := t.(*types.Enum); ok {
		return repr(en, fmt.Sprintf("((%s *)%s)", enumName(en), err))
	}
	return fmt.Sprintf("((%s *)%s)->f_message", structName(info.Error), err)
}

// Returns the statement that leaves a function whose result type is [t]
// after it has thrown.
func zeroReturn(t types.Type) string {
	if t == types.Void {
		return "return;"
	}
	return fmt.Sprintf("return (%s){0};", ctype(t))
}

// Joins the arguments of the call [e], adding where it stores its error
// when it can throw.
func (g *gen) args(e *ast.CallExpr, cs []string) string {
	if g.info.Throwing[e] != nil {
		cs = append(cs, "&"+g.tries[len(g.tries)-1].err)
	}
	return strings.Join(cs, ", ")
}

// Wraps the call [code], which throws [thrown], so that an error jumps to
// the handler of the enclosing try, converted to the type the try holds.
func (g *gen) throwingCall(e ast.Expr, code string, thrown types.Type) string {
	tr := g.tries[len(g.tries)-1]
	fail := fmt.Sprintf("if (__builtin_expect(%s != NULL, 0)) { ", tr.err)
	if en, ok := thrown.(*types.Enum); ok && tr.target == types.Type(g.info.Error) {
		fail += fmt.Sprintf("%s = new_%s(%s); ", tr.err, structName(g.info.Error), errorMessage(g.info, en, tr.err))
	}
	fail += "goto " + tr.label + "; }"
	t := g.info.Types[e]
	if t == types.Void {
		return fmt.Sprintf("({ %s; %s })", code, fail)
	}
	r := g.temp()
	return fmt.Sprintf("({ %s = %s; %s %s; })", decl(t, r), code, fail, r)
}

// Generates a try. Its calls jump to a handler block that the normal path
// skips, which passes the error up, runs the catch, or gives nil.
func (g *gen) tryExpr(e *ast.TryExpr) string {
	info := g.info.Tries[e]
	t := g.info.Types[e]
	tr := tryGen{err: g.temp(), target: info.Target}
	tr.label = "_L" + tr.err
	g.tries = append(g.tries, tr)
	x := g.expr(e.X)
	g.tries = g.tries[:len(g.tries)-1]

	var b strings.Builder
	fmt.Fprintf(&b, "({ __label__ %s; void *%s = NULL; ", tr.label, tr.err)
	r := ""
	if t != types.Void && t != types.Invalid {
		r = g.temp()
		fmt.Fprintf(&b, "%s; ", decl(t, r))
	}
	value := x
	if _, already := g.info.Types[e.X].(*types.Optional); e.Optional && !already {
		value = wrapOpt(t.(*types.Optional), x)
	}
	if r != "" {
		fmt.Fprintf(&b, "%s = %s; ", r, value)
	} else {
		fmt.Fprintf(&b, "%s; ", value)
	}
	fmt.Fprintf(&b, "if (0) { %s: ; ", tr.label)
	switch {
	case e.Optional:
		fmt.Fprintf(&b, "%s = (%s){false}; ", r, ctype(t))
	case e.Catch != nil:
		if info.Bind != nil {
			fmt.Fprintf(&b, "%s = %s; ", decl(info.Bind.Type, g.declare(info.Bind)), tr.err)
		}
		b.WriteString(g.catchBody(e.Catch.Body, info, r))
	default:
		fmt.Fprintf(&b, "*vx_err = %s; %s%s ", tr.err, g.leaving(), zeroReturn(g.result))
	}
	b.WriteString("} ")
	if r != "" {
		b.WriteString(r + "; ")
	}
	b.WriteString("})")
	return b.String()
}

// Returns the statements of a catch block as C code. When the block gives
// the try's value, its last expression is stored in [r].
func (g *gen) catchBody(body *ast.Block, info *types.Try, r string) string {
	outer, ind := g.b, g.ind
	var b strings.Builder
	g.b, g.ind = &b, 0
	stmts := body.Stmts
	if info.Value {
		stmts = stmts[:len(stmts)-1]
	}
	g.stmtsIn(stmts, func() {
		if info.Value {
			x := body.Stmts[len(body.Stmts)-1].(*ast.ExprStmt).X
			g.line("%s = %s;", r, g.expr(x))
		}
	})
	g.b, g.ind = outer, ind
	return b.String()
}
