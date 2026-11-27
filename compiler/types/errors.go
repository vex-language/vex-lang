package types

import (
	"fmt"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// The calls of a try being checked, and what they throw.
type tryCtx struct {
	errs []Type
}

// Declares the built-in `Error` struct, whose one field is its message.
func (c *checker) declareError() {
	e := &Struct{Name: "Error"}
	e.Fields = []*Field{{Name: "message", Type: String, Owner: e}}
	for _, m := range c.info.Modules {
		m.structs[e.Name] = e
	}
	c.info.Structs = append(c.info.Structs, e)
	c.info.Error = e
}

// Returns what a `throws` clause throws: Error, or the enum it names.
func (c *checker) throwsType(th *ast.Throws) Type {
	if th.Type == nil {
		return c.info.Error
	}
	switch t := c.resolveType(th.Type).(type) {
	case *Enum:
		return t
	case *Struct:
		if t == c.info.Error {
			return t
		}
		c.errorAt(th.Type, "declare an enum of the errors, e.g. enum LoadError { notFound, denied }", "throws(...) takes an enum, found struct %s", t.Name)
	}
	return c.info.Error
}

// Returns what the enclosing function or closure throws, and how errors
// name it.
func (c *checker) curThrows() (Type, string) {
	if c.inDefer {
		return nil, "a defer block"
	}
	if len(c.lits) > 0 {
		ctx := c.lits[len(c.lits)-1]
		return ctx.throws, ctx.name
	}
	if c.fn != nil {
		return c.fn.Throws, c.fn.Name
	}
	return nil, ""
}

// Records what the call [e] throws, which a try around it must handle.
func (c *checker) checkThrows(e *ast.CallExpr) {
	call := c.info.Calls[e]
	if call == nil {
		return
	}
	var thrown Type
	name := "this function"
	switch {
	case call.Func != nil:
		thrown, name = call.Func.Throws, call.Func.Name
	case call.Kind == ValueCall:
		if ft, ok := c.info.Types[call.Callee].(*FuncType); ok {
			thrown = ft.Throws
		}
	}
	if thrown == nil {
		return
	}
	c.noteThrows(e, thrown, fmt.Sprintf("%s can throw, so the call needs try", name))
}

// Records that [e] throws [thrown], which a try around it must handle,
// reporting [msg] when there is none.
func (c *checker) noteThrows(e ast.Expr, thrown Type, msg string) {
	c.info.Throwing[e] = thrown
	if len(c.tries) == 0 {
		c.errorAt(e, "write try before it to pass the error up, try ... catch to handle it, or try? to get nil", "%s", msg)
		return
	}
	ctx := c.tries[len(c.tries)-1]
	ctx.errs = append(ctx.errs, thrown)
}

// Checks a try. [stmt] is set when its value isn't used.
func (c *checker) tryExpr(e *ast.TryExpr, want Type, stmt bool) Type {
	hint := want
	if o, ok := want.(*Optional); ok && e.Optional {
		hint = o.Elem
	}
	ctx := &tryCtx{}
	c.tries = append(c.tries, ctx)
	t := c.expr(e.X, hint)
	c.tries = c.tries[:len(c.tries)-1]
	info := &Try{}
	c.info.Tries[e] = info
	if len(ctx.errs) == 0 && t != Invalid {
		c.errorAt(e, "remove try", "nothing after try can throw")
		return t
	}

	switch {
	case e.Optional:
		switch t.(type) {
		case *Optional:
			return t
		}
		if t == Invalid {
			return Invalid
		}
		if t == Void {
			c.errorAt(e, "use try ... catch { } to ignore the error", "try? needs a call that returns a value")
			return Invalid
		}
		return &Optional{Elem: t}

	case e.Catch != nil:
		target := c.catchType(ctx.errs)
		info.Target = target
		c.push()
		if e.Catch.Name != nil {
			info.Bind = c.declare(e.Catch.Name, target, false, LocalVar)
			if n := len(c.locals); n > 0 && c.locals[n-1].v == info.Bind {
				c.locals[n-1].hint = "use it, or leave the name out: catch { ... }"
			}
		}
		body := e.Catch.Body
		if stmt || t == Void || t == Invalid {
			c.stmts(body.Stmts)
		} else {
			last := len(body.Stmts) - 1
			c.stmts(body.Stmts[:max(last, 0)])
			es, ok := ast.Stmt(nil), false
			if last >= 0 {
				es = body.Stmts[last]
				_, ok = es.(*ast.ExprStmt)
			}
			noValue := func() {
				c.errorAtPos(c.file.PosOf(body.End-1), 1, fmt.Sprintf("end it with %s value, or with return, break, continue, throw or panic(...)", article(t)),
					"the catch block must give a value of type %s", t)
			}
			switch {
			case ok && !IsPanic(es.(*ast.ExprStmt).X):
				x := es.(*ast.ExprStmt).X
				if vt := c.expr(x, t); vt == Void {
					noValue()
				} else {
					c.assignable(t, vt, x, "the value of catch")
				}
				info.Value = true
			case es != nil:
				c.stmt(es)
				fallthrough
			default:
				if !leaves(body) {
					noValue()
				}
			}
		}
		c.pop()
		return t
	}

	th, where := c.curThrows()
	info.Target = th
	switch {
	case th == nil && where == "":
		if c.constOwner != nil && c.constOwner.Lazy {
			c.errorAt(e, "use try? or try ... catch", "a starting value can't pass errors up")
		} else {
			c.errorAt(e, "use try? or try ... catch", "default values can't pass errors up")
		}
	case th == nil && where == "the closure":
		c.errorAt(e, "handle it with catch, use try?, or declare the closure fn(...) throws", "the error can't pass up: the closure doesn't throw")
	case th == nil && c.inInit():
		c.errorAt(e, "handle it with catch or try?, or create the struct in a static fn that throws", "the error can't pass up: init can't throw")
	case th == nil:
		c.errorAt(e, fmt.Sprintf("handle it with catch, use try?, or declare fn %s(...) throws", where), "the error can't pass up: %s doesn't throw", where)
	default:
		if en, ok := th.(*Enum); ok {
			for _, err := range ctx.errs {
				if !Identical(err, en) {
					c.errorAt(e, "catch it here", "this call throws %s, but %s can only throw %s", errorName(err), where, en.Name)
					break
				}
			}
		}
	}
	return t
}

// Returns the type a catch receives: the enum all the calls throw, or Error
// when they throw Error or different enums.
func (c *checker) catchType(errs []Type) Type {
	if len(errs) == 0 {
		return c.info.Error
	}
	first, ok := errs[0].(*Enum)
	if !ok {
		return c.info.Error
	}
	for _, e := range errs[1:] {
		if e != first {
			return c.info.Error
		}
	}
	return first
}

func errorName(t Type) string { return t.String() }

func (c *checker) throwStmt(s *ast.ThrowStmt) {
	th, where := c.curThrows()
	if c.inDefer {
		c.expr(s.Value, nil)
		c.errorAt(s, "a defer block runs as its block ends, so it can't throw", "throw can't be used in a defer block")
		return
	}
	if th == nil {
		c.expr(s.Value, nil)
		hint := "declare the closure fn(...) throws"
		switch {
		case c.inInit():
			c.errorAt(s, "check with assert, or create the struct in a static fn that throws", "init can't throw")
			return
		case where != "the closure":
			hint = fmt.Sprintf("declare fn %s(...) throws", where)
		}
		c.errorAt(s, hint, "throw can only be used in a function that throws")
		return
	}
	t := c.expr(s.Value, th)
	if en, ok := t.(*Enum); ok && th == c.info.Error {
		c.errorAt(s.Value, fmt.Sprintf("declare it throws(%s), or throw Error(\"...\")", en.Name), "%s throws Error, not %s", where, en.Name)
		return
	}
	c.assignable(th, t, s.Value, "")
}
