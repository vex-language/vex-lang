package types

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// How the names a pattern binds are declared. A later alternative of `||`
// reuses the variables the first one declared.
type binder struct {
	kind    VarKind
	mutable bool
	vars    map[string]*Var
	order   []*Var
	alt     map[string]bool
	reuse   bool
}

func newBinder(kind VarKind, mutable bool) *binder {
	return &binder{kind: kind, mutable: mutable, vars: map[string]*Var{}, alt: map[string]bool{}}
}

// Declares [id], bound to a value of type [t].
func (c *checker) bindName(b *binder, id *ast.Ident, t Type) {
	if b.alt[id.Name] {
		c.errorAt(id, "use another name, or _", "'%s' is bound twice in this pattern", id.Name)
		return
	}
	b.alt[id.Name] = true
	if b.reuse {
		v := b.vars[id.Name]
		switch {
		case v == nil:
			c.errorAt(id, "bind the same names in every alternative", "'%s' isn't bound by the first alternative of ||", id.Name)
		case !Identical(v.Type, t) && v.Type != Invalid && t != Invalid:
			c.errorAt(id, "", "'%s' is %s here but %s in the first alternative", id.Name, article(t), article(v.Type))
		default:
			c.info.Defs[id] = v
		}
		return
	}
	v := c.declare(id, t, b.mutable, b.kind)
	b.vars[id.Name] = v
	b.order = append(b.order, v)
}

// Binds every name in [p] as invalid, after an error, so later uses of
// them aren't reported again.
func (c *checker) bindInvalid(p ast.Pattern, b *binder) {
	switch p := p.(type) {
	case *ast.NamePattern:
		if !b.alt[p.Name.Name] {
			c.bindName(b, p.Name, Invalid)
		}
	case *ast.TypedPattern:
		if !b.alt[p.Name.Name] {
			c.bindName(b, p.Name, Invalid)
		}
	case *ast.CasePattern:
		for _, f := range p.Args {
			c.bindInvalid(f.Pattern, b)
		}
	case *ast.RecordPattern:
		for _, f := range p.Fields {
			c.bindInvalid(f.Pattern, b)
		}
	case *ast.ListPattern:
		for _, e := range p.Elems {
			c.bindInvalid(e, b)
		}
	case *ast.RestPattern:
		if p.Bind != nil && !b.alt[p.Bind.Name] {
			c.bindName(b, p.Bind, Invalid)
		}
	case *ast.OrPattern:
		if len(p.Alts) > 0 {
			c.bindInvalid(p.Alts[0], b)
		}
	}
}

// Checks that [p] can test a value of type [t], and binds its names.
// [arm] is set for the whole pattern of a match arm, where a bare name
// must be a case unless the arm has a guard.
func (c *checker) pattern(p ast.Pattern, t Type, b *binder, arm, guarded bool) {
	c.info.PatternTypes[p] = t
	switch p := p.(type) {
	case *ast.WildcardPattern:
	case *ast.NamePattern:
		c.namePattern(p, t, b, arm, guarded)
	case *ast.CasePattern:
		c.casePattern(p, t, b)
	case *ast.TypedPattern:
		c.typedPattern(p, t, b)
	case *ast.RecordPattern:
		c.recordPattern(p, t, b)
	case *ast.ListPattern:
		c.listPattern(p, t, b)
	case *ast.LiteralPattern:
		switch t.(type) {
		case *Enum:
			c.errorAt(p, caseHint("match its cases, e.g. ", t.(*Enum), false), "a literal can't match a %s value", t.(*Enum).Name)
			return
		case *Struct, *Record, *List:
			c.errorAt(p, "", "a literal can't match a value of type %s", t)
			return
		}
		lt := c.expr(p.Value, t)
		if t != Invalid && !Identical(lt, t) {
			c.mismatch(p, t, lt, "pattern")
		}
	case *ast.RangePattern:
		if t != Int && t != Float {
			if t != Invalid {
				c.errorAt(p, "", "a range matches int or float values, found %s", t)
			}
			return
		}
		for _, x := range []ast.Expr{p.Lo, p.Hi} {
			if xt := c.expr(x, t); !Identical(xt, t) {
				c.mismatch(x, t, xt, "range bound")
				return
			}
		}
		if lo, hi, ok := literalNumbers(p.Lo, p.Hi); ok && (lo > hi || lo == hi && !p.Inclusive) {
			c.errorAt(p, "write the smaller bound first", "the range %s matches nothing", ast.PatternString(p))
		}
	case *ast.RelationalPattern:
		if !ordered(t) {
			if t != Invalid {
				c.errorAt(p, "", "a comparison matches int, float or string values, found %s", t)
			}
			return
		}
		if vt := c.expr(p.Value, t); !Identical(vt, t) {
			c.mismatch(p.Value, t, vt, "pattern")
		}
	case *ast.OrPattern:
		var first map[string]bool
		reuse := b.reuse
		for i, alt := range p.Alts {
			outer := b.alt
			b.alt = map[string]bool{}
			b.reuse = reuse || i > 0
			c.pattern(alt, t, b, arm, guarded)
			names := b.alt
			b.alt = outer
			for n := range names {
				b.alt[n] = true
			}
			if i == 0 {
				first = names
				continue
			}
			for n := range first {
				if !names[n] {
					c.errorAt(alt, "bind the same names in every alternative", "this alternative doesn't bind '%s'", n)
				}
			}
		}
		b.reuse = reuse
	}
}

// Returns the values of two number literals, when both are.
func literalNumbers(a, b ast.Expr) (float64, float64, bool) {
	x, ok1 := literalNumber(a)
	y, ok2 := literalNumber(b)
	return x, y, ok1 && ok2
}

func literalNumber(e ast.Expr) (float64, bool) {
	switch e := e.(type) {
	case *ast.IntLit:
		v, err := strconv.ParseInt(e.Value, 0, 64)
		return float64(v), err == nil
	case *ast.FloatLit:
		v, err := strconv.ParseFloat(e.Value, 64)
		return v, err == nil
	case *ast.UnaryExpr:
		v, ok := literalNumber(e.X)
		return -v, ok
	}
	return 0, false
}

func (c *checker) namePattern(p *ast.NamePattern, t Type, b *binder, arm, guarded bool) {
	name := p.Name.Name
	if en, ok := t.(*Enum); ok {
		if cs := en.Case(name); cs != nil {
			c.info.PatternCases[p] = cs
			return
		}
	}
	// A pattern names a constant to compare with it; variables declared
	// outside functions don't count, so their names bind as usual.
	if k := c.findConst(name); c.lookupVar(name) == nil && k != nil && !k.Lazy {
		if k := c.constIdent(p.Name); k != nil {
			switch {
			case k.Type == Invalid || t == Invalid:
			case !comparable(k.Type):
				c.errorAt(p, "use a guard: x when x == ...", "constant '%s' can't be a pattern: %s values can't be compared with ==", k, k.Type)
			case !Identical(k.Type, t):
				c.mismatch(p, t, k.Type, "pattern")
			}
			return
		}
	}
	if en, ok := t.(*Enum); ok {
		if arm && !guarded {
			c.errorAt(p.Name, didYouMean(name, enumNames(en)), "%s has no case '%s'", en.Name, name)
			c.bindName(b, p.Name, Invalid)
			return
		}
	} else if arm && !guarded && t != Invalid {
		c.errorAt(p.Name, "match literal values such as 1 or \"yes\", or _", "'%s' isn't a case: the match is on %s", name, t)
		c.bindName(b, p.Name, Invalid)
		return
	}
	c.bindName(b, p.Name, t)
}

func (c *checker) casePattern(p *ast.CasePattern, t Type, b *binder) {
	fail := func() { c.bindInvalid(p, b) }
	switch t := t.(type) {
	case *Enum:
		cs := t.Case(p.Name.Name)
		if cs == nil {
			c.errorAt(p.Name, didYouMean(p.Name.Name, enumNames(t)), "%s has no case '%s'", t.Name, p.Name.Name)
			fail()
			return
		}
		c.info.PatternCases[p] = cs
		if len(cs.Fields) == 0 {
			c.errorAt(p, "write it without (): "+cs.Name, "case '%s' carries no values", cs.Name)
			fail()
			return
		}
		fields := make([]slotField, len(cs.Fields))
		for i, f := range cs.Fields {
			fields[i] = slotField{f.Name, f.Type}
		}
		if named(p.Args) {
			c.namedFields(p.Args, fields, "case '"+cs.Name+"'", b)
			return
		}
		if len(p.Args) != len(cs.Fields) {
			names := make([]string, len(cs.Fields))
			for i, f := range cs.Fields {
				names[i] = f.Name
			}
			c.errorAt(p, fmt.Sprintf("write %s(%s), using _ for values you don't need", cs.Name, strings.Join(names, ", ")),
				"case '%s' carries %s, found %d", cs.Name, plural(len(cs.Fields), "value"), len(p.Args))
			fail()
			return
		}
		for i, a := range p.Args {
			c.pattern(a.Pattern, cs.Fields[i].Type, b, false, false)
		}
	case *Struct:
		if p.Name.Name != t.Name && p.Name.Name != t.Base {
			c.errorAt(p.Name, "", "a %s pattern can't match a value of type %s", p.Name.Name, t)
			fail()
			return
		}
		c.info.PatternStructs[p] = t
		for _, a := range p.Args {
			if a.Name == nil {
				c.errorAt(a.Pattern, fmt.Sprintf("name the field: %s(x: 0, :y)", p.Name.Name), "a struct pattern names its fields")
				fail()
				return
			}
		}
		fields := make([]slotField, len(t.Fields))
		for i, f := range t.Fields {
			fields[i] = slotField{f.Name, f.Type}
		}
		c.namedFields(p.Args, fields, t.Name, b)
	case *Interface:
		target := c.testTarget(&ast.NamedType{Span: p.Name.Span, Name: p.Name.Name}, t, "a struct pattern", p.Name)
		s, ok := target.(*Struct)
		if !ok {
			if target != nil {
				c.errorAt(p.Name, "bind it with a typed pattern, e.g. x: "+target.String(), "only a struct pattern can take %s apart", t)
			}
			fail()
			return
		}
		c.info.PatternTests[p] = s
		c.casePattern(p, s, b)
	default:
		if t != Invalid {
			c.errorAt(p.Name, "", "'%s(...)' can't match a value of type %s", p.Name.Name, t)
		}
		fail()
	}
}

type slotField struct {
	name string
	typ  Type
}

// Reports whether the fields of a pattern are given by name.
func named(fs []*ast.PatternField) bool {
	return len(fs) > 0 && fs[0].Name != nil
}

// Checks fields given by name against [fields] of [what]; any subset may
// be given.
func (c *checker) namedFields(args []*ast.PatternField, fields []slotField, what string, b *binder) {
	seen := map[string]bool{}
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.name
	}
	for _, a := range args {
		if a.Name == nil {
			c.errorAt(a.Pattern, "name every field, or none", "%s mixes named and positional fields", what)
			c.bindInvalid(a.Pattern, b)
			continue
		}
		var ft Type
		for _, f := range fields {
			if f.name == a.Name.Name {
				ft = f.typ
			}
		}
		switch {
		case ft == nil:
			c.errorAt(a.Name, didYouMean(a.Name.Name, names), "%s has no field '%s'", what, a.Name.Name)
			c.bindInvalid(a.Pattern, b)
		case seen[a.Name.Name]:
			c.errorAt(a.Name, "", "field '%s' is matched twice", a.Name.Name)
			c.bindInvalid(a.Pattern, b)
		default:
			seen[a.Name.Name] = true
			c.pattern(a.Pattern, ft, b, false, false)
		}
	}
}

func (c *checker) recordPattern(p *ast.RecordPattern, t Type, b *binder) {
	r, ok := t.(*Record)
	if !ok {
		if t != Invalid {
			c.errorAt(p, "", "only records can be taken apart, found %s", t)
		}
		c.bindInvalid(p, b)
		return
	}
	pos := 0
	seen := map[string]bool{}
	for _, f := range p.Fields {
		if f.Name == nil {
			pos++
		} else {
			seen[f.Name.Name] = true
		}
	}
	missing := false
	for _, nf := range r.Named {
		missing = missing || !seen[nf.Name]
	}
	if pos != len(r.Positional) || missing {
		c.errorAt(p, "name every field: "+patternFor(r), "the pattern must match every field of %s", r)
		c.bindInvalid(p, b)
		return
	}
	pos = 0
	for _, f := range p.Fields {
		if f.Name == nil {
			c.pattern(f.Pattern, r.Positional[pos], b, false, false)
			pos++
			continue
		}
		ft := r.Field(f.Name.Name)
		if ft == nil {
			c.errorAt(f.Name, didYouMean(f.Name.Name, r.FieldNames()), "%s has no field '%s'", r, f.Name.Name)
			c.bindInvalid(f.Pattern, b)
			continue
		}
		c.pattern(f.Pattern, ft, b, false, false)
	}
}

func (c *checker) listPattern(p *ast.ListPattern, t Type, b *binder) {
	l, ok := t.(*List)
	if !ok {
		if t != Invalid {
			c.errorAt(p, "", "a list pattern can't match a value of type %s", t)
		}
		c.bindInvalid(p, b)
		return
	}
	rests := 0
	for _, e := range p.Elems {
		if r, ok := e.(*ast.RestPattern); ok {
			rests++
			if rests > 1 {
				c.errorAt(r, "keep one ...", "a list pattern can have only one ...")
				continue
			}
			c.info.PatternTypes[r] = l
			if r.Bind != nil {
				c.bindName(b, r.Bind, l)
			}
			continue
		}
		c.pattern(e, l.Elem, b, false, false)
	}
}

// Checks a pattern in `let`, `var` or `for`, which must match every value
// of type [t].
func (c *checker) declPattern(p ast.Pattern, t Type, kind VarKind, mutable bool) {
	before := len(c.errs)
	b := newBinder(kind, mutable)
	c.pattern(p, t, b, false, false)
	c.info.PatternVars[p] = b.order
	if t == Invalid || len(c.errs) > before {
		return
	}
	if w := c.exhaust().missing([]patRow{{pats: []*spat{c.lower(p, t)}}}, []Type{t}); w != nil {
		c.errorAt(p, "use match or if case for patterns that can fail", "this pattern doesn't match every value: %s isn't handled", w[0])
	}
}
