package types

import (
	"fmt"

	"github.com/irshadio/vex-lang/compiler/ast"
)

// A closure being checked.
type litCtx struct {
	lit *ast.FuncLit

	// The result type; nil while it is still being inferred from the
	// first `return`.
	result Type

	// The loop depth of the enclosing code, restored after the closure.
	outerLoops int
}

// Returns the closure being checked, or nil inside a plain function body.
func (c *checker) curLit() *ast.FuncLit {
	if len(c.lits) == 0 {
		return nil
	}
	return c.lits[len(c.lits)-1].lit
}

// Records that [v] is used here. When [v] is declared outside the current
// closure, it is captured by that closure and every closure between them.
func (c *checker) use(v *Var) {
	for i := len(c.lits) - 1; i >= 0 && c.lits[i].lit != v.lit; i-- {
		v.Captured = true
		lit := c.lits[i].lit
		found := false
		for _, w := range c.info.Captures[lit] {
			found = found || w == v
		}
		if !found {
			c.info.Captures[lit] = append(c.info.Captures[lit], v)
		}
	}
}

// Checks a closure. [want] is the function type the context expects, if
// any; it supplies parameter types and the result type when they are left
// out.
func (c *checker) funcLit(e *ast.FuncLit, want Type) Type {
	wantFn, _ := want.(*FuncType)
	// After a wrong parameter count, missing types aren't reported again
	// and the closure's type is left invalid, so the mistake gives one error.
	wrongArity := wantFn != nil && len(wantFn.Params) != len(e.Params)
	if wrongArity {
		c.errorAt(e, "", "expected a function with %s, found one with %d", plural(len(wantFn.Params), "parameter"), len(e.Params))
		wantFn = nil
	}
	ft := &FuncType{}
	for i, p := range e.Params {
		switch {
		case p.Type != nil:
			ft.Params = append(ft.Params, c.resolveType(p.Type))
		case wantFn != nil:
			ft.Params = append(ft.Params, wantFn.Params[i])
		case wrongArity:
			ft.Params = append(ft.Params, Invalid)
		default:
			c.errorAt(p.Name, fmt.Sprintf("write its type: fn(%s: int) ...", p.Name.Name), "can't tell the type of parameter '%s'", p.Name.Name)
			ft.Params = append(ft.Params, Invalid)
		}
	}
	switch {
	case e.Result != nil:
		ft.Result = c.resolveType(e.Result)
	case wantFn != nil && wantFn.Result != nil:
		ft.Result = wantFn.Result
	}

	ctx := &litCtx{lit: e, result: ft.Result, outerLoops: c.loops}
	c.lits = append(c.lits, ctx)
	c.loops = 0
	c.push()
	for i, p := range e.Params {
		c.declare(p.Name, ft.Params[i], false, ParamVar)
	}
	if e.ExprBody != nil {
		var exprWant Type
		if ctx.result != Void {
			exprWant = ctx.result
		}
		t := c.expr(e.ExprBody, exprWant)
		switch {
		case ctx.result == nil:
			ctx.result = t
		case ctx.result != Void:
			c.assignable(ctx.result, t, e.ExprBody, "closure result")
		}
	} else {
		for _, s := range e.Body.Stmts {
			c.stmt(s)
		}
		if ctx.result == nil {
			ctx.result = Void
		}
		if ctx.result != Void && !terminates(e.Body) {
			c.errorAtPos(c.file.PosOf(e.Body.End-1), 1, "add a return statement before the closing }",
				"missing return: the closure must return %s", ctx.result)
		}
	}
	c.pop()
	c.loops = ctx.outerLoops
	c.lits = c.lits[:len(c.lits)-1]
	ft.Result = ctx.result
	if wrongArity {
		return Invalid
	}
	return ft
}

// Checks `return` inside a closure. A closure without a written result type
// takes it from its first `return`.
func (c *checker) closureReturn(s *ast.ReturnStmt, ctx *litCtx) {
	switch {
	case ctx.result == nil && s.Value == nil:
		ctx.result = Void
	case ctx.result == nil:
		t := c.expr(s.Value, nil)
		if t == Void {
			c.errorAt(s.Value, "", "this expression has no value to return")
			t = Invalid
		}
		ctx.result = t
	case s.Value == nil && ctx.result != Void:
		c.errorAt(s, fmt.Sprintf("return a value, e.g. return %s", zeroExample(ctx.result)), "missing return value: the closure must return %s", ctx.result)
	case s.Value != nil && ctx.result == Void:
		t := c.expr(s.Value, nil)
		c.errorAt(s.Value, fmt.Sprintf("declare the result type: fn(...) -> %s { ... }", t), "this closure doesn't return a value")
	case s.Value != nil:
		c.assignable(ctx.result, c.expr(s.Value, ctx.result), s.Value, "")
	}
}

// Checks a call of a function value such as `onTap()` or `handlers[0](x)`.
func (c *checker) callValue(e *ast.CallExpr) Type {
	t := c.expr(e.Fn, nil)
	ft, ok := t.(*FuncType)
	if !ok {
		c.checkArgs(e)
		if t != Invalid {
			c.errorAt(e.Fn, "", "a value of type %s can't be called", t)
		}
		return Invalid
	}
	for _, a := range e.Args {
		if a.Name != nil {
			c.checkArgs(e)
			c.errorAt(a.Name, "pass the arguments in order", "function values don't have parameter names")
			return ft.Result
		}
	}
	given := len(e.Args)
	if e.Trailing != nil {
		given++
	}
	if given != len(ft.Params) {
		c.checkArgs(e)
		c.errorAt(e.Fn, "", "expected %s for %s, found %d", plural(len(ft.Params), "argument"), ft, given)
		return ft.Result
	}
	slots := make([]slot, len(ft.Params))
	for i, p := range ft.Params {
		slots[i] = slot{name: fmt.Sprint(i + 1), label: fmt.Sprintf("argument %d", i+1), typ: p}
	}
	c.info.Calls[e] = &Call{Kind: ValueCall, Callee: e.Fn, Args: c.bind(e, slots, ft.String())}
	return ft.Result
}
