package types

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/lexer"
)

// A struct or enum, which can define operators.
type opOwner interface {
	Type
	Operator(name string) *Func
}

// Returns the name of the struct or enum [t] as written in its
// declaration, and the keyword that declares it.
func ownerName(t opOwner) (string, string) {
	if e, ok := t.(*Enum); ok {
		return e.Name, "enum"
	}
	return t.(*Struct).Name, "struct"
}

// Checks the declaration of operator [m] of struct or enum [t].
func (c *checker) checkOperator(t opOwner, m *Func) {
	md := m.Decl
	name, _ := ownerName(t)
	for _, p := range md.Params {
		if p.Default != nil {
			c.errorAt(p.Default, "", "operator parameters can't have default values")
		}
	}
	switch m.Name {
	case "==", "<":
		if len(m.Params) == 1 && m.Params[0].Type != Invalid && !Identical(m.Params[0].Type, t) {
			c.errorAt(md.Params[0].Type, fmt.Sprintf("write operator %s(other: %s) -> bool", m.Name, name),
				"operator '%s' compares two %s values, so it takes a %s", m.Name, name, name)
		}
		if m.Result != Invalid && !Identical(m.Result, Bool) {
			c.errorAt(resultNode(md), fmt.Sprintf("write operator %s(other: %s) -> bool", m.Name, name), "operator '%s' must return bool", m.Name)
		}
	case "[]=":
		if m.Result != Void {
			c.errorAt(md.Result, "remove the result type", "operator '[]=' doesn't return a value")
		}
	default:
		if m.Result == Void {
			c.errorAt(md.Name, fmt.Sprintf("give its result type: operator %s(...) -> %s", strings.TrimPrefix(m.Name, "unary "), name),
				"operator '%s' must return a value", m.Name)
		}
	}
}

// Returns where the result type of [fd] is written, or its name when it
// has none.
func resultNode(fd *ast.FuncDecl) ast.Node {
	if fd.Result != nil {
		return fd.Result
	}
	return fd.Name
}

// The struct operator each binary operator runs. `!=` runs `==`, and the
// ordering operators run `<`.
var binaryOperators = map[lexer.Kind]string{
	lexer.Plus: "+", lexer.Minus: "-", lexer.Star: "*", lexer.Slash: "/", lexer.Percent: "%",
	lexer.Eq: "==", lexer.NotEq: "==", lexer.Lt: "<", lexer.Gt: "<", lexer.LtEq: "<", lexer.GtEq: "<",
}

// The binary operator of each compound assignment.
var compoundOperators = map[lexer.Kind]string{
	lexer.PlusAssign: "+", lexer.MinusAssign: "-", lexer.StarAssign: "*", lexer.SlashAssign: "/", lexer.PercentAssign: "%",
}

// Checks the binary expression [e] whose left operand is a value of struct
// or enum [s], which must define the operator.
func (c *checker) binaryOperator(e *ast.BinaryExpr, s opOwner) Type {
	written := strings.Trim(e.Op.String(), "'")
	sname, kind := ownerName(s)
	ext, _ := s.(*Struct)
	if ext != nil && ext.Underlying() == nil {
		ext = nil
	}
	name, ok := binaryOperators[e.Op]
	var f *Func
	if ok {
		f = s.Operator(name)
	}
	if f == nil {
		c.expr(e.Y, nil)
		switch {
		case !ok:
			c.errorAt(e, "", "'%s' works only on int, found %s", written, s)
		case name == "==" && ext != nil && ext.Module != nil && ext.Module.Path == "vex:crypto":
			c.errorAt(e, fmt.Sprintf("compare their bytes with crypto.equal(a.%s, b.%s), which takes the same time for any bytes", ext.Fields[0].Name, ext.Fields[0].Name), "values of type %s can't be compared with %s", s, written)
		case name == "==" && ext != nil:
			c.errorAt(e, fmt.Sprintf("list Equatable on it: %s: Equatable", ext.header()), "values of type %s can't be compared with %s", s, written)
		case name == "==":
			c.errorAt(e, fmt.Sprintf("list Equatable on it to compare the fields: %s %s: Equatable", kind, sname), "values of type %s can't be compared with %s", s, written)
		case name == "<" && ext != nil:
			c.errorAt(e, fmt.Sprintf("list Comparable on it: %s: Comparable", ext.header()), "values of type %s can't be ordered with %s", s, written)
		case name == "<":
			c.errorAt(e, fmt.Sprintf("define it in %s: operator <(other: %s) -> bool", sname, sname), "values of type %s can't be ordered with %s", s, written)
		default:
			c.errorAt(e, fmt.Sprintf("define it in %s: operator %s(other: ...) -> ...", sname, name), "'%s' can't be used with %s", written, s)
		}
		if comparison(e.Op) {
			return Bool
		}
		return Invalid
	}
	c.methodPrivate(e, f)
	if len(f.Params) == 1 {
		c.assignable(f.Params[0].Type, c.expr(e.Y, f.Params[0].Type), e.Y, "")
	}
	c.info.Operators[e] = f
	if comparison(e.Op) {
		return Bool
	}
	return f.Result
}

// Checks the target `x[i]` of assignment [s], where x is a value of struct
// [st], and returns the type of the value assigned, or nil.
func (c *checker) indexSet(s *ast.AssignStmt, x *ast.IndexExpr, st *Struct) Type {
	set := st.Operator("[]=")
	if set == nil {
		c.expr(x.Index, nil)
		c.expr(s.Value, nil)
		c.errorAt(x.X, fmt.Sprintf("define it in %s: operator []=(index: int, value: ...)", st.Name), "values of type %s can't be changed through []", st)
		return nil
	}
	c.methodPrivate(x, set)
	if len(set.Params) != 2 {
		return nil
	}
	c.assignable(set.Params[0].Type, c.expr(x.Index, set.Params[0].Type), x.Index, "index")
	if s.Op != lexer.Assign {
		op := strings.Trim(s.Op.String(), "'")
		get := st.Operator("[]")
		switch {
		case get == nil:
			c.errorAt(x.X, fmt.Sprintf("define it in %s: operator [](index: ...) -> ...", st.Name), "%s reads the value first, so %s needs operator [] too", op, st)
			return nil
		case !Identical(get.Params[0].Type, set.Params[0].Type) || !Identical(get.Result, set.Params[1].Type):
			c.errorAt(x.X, "make operator [] take the index and return the value operator []= takes",
				"%s needs operator [] and operator []= of %s to agree on types", op, st)
			return nil
		}
		c.methodPrivate(x, get)
		c.info.Operators[x] = get
	}
	c.info.IndexSets[x] = set
	t := set.Params[1].Type
	c.info.Types[x] = t
	return t
}

// Checks the compound assignment [s] to a value of struct or enum [st],
// which runs its operator.
func (c *checker) compoundOperator(s *ast.AssignStmt, st opOwner) {
	written := strings.Trim(s.Op.String(), "'")
	sname, _ := ownerName(st)
	name, ok := compoundOperators[s.Op]
	var f *Func
	if ok {
		f = st.Operator(name)
	}
	if f == nil {
		c.expr(s.Value, nil)
		hint := ""
		if ok {
			hint = fmt.Sprintf("define it in %s: operator %s(other: ...) -> %s", sname, name, sname)
		}
		c.errorAt(s.Target, hint, "%s can't be used with %s", written, st)
		return
	}
	c.methodPrivate(s.Target, f)
	if len(f.Params) == 1 {
		c.assignable(f.Params[0].Type, c.expr(s.Value, f.Params[0].Type), s.Value, "")
	}
	if !assignableType(st, f.Result) {
		c.errorAt(s.Target, fmt.Sprintf("use x = x %s ..., or return %s from operator %s", name, sname, name),
			"%s needs operator %s to return %s, but it returns %s", written, name, st, f.Result)
		return
	}
	c.info.Operators[s] = f
}
