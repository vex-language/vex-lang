// Package ast defines the syntax tree of a Vex source file.
package ast

import (
	"github.com/irshadio/vex-lang/compiler/diag"
	"github.com/irshadio/vex-lang/compiler/lexer"
)

// The source range covered by a node.
type Span struct {
	Start diag.Pos

	// The byte offset just after the node.
	End int
}

// Returns where the node starts.
func (s Span) Pos() diag.Pos { return s.Start }

// Returns the byte offset just after the node.
func (s Span) EndOffset() int { return s.End }

// Any node of the syntax tree.
type Node interface {
	Pos() diag.Pos
	EndOffset() int
}

// A top-level declaration: [FuncDecl] or [StructDecl].
type Decl interface {
	Node
	declNode()
}

// A statement inside a block.
type Stmt interface {
	Node
	stmtNode()
}

// An expression that produces a value.
type Expr interface {
	Node
	exprNode()
}

// A written type such as `int` or `[string]`.
type TypeExpr interface {
	Node
	typeNode()
}

// A parsed source file.
type File struct {
	Span
	Imports []*Import
	Decls   []Decl

	// Problems that don't stop the program from building, such as a
	// needless `;`.
	Warnings diag.List
}

// A name, such as a variable, function, field or type name.
type Ident struct {
	Span
	Name string

	// The imported module the name belongs to, as in `math.sqrt`; empty
	// for a name of the current module or a local.
	Module string
}

// --- Declarations ---

// A C pointer type: `extern type Db`, optionally `as "sqlite3"`, the C
// name it is shown with.
type ExternTypeDecl struct {
	Span
	Pub   bool
	Name  *Ident
	CName string
}

// A function or method: `fn add(a: int, b: int) -> int { ... }`.
type FuncDecl struct {
	Span
	Pub bool

	// Set for `extern fn`, a function written in C, whose C name is
	// [CName] when it differs from the Vex name.
	Extern bool
	CName  string

	// Set for `export fn`, which C code can call by its name.
	Export bool

	// Set for `async fn`, which can await.
	Async bool

	Name       *Ident
	TypeParams []*TypeParam
	Params     []*Param

	// The declared result type, or nil when the function returns nothing.
	Result TypeExpr

	// Set when the function can throw.
	Throws *Throws

	// Exactly one of Body and ExprBody is set; ExprBody holds the
	// expression of a one-line `=> expr` function.
	Body     *Block
	ExprBody Expr

	// Reports whether this is a `static fn` inside a struct.
	Static bool

	// What kind of struct member this is: a method, or a property accessor.
	Accessor Accessor

	// The type an extension adds this member to, or nil.
	Recv TypeExpr
}

// The kind of a struct member declared like a function.
type Accessor int

const (
	// An ordinary method or function.
	Method Accessor = iota

	// `get name -> T`, read like a field.
	Getter

	// `set name(value: T)`, run when the property is assigned.
	Setter

	// The `didSet` block of a field, run after each assignment to it with
	// the previous value as `oldValue`. Its name is the field's name.
	Observer

	// `operator +(other: T) -> T`, run by an operator whose left operand
	// is the struct. Its name is the operator: `+`, `unary -`, `[]` or
	// `[]=`.
	Operator
)

// A function parameter: `name: string = "Hi"`.
type Param struct {
	Span
	Name    *Ident
	Type    TypeExpr
	Default Expr
}

// A struct with fields and methods.
type StructDecl struct {
	Span
	Pub        bool
	Name       *Ident
	TypeParams []*TypeParam

	// The interfaces the struct implements: `struct Circle: Shape`.
	Interfaces []*NamedType

	Fields  []*Field
	Methods []*FuncDecl
	Consts  []*ConstDecl
}

// A type parameter of a generic function or struct: `T` or
// `T: Comparable & Hashable`.
type TypeParam struct {
	Span
	Name        *Ident
	Constraints []*NamedType
}

// An import at the top of a file: `import "vex:math"` or
// `import "models" as m`.
type Import struct {
	Span
	Path string

	// The position of the quoted path, for errors about it.
	PathSpan Span

	// The name the module is used by, when it is renamed with `as`.
	As *Ident
}

// A struct field: `age: int = 18`.
type Field struct {
	Span
	Pub     bool
	Name    *Ident
	Type    TypeExpr
	Default Expr
}

// An enum: a value that is one of several cases, each of which may carry
// values.
type EnumDecl struct {
	Span
	Pub        bool
	Name       *Ident
	TypeParams []*TypeParam
	Interfaces []*NamedType
	Cases      []*EnumCase
	Methods    []*FuncDecl
	Consts     []*ConstDecl
}

// An extension: `extension string { ... }` adds methods, properties and
// static members to an existing type. [TypeParams] are set for an
// extension of a built-in generic type, as in `extension<T> [T]`.
type ExtensionDecl struct {
	Span
	Pub        bool
	TypeParams []*TypeParam
	Type       TypeExpr
	Methods    []*FuncDecl
	Consts     []*ConstDecl
}

// A type alias: `typedef Name = Type`, or with type parameters
// `typedef Pair<T> = (T, T)`. It is another name for [Type].
type TypeAliasDecl struct {
	Span
	Pub        bool
	Name       *Ident
	TypeParams []*TypeParam
	Type       TypeExpr
}

// A constant: `const name: type = value` at the top level, or `static
// const` inside a struct or enum. [Type] is nil when it is inferred.
type ConstDecl struct {
	Span
	Pub   bool
	Name  *Ident
	Type  TypeExpr
	Value Expr
}

// An interface: methods and properties that implementing structs and
// enums provide. A method with a body is a default they may leave out.
type InterfaceDecl struct {
	Span
	Pub        bool
	Name       *Ident
	TypeParams []*TypeParam

	// The interfaces it extends: `interface Shape: Drawable`.
	Supers  []*NamedType
	Methods []*FuncDecl
}

// An enum case: `circle(radius: float)` or `empty`.
type EnumCase struct {
	Span
	Name *Ident

	// The values the case carries; their defaults are always nil.
	Fields []*Field
}

func (*FuncDecl) declNode()       {}
func (*StructDecl) declNode()     {}
func (*EnumDecl) declNode()       {}
func (*InterfaceDecl) declNode()  {}
func (*ExternTypeDecl) declNode() {}
func (*ConstDecl) declNode()      {}
func (*TypeAliasDecl) declNode()  {}
func (*ExtensionDecl) declNode()  {}

// --- Types ---

// A type referred to by name: `int`, `User`.
type NamedType struct {
	Span

	// The module a type comes from, as in `models.User`; empty for a type
	// of the current module.
	Module string
	Name   string

	// The type arguments of a generic struct: `Box<int>`.
	Args []TypeExpr
}

// A list type: `[int]`.
// A record type: `(int, string)`, `({int x, int y})` or
// `(int, {string name})`.
type RecordType struct {
	Span
	Positional []TypeExpr
	Named      []*NamedField
}

// A named field of a [RecordType]: `int x`.
type NamedField struct {
	Type TypeExpr
	Name *Ident
}

type ListType struct {
	Span
	Elem TypeExpr
}

// A map type: `[string: int]`.
type MapType struct {
	Span
	Key, Value TypeExpr
}

// A function type: `fn(int, string) -> bool`.
type FuncType struct {
	Span
	Params []TypeExpr

	// Nil when the function returns nothing.
	Result TypeExpr

	// Set when the function can throw.
	Throws *Throws
}

// The `throws` of a function, or `throws(E)` when it names the enum it
// throws; Type is nil for the built-in Error.
type Throws struct {
	Span
	Type TypeExpr
}

// A type already resolved by the type checker, for code the checker
// writes itself, such as the function a tear-off stands for.
type KnownType struct {
	Span
	Type any
}

func (*KnownType) typeNode() {}

// An expression the type checker has already checked, with its type, for
// code the checker writes itself.
type KnownExpr struct {
	Span
	X    Expr
	Type any
}

func (*NamedType) typeNode() {}

// An optional type: `User?`, which holds a User or nil.
type OptionalType struct {
	Span
	Elem TypeExpr
}

func (*ListType) typeNode()     {}
func (*RecordType) typeNode()   {}
func (*MapType) typeNode()      {}
func (*OptionalType) typeNode() {}
func (*FuncType) typeNode()     {}

// --- Statements ---

// A list of statements in braces.
type Block struct {
	Span
	Stmts []Stmt
}

// A variable declaration: `let x = 1` or `var count: int = 0`.
type LetStmt struct {
	Span
	Mutable bool
	Name    *Ident
	Type    TypeExpr
	Value   Expr

	// For `let (a, b) = value` or `let Point(:x, :y) = p`, the pattern
	// taking the value apart; Name is then nil.
	Pattern Pattern
}

// An assignment: `x = 1`, `total += n`, `xs[0] = 5`, `user.age = 30`.
type AssignStmt struct {
	Span
	Target Expr

	// One of [lexer.Assign], [lexer.PlusAssign], [lexer.MinusAssign],
	// [lexer.StarAssign], [lexer.SlashAssign] or [lexer.PercentAssign].
	Op    lexer.Kind
	Value Expr
}

// An expression used as a statement, usually a call.
type ExprStmt struct {
	Span
	X Expr
}

// An `if` statement. Else is nil, a [*Block], or an [*IfStmt] for `else if`.
type IfStmt struct {
	Span
	Cond Expr
	Then *Block
	Else Stmt

	// For `if let name = value`, the name bound when [Cond], an optional,
	// holds a value.
	Bind *Ident

	// For `if value case pattern when guard`, the pattern [Cond] must
	// match, and the condition that must also hold, or nil.
	Case  Pattern
	Guard Expr
}

// `guard let name = value else { ... }`: runs the else block, which must
// leave, when [Value] is nil; otherwise binds [Bind] for the rest of the
// block.
type GuardStmt struct {
	Span
	Bind  *Ident
	Value Expr
	Else  *Block
}

// A `while` loop.
type WhileStmt struct {
	Span
	Cond Expr
	Body *Block

	// For `while let name = value`, the name bound each time [Cond], an
	// optional, holds a value; the loop ends at nil.
	Bind *Ident
}

// A `for` loop: `for x in xs`, `for i, x in xs` or `for i in 0..10`.
type ForStmt struct {
	Span

	// The index variable of `for i, x in xs`, or nil.
	Index *Ident
	Elem  *Ident

	// For `for (a, b) in pairs`, the pattern each element is taken apart
	// with; [Elem] then holds the whole element.
	Pattern Pattern

	// The list or [*RangeExpr] being iterated.
	Iter Expr
	Body *Block
}

// A `return` statement; Value is nil for a bare `return`.
type ReturnStmt struct {
	Span
	Value Expr
}

// A `throw` statement.
type ThrowStmt struct {
	Span
	Value Expr
}

// A `break` statement.
type BreakStmt struct{ Span }

// A `continue` statement.
type ContinueStmt struct{ Span }

func (*Block) stmtNode()        {}
func (*LetStmt) stmtNode()      {}
func (*AssignStmt) stmtNode()   {}
func (*ExprStmt) stmtNode()     {}
func (*IfStmt) stmtNode()       {}
func (*GuardStmt) stmtNode()    {}
func (*WhileStmt) stmtNode()    {}
func (*ForStmt) stmtNode()      {}
func (*ReturnStmt) stmtNode()   {}
func (*ThrowStmt) stmtNode()    {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}

// --- Expressions ---

// An integer literal; Value has digit separators removed.
type IntLit struct {
	Span
	Value string
}

// A float literal; Value has digit separators removed.
type FloatLit struct {
	Span
	Value string
}

// `true` or `false`.
type BoolLit struct {
	Span
	Value bool
}

// The literal `nil`: no value, for an optional.
type NilLit struct {
	Span
}

// A string without interpolation; Value has escapes resolved.
type StringLit struct {
	Span
	Value string
}

// A string with interpolation: `"Hi {name}!"`.
//
// Texts always has one more element than Exprs:
// Texts[0], Exprs[0], Texts[1], ..., Texts[len(Exprs)].
type InterpolatedString struct {
	Span
	Texts []string
	Exprs []Expr
}

// A list literal: `[1, 2, 3]`.
type ListLit struct {
	Span
	Elems []Expr

	// The elements as statements, set instead of [Elems] when the literal
	// uses `if`, `for` or `...`, and for a builder block. Each expression
	// statement in it is one element.
	Body *Block
}

// A map literal: `["a": 1, "b": 2]`, or `[:]` when empty.
type MapLit struct {
	Span
	Keys, Values []Expr

	// The entries as statements, set instead of [Keys] and [Values] when
	// the literal uses `if`, `for` or `...`. Each expression statement in
	// it is a [MapEntry] or a spread map.
	Body *Block
}

// A `key: value` entry of a map literal that has a [MapLit.Body].
type MapEntry struct {
	Span
	Key, Value Expr
}

// A generic function or enum with its type arguments written out, used
// without a call: `first<int>` as a value, or `Result<int>` before
// `.case`.
type TypeArgsExpr struct {
	Span
	X    Expr
	Args []TypeExpr
}

// A spread element: `...xs` adds every element of xs, and `...?xs` does
// nothing when xs is nil.
type SpreadExpr struct {
	Span
	X        Expr
	Optional bool
}

// A prefix operation: `-x` or `!done`.
type UnaryExpr struct {
	Span
	Op lexer.Kind
	X  Expr
}

// A binary operation such as `a + b` or `x == y`.
type BinaryExpr struct {
	Span
	Op   lexer.Kind
	X, Y Expr
}

// A range in a `for` loop: `0..10` or `1..=10`.
type RangeExpr struct {
	Span
	Lo, Hi    Expr
	Inclusive bool
}

// A call: `f(1, name: "Ann")`, optionally with a trailing block
// `Button("Add") { count += 1 }`.
type CallExpr struct {
	Span
	Fn Expr

	// Type arguments written at the call: `first<int>(xs)`.
	TypeArgs []TypeExpr

	Args     []*Arg
	Trailing *Block
}

// A call argument; Name is nil for a positional argument.
type Arg struct {
	Span
	Name  *Ident
	Value Expr
}

// An index operation: `xs[0]`.
type IndexExpr struct {
	Span
	X     Expr
	Index Expr
}

// A member access: `user.name`, `nums.push`, `User.guest`.
type MemberExpr struct {
	Span
	X    Expr
	Name *Ident

	// Set for `x?.name`, which is nil when [X] is nil.
	Optional bool
}

// A closure: `fn(x: int) -> int => x * 2` or `fn() { ... }`.
//
// Parameter types may be left out where the expected function type gives
// them. A trailing block passed to a call becomes a FuncLit with no
// parameters during checking.
type FuncLit struct {
	Span
	Params   []*Param
	Result   TypeExpr
	Throws   *Throws
	Body     *Block
	ExprBody Expr
}

// A case of the enum the context expects: `.empty`, or `.rect(...)` when
// called.
type ImplicitMember struct {
	Span
	Name *Ident
}

// A record value: `(1, "a")`, `(x: 1, y: 2)` or `(1, name: "a")`. Fields
// with a name are named fields; the others are positional.
type RecordLit struct {
	Span
	Fields []*Arg
}

// `await x`: waits for the future [X], or runs the async call [X].
type AwaitExpr struct {
	Span
	X Expr
}

// A call that may throw: `try load(path)`, `try? load(path)`, or
// `try load(path) catch e { ... }`. X holds the calls being tried.
type TryExpr struct {
	Span
	X Expr

	// Set for `try?`, which gives nil instead of the error.
	Optional bool

	// The `catch` that handles the error; nil when it passes up to the
	// caller.
	Catch *Catch
}

// The `catch e { ... }` of a [TryExpr]; Name is nil when the error isn't
// named. When the try has a value, the block's last expression gives it.
type Catch struct {
	Span
	Name *Ident
	Body *Block
}

// A match: `match shape { circle(r) => ..., _ => ... }`. Used as a
// statement, arm bodies may be blocks.
type MatchExpr struct {
	Span
	Subject Expr
	Arms    []*MatchArm
}

// One arm of a match: a pattern, an optional `when` guard, and either an
// expression or a block.
type MatchArm struct {
	Span
	Pattern Pattern
	Guard   Expr
	Expr    Expr
	Block   *Block
}

// A pattern in a match arm.
type Pattern interface {
	Node
	patternNode()
}

// The pattern `_`, which matches anything.
type WildcardPattern struct {
	Span
}

// A bare name: an enum case such as `empty` where one is expected,
// otherwise a name bound to the value.
type NamePattern struct {
	Span
	Name *Ident
}

// A name with fields: an enum case such as `circle(r)` or
// `circle(radius: r)`, or a struct such as `Point(x: 0, :y)`.
type CasePattern struct {
	Span
	Name *Ident
	Args []*PatternField
}

// A record pattern: `(a, b)`, `(min: lo, max: _)` or `(:min, :max)`.
type RecordPattern struct {
	Span
	Fields []*PatternField
}

// A field inside a pattern. [Name] is the field for a named one, nil for
// a positional one. `:x` stands for `x: x`.
type PatternField struct {
	Name    *Ident
	Pattern Pattern
}

// A list pattern: `[]`, `[a, b]` or `[first, ...rest]`. At most one
// element is a [RestPattern].
type ListPattern struct {
	Span
	Elems []Pattern
}

// `...rest` or `...` inside a list pattern: the elements the others don't
// match, bound as a list when [Bind] is set.
type RestPattern struct {
	Span
	Bind *Ident
}

// A range pattern: `1..=9`, or `0..10` without the end.
type RangePattern struct {
	Span
	Lo, Hi    Expr
	Inclusive bool
}

// A comparison pattern: `> 100`, `<= 0`.
type RelationalPattern struct {
	Span
	Op    lexer.Kind
	Value Expr
}

// Alternatives: `200 || 201`. Each binds the same names.
type OrPattern struct {
	Span
	Alts []Pattern
}

// A literal pattern: `1`, `-1`, `"yes"`, `true`.
type LiteralPattern struct {
	Span
	Value Expr
}

func (*WildcardPattern) patternNode()   {}
func (*CasePattern) patternNode()       {}
func (*NamePattern) patternNode()       {}
func (*RecordPattern) patternNode()     {}
func (*ListPattern) patternNode()       {}
func (*RestPattern) patternNode()       {}
func (*RangePattern) patternNode()      {}
func (*RelationalPattern) patternNode() {}
func (*OrPattern) patternNode()         {}
func (*LiteralPattern) patternNode()    {}

func (*ImplicitMember) exprNode()     {}
func (*MatchExpr) exprNode()          {}
func (*NilLit) exprNode()             {}
func (*KnownExpr) exprNode()          {}
func (*FuncLit) exprNode()            {}
func (*Ident) exprNode()              {}
func (*IntLit) exprNode()             {}
func (*FloatLit) exprNode()           {}
func (*BoolLit) exprNode()            {}
func (*StringLit) exprNode()          {}
func (*InterpolatedString) exprNode() {}
func (*ListLit) exprNode()            {}
func (*MapLit) exprNode()             {}
func (*MapEntry) exprNode()           {}
func (*SpreadExpr) exprNode()         {}
func (*TypeArgsExpr) exprNode()       {}
func (*UnaryExpr) exprNode()          {}
func (*BinaryExpr) exprNode()         {}
func (*RangeExpr) exprNode()          {}
func (*CallExpr) exprNode()           {}
func (*TryExpr) exprNode()            {}
func (*AwaitExpr) exprNode()          {}
func (*RecordLit) exprNode()          {}
func (*IndexExpr) exprNode()          {}
func (*MemberExpr) exprNode()         {}
