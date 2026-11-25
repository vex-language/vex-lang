package ast

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/irshadio/vex-lang/compiler/lexer"
)

// A node reduced to a label and children, shared by both printers.
type tree struct {
	label string
	kids  []*tree
}

func leaf(label string) *tree { return &tree{label: label} }

func branch(label string, kids ...*tree) *tree {
	t := &tree{label: label}
	for _, k := range kids {
		if k != nil {
			t.kids = append(t.kids, k)
		}
	}
	return t
}

// Returns [n] as a compact S-expression, such as `(+ 1 (* 2 x))`.
func Sexpr(n Node) string {
	var b strings.Builder
	writeSexpr(&b, build(n))
	return b.String()
}

func writeSexpr(b *strings.Builder, t *tree) {
	if len(t.kids) == 0 {
		b.WriteString(t.label)
		return
	}
	b.WriteString("(" + t.label)
	for _, k := range t.kids {
		b.WriteByte(' ')
		writeSexpr(b, k)
	}
	b.WriteByte(')')
}

// Writes [n] to [w] as an indented tree, one node per line.
func Fprint(w io.Writer, n Node) {
	var walk func(t *tree, depth int)
	walk = func(t *tree, depth int) {
		fmt.Fprintf(w, "%s%s\n", strings.Repeat("  ", depth), t.label)
		for _, k := range t.kids {
			walk(k, depth+1)
		}
	}
	walk(build(n), 0)
}

// Returns a written type in source form, such as `[int]`.
func TypeString(t TypeExpr) string {
	switch t := t.(type) {
	case *NamedType:
		return t.Name
	case *ListType:
		return "[" + TypeString(t.Elem) + "]"
	case *OptionalType:
		if f, ok := t.Elem.(*FuncType); ok && f.Result != nil {
			return "(" + TypeString(t.Elem) + ")?"
		}
		return TypeString(t.Elem) + "?"
	case *FuncType:
		params := make([]string, len(t.Params))
		for i, p := range t.Params {
			params[i] = TypeString(p)
		}
		s := "fn(" + strings.Join(params, ", ") + ")" + ThrowsString(t.Throws)
		if t.Result != nil {
			s += " -> " + TypeString(t.Result)
		}
		return s
	}
	return "?"
}

// Returns ` throws`, ` throws(E)`, or "" when [th] is nil.
func ThrowsString(th *Throws) string {
	switch {
	case th == nil:
		return ""
	case th.Type == nil:
		return " throws"
	}
	return " throws(" + TypeString(th.Type) + ")"
}

// Returns [p] in source form, such as `circle(r, _)`.
func PatternString(p Pattern) string {
	switch p := p.(type) {
	case *WildcardPattern:
		return "_"
	case *CasePattern:
		if p.Binds == nil {
			return p.Name.Name
		}
		names := make([]string, len(p.Binds))
		for i, b := range p.Binds {
			names[i] = b.Name
		}
		return p.Name.Name + "(" + strings.Join(names, ", ") + ")"
	case *LiteralPattern:
		if u, ok := p.Value.(*UnaryExpr); ok {
			return "-" + Sexpr(u.X)
		}
		return Sexpr(p.Value)
	}
	return "?"
}

func op(k lexer.Kind) string { return strings.Trim(k.String(), "'") }

// Builds the tree of a function declaration or closure.
func funcTree(label string, params []*Param, th *Throws, result TypeExpr, body *Block, exprBody Expr) *tree {
	t := branch(label)
	for _, p := range params {
		name := "param " + p.Name.Name
		if p.Type != nil {
			name += ": " + TypeString(p.Type)
		}
		t.kids = append(t.kids, branch(name, buildExpr(p.Default)))
	}
	if th != nil {
		t.kids = append(t.kids, leaf(strings.TrimSpace(ThrowsString(th))))
	}
	if result != nil {
		t.kids = append(t.kids, leaf("-> "+TypeString(result)))
	}
	if exprBody != nil {
		t.kids = append(t.kids, branch("=>", buildExpr(exprBody)))
	} else {
		t.kids = append(t.kids, build(body))
	}
	return t
}

func build(n Node) *tree {
	switch n := n.(type) {
	case nil:
		return nil
	case *File:
		t := branch("file")
		for _, d := range n.Decls {
			t.kids = append(t.kids, build(d))
		}
		return t

	case *FuncDecl:
		label := "fn " + n.Name.Name
		if n.Static {
			label = "static " + label
		}
		return funcTree(label, n.Params, n.Throws, n.Result, n.Body, n.ExprBody)
	case *StructDecl:
		t := branch("struct " + n.Name.Name)
		for _, f := range n.Fields {
			t.kids = append(t.kids, branch("field "+f.Name.Name+": "+TypeString(f.Type), buildExpr(f.Default)))
		}
		for _, m := range n.Methods {
			t.kids = append(t.kids, build(m))
		}
		return t

	case *EnumDecl:
		t := branch("enum " + n.Name.Name)
		for _, c := range n.Cases {
			ct := branch("case " + c.Name.Name)
			for _, f := range c.Fields {
				ct.kids = append(ct.kids, leaf(f.Name.Name+": "+TypeString(f.Type)))
			}
			t.kids = append(t.kids, ct)
		}
		for _, m := range n.Methods {
			t.kids = append(t.kids, build(m))
		}
		return t

	case *Block:
		if n == nil {
			return nil
		}
		t := branch("block")
		for _, s := range n.Stmts {
			t.kids = append(t.kids, build(s))
		}
		return t
	case *LetStmt:
		label := "let "
		if n.Mutable {
			label = "var "
		}
		label += n.Name.Name
		if n.Type != nil {
			label += ": " + TypeString(n.Type)
		}
		return branch(label, buildExpr(n.Value))
	case *AssignStmt:
		return branch(op(n.Op), buildExpr(n.Target), buildExpr(n.Value))
	case *ExprStmt:
		return buildExpr(n.X)
	case *IfStmt:
		var els *tree
		if n.Else != nil {
			els = build(n.Else)
		}
		if n.Bind != nil {
			return branch("if let "+n.Bind.Name, buildExpr(n.Cond), build(n.Then), els)
		}
		return branch("if", buildExpr(n.Cond), build(n.Then), els)
	case *GuardStmt:
		return branch("guard let "+n.Bind.Name, buildExpr(n.Value), build(n.Else))
	case *WhileStmt:
		return branch("while", buildExpr(n.Cond), build(n.Body))
	case *ForStmt:
		label := "for " + n.Elem.Name
		if n.Index != nil {
			label = "for " + n.Index.Name + ", " + n.Elem.Name
		}
		return branch(label, buildExpr(n.Iter), build(n.Body))
	case *ReturnStmt:
		return branch("return", buildExpr(n.Value))
	case *ThrowStmt:
		return branch("throw", buildExpr(n.Value))
	case *BreakStmt:
		return leaf("break")
	case *ContinueStmt:
		return leaf("continue")

	case Expr:
		return buildExpr(n)
	}
	return leaf(fmt.Sprintf("<%T>", n))
}

func buildExpr(e Expr) *tree {
	switch e := e.(type) {
	case nil:
		return nil
	case *Ident:
		return leaf(e.Name)
	case *IntLit:
		return leaf(e.Value)
	case *FloatLit:
		return leaf(e.Value)
	case *BoolLit:
		return leaf(strconv.FormatBool(e.Value))
	case *NilLit:
		return leaf("nil")
	case *StringLit:
		return leaf(strconv.Quote(e.Value))
	case *InterpolatedString:
		t := branch("interp")
		for i, text := range e.Texts {
			if text != "" {
				t.kids = append(t.kids, leaf(strconv.Quote(text)))
			}
			if i < len(e.Exprs) {
				t.kids = append(t.kids, buildExpr(e.Exprs[i]))
			}
		}
		return t
	case *ListLit:
		t := branch("list")
		for _, el := range e.Elems {
			t.kids = append(t.kids, buildExpr(el))
		}
		return t
	case *UnaryExpr:
		return branch(op(e.Op), buildExpr(e.X))
	case *BinaryExpr:
		return branch(op(e.Op), buildExpr(e.X), buildExpr(e.Y))
	case *RangeExpr:
		label := ".."
		if e.Inclusive {
			label = "..="
		}
		return branch(label, buildExpr(e.Lo), buildExpr(e.Hi))
	case *FuncLit:
		return funcTree("fn", e.Params, e.Throws, e.Result, e.Body, e.ExprBody)
	case *ImplicitMember:
		return leaf("." + e.Name.Name)
	case *TryExpr:
		label := "try"
		if e.Optional {
			label = "try?"
		}
		t := branch(label, buildExpr(e.X))
		if e.Catch != nil {
			name := "catch"
			if e.Catch.Name != nil {
				name += " " + e.Catch.Name.Name
			}
			t.kids = append(t.kids, branch(name, build(e.Catch.Body)))
		}
		return t
	case *MatchExpr:
		t := branch("match", buildExpr(e.Subject))
		for _, a := range e.Arms {
			body := build(a.Block)
			if a.Expr != nil {
				body = buildExpr(a.Expr)
			}
			t.kids = append(t.kids, branch(PatternString(a.Pattern)+" =>", body))
		}
		return t
	case *CallExpr:
		t := branch("call", buildExpr(e.Fn))
		for _, a := range e.Args {
			if a.Name != nil {
				t.kids = append(t.kids, branch(a.Name.Name+":", buildExpr(a.Value)))
			} else {
				t.kids = append(t.kids, buildExpr(a.Value))
			}
		}
		if e.Trailing != nil {
			t.kids = append(t.kids, build(e.Trailing))
		}
		return t
	case *IndexExpr:
		return branch("index", buildExpr(e.X), buildExpr(e.Index))
	case *MemberExpr:
		if e.Optional {
			return branch("?.", buildExpr(e.X), leaf(e.Name.Name))
		}
		return branch(".", buildExpr(e.X), leaf(e.Name.Name))
	}
	return leaf(fmt.Sprintf("<%T>", e))
}
