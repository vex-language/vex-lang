package types

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/lexer"
)

// A constant: `const name = value`, or a `static const` of a struct or
// enum. A variable declared outside functions with `let` or `var` is one
// too, with [Lazy] set: it is built on first use, separately in each
// isolate.
type Const struct {
	Name   string
	Module *Module
	Decl   *ast.ConstDecl
	Type   Type

	// Set for `let` and `var`, and [Mutable] for `var`.
	Lazy    bool
	Mutable bool

	// The struct or enum a static constant belongs to, or nil.
	Owner *Struct
	Enum  *Enum

	// The value the compiler worked out, for constants of type int, bool
	// and string, and float literals: an int64, float64, bool or string.
	// Nil for constants built when the program starts.
	Value any

	unit  *unit
	state uint8
}

// Returns the constant's name, with its type for a static one.
func (k *Const) String() string {
	switch {
	case k.Owner != nil:
		return k.Owner.Name + "." + k.Name
	case k.Enum != nil:
		return k.Enum.Name + "." + k.Name
	}
	return k.Name
}

// Declares the top-level constant [d].
func (c *checker) declareConst(d *ast.ConstDecl) {
	if c.reserved(d.Name, "a constant") || c.ifaceTaken(d.Name) {
		return
	}
	name := d.Name.Name
	line := 0
	switch {
	case c.consts[name] != nil:
		line = c.consts[name].Decl.Pos().Line
	case c.funcs[name] != nil:
		line = c.funcs[name].Decl.Pos().Line
	case c.genFuncs[name] != nil:
		line = c.genFuncs[name].decl.Pos().Line
	case c.structs[name] != nil && c.structs[name].Decl != nil:
		line = c.structs[name].Decl.Pos().Line
	case c.enums[name] != nil:
		line = c.enums[name].Decl.Pos().Line
	case c.genStructs[name] != nil:
		line = c.genStructs[name].decl.Pos().Line
	case c.genEnums[name] != nil:
		line = c.genEnums[name].decl.Pos().Line
	}
	if line > 0 {
		c.errorAt(d.Name, "", "'%s' is already declared on line %d", name, line)
		return
	}
	c.consts[name] = &Const{Name: name, Module: c.mod, Decl: d, Lazy: d.Lazy, Mutable: d.Mutable, unit: c.unit}
}

// Declares the static constants of a struct or enum, whose other member
// names are [taken].
func (c *checker) staticConsts(decls []*ast.ConstDecl, owner *Struct, en *Enum, taken func(string) bool) []*Const {
	var out []*Const
	seen := map[string]int{}
	for _, d := range decls {
		name := d.Name.Name
		if c.reserved(d.Name, "a constant") {
			continue
		}
		if line := seen[name]; line > 0 {
			c.errorAt(d.Name, "", "constant '%s' is already declared on line %d", name, line)
			continue
		}
		if taken(name) {
			c.errorAt(d.Name, "use another name", "'%s' is already a member of this type", name)
			continue
		}
		seen[name] = d.Pos().Line
		out = append(out, &Const{Name: name, Module: c.mod, Decl: d, Lazy: d.Lazy, Mutable: d.Mutable, Owner: owner, Enum: en, unit: c.unit})
	}
	return out
}

// Checks [k] if it isn't yet, after the constants it uses. Constants are
// added to Info.Consts in that order.
func (c *checker) checkConst(k *Const) {
	switch k.state {
	case 2:
		return
	case 1:
		if k.Lazy {
			c.errorAt(k.Decl.Name, "give it a value that doesn't use it", "'%s' is used in its own starting value", k.Name)
		} else {
			c.errorAt(k.Decl.Name, "a constant can't be worked out from itself", "constant '%s' depends on itself", k.Name)
		}
		k.Type = Invalid
		return
	}
	k.state = 1
	saved := *c
	c.setUnit(k.unit)
	c.scope, c.fn, c.self, c.loops, c.writing, c.lits, c.tries, c.awaited = nil, nil, nil, 0, false, nil, nil, nil
	c.constOwner = k
	defer func() {
		c.unit, c.mod, c.file, c.imports, c.tables = saved.unit, saved.mod, saved.file, saved.imports, saved.tables
		c.scope, c.fn, c.self, c.loops, c.writing, c.lits, c.tries, c.awaited = saved.scope, saved.fn, saved.self, saved.loops, saved.writing, saved.lits, saved.tries, saved.awaited
		c.constOwner = saved.constOwner
		k.state = 2
	}()

	var want Type
	if k.Decl.Type != nil {
		want = c.resolveType(k.Decl.Type)
	}
	t := c.expr(k.Decl.Value, want)
	if want != nil {
		what := "constant '" + k.Name + "'"
		if k.Lazy {
			what = "'" + k.Name + "'"
		}
		c.assignable(want, t, k.Decl.Value, what)
		t = want
	}
	if k.Type == Invalid {
		return
	}
	k.Type = t
	if t == Invalid {
		return
	}
	if t == Void {
		c.errorAt(k.Decl.Value, "", "this expression has no value to store in a %s", map[bool]string{true: "variable", false: "constant"}[k.Lazy])
		k.Type = Invalid
		return
	}
	if k.Lazy {
		c.info.Consts = append(c.info.Consts, k)
		return
	}
	if !c.constExpr(k.Decl.Value) {
		k.Type = Invalid
		return
	}
	if v := c.fold(k.Decl.Value, t); v != nil {
		// An enum case stays in a global; [checker.constValue] finds it.
		if _, isCase := v.(*Case); !isCase {
			k.Value = v
		}
	}
	c.info.Consts = append(c.info.Consts, k)
}

// The values of the constants of the built-in types, such as `int.max`.
var basicConsts = map[string]any{
	"int.max":        int64(math.MaxInt64),
	"int.min":        int64(math.MinInt64),
	"float.infinity": math.Inf(1),
	"float.nan":      math.NaN(),
}

// Reports whether [e] is made only of values known when compiling, and
// reports the first part that isn't.
func (c *checker) constExpr(e ast.Expr) bool {
	bad := func(n ast.Node, hint, what string) bool {
		c.errorAt(n, hint, "a constant must be known when compiling, but %s isn't", what)
		return false
	}
	all := func(es []ast.Expr) bool {
		for _, x := range es {
			if !c.constExpr(x) {
				return false
			}
		}
		return true
	}
	if c.info.CaseValues[e] != nil {
		return true
	}
	switch e := e.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.NilLit:
		return true
	case *ast.InterpolatedString:
		return all(e.Exprs)
	case *ast.Ident:
		if k, ok := c.info.Uses[e].(*Const); ok && !k.Lazy {
			return true
		}
		return bad(e, "use a literal or another constant", "'"+e.Name+"'")
	case *ast.MemberExpr:
		m := c.info.Members[e]
		switch {
		case m != nil && m.Const != nil && !m.Const.Lazy:
			return true
		case m != nil && (m.RecordField != "" || m.Builtin == "len"):
			return c.constExpr(e.X)
		case m != nil && basicConsts[m.Builtin] != nil:
			return true
		}
		return bad(e, "use a literal or another constant", "'"+c.source(e)+"'")
	case *ast.UnaryExpr:
		return c.constExpr(e.X)
	case *ast.BinaryExpr:
		return c.constExpr(e.X) && c.constExpr(e.Y)
	case *ast.RangeExpr:
		return c.constExpr(e.Lo) && c.constExpr(e.Hi)
	case *ast.RecordLit:
		for _, f := range e.Fields {
			if !c.constExpr(f.Value) {
				return false
			}
		}
		return true
	case *ast.ListLit:
		if e.Body != nil {
			return bad(e, "list every element", "a list built with if, for or ...")
		}
		return all(e.Elems)
	case *ast.MapLit:
		if e.Body != nil {
			return bad(e, "list every entry", "a map built with if, for or ...")
		}
		return all(e.Keys) && all(e.Values)
	case *ast.CallExpr:
		if call := c.info.Calls[e]; call != nil && (call.Kind == CaseCall || call.Kind == ConstructorCall && call.Struct.Underlying() != nil) {
			for _, a := range e.Args {
				if !c.constExpr(a.Value) {
					return false
				}
			}
			return true
		}
		return bad(e, "compute it in a function instead", "a call")
	}
	return bad(e, "", "this expression")
}

// Works out the value of [e], of type [t], when it is an int, bool or
// string made of literals and other such constants, or a float literal.
// Returns nil otherwise. Reports overflow and division by zero.
func (c *checker) fold(e ast.Expr, t Type) any {
	if cs := c.info.CaseValues[e]; cs != nil && len(cs.Fields) == 0 {
		return cs
	}
	switch e := e.(type) {
	case *ast.IntLit:
		v, err := strconv.ParseInt(e.Value, 0, 64)
		if err != nil {
			return nil
		}
		if t == Float {
			return float64(v)
		}
		return v
	case *ast.FloatLit:
		if v, err := strconv.ParseFloat(e.Value, 64); err == nil {
			return v
		}
	case *ast.StringLit:
		return e.Value
	case *ast.BoolLit:
		return e.Value
	case *ast.Ident:
		if k, ok := c.info.Uses[e].(*Const); ok {
			return c.constValue(k)
		}
	case *ast.MemberExpr:
		if m := c.info.Members[e]; m != nil && m.Const != nil {
			return c.constValue(m.Const)
		}
		if m := c.info.Members[e]; m != nil && basicConsts[m.Builtin] != nil {
			return basicConsts[m.Builtin]
		}
	case *ast.InterpolatedString:
		if e.Specs != nil {
			return nil
		}
		var b strings.Builder
		for i, text := range e.Texts {
			b.WriteString(text)
			if i < len(e.Exprs) {
				switch v := c.fold(e.Exprs[i], c.info.Types[e.Exprs[i]]).(type) {
				case int64:
					b.WriteString(strconv.FormatInt(v, 10))
				case bool:
					b.WriteString(strconv.FormatBool(v))
				case string:
					b.WriteString(v)
				default:
					return nil
				}
			}
		}
		return b.String()
	case *ast.UnaryExpr:
		if IsMinInt(e) && t == Int {
			return int64(math.MinInt64)
		}
		x := c.fold(e.X, c.info.Types[e.X])
		switch v := x.(type) {
		case int64:
			if e.Op == lexer.Tilde {
				return ^v
			}
			if e.Op == lexer.Minus {
				if v == math.MinInt64 {
					c.errorAt(e, "", "this constant overflows int")
					return nil
				}
				return -v
			}
		case float64:
			if e.Op == lexer.Minus {
				return -v
			}
		case bool:
			if e.Op == lexer.Not {
				return !v
			}
		}
	case *ast.BinaryExpr:
		x, y := c.fold(e.X, c.info.Types[e.X]), c.fold(e.Y, c.info.Types[e.Y])
		if x == nil || y == nil {
			return nil
		}
		return c.foldBinary(e, x, y)
	}
	return nil
}

func (c *checker) foldBinary(e *ast.BinaryExpr, x, y any) any {
	switch a := x.(type) {
	case *Case:
		b, ok := y.(*Case)
		if !ok {
			return nil
		}
		switch e.Op {
		case lexer.Eq:
			return a == b
		case lexer.NotEq:
			return a != b
		}
	case int64:
		b, ok := y.(int64)
		if !ok {
			return nil
		}
		return c.foldInt(e, a, b)
	case bool:
		b, ok := y.(bool)
		if !ok {
			return nil
		}
		switch e.Op {
		case lexer.AndAnd:
			return a && b
		case lexer.OrOr:
			return a || b
		case lexer.Eq:
			return a == b
		case lexer.NotEq:
			return a != b
		}
	case string:
		b, ok := y.(string)
		if !ok {
			return nil
		}
		switch e.Op {
		case lexer.Plus:
			return a + b
		case lexer.Eq:
			return a == b
		case lexer.NotEq:
			return a != b
		case lexer.Lt:
			return a < b
		case lexer.LtEq:
			return a <= b
		case lexer.Gt:
			return a > b
		case lexer.GtEq:
			return a >= b
		}
	}
	return nil
}

func (c *checker) foldInt(e *ast.BinaryExpr, a, b int64) any {
	overflow := func() any {
		c.errorAt(e, "use &+, &- or &* to wrap around instead", "this constant overflows int")
		return nil
	}
	switch e.Op {
	case lexer.Plus:
		r := a + b
		if (r > a) != (b > 0) {
			return overflow()
		}
		return r
	case lexer.Minus:
		r := a - b
		if (r < a) != (b > 0) {
			return overflow()
		}
		return r
	case lexer.Star:
		if a != 0 && b != 0 {
			r := a * b
			if r/b != a || (a == -1 && b == math.MinInt64) || (b == -1 && a == math.MinInt64) {
				return overflow()
			}
			return r
		}
		return int64(0)
	case lexer.Slash, lexer.Percent:
		if b == 0 {
			c.errorAt(e, "", "this constant divides by zero")
			return nil
		}
		if a == math.MinInt64 && b == -1 {
			if e.Op == lexer.Percent {
				return int64(0)
			}
			return overflow()
		}
		if e.Op == lexer.Slash {
			return a / b
		}
		return a % b
	case lexer.AmpPlus:
		return int64(uint64(a) + uint64(b))
	case lexer.AmpMinus:
		return int64(uint64(a) - uint64(b))
	case lexer.AmpStar:
		return int64(uint64(a) * uint64(b))
	case lexer.Amp:
		return a & b
	case lexer.Pipe:
		return a | b
	case lexer.Caret:
		return a ^ b
	case lexer.Shl, lexer.Shr:
		if b < 0 || b > 63 {
			c.errorAt(e.Y, "", "this constant shifts by %d, outside 0 to 63", b)
			return nil
		}
		if e.Op == lexer.Shl {
			return int64(uint64(a) << b)
		}
		return a >> b
	case lexer.Eq:
		return a == b
	case lexer.NotEq:
		return a != b
	case lexer.Lt:
		return a < b
	case lexer.LtEq:
		return a <= b
	case lexer.Gt:
		return a > b
	case lexer.GtEq:
		return a >= b
	}
	return nil
}

// Returns the constant whose value [e] reads, directly or through
// indexing and record fields, or nil.
func (c *checker) constRoot(e ast.Expr) *Const {
	switch e := e.(type) {
	case *ast.Ident:
		if k, _ := c.info.Uses[e].(*Const); k != nil && !k.Lazy {
			return k
		}
	case *ast.MemberExpr:
		if m := c.info.Members[e]; m != nil {
			if m.Const != nil {
				if m.Const.Lazy {
					return nil
				}
				return m.Const
			}
			if m.RecordField != "" {
				return c.constRoot(e.X)
			}
		}
	case *ast.IndexExpr:
		return c.constRoot(e.X)
	}
	return nil
}

// Reports changing [e], a list or map, when it is part of a constant.
func (c *checker) constChange(e ast.Expr, what string) bool {
	k := c.constRoot(e)
	if k == nil {
		return false
	}
	c.errorAt(e, fmt.Sprintf("change a copy instead: var mine = %s.copy()", c.source(e)), "can't change a constant %s: '%s' is constant", what, k)
	return true
}

// Declares the static constants of every struct and enum.
func (c *checker) declareStaticConsts() {
	for _, d := range c.decls {
		back := c.enter(d)
		switch d := d.(type) {
		case *ast.StructDecl:
			s := c.structs[d.Name.Name]
			switch {
			case len(d.Consts) == 0:
			case len(d.TypeParams) > 0:
				c.errorAt(d.Consts[0].Name, "move it to the top level", "a generic struct can't have %s", staticKind(d.Consts[0]))
			case s != nil && s.Decl == d:
				s.Consts = c.staticConsts(d.Consts, s, nil, func(name string) bool {
					return s.Field(name) != nil || s.Method(name) != nil || s.Property(name) != nil
				})
			}
		case *ast.EnumDecl:
			e := c.enums[d.Name.Name]
			switch {
			case len(d.Consts) == 0:
			case len(d.TypeParams) > 0:
				c.errorAt(d.Consts[0].Name, "move it to the top level", "a generic enum can't have %s", staticKind(d.Consts[0]))
			case e != nil && e.Decl == d:
				e.Consts = c.staticConsts(d.Consts, nil, e, func(name string) bool {
					return e.Case(name) != nil || e.Method(name) != nil || e.Property(name) != nil
				})
			}
		case *ast.ExtensionDecl:
			c.extensionConsts(d)
		}
		back()
	}
}

// Declares the constants that extension [d] adds to a struct or enum.
func (c *checker) extensionConsts(d *ast.ExtensionDecl) {
	target := c.extTargets[d]
	if target == nil || len(d.Consts) == 0 {
		return
	}
	tables := c.units[target].mod.tables
	switch t := target.(type) {
	case *ast.StructDecl:
		s := tables.structs[t.Name.Name]
		if len(t.TypeParams) > 0 || s == nil {
			c.errorAt(d.Consts[0].Name, "move it to the top level", "a generic struct can't have constants")
			return
		}
		s.Consts = append(s.Consts, c.staticConsts(d.Consts, s, nil, func(name string) bool {
			return s.Field(name) != nil || s.Method(name) != nil || s.Property(name) != nil || constNamed(s.Consts, name) != nil
		})...)
	case *ast.EnumDecl:
		e := tables.enums[t.Name.Name]
		if len(t.TypeParams) > 0 || e == nil {
			c.errorAt(d.Consts[0].Name, "move it to the top level", "a generic enum can't have constants")
			return
		}
		e.Consts = append(e.Consts, c.staticConsts(d.Consts, nil, e, func(name string) bool {
			return e.Case(name) != nil || e.Method(name) != nil || e.Property(name) != nil || constNamed(e.Consts, name) != nil
		})...)
	}
}

// Reports assigning to [k] at [at], unless it is a `var`.
func (c *checker) constAssign(at ast.Node, k *Const) {
	switch {
	case k.Mutable:
	case k.Lazy:
		c.errorAt(at, fmt.Sprintf("declare it with var %s = ... to make it changeable", k.Name), "can't change '%s': it's declared with let", k)
	default:
		c.errorAt(at, "", "can't change constant '%s'", k)
	}
}

// Returns what [d] declares, in the plural, for errors.
func staticKind(d *ast.ConstDecl) string {
	if d.Lazy {
		return "static variables"
	}
	return "constants"
}

// Returns the constant called [name] in [ks], or nil.
func constNamed(ks []*Const, name string) *Const {
	for _, k := range ks {
		if k.Name == name {
			return k
		}
	}
	return nil
}

// Returns the static constant called [name] that a bare name can reach
// here: one of the type whose method or constant is being checked.
func (c *checker) ownConst(name string) *Const {
	var s *Struct
	var e *Enum
	switch {
	case c.constOwner != nil:
		s, e = c.constOwner.Owner, c.constOwner.Enum
	case c.fn != nil:
		s, e = c.fn.Owner, c.fn.Enum
	}
	if s != nil {
		return constNamed(s.Consts, name)
	}
	if e != nil {
		return constNamed(e.Consts, name)
	}
	return nil
}

// Returns the constant or variable a bare [name] reaches here, or nil.
func (c *checker) findConst(name string) *Const {
	if k := c.ownConst(name); k != nil {
		return k
	}
	return c.consts[name]
}

// Resolves [id] to a constant, or returns nil.
func (c *checker) constIdent(id *ast.Ident) *Const {
	k := c.findConst(id.Name)
	if k == nil {
		return nil
	}
	c.checkConst(k)
	c.info.Uses[id] = k
	return k
}

// Resolves `Type.name` to a static constant of [s] or [e], or returns nil.
func (c *checker) staticConst(e *ast.MemberExpr, s *Struct, en *Enum) *Const {
	var k *Const
	if s != nil {
		k = constNamed(s.Consts, e.Name.Name)
	} else {
		k = constNamed(en.Consts, e.Name.Name)
	}
	if k == nil {
		return nil
	}
	if c.private(e.Name, k.Module, k.Decl.Pub, "constant '"+k.String()+"'") {
		return k
	}
	c.checkConst(k)
	c.info.Members[e] = &Member{Const: k}
	return k
}

// Returns the source text of [n].
func (c *checker) source(n ast.Node) string {
	start, end := n.Pos().Offset, n.EndOffset()
	if c.file == nil || start < 0 || end > len(c.file.Src) || start > end {
		return ""
	}
	return c.file.Src[start:end]
}

// Returns the value of [k] when it is known: [Const.Value], or the enum
// case without fields that its starting value names.
func (c *checker) constValue(k *Const) any {
	if k.Value != nil || k.Lazy || k.Decl == nil || k.Decl.Value == nil {
		return k.Value
	}
	if cs := c.info.CaseValues[k.Decl.Value]; cs != nil && len(cs.Fields) == 0 {
		return cs
	}
	return nil
}
