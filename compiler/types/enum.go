package types

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
)

func (c *checker) declareEnum(ed *ast.EnumDecl) {
	e := &Enum{Name: ed.Name.Name, Module: c.mod, Decl: ed}
	if c.reserved(ed.Name, "an enum") || c.ifaceTaken(ed.Name) {
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
	if g := c.genEnums[e.Name]; g != nil {
		c.errorAt(ed.Name, "", "enum '%s' is already declared on line %d", e.Name, g.decl.Pos().Line)
		return
	}
	c.enums[e.Name] = e
	c.info.Enums = append(c.info.Enums, e)
}

// Resolves the cases and method signatures of [e].
func (c *checker) enumMembers(e *Enum) {
	defer c.enter(e.Decl)()
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
		if !e.abstract {
			c.info.FuncOf[md] = m
		}
		if c.reserved(md.Name, "a method") {
			continue
		}
		if prev := e.member(m.Name); prev != nil {
			c.errorAt(md.Name, "", "'%s' is already declared on line %d", m.Name, prev.Decl.Pos().Line)
			continue
		}
		if e.Case(m.Name) != nil {
			c.errorAt(md.Name, "", "'%s' is already a case of %s", m.Name, e.Name)
			continue
		}
		e.Methods = append(e.Methods, m)
		if c.genericMethod(m, e.abstract, e.generic == nil || isTemplate(e.Args, e.generic.params)) {
			continue
		}
		if !e.abstract {
			c.info.Funcs = append(c.info.Funcs, m)
		}
		c.signature(m)
	}
	c.enumConformances(e)
}

// Returns the enum named by [x] when it is used as a type, as in
// `Shape.circle(...)`, or nil.
func (c *checker) enumRef(x ast.Expr) *Enum {
	if ta, ok := x.(*ast.TypeArgsExpr); ok {
		g := c.genEnumRef(ta.X)
		if g == nil {
			return nil
		}
		te := &ast.NamedType{Span: ta.Span, Name: g.decl.Name.Name, Args: ta.Args}
		e, _ := c.genericEnumInst(te, g, c.typeArgs(te)).(*Enum)
		if e == nil {
			e = &Enum{Name: g.decl.Name.Name, Decl: g.decl}
		}
		return e
	}
	id, ok := x.(*ast.Ident)
	if ok && id.Module != "" {
		_, e := c.moduleRef(id)
		return e
	}
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
func IsPanic(e ast.Expr) bool {
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
		if a.Block != nil && !terminates(a.Block) || a.Expr != nil && !IsPanic(a.Expr) {
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

	var result Type
	if !stmt && want != Void {
		result = want
	}
	var rows []patRow
	sound := subject != Invalid
	seen := map[string]int{}
	wildcard := 0
	for _, arm := range m.Arms {
		a := &MatchArm{}
		info.Arms = append(info.Arms, a)
		c.push()
		before := len(c.errs)
		b := newBinder(PatternVar, false)
		c.pattern(arm.Pattern, subject, b, true, arm.Guard != nil)
		a.Vars = b.order
		if arm.Guard != nil {
			c.cond(arm.Guard, "when")
		}
		if len(c.errs) > before {
			sound = false
		} else if subject != Invalid {
			sp := c.lower(arm.Pattern, subject)
			key := simpleKey(arm.Pattern, c.info)
			if !c.exhaust().useful(rows, []*spat{sp}, []Type{subject}) {
				switch {
				case wildcard > 0:
					c.errorAt(arm.Pattern, "remove it, or move _ to the end",
						"this arm is never reached: _ on line %d matches everything", wildcard)
				case key != "" && seen[key] > 0 && strings.HasPrefix(key, "case "):
					c.errorAt(arm.Pattern, "remove one of the arms", "case '%s' is already handled on line %d", key[5:], seen[key])
				case key != "" && seen[key] > 0:
					c.errorAt(arm.Pattern, "remove one of the arms", "%s is already handled on line %d", key, seen[key])
				default:
					c.errorAt(arm.Pattern, "remove it, or move it above the arms that cover it",
						"this arm is never reached: earlier arms match everything it does")
				}
			}
			if arm.Guard == nil {
				rows = append(rows, patRow{[]*spat{sp}})
				if _, ok := arm.Pattern.(*ast.WildcardPattern); ok && wildcard == 0 {
					wildcard = arm.Pattern.Pos().Line
				}
				if key != "" && seen[key] == 0 {
					seen[key] = arm.Pattern.Pos().Line
				}
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
		case IsPanic(arm.Expr):
			c.expr(arm.Expr, nil)
			a.NoValue = true
		default:
			t := c.expr(arm.Expr, result)
			switch {
			case result == nil || result == Invalid:
				result = t
			case !assignableType(result, t):
				c.errorAt(arm.Expr, conversionHint(result, t), "match arms must have the same type: expected %s, found %s", result, t)
			}
		}
		c.pop()
	}

	if sound {
		c.checkExhaustive(m, subject, rows)
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

// Returns a key naming what a simple arm pattern matches, `case circle`
// or a literal, for messages about repeated arms; "" for other patterns.
func simpleKey(p ast.Pattern, info *Info) string {
	switch p := p.(type) {
	case *ast.NamePattern:
		if cs := info.PatternCases[p]; cs != nil {
			return "case " + cs.Name
		}
	case *ast.CasePattern:
		cs := info.PatternCases[p]
		if cs == nil {
			return ""
		}
		for _, a := range p.Args {
			switch a.Pattern.(type) {
			case *ast.WildcardPattern, *ast.NamePattern:
				if info.PatternCases[a.Pattern] != nil {
					return ""
				}
			default:
				return ""
			}
		}
		return "case " + cs.Name
	case *ast.LiteralPattern:
		return ast.PatternString(p)
	}
	return ""
}

// Reports values a match leaves out.
func (c *checker) checkExhaustive(m *ast.MatchExpr, subject Type, rows []patRow) {
	e := c.exhaust()
	w := e.missing(rows, []Type{subject})
	if w == nil {
		return
	}
	var missing []string
	switch subject.(type) {
	case *Enum:
	default:
		if subject != Bool {
			switch {
			case w[0] != "_":
			case subject == Int || subject == Float || subject == String:
				c.errorAtPos(m.Pos(), 5, "add _ => ... as the last arm", "a match on %s needs a _ arm for the other values", subject)
				return
			default:
				c.errorAtPos(m.Pos(), 5, "add _ => ... as the last arm", "match doesn't handle every %s value", subject)
				return
			}
			missing = []string{w[0]}
		}
	}
	if missing == nil {
		for _, k := range ctors(subject, nil) {
			spec := specializeRows(rows, k)
			if len(spec) == 0 {
				missing = append(missing, k.key)
			} else if w := e.missing(spec, k.types); w != nil {
				missing = append(missing, show(subject, k, w))
			}
		}
	}
	switch len(missing) {
	case 0:
	case 1:
		c.errorAtPos(m.Pos(), 5, fmt.Sprintf("add %s => ..., or _ => ...", missing[0]), "match doesn't handle %s", quoteList(missing))
	default:
		c.errorAtPos(m.Pos(), 5, "add the missing arms, or _ => ...", "match doesn't handle %s", quoteList(missing))
	}
}
