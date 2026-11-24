package types

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
)

func (c *checker) declareEnum(ed *ast.EnumDecl) {
	e := &Enum{Name: ed.Name.Name, Decl: ed}
	if c.reserved(ed.Name, "an enum") {
		return
	}
	if prev := c.structs[e.Name]; prev != nil {
		c.errorAt(ed.Name, "", "'%s' is already declared as a struct on line %d", e.Name, prev.Decl.Pos().Line)
		return
	}
	if prev := c.enums[e.Name]; prev != nil {
		c.errorAt(ed.Name, "", "enum '%s' is already declared on line %d", e.Name, prev.Decl.Pos().Line)
		return
	}
	c.enums[e.Name] = e
	c.info.Enums = append(c.info.Enums, e)
}

// Resolves the cases and method signatures of [e].
func (c *checker) enumMembers(e *Enum) {
	for _, cd := range e.Decl.Cases {
		if c.reserved(cd.Name, "a case") {
			continue
		}
		if prev := e.Case(cd.Name.Name); prev != nil {
			c.errorAt(cd.Name, "", "case '%s' is already declared on line %d", cd.Name.Name, prev.Decl.Pos().Line)
			continue
		}
		cs := &Case{Name: cd.Name.Name, Enum: e, Index: len(e.Cases), Decl: cd}
		for _, fd := range cd.Fields {
			for _, f := range cs.Fields {
				if f.Name == fd.Name.Name {
					c.errorAt(fd.Name, "", "case value '%s' is declared twice", f.Name)
				}
			}
			cs.Fields = append(cs.Fields, &Field{Name: fd.Name.Name, Type: c.resolveType(fd.Type), Decl: fd})
		}
		e.Cases = append(e.Cases, cs)
	}
	if len(e.Cases) == 0 {
		c.errorAt(e.Decl.Name, "add cases, e.g. enum "+e.Name+" { first, second }", "enum '%s' has no cases", e.Name)
	}
	for _, md := range e.Decl.Methods {
		m := &Func{Name: md.Name.Name, Decl: md, Enum: e, Static: md.Static}
		c.info.FuncOf[md] = m
		if c.reserved(md.Name, "a method") {
			continue
		}
		if prev := e.Method(m.Name); prev != nil {
			c.errorAt(md.Name, "", "method '%s' is already declared on line %d", m.Name, prev.Decl.Pos().Line)
			continue
		}
		if e.Case(m.Name) != nil {
			c.errorAt(md.Name, "", "'%s' is already a case of %s", m.Name, e.Name)
			continue
		}
		e.Methods = append(e.Methods, m)
		c.info.Funcs = append(c.info.Funcs, m)
		c.signature(m)
	}
}

// Returns the enum named by [x] when it is used as a type, as in
// `Shape.circle(...)`, or nil.
func (c *checker) enumRef(x ast.Expr) *Enum {
	id, ok := x.(*ast.Ident)
	if !ok || c.lookupVar(id.Name) != nil {
		return nil
	}
	if owner := c.ownStruct(); owner != nil && owner.Field(id.Name) != nil {
		return nil
	}
	return c.enums[id.Name]
}

// Returns [prefix] followed by the first case of [e], qualified with the
// enum name if [qualified], or "" when [e] has no cases.
func caseHint(prefix string, e *Enum, qualified bool) string {
	if len(e.Cases) == 0 {
		return ""
	}
	if qualified {
		return prefix + e.Name + "." + e.Cases[0].Name
	}
	return prefix + e.Cases[0].Name
}

// Returns the names of the cases and methods of [e], for suggestions.
func enumNames(e *Enum) []string {
	var names []string
	for _, cs := range e.Cases {
		names = append(names, cs.Name)
	}
	for _, m := range e.Methods {
		names = append(names, m.Name)
	}
	return names
}

// Checks a case used as a value without a call: `Shape.empty` or `.empty`.
// [written] is how the case was spelled, for hints.
func (c *checker) caseValue(x ast.Expr, e *Enum, name *ast.Ident, written string) Type {
	cs := e.Case(name.Name)
	if cs == nil {
		if e.Method(name.Name) != nil {
			c.errorAt(name, fmt.Sprintf("call it: %s.%s()", e.Name, name.Name), "method '%s' must be called", name.Name)
		} else {
			c.errorAt(name, didYouMean(name.Name, enumNames(e)), "%s has no case '%s'", e.Name, name.Name)
		}
		return Invalid
	}
	if len(cs.Fields) > 0 {
		c.errorAt(name, fmt.Sprintf("give its values: %s(%s: ...)", written, cs.Fields[0].Name),
			"case '%s' carries values, so it must be called", cs.Name)
		return Invalid
	}
	c.info.CaseValues[x] = cs
	return e
}

// Checks a call that creates a case: `Shape.circle(radius: 1)`.
func (c *checker) caseCall(e *ast.CallExpr, en *Enum, name *ast.Ident, written string) Type {
	cs := en.Case(name.Name)
	if cs == nil {
		c.checkArgs(e)
		c.errorAt(name, didYouMean(name.Name, enumNames(en)), "%s has no case '%s'", en.Name, name.Name)
		return Invalid
	}
	if len(cs.Fields) == 0 {
		c.checkArgs(e)
		c.errorAt(name, "write it without (): "+written, "case '%s' carries no values", cs.Name)
		return en
	}
	slots := make([]slot, len(cs.Fields))
	for i, f := range cs.Fields {
		slots[i] = slot{name: f.Name, typ: f.Type}
	}
	c.info.Calls[e] = &Call{Kind: CaseCall, Case: cs, Args: c.bind(e, slots, written)}
	return en
}

// Returns the enum the context expects for `.name`, reporting an error if
// there is none.
func (c *checker) implicitEnum(im *ast.ImplicitMember, want Type) *Enum {
	if e, ok := want.(*Enum); ok {
		return e
	}
	hint := "write the enum name, e.g. Shape." + im.Name.Name
	if want != nil && want != Invalid {
		c.errorAt(im, "", "'.%s' needs an enum here, but %s is expected", im.Name.Name, want)
		return nil
	}
	c.errorAt(im, hint, "can't tell which enum '.%s' belongs to", im.Name.Name)
	return nil
}

// --- Match ---

// Reports whether [e] is a call of `panic`, which never returns.
func isPanic(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fn.(*ast.Ident)
	return ok && id.Name == "panic"
}

// Reports whether every arm of [m] ends by returning or stopping.
func matchTerminates(m *ast.MatchExpr) bool {
	for _, a := range m.Arms {
		if a.Block != nil && !terminates(a.Block) || a.Expr != nil && !isPanic(a.Expr) {
			return false
		}
	}
	return len(m.Arms) > 0
}

// Checks a match and returns its type: the arms' common type, or void when
// [stmt] is set.
func (c *checker) match(m *ast.MatchExpr, want Type, stmt bool) Type {
	subject := c.expr(m.Subject, nil)
	info := &Match{Stmt: stmt}
	c.info.Matches[m] = info
	en, _ := subject.(*Enum)
	info.Enum = en
	switch {
	case en != nil, subject == Int, subject == Bool, subject == String, subject == Invalid:
	case subject == Float:
		c.errorAt(m.Subject, "compare with if ... else instead", "can't match on float values")
		subject = Invalid
	default:
		c.errorAt(m.Subject, "use if ... else instead", "can't match on a value of type %s", subject)
		subject = Invalid
	}

	var result Type
	if !stmt && want != Void {
		result = want
	}
	covered := map[*Case]int{}
	literals := map[string]int{}
	wildcard := 0
	for _, arm := range m.Arms {
		a := &MatchArm{}
		info.Arms = append(info.Arms, a)
		if wildcard > 0 {
			c.errorAt(arm.Pattern, "remove it, or move _ to the end",
				"this arm is never reached: _ on line %d matches everything", wildcard)
		}
		c.push()
		switch p := arm.Pattern.(type) {
		case *ast.WildcardPattern:
			if wildcard == 0 {
				wildcard = p.Pos().Line
			}
		case *ast.CasePattern:
			c.casePattern(p, a, en, subject, covered)
		case *ast.LiteralPattern:
			t := c.expr(p.Value, subject)
			key := ast.Sexpr(p.Value)
			switch {
			case en != nil:
				c.errorAt(p, caseHint("match its cases, e.g. ", en, false), "a literal can't match a %s value", en.Name)
			case subject == Invalid:
			case !Identical(t, subject):
				c.mismatch(p, subject, t, "pattern")
			case literals[key] > 0:
				c.errorAt(p, "remove one of the arms", "%s is already handled on line %d", key, literals[key])
			default:
				literals[key] = p.Pos().Line
			}
		}

		switch {
		case arm.Block != nil:
			if !stmt {
				c.errorAt(arm.Block, "use an expression, or use the match as a statement", "a match used as a value needs an expression after =>")
				result = Invalid
			}
			c.stmt(arm.Block)
		case stmt:
			c.expr(arm.Expr, nil)
		case isPanic(arm.Expr):
			c.expr(arm.Expr, nil)
			a.NoValue = true
		default:
			t := c.expr(arm.Expr, result)
			switch {
			case result == nil || result == Invalid:
				result = t
			case !Identical(result, t):
				c.errorAt(arm.Expr, conversionHint(result, t), "match arms must have the same type: expected %s, found %s", result, t)
			}
		}
		c.pop()
	}

	if wildcard == 0 && subject != Invalid {
		c.checkExhaustive(m, en, subject, covered, literals)
	}
	if stmt {
		return Void
	}
	if result == nil {
		if want != nil {
			return want
		}
		return Void
	}
	return result
}

// Checks a case pattern such as `circle(r)` and declares its bindings.
func (c *checker) casePattern(p *ast.CasePattern, a *MatchArm, en *Enum, subject Type, covered map[*Case]int) {
	if !c.bindCase(p, a, en, subject, covered) {
		for _, b := range p.Binds {
			if b.Name != "_" {
				c.declare(b, Invalid, false, PatternVar)
			}
		}
	}
}

// Checks [p] and declares its bindings, reporting whether they were
// declared.
func (c *checker) bindCase(p *ast.CasePattern, a *MatchArm, en *Enum, subject Type, covered map[*Case]int) bool {
	if en == nil {
		if subject != Invalid {
			c.errorAt(p.Name, "match literal values such as 1 or \"yes\", or _",
				"'%s' isn't a case: the match is on %s", p.Name.Name, subject)
		}
		return false
	}
	cs := en.Case(p.Name.Name)
	if cs == nil {
		c.errorAt(p.Name, didYouMean(p.Name.Name, enumNames(en)), "%s has no case '%s'", en.Name, p.Name.Name)
		return false
	}
	a.Case = cs
	if line := covered[cs]; line > 0 {
		c.errorAt(p, "remove one of the arms", "case '%s' is already handled on line %d", cs.Name, line)
	} else {
		covered[cs] = p.Pos().Line
	}
	if p.Binds == nil {
		return true
	}
	if len(cs.Fields) == 0 {
		c.errorAt(p, "write it without (): "+cs.Name, "case '%s' carries no values", cs.Name)
		return false
	}
	if len(p.Binds) != len(cs.Fields) {
		names := make([]string, len(cs.Fields))
		for i, f := range cs.Fields {
			names[i] = f.Name
		}
		c.errorAt(p, fmt.Sprintf("write %s(%s), using _ for values you don't need", cs.Name, strings.Join(names, ", ")),
			"case '%s' carries %s, found %d", cs.Name, plural(len(cs.Fields), "value"), len(p.Binds))
		return false
	}
	a.Binds = make([]*Var, len(p.Binds))
	for i, b := range p.Binds {
		if b.Name != "_" {
			a.Binds[i] = c.declare(b, cs.Fields[i].Type, false, PatternVar)
		}
	}
	return true
}

// Reports cases or values a match without `_` leaves out.
func (c *checker) checkExhaustive(m *ast.MatchExpr, en *Enum, subject Type, covered map[*Case]int, literals map[string]int) {
	var missing []string
	switch {
	case en != nil:
		for _, cs := range en.Cases {
			if covered[cs] == 0 {
				missing = append(missing, cs.Name)
			}
		}
	case subject == Bool:
		for _, v := range []string{"true", "false"} {
			if literals[v] == 0 {
				missing = append(missing, v)
			}
		}
	default:
		c.errorAtPos(m.Pos(), 5, "add _ => ... as the last arm", "a match on %s needs a _ arm for the other values", subject)
		return
	}
	switch len(missing) {
	case 0:
	case 1:
		c.errorAtPos(m.Pos(), 5, fmt.Sprintf("add %s => ..., or _ => ...", missing[0]), "match doesn't handle %s", quoteList(missing))
	default:
		c.errorAtPos(m.Pos(), 5, "add the missing arms, or _ => ...", "match doesn't handle %s", quoteList(missing))
	}
}
