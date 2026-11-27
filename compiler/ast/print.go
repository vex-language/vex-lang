package ast

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/vex-language/vex-lang/compiler/lexer"
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
	case *KnownType:
		return fmt.Sprint(t.Type)
	case *NamedType:
		if t.Module != "" {
			return t.Module + "." + (&NamedType{Name: t.Name, Args: t.Args}).typeString()
		}
		return t.typeString()
	case *ListType:
		return "[" + TypeString(t.Elem) + "]"
	case *RecordType:
		var parts []string
		for _, p := range t.Positional {
			parts = append(parts, TypeString(p))
		}
		if len(t.Named) > 0 {
			named := make([]string, len(t.Named))
			for i, f := range t.Named {
				named[i] = TypeString(f.Type) + " " + f.Name.Name
			}
			parts = append(parts, "{"+strings.Join(named, ", ")+"}")
		}
		if len(t.Positional) == 1 && len(t.Named) == 0 {
			return "(" + parts[0] + ",)"
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *MapType:
		return "[" + TypeString(t.Key) + ": " + TypeString(t.Value) + "]"
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
		if t.Async {
			s = "async " + s
		}
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
	case *NamePattern:
		return p.Name.Name
	case *TypedPattern:
		return p.Name.Name + ": " + TypeString(p.Type)
	case *CasePattern:
		return p.Name.Name + "(" + fieldsString(p.Args) + ")"
	case *RecordPattern:
		if len(p.Fields) == 1 && p.Fields[0].Name == nil {
			return "(" + fieldsString(p.Fields) + ",)"
		}
		return "(" + fieldsString(p.Fields) + ")"
	case *ListPattern:
		parts := make([]string, len(p.Elems))
		for i, e := range p.Elems {
			parts[i] = PatternString(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *RestPattern:
		if p.Bind != nil {
			return "..." + p.Bind.Name
		}
		return "..."
	case *LiteralPattern:
		return literalString(p.Value)
	case *RangePattern:
		sep := ".."
		if p.Inclusive {
			sep = "..="
		}
		return literalString(p.Lo) + sep + literalString(p.Hi)
	case *RelationalPattern:
		return op(p.Op) + " " + literalString(p.Value)
	case *OrPattern:
		parts := make([]string, len(p.Alts))
		for i, a := range p.Alts {
			parts[i] = PatternString(a)
		}
		return strings.Join(parts, " || ")
	}
	return "?"
}

func literalString(e Expr) string {
	if u, ok := e.(*UnaryExpr); ok {
		return "-" + Sexpr(u.X)
	}
	return Sexpr(e)
}

func fieldsString(fs []*PatternField) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		switch {
		case f.Name == nil:
			parts[i] = PatternString(f.Pattern)
		case isName(f.Pattern, f.Name.Name):
			parts[i] = ":" + f.Name.Name
		default:
			parts[i] = f.Name.Name + ": " + PatternString(f.Pattern)
		}
	}
	return strings.Join(parts, ", ")
}

func isName(p Pattern, name string) bool {
	n, ok := p.(*NamePattern)
	return ok && n.Name.Name == name
}

func op(k lexer.Kind) string { return strings.Trim(k.String(), "'") }

// Builds the tree of a function declaration or closure.
// Returns the type without its module.
func (t *NamedType) typeString() string {
	if len(t.Args) == 0 {
		return t.Name
	}
	args := make([]string, len(t.Args))
	for i, a := range t.Args {
		args[i] = TypeString(a)
	}
	return t.Name + "<" + strings.Join(args, ", ") + ">"
}

// Returns type parameters as written: `<T, U: Comparable & Hashable>`.
func typeParamsString(tps []*TypeParam) string {
	if len(tps) == 0 {
		return ""
	}
	parts := make([]string, len(tps))
	for i, tp := range tps {
		parts[i] = tp.Name.Name
		for j, c := range tp.Constraints {
			sep := " & "
			if j == 0 {
				sep = ": "
			}
			parts[i] += sep + TypeString(c)
		}
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

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
	switch {
	case exprBody != nil:
		t.kids = append(t.kids, branch("=>", buildExpr(exprBody)))
	case body != nil:
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
		for _, im := range n.Imports {
			label := "import " + strconv.Quote(im.Path)
			if im.As != nil {
				label += " as " + im.As.Name
			}
			t.kids = append(t.kids, leaf(label))
		}
		for _, d := range n.Decls {
			t.kids = append(t.kids, build(d))
		}
		return t

	case *FuncDecl:
		label := "fn " + n.Name.Name + typeParamsString(n.TypeParams)
		switch n.Accessor {
		case Getter:
			label = "get " + n.Name.Name
		case Setter:
			label = "set " + n.Name.Name
		case Observer:
			label = "didSet " + n.Name.Name
		case Operator:
			label = "operator " + n.Name.Name
		}
		if n.Static {
			label = "static " + label
		}
		if n.Extern {
			label = "extern " + label
			if n.CName != "" {
				label += " as " + n.CName
			}
		}
		if n.Export {
			label = "export " + label
		}
		if n.Async {
			label = "async " + label
		}
		if n.Pub {
			label = "pub " + label
		}
		return funcTree(label, n.Params, n.Throws, n.Result, n.Body, n.ExprBody)
	case *ExternTypeDecl:
		label := "extern type " + n.Name.Name
		if n.CName != "" {
			label += " as " + n.CName
		}
		if n.Pub {
			label = "pub " + label
		}
		return leaf(label)
	case *ExtensionDecl:
		label := "extension" + typeParamsString(n.TypeParams) + " " + TypeString(n.Type)
		if n.Pub {
			label = "pub " + label
		}
		t := branch(label)
		for _, k := range n.Consts {
			t.kids = append(t.kids, build(k))
		}
		for _, m := range n.Methods {
			t.kids = append(t.kids, build(m))
		}
		return t
	case *TypeAliasDecl:
		label := "typedef " + n.Name.Name + typeParamsString(n.TypeParams) + " = " + TypeString(n.Type)
		if n.Pub {
			label = "pub " + label
		}
		return leaf(label)
	case *ConstDecl:
		keyword := "const "
		switch {
		case n.Mutable:
			keyword = "var "
		case n.Lazy:
			keyword = "let "
		}
		label := keyword + n.Name.Name
		if n.Type != nil {
			label += ": " + TypeString(n.Type)
		}
		if n.Pub {
			label = "pub " + label
		}
		return branch(label, buildExpr(n.Value))
	case *StructDecl:
		label := "struct " + n.Name.Name + typeParamsString(n.TypeParams)
		if n.Pub {
			label = "pub " + label
		}
		label += interfacesString(n.Interfaces)
		t := branch(label)
		for _, f := range n.Fields {
			label := "field " + f.Name.Name
			if f.Type != nil {
				label += ": " + TypeString(f.Type)
			}
			if f.Let {
				label = "let " + label
			}
			if f.PrivateSet {
				label = "private(set) " + label
			}
			if f.Pub {
				label = "pub " + label
			}
			t.kids = append(t.kids, branch(label, buildExpr(f.Default)))
		}
		if n.Init != nil {
			t.kids = append(t.kids, branch("init", build(n.Init.Body)))
		}
		for _, k := range n.Consts {
			t.kids = append(t.kids, build(k))
		}
		for _, m := range n.Methods {
			t.kids = append(t.kids, build(m))
		}
		return t

	case *InterfaceDecl:
		label := "interface " + n.Name.Name + typeParamsString(n.TypeParams)
		if n.Pub {
			label = "pub " + label
		}
		label += interfacesString(n.Supers)
		t := branch(label)
		for _, m := range n.Methods {
			t.kids = append(t.kids, build(m))
		}
		return t
	case *EnumDecl:
		label := "enum " + n.Name.Name + typeParamsString(n.TypeParams)
		if n.Pub {
			label = "pub " + label
		}
		label += interfacesString(n.Interfaces)
		t := branch(label)
		for _, c := range n.Cases {
			ct := branch("case " + c.Name.Name)
			for _, f := range c.Fields {
				ct.kids = append(ct.kids, leaf(f.Name.Name+": "+TypeString(f.Type)))
			}
			t.kids = append(t.kids, ct)
		}
		for _, k := range n.Consts {
			t.kids = append(t.kids, build(k))
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
		if n.Pattern != nil {
			label += PatternString(n.Pattern)
		} else {
			label += n.Name.Name
		}
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
		if len(n.More) > 0 {
			return branch("if", append(conds(n.Bind, n.Cond, n.More), build(n.Then), els)...)
		}
		if n.Bind != nil {
			return branch("if let "+n.Bind.Name, buildExpr(n.Cond), build(n.Then), els)
		}
		if n.Case != nil {
			label := "if case " + PatternString(n.Case)
			if n.Guard != nil {
				label += " when " + Sexpr(n.Guard)
			}
			return branch(label, buildExpr(n.Cond), build(n.Then), els)
		}
		return branch("if", buildExpr(n.Cond), build(n.Then), els)
	case *GuardStmt:
		if n.Bind == nil || len(n.More) > 0 {
			return branch("guard", append(conds(n.Bind, n.Value, n.More), build(n.Else))...)
		}
		return branch("guard let "+n.Bind.Name, buildExpr(n.Value), build(n.Else))
	case *WhileStmt:
		if len(n.More) > 0 {
			return branch("while", append(conds(n.Bind, n.Cond, n.More), build(n.Body))...)
		}
		if n.Bind != nil {
			return branch("while let "+n.Bind.Name, buildExpr(n.Cond), build(n.Body))
		}
		return branch("while", buildExpr(n.Cond), build(n.Body))
	case *ForStmt:
		elem := n.Elem.Name
		if n.Pattern != nil {
			elem = PatternString(n.Pattern)
		}
		if n.Index != nil {
			elem = n.Index.Name + ", " + elem
		}
		label := "for " + elem
		if n.Await {
			label = "for await " + elem
		}
		return branch(label, buildExpr(n.Iter), build(n.Body))
	case *ReturnStmt:
		return branch("return", buildExpr(n.Value))
	case *YieldStmt:
		return branch("yield", buildExpr(n.Value))
	case *LocalFuncStmt:
		return build(n.Decl)
	case *LabeledStmt:
		return branch(n.Label.Name+":", build(n.Loop))
	case *DeferStmt:
		return branch("defer", build(n.Body))
	case *RepeatStmt:
		return branch("repeat", build(n.Body), buildExpr(n.Cond))
	case *ThrowStmt:
		return branch("throw", buildExpr(n.Value))
	case *BreakStmt:
		if n.Label != nil {
			return leaf("break " + n.Label.Name)
		}
		return leaf("break")
	case *ContinueStmt:
		if n.Label != nil {
			return leaf("continue " + n.Label.Name)
		}
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
		if e.Module != "" {
			return leaf(e.Module + "." + e.Name)
		}
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
	case *MapLit:
		t := branch("map")
		if e.Body != nil {
			for _, s := range e.Body.Stmts {
				t.kids = append(t.kids, build(s))
			}
		}
		for i, k := range e.Keys {
			t.kids = append(t.kids, branch(":", buildExpr(k), buildExpr(e.Values[i])))
		}
		return t
	case *MapEntry:
		return branch(":", buildExpr(e.Key), buildExpr(e.Value))
	case *TypeArgsExpr:
		parts := make([]string, len(e.Args))
		for i, a := range e.Args {
			parts[i] = TypeString(a)
		}
		return branch("<"+strings.Join(parts, ", ")+">", buildExpr(e.X))
	case *SpreadExpr:
		label := "..."
		if e.Optional {
			label = "...?"
		}
		return branch(label, buildExpr(e.X))
	case *RecordLit:
		t := branch("record")
		for _, a := range e.Fields {
			if a.Name != nil {
				t.kids = append(t.kids, branch(a.Name.Name+":", buildExpr(a.Value)))
			} else {
				t.kids = append(t.kids, buildExpr(a.Value))
			}
		}
		return t
	case *ListLit:
		t := branch("list")
		if e.Body != nil {
			for _, s := range e.Body.Stmts {
				t.kids = append(t.kids, build(s))
			}
		}
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
		if e.Async {
			return funcTree("async fn", e.Params, e.Throws, e.Result, e.Body, e.ExprBody)
		}
		return funcTree("fn", e.Params, e.Throws, e.Result, e.Body, e.ExprBody)
	case *ImplicitMember:
		return leaf("." + e.Name.Name)
	case *AwaitExpr:
		return branch("await", buildExpr(e.X))
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
	case *IfExpr:
		return build(e.If)
	case *JumpExpr:
		return build(e.Stmt)
	case *TypeTest:
		op := "is"
		if e.Cast {
			op = "as?"
		}
		return branch(op, buildExpr(e.X), leaf(TypeString(e.Type)))
	case *MatchExpr:
		t := branch("match", buildExpr(e.Subject))
		for _, a := range e.Arms {
			body := build(a.Block)
			if a.Expr != nil {
				body = buildExpr(a.Expr)
			}
			label := PatternString(a.Pattern)
			if a.Guard != nil {
				label += " when " + Sexpr(a.Guard)
			}
			t.kids = append(t.kids, branch(label+" =>", body))
		}
		return t
	case *CallExpr:
		t := branch("call", buildExpr(e.Fn))
		if len(e.TypeArgs) > 0 {
			args := make([]string, len(e.TypeArgs))
			for i, a := range e.TypeArgs {
				args[i] = TypeString(a)
			}
			t.label += "<" + strings.Join(args, ", ") + ">"
		}
		for _, a := range e.Args {
			if a.Name != nil {
				t.kids = append(t.kids, branch(a.Name.Name+":", buildExpr(a.Value)))
			} else {
				t.kids = append(t.kids, buildExpr(a.Value))
			}
		}
		if e.Trailing != nil {
			b := build(e.Trailing)
			if len(e.BlockParams) > 0 {
				var names []string
				for _, p := range e.BlockParams {
					names = append(names, p.Name)
				}
				b = branch(strings.Join(names, ", ")+" in", b)
			}
			t.kids = append(t.kids, b)
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

// Returns the interfaces in a declaration's header, such as `: A, B<int>`.
func interfacesString(ifaces []*NamedType) string {
	s := ""
	for i, id := range ifaces {
		sep := ", "
		if i == 0 {
			sep = ": "
		}
		s += sep + TypeString(id)
	}
	return s
}

// Returns the trees of a header's conditions: its first, then [more].
func conds(bind *Ident, value Expr, more []*Cond) []*tree {
	one := func(bind *Ident, value Expr) *tree {
		if bind != nil {
			return branch("let "+bind.Name, buildExpr(value))
		}
		return buildExpr(value)
	}
	out := []*tree{one(bind, value)}
	for _, c := range more {
		out = append(out, one(c.Bind, c.Value))
	}
	return out
}
