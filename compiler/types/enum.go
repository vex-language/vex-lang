package types

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/lexer"
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
	c.rawValues(e)
	for _, md := range e.Decl.Methods {
		m := &Func{Name: md.Name.Name, Decl: md, Enum: e, Static: md.Static, Ext: c.extModule(md)}
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
		if md.Accessor == ast.Operator && m.Name == "[]=" {
			c.errorAt(md.Name, "return a changed copy from a method instead", "enum values never change, so an enum can't define operator []=")
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
		if md.Accessor == ast.Operator {
			c.checkOperator(e, m)
		}
	}
	c.enumConformances(e)
}

// Resolves the raw type of [e] and the raw value of each case: the value
// written after `=`, or for int the previous case's plus one, starting
// at 0, and for string the case's name.
func (c *checker) rawValues(e *Enum) {
	d := e.Decl
	if d.RawType != nil {
		switch t := builtinTypes[d.RawType.Name]; t {
		case Int, String:
			e.Raw = t
		default:
			c.errorAt(d.RawType, fmt.Sprintf("use int or string: enum %s: int", d.Name.Name), "raw values must be int or string, found %s", d.RawType.Name)
		}
	}
	next := int64(0)
	seen := map[any]*Case{}
	for _, cs := range e.Cases {
		v := cs.Decl.Value
		switch {
		case len(cs.Fields) > 0 && v != nil:
			c.errorAt(v, "remove the raw value, or the values the case carries", "case '%s' carries values, so it can't have a raw value", cs.Name)
			continue
		case len(cs.Fields) > 0 && d.RawType != nil:
			c.errorAt(cs.Decl.Name, fmt.Sprintf("remove the values, or remove ': %s'", d.RawType.Name), "case '%s' carries values, so %s can't have raw values", cs.Name, e.Name)
			continue
		case v != nil && d.RawType == nil:
			c.errorAt(v, fmt.Sprintf("give the enum a raw type: enum %s: int", d.Name.Name), "case '%s' has a raw value, but %s has no raw type", cs.Name, e.Name)
			continue
		case e.Raw == nil:
			continue
		}
		switch {
		case v == nil && e.Raw == Int:
			cs.Raw = next
		case v == nil:
			cs.Raw = cs.Name
		default:
			cs.Raw = c.rawLiteral(v, e.Raw)
			if cs.Raw == nil {
				continue
			}
		}
		if n, ok := cs.Raw.(int64); ok {
			next = n + 1
		}
		if prev := seen[cs.Raw]; prev != nil {
			at := ast.Node(cs.Decl.Name)
			if v != nil {
				at = v
			}
			c.errorAt(at, "give each case its own raw value", "raw value %s is already used by case '%s'", rawString(cs.Raw), prev.Name)
			continue
		}
		seen[cs.Raw] = cs
	}
}

// Returns the value of the raw value literal [v] of type [t], or nil after
// reporting an error.
func (c *checker) rawLiteral(v ast.Expr, t Type) any {
	var val any
	switch x := v.(type) {
	case *ast.IntLit:
		val = c.fold(x, Int)
	case *ast.UnaryExpr:
		if _, ok := x.X.(*ast.IntLit); ok && x.Op == lexer.Minus {
			if n, ok := c.fold(x.X, Int).(int64); ok {
				val = -n
			}
		}
	case *ast.StringLit:
		val = x.Value
	}
	switch val.(type) {
	case int64:
		if t == Int {
			return val
		}
	case string:
		if t == String {
			return val
		}
	default:
		example := "1"
		if t == String {
			example = `"a"`
		}
		c.errorAt(v, "write a literal, like "+example, "a raw value must be a literal %s", t)
		return nil
	}
	c.errorAt(v, "", "raw values of this enum are %s, but this is %s", t, map[Type]string{Int: "a string", String: "an int"}[t])
	return nil
}

// Returns the raw value [v] as written in source.
func rawString(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	return fmt.Sprint(v)
}

// Checks `Color.allCases`, the list of every case of [en] in declaration
// order.
func (c *checker) allCases(e *ast.MemberExpr, en *Enum) Type {
	for _, cs := range en.Cases {
		if len(cs.Fields) > 0 {
			c.errorAt(e.Name, "list the cases yourself, or match on a value", "allCases needs an enum whose cases carry no values, but case '%s' of %s does", cs.Name, en.Name)
			return Invalid
		}
	}
	c.info.Members[e] = &Member{Builtin: "enum.allCases"}
	return &List{Elem: en}
}

// Checks a call of the enum [en] itself, which finds a case by its raw
// value: `Color(rawValue: 2)`. [prefix] is the module the enum is written
// with, for hints.
func (c *checker) enumCall(e *ast.CallExpr, en *Enum, id *ast.Ident, prefix string) Type {
	byRaw := len(e.Args) == 1 && e.Args[0].Name != nil && e.Args[0].Name.Name == "rawValue"
	if byRaw && en.Raw == nil && (en.Decl == nil || en.Decl.RawType == nil) {
		c.checkArgs(e)
		c.errorAt(id, fmt.Sprintf("give it a raw type: enum %s: int { ... }", en.Name), "%s has no raw values", en.Name)
		return Invalid
	}
	if en.Raw == nil || !byRaw {
		c.checkArgs(e)
		hint := caseHint("create it from a case, e.g. "+prefix, en, true)
		if en.Raw != nil {
			hint = fmt.Sprintf("find a case by its raw value: %s%s(rawValue: ...), or create it from a case", prefix, en.Name)
		}
		c.errorAt(id, hint, "enums are created from one of their cases")
		return Invalid
	}
	c.info.Calls[e] = &Call{Kind: RawCall, Enum: en, Args: c.bind(e, []slot{{name: "rawValue", typ: en.Raw}}, prefix+en.Name)}
	return &Optional{Elem: en}
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
	if e, ok := c.env[id.Name].(*Enum); ok {
		return e
	}
	if e := c.enums[id.Name]; e != nil {
		return e
	}
	_, t := c.aliasedNominal(c.mod, id.Name)
	e, _ := t.(*Enum)
	return e
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
	me, _ := x.(*ast.MemberExpr)
	called := func(x ast.Expr) ast.Expr { return memberAccess(x, name.Name, name.Span) }
	if cs == nil {
		if m := e.Method(name.Name); m != nil && m.Static && me != nil {
			if c.methodPrivate(name, m) {
				return Invalid
			}
			if sig := c.funcSig(name, m); sig != nil {
				return c.tearOff(me, sig, nil, called, nil)
			}
			return Invalid
		} else if m != nil {
			c.errorAt(name, fmt.Sprintf("tear it off a value instead: value.%s", name.Name), "method '%s' isn't static, so it needs a value of type %s", name.Name, e.Name)
		} else {
			c.errorAt(name, didYouMean(name.Name, enumNames(e)), "%s has no case '%s'", e.Name, name.Name)
		}
		return Invalid
	}
	if len(cs.Fields) > 0 {
		if me != nil {
			return c.tearOff(me, fieldsSig(cs.Fields, e), nil, called, nil)
		}
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
	info := &Match{Stmt: stmt, Known: -1}
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
			if wholeMember(sp) {
				key = "value " + sp.key
			}
			// Literals and constants with the same value repeat each other.
			if sp.kind == sOpaque && !strings.HasPrefix(sp.key, "const ") && !strings.HasPrefix(sp.key, "range ") {
				key = "value " + sp.key
			}
			if !c.exhaust().useful(rows, []*spat{sp}, []Type{subject}) {
				switch {
				case wildcard > 0:
					c.errorAt(arm.Pattern, "remove it, or move _ to the end",
						"this arm is never reached: _ on line %d matches everything", wildcard)
				case key != "" && seen[key] > 0 && strings.HasPrefix(key, "case "):
					c.errorAt(arm.Pattern, "remove one of the arms", "case '%s' is already handled on line %d", key[5:], seen[key])
				case key != "" && seen[key] > 0:
					c.errorAt(arm.Pattern, "remove one of the arms", "%s is already handled on line %d", c.source(arm.Pattern), seen[key])
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
		info.Known = c.knownArm(m)
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
	var arms []string
	var hidden Type
	sealed := e.c.sealedMembers(subject) != nil
	switch subject.(type) {
	case *Enum:
	default:
		if sealed {
			break
		}
		if subject != Bool {
			switch {
			case w[0] != "_":
			case subject == Int || subject == Float || subject == String:
				c.errorAtPos(m.Pos(), 5, "add _ => ... as the last arm", "a match on %s needs a _ arm for the other values", subject)
				return
			default:
				hint := "add _ => ... as the last arm"
				if i, ok := subject.(*Interface); ok && i.Sealed && genericMember(i) {
					hint += ", since a generic type implements " + i.String()
				}
				c.errorAtPos(m.Pos(), 5, hint, "match doesn't handle every %s value", subject)
				return
			}
			missing = []string{w[0]}
		}
	}
	if missing == nil {
		for _, k := range e.ctors(subject, nil) {
			spec := specializeRows(rows, k)
			switch {
			case len(spec) == 0 && sealed:
				missing = append(missing, k.types[0].String())
				arms = append(arms, memberArm(k.types[0]))
				if hidden == nil && !c.visibleType(k.types[0]) {
					hidden = k.types[0]
				}
			case len(spec) == 0:
				missing = append(missing, k.key)
			default:
				if w := e.missing(spec, k.types); w != nil {
					shown := show(subject, k, w)
					missing = append(missing, shown)
					if sealed && shown == k.types[0].String() {
						shown = memberArm(k.types[0])
					}
					arms = append(arms, shown)
				}
			}
		}
	}
	switch {
	case len(missing) == 0:
	case hidden != nil:
		c.errorAtPos(m.Pos(), 5, fmt.Sprintf("add _ => ..., since %s is private to %s", hidden, typeModule(hidden)), "match doesn't handle %s", quoteList(missing))
	case len(missing) == 1:
		arm := missing[0]
		if sealed {
			arm = arms[0]
		}
		c.errorAtPos(m.Pos(), 5, fmt.Sprintf("add %s => ..., or _ => ...", arm), "match doesn't handle %s", quoteList(missing))
	default:
		c.errorAtPos(m.Pos(), 5, "add the missing arms, or _ => ...", "match doesn't handle %s", quoteList(missing))
	}
}

// Returns the arm of [m] that runs when its subject is an enum case known
// when compiling, or -1. Only plain case patterns may come before it.
func (c *checker) knownArm(m *ast.MatchExpr) int {
	cs, ok := c.fold(m.Subject, nil).(*Case)
	if !ok {
		return -1
	}
	var matches func(p ast.Pattern) (bool, bool)
	// Reports whether [p] matches [cs], and whether that is certain.
	matches = func(p ast.Pattern) (bool, bool) {
		switch p := p.(type) {
		case *ast.WildcardPattern:
			return true, true
		case *ast.NamePattern:
			if k := c.info.PatternCases[p]; k != nil {
				return k == cs, true
			}
		case *ast.OrPattern:
			for _, a := range p.Alts {
				hit, sure := matches(a)
				if !sure {
					return false, false
				}
				if hit {
					return true, true
				}
			}
			return false, true
		}
		return false, false
	}
	for i, arm := range m.Arms {
		hit, sure := matches(arm.Pattern)
		if !sure || hit && arm.Guard != nil {
			return -1
		}
		if hit {
			return i
		}
	}
	return -1
}
