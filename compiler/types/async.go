package types

import (
	"github.com/irshadio/vex-lang/compiler/ast"
)

// Built-in functions on futures. Unlike the other built-ins, a function
// of the program with the same name replaces them.
var asyncBuiltins = map[string]bool{"all": true, "any": true, "run": true}

// Returns the type written as `Future<T>` or `Future<T, E>`.
func (c *checker) futureType(te *ast.NamedType) Type {
	if len(te.Args) > 2 {
		c.errorAt(te, "write Future<int>, or Future<int, Error> when it can fail", "Future takes 1 or 2 type arguments, found %d", len(te.Args))
		return Invalid
	}
	f := &Future{Elem: Void}
	if nt, ok := te.Args[0].(*ast.NamedType); !ok || nt.Name != "void" || nt.Module != "" {
		f.Elem = c.resolveType(te.Args[0])
	}
	if len(te.Args) == 2 {
		switch t := c.resolveType(te.Args[1]).(type) {
		case *Enum:
			f.Throws = t
		case *Struct:
			if t != c.info.Error {
				c.errorAt(te.Args[1], "use Error or an enum of errors", "a future fails with Error or an enum, found %s", t)
			}
			f.Throws = c.info.Error
		default:
			if t != Invalid {
				c.errorAt(te.Args[1], "use Error or an enum of errors", "a future fails with Error or an enum, found %s", t)
			}
			return Invalid
		}
	}
	if f.Elem == Invalid {
		return Invalid
	}
	return f
}

// Checks `await x`: x is a call of an async function, which runs here
// until it is done, or a future, whose value it waits for.
func (c *checker) awaitExpr(e *ast.AwaitExpr, want Type) Type {
	switch {
	case len(c.lits) > 0:
		if !c.lits[len(c.lits)-1].async {
			c.errorAt(e, "mark the closure async: async fn() { ... }", "await is only allowed in async closures")
		}
	case c.fn == nil || !c.fn.Async:
		hint := "mark the function async: async fn ..."
		if c.fn != nil && c.fn.Name == "main" && c.fn.Owner == nil && c.fn.Enum == nil {
			hint = "make main async: async fn main()"
		}
		c.errorAt(e, hint, "await is only allowed in async functions")
	}
	call, isCall := e.X.(*ast.CallExpr)
	if isCall {
		c.awaited = call
	}
	t := c.expr(e.X, want)
	c.awaited = nil
	if isCall {
		if async, _ := c.asyncCall(call); async {
			return t
		}
	}
	f, ok := t.(*Future)
	if !ok {
		if t != Invalid {
			c.errorAt(e.X, "remove await", "await needs a future or a call of an async function, found %s", t)
		}
		return Invalid
	}
	if f.Throws != nil {
		c.noteThrows(e, f.Throws, "the future can fail, so await needs try")
	}
	return f.Elem
}

// Checks a call of all, any or run.
func (c *checker) asyncBuiltin(e *ast.CallExpr, name string, want Type) Type {
	call := &Call{Kind: BuiltinCall, Builtin: name}
	c.info.Calls[e] = call
	if len(e.Args) != 1 || e.Args[0].Name != nil || e.Trailing != nil {
		c.checkArgs(e)
		hint := map[string]string{"all": "all([a, b])", "any": "any([a, b])", "run": "run(task())"}[name]
		c.errorAt(e.Fn, "write "+hint, "%s takes one argument", name)
		return Invalid
	}
	arg := e.Args[0].Value
	call.Args = []ast.Expr{arg}
	if name == "run" {
		if c.inAsync() {
			c.errorAt(e.Fn, "use await inside async functions", "run waits for the whole loop, so async functions can't use it")
		}
		var hint Type
		if want != nil {
			hint = &Future{Elem: want}
		}
		t := c.expr(arg, hint)
		f, ok := t.(*Future)
		if !ok {
			if t != Invalid {
				c.errorAt(arg, "pass the future an async call gives, e.g. run(load())", "run needs a future, found %s", t)
			}
			return Invalid
		}
		if f.Throws != nil {
			c.noteThrows(e, f.Throws, "the future can fail, so run needs try")
		}
		return f.Elem
	}
	t := c.expr(arg, nil)
	l, ok := t.(*List)
	f, isFuture := Type(nil), false
	if ok {
		f, isFuture = l.Elem.(*Future)
	}
	if !isFuture {
		if t != Invalid {
			c.errorAt(arg, "pass a list of futures, e.g. "+name+"([load(a), load(b)])", "%s needs a list of futures, found %s", name, t)
		}
		return Invalid
	}
	fut := f.(*Future)
	if name == "any" || fut.Elem == Void {
		return &Future{Elem: fut.Elem, Throws: fut.Throws}
	}
	return &Future{Elem: &List{Elem: fut.Elem}, Throws: fut.Throws}
}

// Reports whether the code being checked may await: the body of an async
// function or closure.
func (c *checker) inAsync() bool {
	if len(c.lits) > 0 {
		return c.lits[len(c.lits)-1].async
	}
	return c.fn != nil && c.fn.Async
}

// Reports whether the call [e] runs an async function or function value,
// and what it throws.
func (c *checker) asyncCall(e *ast.CallExpr) (bool, Type) {
	call := c.info.Calls[e]
	switch {
	case call == nil:
		return false, nil
	case call.Func != nil:
		return call.Func.Async, call.Func.Throws
	case call.Kind == ValueCall:
		if ft, ok := c.info.Types[call.Callee].(*FuncType); ok {
			return ft.Async, ft.Throws
		}
	}
	return false, nil
}

// Checks `time.timeout(future, ms)`, a future of the value, or nil when
// the time runs out first. A future of void gives whether it finished.
func (c *checker) timeoutCall(e *ast.CallExpr, want Type) Type {
	call := &Call{Kind: BuiltinCall, Builtin: "time.timeout"}
	c.info.Calls[e] = call
	if len(e.Args) != 2 || e.Args[0].Name != nil || e.Args[1].Name != nil || e.Trailing != nil {
		c.checkArgs(e)
		c.errorAt(e.Fn, "write time.timeout(load(), 5000)", "time.timeout takes a future and the milliseconds to wait")
		return Invalid
	}
	var hint Type
	if o, ok := want.(*Optional); ok {
		hint = &Future{Elem: o.Elem}
	}
	t := c.expr(e.Args[0].Value, hint)
	if ms := c.expr(e.Args[1].Value, Int); ms != Int && ms != Invalid {
		c.errorAt(e.Args[1].Value, "", "the milliseconds must be int, found %s", ms)
	}
	call.Args = []ast.Expr{e.Args[0].Value, e.Args[1].Value}
	f, ok := t.(*Future)
	switch {
	case t == Invalid:
		return Invalid
	case !ok:
		c.errorAt(e.Args[0].Value, "pass the future an async call gives, e.g. time.timeout(load(), 5000)", "time.timeout needs a future, found %s", t)
		return Invalid
	case f.Elem == Void:
		return &Future{Elem: Bool, Throws: f.Throws}
	}
	if _, ok := f.Elem.(*Optional); ok {
		c.errorAt(e.Args[0].Value, "wrap the value, e.g. in a record, to tell nil from a timeout", "time.timeout gives nil when the time runs out, so the future's value can't be optional itself")
		return Invalid
	}
	return &Future{Elem: &Optional{Elem: f.Elem}, Throws: f.Throws}
}
