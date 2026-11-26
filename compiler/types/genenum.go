package types

import (
	"fmt"

	"github.com/irshadio/vex-lang/compiler/ast"
)

// A generic enum: its declaration, type parameters and instances.
type genericEnum struct {
	decl   *ast.EnumDecl
	params []*TypeParam
	insts  map[string]*Enum
	tmpl   *Enum
	failed bool
}

func (c *checker) declareGenericEnum(ed *ast.EnumDecl) {
	if c.reserved(ed.Name, "an enum") || c.taken(ed.Name) {
		return
	}
	g := &genericEnum{decl: ed, insts: map[string]*Enum{}}
	g.params = c.typeParams(ed.TypeParams)
	c.genEnums[ed.Name.Name] = g
}

// Returns [g] as seen by generic code, with its own type parameters.
func (c *checker) enumTemplate(g *genericEnum) *Enum {
	if g.tmpl == nil {
		g.tmpl = c.instEnum(g, paramTypes(g.params), nil)
	}
	return g.tmpl
}

// Returns the instance of [g] for [args], creating it on first use. [at]
// is where the instance is needed, for errors; it is nil for templates.
func (c *checker) instEnum(g *genericEnum, args []Type, at ast.Node) *Enum {
	key := typeKey(args)
	if e := g.insts[key]; e != nil {
		return e
	}
	abstract := false
	for _, a := range args {
		abstract = abstract || hasTypeParam(a)
	}
	if !c.deepEnough(at, g.decl.Name.Name, args, abstract) {
		return nil
	}
	defer c.enter(g.decl)()
	e := &Enum{Name: g.decl.Name.Name + "<" + typeList(args) + ">", Module: c.mod, Base: g.decl.Name.Name, Args: args, Decl: g.decl, generic: g, abstract: abstract}
	if !abstract {
		e.Decl = ast.Clone(g.decl)
		c.registerClone(e.Decl, g.decl)
		c.info.Enums = append(c.info.Enums, e)
	}
	g.insts[key] = e
	env := envOf(g.params, args)
	c.withEnv(env, func() { c.enumMembers(e) })
	if !abstract {
		depth := c.depth + 1
		c.jobs = append(c.jobs, func() {
			c.runInstance(depth, g.failed, func() {
				c.withEnv(env, func() { c.enumBodies(e) })
			})
		})
	}
	return e
}

// Checks the parameter defaults and method bodies of [e].
func (c *checker) enumBodies(e *Enum) {
	for _, m := range plainMethods(e.Methods) {
		c.paramDefaults(m)
		c.funcBody(m)
	}
}

// Returns the instance of [g] for [args], written as [te].
func (c *checker) genericEnumInst(te *ast.NamedType, g *genericEnum, args []Type) Type {
	if len(args) != len(g.params) {
		c.errorAt(te, "", "%s takes %s, found %d", te.Name, plural(len(g.params), "type argument"), len(args))
		return Invalid
	}
	for i, p := range g.params {
		if args[i] == Invalid {
			return Invalid
		}
		if !c.meetsConstraints(p, args[i], te.Args[i], te.Name, bindOf(g.params, args)) {
			return Invalid
		}
	}
	if e := c.instEnum(g, args, te); e != nil {
		return e
	}
	return Invalid
}

// Returns the generic enum named by [x], as in `Result.ok(...)`, or nil.
func (c *checker) genEnumRef(x ast.Expr) *genericEnum {
	id, ok := x.(*ast.Ident)
	if !ok {
		return nil
	}
	if id.Module != "" {
		m := c.imported(id.Module)
		if m == nil {
			return nil
		}
		g := m.genEnums[id.Name]
		if g != nil {
			c.private(id, m, g.decl.Pub, "enum '"+id.Name+"'")
		}
		return g
	}
	if c.lookupVar(id.Name) != nil {
		return nil
	}
	if owner := c.ownStruct(); owner != nil && (owner.Field(id.Name) != nil || owner.Property(id.Name) != nil) {
		return nil
	}
	return c.genEnums[id.Name]
}

// Checks `Result.ok(...)` on the generic enum [g], working out its type
// arguments from the case's values and the expected type [want].
func (c *checker) genericCaseCall(e *ast.CallExpr, g *genericEnum, name *ast.Ident, want Type) Type {
	tmpl := c.enumTemplate(g)
	written := g.decl.Name.Name + "." + name.Name
	cs := tmpl.Case(name.Name)
	if cs == nil || len(cs.Fields) == 0 {
		return c.caseCall(e, tmpl, name, written)
	}
	slots := make([]slot, len(cs.Fields))
	for i, f := range cs.Fields {
		slots[i] = slot{name: f.Name, typ: f.Type}
	}
	args := c.inferTypeArgs(e, g.params, slots, tmpl, want, written)
	if args == nil {
		return Invalid
	}
	inst := c.instEnum(g, args, e)
	if inst == nil {
		c.checkArgs(e)
		return Invalid
	}
	return c.caseCall(e, inst, name, written)
}

// Checks `Option.none` on the generic enum [g], whose type arguments must
// come from the expected type [want].
func (c *checker) genericCaseValue(x ast.Expr, g *genericEnum, name *ast.Ident, want Type) Type {
	if o, ok := want.(*Optional); ok {
		want = o.Elem
	}
	if inst, ok := want.(*Enum); ok && inst.generic == g {
		return c.caseValue(x, inst, name, g.decl.Name.Name+"."+name.Name)
	}
	tmpl := c.enumTemplate(g)
	if cs := tmpl.Case(name.Name); cs != nil && len(cs.Fields) > 0 {
		if me, ok := x.(*ast.MemberExpr); ok {
			var names []string
			for _, f := range cs.Fields {
				names = append(names, f.Name)
			}
			return c.genericTearOff(me, names, want, written(me.X)+"."+name.Name, func(x ast.Expr) ast.Expr { return memberAccess(x, name.Name, name.Span) })
		}
	}
	if cs := tmpl.Case(name.Name); cs == nil || len(cs.Fields) > 0 {
		return c.caseValue(x, tmpl, name, g.decl.Name.Name+"."+name.Name)
	}
	c.errorAt(name, fmt.Sprintf("write it: %s<%s>.%s, or give the type", g.decl.Name.Name, typeParamExample(g.params), name.Name),
		"can't tell the type of %s for %s.%s", g.params[0].Name, g.decl.Name.Name, name.Name)
	return Invalid
}
