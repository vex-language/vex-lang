package types

import (
	"github.com/vex-language/vex-lang/compiler/ast"
)

// Returns the type written as `Channel<T>`, or created by `Channel<T>()`.
func (c *checker) channelType(at ast.Node, args []ast.TypeExpr) Type {
	if len(args) != 1 {
		c.errorAt(at, "write Channel<int>", "Channel takes 1 type argument, found %d", len(args))
		return Invalid
	}
	elem := c.resolveType(args[0])
	if elem == Invalid {
		return Invalid
	}
	if bad := c.unsendable(elem); bad != "" {
		c.errorAt(args[0], "send plain values: numbers, strings, lists, maps, structs, enums and channels", "a channel can't carry %s: %s", elem, uncopyable(elem, bad))
		return Invalid
	}
	return &Channel{Elem: elem}
}

// Checks `Channel<T>()`, which makes a new channel.
func (c *checker) newChannel(e *ast.CallExpr, want Type) Type {
	c.info.Calls[e] = &Call{Kind: BuiltinCall, Builtin: "Channel"}
	if len(e.Args) > 0 || e.Trailing != nil {
		c.checkArgs(e)
		c.errorAt(e.Fn, "write Channel<int>()", "Channel<T>() takes no arguments")
		return Invalid
	}
	if len(e.TypeArgs) == 0 {
		if ch, ok := want.(*Channel); ok {
			return ch
		}
		if want == Invalid {
			return Invalid
		}
		c.errorAt(e.Fn, "write its value type: Channel<int>()", "can't tell what the channel carries")
		return Invalid
	}
	return c.channelType(e.Fn, e.TypeArgs)
}

// Explains why [t], which holds the type [bad], can't be copied to another
// isolate.
func uncopyable(t Type, bad string) string {
	if t.String() == bad {
		return "it can't be copied to another isolate"
	}
	return "it holds " + bad + ", which can't be copied to another isolate"
}

// Returns, for a value of [t] that can't be copied to another isolate,
// the type inside it that stops it; empty when it can be.
func (c *checker) unsendable(t Type) string {
	return unsendable(t, map[Type]bool{})
}

func unsendable(t Type, seen map[Type]bool) string {
	if seen[t] {
		return ""
	}
	seen[t] = true
	switch t := t.(type) {
	case *List:
		return unsendable(t.Elem, seen)
	case *Map:
		if bad := unsendable(t.Key, seen); bad != "" {
			return bad
		}
		return unsendable(t.Value, seen)
	case *Set:
		return unsendable(t.Elem, seen)
	case *Optional:
		return unsendable(t.Elem, seen)
	case *Channel:
		return ""
	case *Stream:
		return t.String()
	case *Record:
		for _, f := range t.FieldTypes() {
			if bad := unsendable(f, seen); bad != "" {
				return bad
			}
		}
		return ""
	case *Struct:
		for _, f := range t.Fields {
			if bad := unsendable(f.Type, seen); bad != "" {
				return bad
			}
		}
		return ""
	case *Enum:
		for _, cs := range t.Cases {
			for _, f := range cs.Fields {
				if bad := unsendable(f.Type, seen); bad != "" {
					return bad
				}
			}
		}
		return ""
	}
	switch t {
	case Int, Float, Bool, String, Range, Bytes, Void, Invalid:
		return ""
	}
	return t.String()
}

// Checks `isolate.run(f)` and `isolate.spawn(f)`. The function runs on a
// new isolate, so it must be a closure or a top-level function, and what
// it uses, gives and throws must be copyable there.
func (c *checker) isolateCall(e *ast.CallExpr, name string, want Type) Type {
	call := &Call{Kind: BuiltinCall, Builtin: "isolate." + name}
	c.info.Calls[e] = call
	example := "isolate." + name + "(fn() -> int { return work() })"
	if name == "spawn" {
		example = "isolate.spawn(fn() { work() })"
	}
	var arg ast.Expr
	switch {
	case len(e.Args) == 1 && e.Args[0].Name == nil && e.Trailing == nil:
		arg = e.Args[0].Value
	case len(e.Args) == 0 && e.Trailing != nil:
		arg = trailingLit(e, &FuncType{})
	default:
		c.checkArgs(e)
		c.errorAt(e.Fn, "write "+example, "isolate.%s takes one function", name)
		return Invalid
	}
	call.Args = []ast.Expr{arg}
	hint := &FuncType{}
	if f, ok := want.(*Future); ok && name == "run" {
		hint.Result = f.Elem
	}
	lit, isLit := arg.(*ast.FuncLit)
	if !isLit {
		id, ok := arg.(*ast.Ident)
		f := (*Func)(nil)
		if ok && id.Module == "" && c.lookupVar(id.Name) == nil {
			f = c.funcs[id.Name]
		}
		if f == nil {
			c.expr(arg, hint)
			c.errorAt(arg, "write "+example, "isolate.%s needs a closure or the name of a function, since it copies what the function uses", name)
			return Invalid
		}
	}
	t := c.expr(arg, hint)
	ft, ok := t.(*FuncType)
	switch {
	case t == Invalid:
		return Invalid
	case !ok || len(ft.Params) > 0:
		c.errorAt(arg, "write "+example, "isolate.%s needs a function that takes no parameters, found %s", name, t)
		return Invalid
	}
	if isLit {
		for _, v := range c.info.Captures[lit] {
			if bad := c.unsendable(v.Type); bad != "" {
				what := "'" + v.Name + "'"
				if v.Kind == SelfVar {
					what = "self"
				}
				c.errorAt(arg, "copy the values it needs into plain variables first", "the closure uses %s, %s: %s", what, article(v.Type), uncopyable(v.Type, bad))
				return Invalid
			}
		}
	}
	if name == "spawn" {
		if ft.Throws != nil {
			c.errorAt(arg, "handle its errors inside it with try ... catch", "the function given to isolate.spawn can't throw, since nothing receives its error")
		}
		return Void
	}
	for _, r := range []Type{ft.Result, ft.Throws} {
		if r == nil {
			continue
		}
		if bad := c.unsendable(r); bad != "" {
			c.errorAt(arg, "return plain values: numbers, strings, lists, maps, structs, enums and channels", "isolate.run can't give back %s: %s", r, uncopyable(r, bad))
			return Invalid
		}
	}
	return &Future{Elem: ft.Result, Throws: ft.Throws}
}
