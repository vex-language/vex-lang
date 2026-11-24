// Package types checks a Vex syntax tree and works out the type of every
// expression.
package types

import (
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
)

// A Vex type.
type Type interface {
	String() string
}

// A built-in scalar type, or void.
type Basic struct{ name string }

// Returns the type's name, such as `int`.
func (b *Basic) String() string { return b.name }

var (
	Int    = &Basic{"int"}
	Float  = &Basic{"float"}
	Bool   = &Basic{"bool"}
	String = &Basic{"string"}

	// The result type of a function that returns nothing.
	Void = &Basic{"void"}

	// The type of an expression that already has an error. It matches every
	// type, so one mistake doesn't cause a chain of follow-up errors.
	Invalid = &Basic{"invalid"}
)

// A list type such as `[int]`.
type List struct{ Elem Type }

// Returns the type in source form, such as `[int]`.
func (l *List) String() string { return "[" + l.Elem.String() + "]" }

// A function type such as `fn(int) -> bool`.
type FuncType struct {
	Params []Type
	Result Type
}

// Returns the type in source form, such as `fn(int) -> bool`.
func (f *FuncType) String() string {
	params := make([]string, len(f.Params))
	for i, p := range f.Params {
		params[i] = p.String()
	}
	s := "fn(" + strings.Join(params, ", ") + ")"
	if f.Result != Void && f.Result != nil {
		s += " -> " + f.Result.String()
	}
	return s
}

// A struct type. Structs are reference types: copies share the same fields.
type Struct struct {
	Name    string
	Fields  []*Field
	Methods []*Func
	Decl    *ast.StructDecl
}

// Returns the struct's name.
func (s *Struct) String() string { return s.Name }

// Returns the field called [name], or nil.
func (s *Struct) Field(name string) *Field {
	for _, f := range s.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// Returns the method called [name], or nil.
func (s *Struct) Method(name string) *Func {
	for _, m := range s.Methods {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// A struct field.
type Field struct {
	Name    string
	Type    Type
	Default ast.Expr
	Owner   *Struct
	Decl    *ast.Field
}

// A function, method or static method.
type Func struct {
	Name   string
	Params []*Param
	Result Type
	Decl   *ast.FuncDecl

	// The struct a method belongs to, or nil for a top-level function.
	Owner  *Struct
	Static bool
}

// Returns the signature, such as `fn add(a: int, b: int) -> int`.
func (f *Func) String() string {
	var b strings.Builder
	b.WriteString("fn ")
	if f.Owner != nil {
		b.WriteString(f.Owner.Name + ".")
	}
	b.WriteString(f.Name + "(")
	for i, p := range f.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name + ": " + p.Type.String())
	}
	b.WriteString(")")
	if f.Result != Void {
		b.WriteString(" -> " + f.Result.String())
	}
	return b.String()
}

// A function parameter.
type Param struct {
	Name    string
	Type    Type
	Default ast.Expr
}

// A local variable, parameter, loop variable or `self`.
type Var struct {
	Name    string
	Type    Type
	Mutable bool
	Kind    VarKind
	Decl    ast.Node

	// Set when a closure uses the variable.
	Captured bool

	// The closure the variable is declared in, or nil for a function body.
	lit *ast.FuncLit
}

// Reports whether the variable must live on the heap, so that closures
// and the enclosing function share changes to it.
func (v *Var) Boxed() bool { return v.Captured && v.Mutable }

// What declared a [Var].
type VarKind uint8

const (
	LocalVar VarKind = iota
	ParamVar
	LoopVar
	SelfVar
)

// Reports whether [a] and [b] are the same type. [Invalid] matches anything.
func Identical(a, b Type) bool {
	if a == Invalid || b == Invalid {
		return true
	}
	switch a := a.(type) {
	case *List:
		b, ok := b.(*List)
		return ok && Identical(a.Elem, b.Elem)
	case *FuncType:
		b, ok := b.(*FuncType)
		if !ok || len(a.Params) != len(b.Params) || !Identical(a.Result, b.Result) {
			return false
		}
		for i := range a.Params {
			if !Identical(a.Params[i], b.Params[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

// Reports whether values of type [t] can be printed and interpolated.
func printable(t Type) bool { return t != Void }

// Reports whether [t] is int or float.
func numeric(t Type) bool { return t == Int || t == Float || t == Invalid }

// How a call is carried out.
type CallKind uint8

const (
	// A top-level function: `add(1, 2)`.
	FuncCall CallKind = iota

	// A method on a value: `user.greet()`, or `greet()` inside a method.
	MethodCall

	// A static method: `User.guest()`.
	StaticCall

	// Creating a struct: `User(name: "Ann")`.
	ConstructorCall

	// A built-in function: `println(x)`.
	BuiltinCall

	// A built-in member of a list or string: `nums.push(4)`.
	MemberBuiltinCall

	// A call of a function value: `onTap()`, `handlers[0](x)`.
	ValueCall
)

// The checked meaning of a call expression.
type Call struct {
	Kind CallKind

	// The function or method called, for FuncCall, MethodCall and StaticCall.
	Func *Func

	// The struct created, for ConstructorCall.
	Struct *Struct

	// The built-in name, such as `println` or `push`.
	Builtin string

	// The function value called, for ValueCall.
	Callee ast.Expr

	// The receiver of a method or member built-in; nil for a method called
	// on the implicit `self` inside another method.
	Recv ast.Expr

	// The arguments in parameter (or field) order, with defaults filled in.
	Args []ast.Expr
}

// The checked meaning of a member access that isn't a call.
type Member struct {
	// The field read, or nil for a built-in property such as `.len`.
	Field *Field

	// The built-in property name, such as `len`.
	Builtin string
}

// Everything the checker learned about a file, for later compiler stages.
type Info struct {
	// The type of every expression.
	Types map[ast.Expr]Type

	// What each identifier in an expression refers to: a [*Var], a
	// [*Field] or [*Func] of the implicit `self`, or a top-level [*Func]
	// used as a value.
	Uses map[*ast.Ident]any

	// The variable each declaration introduces (let, var, params, loops).
	Defs map[*ast.Ident]*Var

	Calls   map[*ast.CallExpr]*Call
	Members map[*ast.MemberExpr]*Member

	// The `self` variable of each method.
	Selves map[*ast.FuncDecl]*Var

	// Declarations in source order.
	Structs []*Struct
	Funcs   []*Func

	// The [Func] for each declaration, including methods.
	FuncOf map[*ast.FuncDecl]*Func

	// The variables each closure uses from outside itself, in first-use
	// order. A closure nested in another also lists what its inner
	// closures need, so it can pass them on.
	Captures map[*ast.FuncLit][]*Var
}
