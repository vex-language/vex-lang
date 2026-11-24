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

func (*FuncDecl) declNode()   {}
func (*StructDecl) declNode() {}

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

func (*NamedType) typeNode() {}
func (*ListType) typeNode()  {}

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

// A `break` statement.
type BreakStmt struct{ Span }

// A `continue` statement.
type ContinueStmt struct{ Span }

func (*Block) stmtNode()        {}
func (*LetStmt) stmtNode()      {}
func (*AssignStmt) stmtNode()   {}
func (*ExprStmt) stmtNode()     {}
func (*IfStmt) stmtNode()       {}
func (*WhileStmt) stmtNode()    {}
func (*ForStmt) stmtNode()      {}
func (*ReturnStmt) stmtNode()   {}
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
}

func (*Ident) exprNode()              {}
func (*IntLit) exprNode()             {}
func (*FloatLit) exprNode()           {}
func (*BoolLit) exprNode()            {}
func (*StringLit) exprNode()          {}
func (*InterpolatedString) exprNode() {}
func (*ListLit) exprNode()            {}
func (*UnaryExpr) exprNode()          {}
func (*BinaryExpr) exprNode()         {}
func (*RangeExpr) exprNode()          {}
func (*CallExpr) exprNode()           {}
func (*IndexExpr) exprNode()          {}
func (*MemberExpr) exprNode()         {}
