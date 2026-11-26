package types

import (
	"fmt"

	"github.com/irshadio/vex-lang/compiler/ast"
)

// The signature of what a tear-off calls.
type tearSig struct {
	// Parameter names, used as argument labels, and types; the types are
	// nil when the expected function type gives them.
	names  []string
	types  []Type
	result Type
	throws Type
}

// Returns the signature of method [f] for a tear-off, or nil after
// reporting why it can't be torn off.
func (c *checker) funcSig(at ast.Node, f *Func) *tearSig {
	switch {
	case f.Async:
		c.errorAt(at, fmt.Sprintf("call it in a closure: fn() => %s()", f.Name), "async method '%s' can't be used as a value yet", f.Name)
		return nil
	case len(f.Decl.TypeParams) > 0:
		c.errorAt(at, fmt.Sprintf("call it in a closure: fn(...) => %s(...)", f.Name), "generic method '%s' can't be used as a value", f.Name)
		return nil
	}
	s := &tearSig{result: f.Result, throws: f.Throws}
	for _, p := range f.Params {
		s.names = append(s.names, p.Name)
		s.types = append(s.types, p.Type)
	}
	return s
}

// Returns the signature of a built-in member for a tear-off.
func memberSigOf(sig *memberSig) *tearSig {
	s := &tearSig{result: sig.result}
	for _, p := range sig.params {
		s.names = append(s.names, p.name)
		s.types = append(s.types, p.typ)
	}
	return s
}

// Returns the signature of creating struct [st] or case [cs].
func fieldsSig(fields []*Field, result Type) *tearSig {
	s := &tearSig{result: result}
	for _, f := range fields {
		s.names = append(s.names, f.Name)
		s.types = append(s.types, f.Type)
	}
	return s
}

// Checks `e`, a member used as a value: it stands for a closure that calls
// [callee] with its parameters. With a receiver type [recv], the closure
// calls the member on the value [e.X] had when the tear-off was made.
func (c *checker) tearOff(e *ast.MemberExpr, sig *tearSig, recv Type, callee func(x ast.Expr) ast.Expr, want Type) Type {
	at := e.Name.Span
	known := func(t Type) ast.TypeExpr {
		if t == nil {
			return nil
		}
		return &ast.KnownType{Span: at, Type: t}
	}
	id := func(name string) *ast.Ident { return &ast.Ident{Span: at, Name: name} }
	lit := &ast.FuncLit{Span: e.Span}
	call := &ast.CallExpr{Span: e.Span}
	x := e.X
	if recv != nil {
		x = id("recv")
	}
	call.Fn = callee(x)
	for i, name := range sig.names {
		arg := fmt.Sprintf("a%d", i)
		lit.Params = append(lit.Params, &ast.Param{Span: at, Name: id(arg), Type: known(sig.types[i])})
		call.Args = append(call.Args, &ast.Arg{Span: at, Name: id(name), Value: id(arg)})
	}
	if sig.result != Void {
		lit.Result = known(sig.result)
	}
	lit.ExprBody = call
	if sig.throws != nil {
		lit.Throws = &ast.Throws{Span: at, Type: known(sig.throws)}
		lit.ExprBody = &ast.TryExpr{Span: e.Span, X: call}
	}
	var value ast.Expr = lit
	if recv != nil {
		// Binds the receiver now: (fn(recv) => fn(...) => recv.m(...))(x).
		// For `x?.m`, the chain gives the value inside the optional.
		outer := &ast.FuncLit{Span: e.Span, Params: []*ast.Param{{Span: at, Name: id("recv"), Type: known(recv)}}, ExprBody: lit,
			Result: known(&FuncType{Params: sig.types, Result: sig.result, Throws: sig.throws})}
		value = &ast.CallExpr{Span: e.Span, Fn: outer, Args: []*ast.Arg{{Span: at, Value: &ast.KnownExpr{Span: ast.Span{Start: e.X.Pos(), End: e.X.EndOffset()}, X: e.X, Type: recv}}}}
	}
	c.info.TearOffs[e] = value
	return c.expr(value, want)
}

// Returns `x.name`, spelled at [at].
func memberAccess(x ast.Expr, name string, at ast.Span) ast.Expr {
	return &ast.MemberExpr{Span: at, X: x, Name: &ast.Ident{Span: at, Name: name}}
}

// Returns the generic struct [x] names, or nil.
func (c *checker) genStructRef(x ast.Expr) *genericStruct {
	id, ok := x.(*ast.Ident)
	if !ok {
		return nil
	}
	if id.Module != "" {
		m := c.imported(id.Module)
		if m == nil {
			return nil
		}
		g := m.genStructs[id.Name]
		if g != nil {
			c.private(id, m, g.decl.Pub, "struct '"+id.Name+"'")
		}
		return g
	}
	if c.lookupVar(id.Name) != nil {
		return nil
	}
	return c.genStructs[id.Name]
}

// Returns the type name [x], as written: `Box` or `geo.Box`.
func written(x ast.Expr) string {
	id := x.(*ast.Ident)
	if id.Module != "" {
		return id.Module + "." + id.Name
	}
	return id.Name
}

// Checks a generic constructor or case, [written], used as a value. The
// expected function type gives its type arguments.
func (c *checker) genericTearOff(e *ast.MemberExpr, names []string, want Type, written string, callee func(x ast.Expr) ast.Expr) Type {
	ft, ok := want.(*FuncType)
	if o, isOpt := want.(*Optional); isOpt {
		ft, ok = o.Elem.(*FuncType)
	}
	if !ok || len(ft.Params) != len(names) {
		c.errorAt(e.Name, fmt.Sprintf("give the type it is used as, e.g. let f: fn(...) -> ... = %s", written), "can't tell the type arguments of %s", written)
		return Invalid
	}
	return c.tearOff(e, &tearSig{names: names, types: make([]Type, len(names)), result: ft.Result}, nil, callee, want)
}
