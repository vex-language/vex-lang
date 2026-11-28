package doc

import (
	"strconv"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/lexer"
)

// Returns the signature of a function, method, accessor or operator,
// such as `static fn parse(text: string) throws -> Json`.
func funcString(f *ast.FuncDecl) string {
	var b strings.Builder
	if f.Static {
		b.WriteString("static ")
	}
	if f.Extern {
		b.WriteString("extern ")
	}
	if f.Export {
		b.WriteString("export ")
	}
	if f.Async {
		b.WriteString("async ")
	}
	switch f.Accessor {
	case ast.Getter:
		b.WriteString("get " + f.Name.Name)
	case ast.Setter:
		b.WriteString("set " + f.Name.Name + paramsString(f.Params))
	case ast.Operator:
		b.WriteString("operator " + strings.TrimPrefix(f.Name.Name, "unary ") + paramsString(f.Params))
	default:
		b.WriteString("fn " + f.Name.Name + typeParamsString(f.TypeParams) + paramsString(f.Params))
	}
	b.WriteString(ast.ThrowsString(f.Throws))
	if f.Result != nil {
		b.WriteString(" -> " + ast.TypeString(f.Result))
	}
	return b.String()
}

// Returns parameters as written: `(a: int, b: string = "x")`.
func paramsString(params []*ast.Param) string {
	parts := make([]string, len(params))
	for i, p := range params {
		s := p.Name.Name
		if p.Type != nil {
			s += ": " + ast.TypeString(p.Type)
		}
		if p.Default != nil {
			s += " = " + exprString(p.Default)
		}
		parts[i] = s
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// Returns type parameters as written: `<T, U: Comparable & Hashable>`.
func typeParamsString(tps []*ast.TypeParam) string {
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
			parts[i] += sep + ast.TypeString(c)
		}
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

// Returns the interfaces in a declaration's header, such as `: A, B<int>`.
func interfacesString(ifaces []*ast.NamedType) string {
	parts := make([]string, len(ifaces))
	for i, t := range ifaces {
		parts[i] = ast.TypeString(t)
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, ", ")
}

// Returns a struct field as written, such as `let id: int` or
// `count: int = 0`.
func fieldString(f *ast.Field) string {
	s := f.Name.Name
	switch {
	case f.Let:
		s = "let " + s
	case f.PrivateSet:
		s = "private(set) " + s
	}
	if f.Type != nil {
		s += ": " + ast.TypeString(f.Type)
	}
	if f.Default != nil {
		s += " = " + exprString(f.Default)
	}
	return s
}

// Returns an enum case as written, such as `circle(radius: float)`.
func caseString(c *ast.EnumCase) string {
	if len(c.Fields) == 0 {
		return c.Name.Name
	}
	parts := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		parts[i] = f.Name.Name + ": " + ast.TypeString(f.Type)
	}
	return c.Name.Name + "(" + strings.Join(parts, ", ") + ")"
}

// Returns a constant or variable as written, such as
// `const limit: int = 10`, with `static` when [static] is set.
func constString(k *ast.ConstDecl, static bool) string {
	kw := "const"
	switch {
	case k.Lazy && k.Mutable:
		kw = "var"
	case k.Lazy:
		kw = "let"
	}
	s := kw + " " + k.Name.Name
	if static {
		s = "static " + s
	}
	if k.Type != nil {
		s += ": " + ast.TypeString(k.Type)
	}
	// Only a constant's value is part of its API; a variable's starting
	// value is not.
	if !k.Lazy && k.Value != nil {
		s += " = " + exprString(k.Value)
	} else if k.Type == nil && k.Value != nil {
		if t := literalType(k.Value); t != "" {
			s += ": " + t
		}
	}
	return s
}

// Returns the type of a literal, or "" for other expressions.
func literalType(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.IntLit:
		return "int"
	case *ast.FloatLit:
		return "float"
	case *ast.BoolLit:
		return "bool"
	case *ast.StringLit, *ast.InterpolatedString:
		return "string"
	case *ast.UnaryExpr:
		return literalType(e.X)
	}
	return ""
}

// The longest default or constant value shown in full; longer ones are
// shortened to `…`.
const maxValue = 60

// Returns a default or constant value in source form, or `…` when it is
// too long or not a simple expression.
func exprString(e ast.Expr) string {
	s, ok := simpleExpr(e)
	if !ok || len(s) > maxValue {
		return "…"
	}
	return s
}

// Returns [e] in source form when it is made of literals, names, member
// accesses, calls, operators and list or map literals.
func simpleExpr(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.IntLit:
		return e.Value, true
	case *ast.FloatLit:
		return e.Value, true
	case *ast.BoolLit:
		return strconv.FormatBool(e.Value), true
	case *ast.NilLit:
		return "nil", true
	case *ast.StringLit:
		return quote(e.Value), true
	case *ast.Ident:
		if e.Module != "" {
			return e.Module + "." + e.Name, true
		}
		return e.Name, true
	case *ast.ImplicitMember:
		return "." + e.Name.Name, true
	case *ast.MemberExpr:
		x, ok := simpleExpr(e.X)
		op := "."
		if e.Optional {
			op = "?."
		}
		return x + op + e.Name.Name, ok
	case *ast.UnaryExpr:
		x, ok := operand(e.X)
		return opString(e.Op) + x, ok
	case *ast.BinaryExpr:
		x, ok1 := operand(e.X)
		y, ok2 := operand(e.Y)
		return x + " " + opString(e.Op) + " " + y, ok1 && ok2
	case *ast.ListLit:
		if e.Body != nil {
			return "", false
		}
		return listString(e.Elems)
	case *ast.MapLit:
		if e.Body != nil {
			return "", false
		}
		if len(e.Keys) == 0 {
			return "[:]", true
		}
		parts := make([]string, len(e.Keys))
		for i := range e.Keys {
			k, ok1 := simpleExpr(e.Keys[i])
			v, ok2 := simpleExpr(e.Values[i])
			if !ok1 || !ok2 {
				return "", false
			}
			parts[i] = k + ": " + v
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	case *ast.CallExpr:
		if e.Trailing != nil || len(e.TypeArgs) > 0 {
			return "", false
		}
		fn, ok := simpleExpr(e.Fn)
		if !ok {
			return "", false
		}
		parts := make([]string, len(e.Args))
		for i, a := range e.Args {
			v, ok := simpleExpr(a.Value)
			if !ok {
				return "", false
			}
			if a.Name != nil {
				v = a.Name.Name + ": " + v
			}
			parts[i] = v
		}
		return fn + "(" + strings.Join(parts, ", ") + ")", true
	}
	return "", false
}

// Returns an operand of an operator in source form, in parentheses when
// it is itself an operation with two operands.
func operand(e ast.Expr) (string, bool) {
	s, ok := simpleExpr(e)
	if _, bin := e.(*ast.BinaryExpr); bin {
		s = "(" + s + ")"
	}
	return s, ok
}

// Returns the text of an operator token, such as `+`.
func opString(k lexer.Kind) string { return strings.Trim(k.String(), "'") }

func listString(elems []ast.Expr) (string, bool) {
	parts := make([]string, len(elems))
	for i, x := range elems {
		s, ok := simpleExpr(x)
		if !ok {
			return "", false
		}
		parts[i] = s
	}
	return "[" + strings.Join(parts, ", ") + "]", true
}

// Returns [s] as a Vex string literal.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\', '{', '}':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
