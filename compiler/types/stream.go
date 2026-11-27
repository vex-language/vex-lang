package types

import (
	"github.com/vex-language/vex-lang/compiler/ast"
)

// Reports whether [name] is that of a built-in stream type.
func streamName(name string) bool { return name == "Stream" || name == "StreamController" }

// Returns the type written as `Stream<T>` or `StreamController<T>`.
func (c *checker) streamType(at ast.Node, name string, args []ast.TypeExpr) Type {
	if len(args) != 1 {
		c.errorAt(at, "write "+name+"<int>", "%s takes 1 type argument, found %d", name, len(args))
		return Invalid
	}
	elem := c.resolveType(args[0])
	if elem == Invalid {
		return Invalid
	}
	return &Stream{Elem: elem, Controller: name == "StreamController"}
}

// Checks `StreamController<T>()`, which makes a controller whose `stream`
// gives the values added to it.
func (c *checker) newController(e *ast.CallExpr, want Type) Type {
	c.info.Calls[e] = &Call{Kind: BuiltinCall, Builtin: "StreamController"}
	if len(e.Args) > 0 || e.Trailing != nil {
		c.checkArgs(e)
		c.errorAt(e.Fn, "write StreamController<int>()", "StreamController<T>() takes no arguments")
		return Invalid
	}
	if len(e.TypeArgs) == 0 {
		if s, ok := want.(*Stream); ok && s.Controller {
			return s
		}
		if want == Invalid {
			return Invalid
		}
		c.errorAt(e.Fn, "write its value type: StreamController<int>()", "can't tell what the stream carries")
		return Invalid
	}
	return c.streamType(e.Fn, "StreamController", e.TypeArgs)
}

// Checks `yield value`, which gives the next value of the stream of the
// enclosing stream function.
func (c *checker) yieldStmt(s *ast.YieldStmt) {
	switch {
	case len(c.lits) > 0:
		c.expr(s.Value, nil)
		c.errorAt(s, "yield in the stream function itself, or add to a StreamController", "yield can't be used inside a closure")
	case c.fn == nil || !c.fn.Streams():
		c.expr(s.Value, nil)
		c.errorAt(s, "declare an async fn that returns a Stream: async fn f() -> Stream<int>", "yield is only allowed in an async fn that returns a Stream")
	default:
		elem := c.fn.Result.(*Stream).Elem
		c.assignable(elem, c.expr(s.Value, elem), s.Value, "")
	}
}

// Checks a stream function's signature: it gives its values with yield,
// so it can't also throw.
func (c *checker) checkStreamFunc(f *Func) {
	if f.Throws != nil {
		c.errorAt(f.Decl.Throws, "catch errors inside it, or yield a result value", "a stream function can't throw yet")
	}
}

// Checks the header of `for await x in stream`.
func (c *checker) forAwait(s *ast.ForStmt) Type {
	if !c.inAsync() {
		hint := "mark the function async: async fn ..."
		if len(c.lits) > 0 {
			hint = "mark the closure async: async fn() { ... }"
		}
		c.errorAt(s, hint, "for await is only allowed in async functions")
	}
	if s.Index != nil {
		c.errorAt(s.Index, "write for await x in stream", "a stream has no index")
	}
	t := c.expr(s.Iter, nil)
	st, ok := t.(*Stream)
	switch {
	case t == Invalid:
		return Invalid
	case ok && st.Controller:
		c.errorAt(s.Iter, "read its stream: for await x in controller.stream", "for await needs a stream, found %s", t)
		return Invalid
	case !ok:
		c.errorAt(s.Iter, "use for x in items for lists, maps and ranges", "for await needs a stream, found %s", t)
		return Invalid
	}
	return st.Elem
}
