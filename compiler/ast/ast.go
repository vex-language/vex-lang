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
	Decls []Decl

	// Problems that don't stop the program from building, such as a
	// needless `;`.
	Warnings diag.List
}

// A name, such as a variable, function, field or type name.
type Ident struct {
	Span
	Name string
}

// --- Declarations ---

// A function or method: `fn add(a: int, b: int) -> int { ... }`.
type FuncDecl struct {
	Span
	Name   *Ident
	Params []*Param

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
}

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
	Name    *Ident
	Fields  []*Field
	Methods []*FuncDecl
}

// A struct field: `age: int = 18`.
type Field struct {
	Span
	Name    *Ident
	Type    TypeExpr
	Default Expr
}

// An enum: a value that is one of several cases, each of which may carry
// values.
type EnumDecl struct {
	Span
	Name    *Ident
	Cases   []*EnumCase
	Methods []*FuncDecl
}

// An enum case: `circle(radius: float)` or `empty`.
type EnumCase struct {
	Span
	Name *Ident

	// The values the case carries; their defaults are always nil.
	Fields []*Field
}

func (*FuncDecl) declNode()   {}
func (*StructDecl) declNode() {}
func (*EnumDecl) declNode()   {}

// --- Types ---

// A type referred to by name: `int`, `User`.
type NamedType struct {
	Span
	Name string
}

// A list type: `[int]`.
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

func (*NamedType) typeNode() {}

// An optional type: `User?`, which holds a User or nil.
type OptionalType struct {
	Span
	Elem TypeExpr
}

func (*ListType) typeNode()     {}
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
}

// A `for` loop: `for x in xs`, `for i, x in xs` or `for i in 0..10`.
type ForStmt struct {
	Span

	// The index variable of `for i, x in xs`, or nil.
	Index *Ident
	Elem  *Ident

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
}

// A map literal: `["a": 1, "b": 2]`, or `[:]` when empty.
type MapLit struct {
	Span
	Keys, Values []Expr
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
	Fn       Expr
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

// One arm of a match: a pattern and either an expression or a block.
type MatchArm struct {
	Span
	Pattern Pattern
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

// A pattern naming an enum case: `empty`, or `circle(r)` binding the
// case's values in order. A `_` binding ignores that value; [Binds] is nil
// when there are no parentheses.
type CasePattern struct {
	Span
	Name  *Ident
	Binds []*Ident
}

// A literal pattern: `1`, `-1`, `"yes"`, `true`.
type LiteralPattern struct {
	Span
	Value Expr
}

func (*WildcardPattern) patternNode() {}
func (*CasePattern) patternNode()     {}
func (*LiteralPattern) patternNode()  {}

func (*ImplicitMember) exprNode()     {}
func (*MatchExpr) exprNode()          {}
func (*NilLit) exprNode()             {}
func (*FuncLit) exprNode()            {}
func (*Ident) exprNode()              {}
func (*IntLit) exprNode()             {}
func (*FloatLit) exprNode()           {}
func (*BoolLit) exprNode()            {}
func (*StringLit) exprNode()          {}
func (*InterpolatedString) exprNode() {}
func (*ListLit) exprNode()            {}
func (*MapLit) exprNode()             {}
func (*UnaryExpr) exprNode()          {}
func (*BinaryExpr) exprNode()         {}
func (*RangeExpr) exprNode()          {}
func (*CallExpr) exprNode()           {}
func (*TryExpr) exprNode()            {}
func (*IndexExpr) exprNode()          {}
func (*MemberExpr) exprNode()         {}
