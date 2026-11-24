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

// An optional type: a value of [Elem], or nil.
type Optional struct {
	Elem Type
}

// Returns the type as written, such as `User?` or `(fn() -> int)?`.
func (o *Optional) String() string {
	if f, ok := o.Elem.(*FuncType); ok && f.Result != Void && f.Result != nil {
		return "(" + f.String() + ")?"
	}
	return o.Elem.String() + "?"
}

// An enum type: a value that is one of [Cases]. Like structs, enum values
// are references, but they never change after they are created.
type Enum struct {
	Name    string
	Cases   []*Case
	Methods []*Func
	Decl    *ast.EnumDecl
}

// Returns the enum's name.
func (e *Enum) String() string { return e.Name }

// Returns the case called [name], or nil.
func (e *Enum) Case(name string) *Case {
	for _, c := range e.Cases {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Returns the method called [name], or nil.
func (e *Enum) Method(name string) *Func {
	for _, m := range e.Methods {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// Reports whether no case carries values, so values can be compared with ==.
func (e *Enum) Simple() bool {
	for _, c := range e.Cases {
		if len(c.Fields) > 0 {
			return false
		}
	}
	return true
}

// An enum case and the values it carries.
type Case struct {
	Name   string
	Fields []*Field
	Enum   *Enum

	// The position of the case in its enum, used as its tag.
	Index int
	Decl  *ast.EnumCase
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

	// The struct or enum a method belongs to; both are nil for a
	// top-level function.
	Owner  *Struct
	Enum   *Enum
	Static bool
}

// Returns the signature, such as `fn add(a: int, b: int) -> int`.
func (f *Func) String() string {
	var b strings.Builder
	b.WriteString("fn ")
	if f.Owner != nil {
		b.WriteString(f.Owner.Name + ".")
	}
	if f.Enum != nil {
		b.WriteString(f.Enum.Name + ".")
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

	// A name bound by a match pattern.
	PatternVar
)

// The checked meaning of a match.
type Match struct {
	// The enum matched on, or nil for int, bool and string subjects.
	Enum *Enum

	// One entry per arm, in order.
	Arms []*MatchArm

	// Set when the match is used as a statement, so its arms may be blocks
	// and their values are dropped.
	Stmt bool
}

// The checked meaning of one match arm.
type MatchArm struct {
	// The case the arm matches, or nil for `_` and literals.
	Case *Case

	// The variable bound to each of the case's values; nil entries are
	// ignored with `_`.
	Binds []*Var

	// Set when the arm's expression never finishes (it calls panic), so
	// it doesn't take part in the match's type.
	NoValue bool
}

// Reports whether [a] and [b] are the same type. [Invalid] matches anything.
func Identical(a, b Type) bool {
	if a == Invalid || b == Invalid {
		return true
	}
	switch a := a.(type) {
	case *List:
		b, ok := b.(*List)
		return ok && Identical(a.Elem, b.Elem)
	case *Optional:
		b, ok := b.(*Optional)
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

	// Creating an enum value: `Shape.circle(radius: 1)`, `.rect(...)`.
	CaseCall
)

// The checked meaning of a call expression.
type Call struct {
	Kind CallKind

	// The function or method called, for FuncCall, MethodCall and StaticCall.
	Func *Func

	// The struct created, for ConstructorCall.
	Struct *Struct

	// The enum case created, for CaseCall.
	Case *Case

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

	// Values of a type used where its optional is expected, such as 5
	// passed as an int?; each is stored as a present value.
	Wraps map[ast.Expr]*Optional

	// Optional chains (`x?.name`, `x?.f()`). The value is true when the
	// member's own value must be wrapped in an optional, false when it
	// already is one or has no value.
	Chains map[ast.Expr]bool

	// Enum cases used as values without a call: `Shape.empty`, `.up`.
	CaseValues map[ast.Expr]*Case

	// What each match does.
	Matches map[*ast.MatchExpr]*Match

	// Enums in source order.
	Enums []*Enum

	// The variables each closure uses from outside itself, in first-use
	// order. A closure nested in another also lists what its inner
	// closures need, so it can pass them on.
	Captures map[*ast.FuncLit][]*Var
}
