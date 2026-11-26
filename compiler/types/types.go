// Package types checks a Vex syntax tree and works out the type of every
// expression.
package types

import (
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/diag"
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

// A map type such as `[string: int]`. Maps are reference types and keep
// the order their keys were added in.
type Map struct{ Key, Value Type }

// Returns the type in source form, such as `[string: int]`.
func (m *Map) String() string { return "[" + m.Key.String() + ": " + m.Value.String() + "]" }

// An interface: the methods a struct promises to have. Interface values
// hold any struct that implements it, and calls go through a table of the
// struct's methods.
type Interface struct {
	Name   string
	Module *Module

	// The methods, getters and setters, in declaration order; a member's
	// place is its slot in the table. Members with a body are defaults.
	Methods []*Func
	Decl    *ast.InterfaceDecl

	// The interfaces it extends directly.
	Supers []*Interface

	// The interface's position in [Info.Interfaces].
	Index int

	// For an instance of a generic interface such as `Store<int>`: the
	// generic interface's name and the type arguments.
	Base string
	Args []Type

	generic  *genericIface
	abstract bool
	resolved bool
}

// Returns [i] and every interface it extends, directly or not.
func (i *Interface) All() []*Interface {
	var out []*Interface
	var add func(j *Interface)
	add = func(j *Interface) {
		for _, k := range out {
			if k == j {
				return
			}
		}
		out = append(out, j)
		for _, s := range j.Supers {
			add(s)
		}
	}
	add(i)
	return out
}

// Reports whether a value of interface [i] can be used as interface [j]:
// [j] is [i] or one it extends.
func (i *Interface) Extends(j *Interface) bool {
	for _, k := range i.All() {
		if k == j {
			return true
		}
	}
	return false
}

// Returns the interface's name.
func (i *Interface) String() string { return qualified(i.Module, i.Name) }

// Returns [name] prefixed with its module unless that is the root module.
func qualified(m *Module, name string) string {
	if m == nil || m.Index == 0 {
		return name
	}
	return m.Name + "." + name
}

// Returns the method called [name], or nil.
func (i *Interface) Method(name string) *Func { return memberOf(i.Methods, name, ast.Method) }

// Returns the property called [name] that [i] itself requires, or nil.
func (i *Interface) Property(name string) *Property {
	return propertyOf(i.Methods, name)
}

// Returns the member of [ms] called [name] of [kind], or nil.
func memberOf(ms []*Func, name string, kind ast.Accessor) *Func {
	for _, m := range ms {
		if m.Name == name && m.Decl.Accessor == kind {
			return m
		}
	}
	return nil
}

// Returns the property in [ms] called [name], or nil when there is
// neither a getter nor a setter of that name.
func propertyOf(ms []*Func, name string) *Property {
	p := &Property{Get: memberOf(ms, name, ast.Getter), Set: memberOf(ms, name, ast.Setter)}
	if p.Get == nil && p.Set == nil {
		return nil
	}
	return p
}

// Returns the slot of [m] in the interface's method table.
func (i *Interface) Slot(m *Func) int {
	for n, x := range i.Methods {
		if x == m {
			return n
		}
	}
	return -1
}

// A type parameter of a generic function or struct, as seen while its
// body is checked. [Constraints] name what the type must support.
type TypeParam struct {
	Name        string
	Constraints []string

	// The interfaces the type must implement.
	Ifaces []*Interface
}

// Returns the parameter's name.
func (t *TypeParam) String() string { return t.Name }

// Reports whether the parameter guarantees [constraint]. Comparable and
// Hashable types can also be compared with `==`.
func (t *TypeParam) Has(constraint string) bool {
	for _, c := range t.Constraints {
		if c == constraint || constraint == "Equatable" && (c == "Comparable" || c == "Hashable") {
			return true
		}
	}
	return false
}

// A function type such as `fn(int) -> bool`.
type FuncType struct {
	Params []Type
	Result Type

	// What the function throws: the built-in Error struct, an enum, or nil
	// when it doesn't throw.
	Throws Type
}

// Returns the type in source form, such as `fn(int) -> bool`.
func (f *FuncType) String() string {
	params := make([]string, len(f.Params))
	for i, p := range f.Params {
		params[i] = p.String()
	}
	s := "fn(" + strings.Join(params, ", ") + ")" + throwsString(f.Throws)
	if f.Result != Void && f.Result != nil {
		s += " -> " + f.Result.String()
	}
	return s
}

// Returns ` throws`, ` throws(E)` or "" for what a function throws.
func throwsString(t Type) string {
	switch t := t.(type) {
	case nil:
		return ""
	case *Enum:
		return " throws(" + t.Name + ")"
	}
	return " throws"
}

// A struct type. Structs are reference types: copies share the same fields.
type Struct struct {
	Name    string
	Module  *Module
	Fields  []*Field
	Methods []*Func
	Decl    *ast.StructDecl

	// For an instance of a generic struct such as `Box<int>`: the generic
	// struct's name and the type arguments. Name is then `Box<int>`.
	Base string
	Args []Type

	// The interfaces the struct implements.
	Interfaces []*Interface

	// The struct's static constants.
	Consts []*Const

	generic *genericStruct

	// Set for instances whose arguments are type parameters. They are only
	// used while checking generic code and are never compiled.
	abstract bool
}

// Returns the struct's name.
func (s *Struct) String() string { return qualified(s.Module, s.Name) }

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
func (s *Struct) Method(name string) *Func { return s.member(name, ast.Method) }

// Returns the operator called [name], such as `+`, `unary -` or `[]`, or
// nil.
func (s *Struct) Operator(name string) *Func { return s.member(name, ast.Operator) }

// Returns the property called [name], or nil when the struct has neither a
// getter nor a setter of that name.
func (s *Struct) Property(name string) *Property { return propertyOf(s.Methods, name) }

func (s *Struct) member(name string, kind ast.Accessor) *Func { return memberOf(s.Methods, name, kind) }

// A C pointer type declared with `extern type`. Vex passes its values
// around but never looks inside them.
type Handle struct {
	Name   string
	Module *Module
	Decl   *ast.ExternTypeDecl

	// The C name it is shown with: [Decl]'s `as` name, or [Name].
	CName string
}

// Returns the type's name.
func (h *Handle) String() string { return qualified(h.Module, h.Name) }

// The future result of async work: a value of [Elem] once it is done, or
// the error it throws, of type [Throws] (nil when it can't fail).
type Future struct {
	Elem   Type
	Throws Type
}

// Returns the type as written: `Future<int>` or `Future<int, Error>`.
func (f *Future) String() string {
	if f.Throws != nil {
		return "Future<" + f.Elem.String() + ", " + f.Throws.String() + ">"
	}
	return "Future<" + f.Elem.String() + ">"
}

// A channel that carries values of [Elem] between isolates.
type Channel struct {
	Elem Type
}

func (c *Channel) String() string { return "Channel<" + c.Elem.String() + ">" }

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
	Module  *Module
	Cases   []*Case
	Methods []*Func
	Decl    *ast.EnumDecl

	// For an instance of a generic enum such as `Result<int>`: the generic
	// enum's name and the type arguments. Name is then `Result<int>`.
	Base string
	Args []Type

	// The interfaces it implements, including those they extend.
	Interfaces []*Interface

	// The enum's static constants.
	Consts []*Const

	generic *genericEnum

	// Set for instances whose arguments are type parameters, used only
	// while checking generic code.
	abstract bool
}

// Returns the enum's name.
func (e *Enum) String() string { return qualified(e.Module, e.Name) }

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
func (e *Enum) Method(name string) *Func { return memberOf(e.Methods, name, ast.Method) }

// Returns the method or getter called [name], or nil.
func (e *Enum) member(name string) *Func {
	for _, m := range e.Methods {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// Returns the getter called [name], as a property, or nil.
func (e *Enum) Property(name string) *Property { return propertyOf(e.Methods, name) }

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

	// The field's `didSet` block, or nil.
	DidSet *Func
}

// A computed property of a struct: its getter and setter, either of which
// may be nil.
type Property struct {
	Get, Set *Func
}

// Returns the property's type: the getter's result or the setter's value.
func (p *Property) Type() Type {
	if p.Get != nil {
		return p.Get.Result
	}
	return p.Set.Params[0].Type
}

// A function, method or static method.
type Func struct {
	Name   string
	Module *Module
	Params []*Param
	Result Type
	Decl   *ast.FuncDecl

	// What the function throws: the built-in Error struct, an enum, or nil
	// when it doesn't throw.
	Throws Type

	// The struct or enum a method belongs to; both are nil for a
	// top-level function.
	Owner  *Struct
	Enum   *Enum
	Static bool

	// The type arguments of an instance of a generic function, such as
	// [int] for `first<int>`.
	TypeArgs []Type

	// The interface a method belongs to. Only methods with a body, the
	// defaults, are compiled; they receive `self` as the interface.
	Iface *Interface

	// Set for `async fn`.
	Async bool

	// The module of the extension that declares this member, or nil.
	Ext *Module

	// The built-in type an extension adds this member to, such as
	// `string`; it is passed as `self`.
	Recv Type

	// For extern and export functions, the C types of the parameters and
	// the result where they differ from the usual ones: a sized number
	// such as `int32_t` for an `int32` in the signature, or "".
	CParams []string
	CResult string

	// For a method with its own type parameters, as seen by generic code:
	// the parameters and the instances. [TypeArgs] is then the parameters.
	gen *genericMethod
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
	b.WriteString(")" + throwsString(f.Throws))
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

// The checked meaning of a [ast.TryExpr].
type Try struct {
	// The type the try holds its error as: the error type of the enclosing
	// function when the error passes up, or the catch's error type. Calls
	// throwing an enum convert it to an Error when this is Error. Nil for
	// `try?`.
	Target Type

	// The catch's error variable, if it names one.
	Bind *Var

	// Set when the catch block's last statement gives the try's value.
	Value bool
}

// The checked meaning of a match.
type Match struct {
	// One entry per arm, in order.
	Arms []*MatchArm

	// Set when the match is used as a statement, so its arms may be blocks
	// and their values are dropped.
	Stmt bool
}

// The checked meaning of one match arm.
type MatchArm struct {
	// The variables the arm's pattern binds.
	Vars []*Var

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
	case *Map:
		b, ok := b.(*Map)
		return ok && Identical(a.Key, b.Key) && Identical(a.Value, b.Value)
	case *Optional:
		b, ok := b.(*Optional)
		return ok && Identical(a.Elem, b.Elem)
	case *Channel:
		b, ok := b.(*Channel)
		return ok && Identical(a.Elem, b.Elem)
	case *Record:
		b, ok := b.(*Record)
		if !ok || !sameShape(a, b) {
			return false
		}
		bt := b.FieldTypes()
		for i, t := range a.FieldTypes() {
			if !Identical(t, bt[i]) {
				return false
			}
		}
		return true
	case *Future:
		b, ok := b.(*Future)
		return ok && Identical(a.Elem, b.Elem) && (a.Throws == nil) == (b.Throws == nil) && (a.Throws == nil || Identical(a.Throws, b.Throws))
	case *FuncType:
		b, ok := b.(*FuncType)
		if !ok || len(a.Params) != len(b.Params) || !Identical(a.Result, b.Result) {
			return false
		}
		if (a.Throws == nil) != (b.Throws == nil) || a.Throws != nil && !Identical(a.Throws, b.Throws) {
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

	// A call of an interface method, through the method table of the
	// receiver. Recv is nil for a call on `self` in a default method.
	InterfaceCall
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

// A name that means a member of `self` inside an extension of a built-in
// type, such as `len` for `self.len`; [Expr] is that member access.
type SelfMember struct {
	Expr *ast.MemberExpr
}

// The checked meaning of a member access that isn't a call.
type Member struct {
	// The field read, or nil for a built-in property such as `.len`.
	Field *Field

	// The computed property read or assigned, or nil.
	Property *Property

	// The built-in property name, such as `len`.
	Builtin string

	// The record field read: `$1` or a name.
	RecordField string

	// The constant read: `Circle.pi` or `math.pi`.
	Const *Const
}

// Everything the checker learned about a file, for later compiler stages.
type Info struct {
	// The modules of the program, the root first.
	Modules []*Module

	// The source files, and the file of each declaration and of each
	// default value; empty for a program of one file.
	Files  []*diag.File
	FileOf map[ast.Node]int

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
	Structs    []*Struct
	Funcs      []*Func
	Interfaces []*Interface

	// The constants, each after the ones its value uses.
	Consts []*Const

	// The [Func] for each declaration, including methods.
	FuncOf map[*ast.FuncDecl]*Func

	// Values of a type used where its optional is expected, such as 5
	// passed as an int?; each is stored as a present value.
	Wraps map[ast.Expr]*Optional

	// Names of functions that don't throw, used as values where a
	// throwing function is expected.
	ThrowingRefs map[ast.Expr]bool

	// The elements of collection literals with a body and of builder
	// blocks: each expression statement that adds an element, mapped to
	// the [ast.ListLit] or [ast.MapLit] it adds to.
	Elements map[*ast.ExprStmt]ast.Expr

	// Assignments to a field with a `didSet` block, and the block to run
	// after each.
	Observers map[*ast.AssignStmt]*Func

	// Optional chains (`x?.name`, `x?.f()`). The value is true when the
	// member's own value must be wrapped in an optional, false when it
	// already is one or has no value.
	Chains map[ast.Expr]bool

	// Methods, static methods, constructors and enum cases used as values
	// without a call, each with the closure it stands for.
	TearOffs map[*ast.MemberExpr]ast.Expr

	// Operators a struct defines, used by binary and unary expressions,
	// indexing and compound assignments. For an index assignment, the
	// `[]` operator that reads the old value of a compound assignment.
	Operators map[ast.Node]*Func

	// The `[]=` operator of each index assignment to a struct.
	IndexSets map[*ast.IndexExpr]*Func

	// Enum cases used as values without a call: `Shape.empty`, `.up`.
	CaseValues map[ast.Expr]*Case

	// What each match does.
	Matches map[*ast.MatchExpr]*Match

	// The type of value each pattern tests.
	PatternTypes map[ast.Pattern]Type

	// Enum cases named by patterns such as `empty` or `circle(r)`.
	PatternCases map[ast.Pattern]*Case

	// Structs named by patterns such as `Point(:x, :y)`.
	PatternStructs map[*ast.CasePattern]*Struct

	// The variables bound by the pattern of each `let`, `for` and `if
	// case`.
	PatternVars map[ast.Pattern][]*Var

	// Enums in source order.
	Enums []*Enum

	// The built-in Error struct, with its one field `message`.
	Error *Struct

	// Calls of functions that can throw, and awaits of futures that can
	// fail, with what each throws.
	Throwing map[ast.Expr]Type

	// Calls of async functions that aren't awaited, which start the call
	// as a task and give its future.
	Spawns map[*ast.CallExpr]bool

	// How each try handles errors.
	Tries map[*ast.TryExpr]*Try

	// The variables each closure uses from outside itself, in first-use
	// order. A closure nested in another also lists what its inner
	// closures need, so it can pass them on.
	Captures map[*ast.FuncLit][]*Var
}
