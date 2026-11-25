package types

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/irshadio/vex-lang/compiler/ast"
)

// A generic interface: its declaration, type parameters and instances.
type genericIface struct {
	decl   *ast.InterfaceDecl
	params []*TypeParam
	insts  map[string]*Interface
	tmpl   *Interface
	failed bool
	ready  bool
}

func (c *checker) declareInterface(d *ast.InterfaceDecl) {
	if c.reserved(d.Name, "an interface") || c.taken(d.Name) {
		return
	}
	if len(d.TypeParams) > 0 {
		c.genIfaces[d.Name.Name] = &genericIface{decl: d, insts: map[string]*Interface{}}
		return
	}
	i := &Interface{Name: d.Name.Name, Module: c.mod, Decl: d, Index: len(c.info.Interfaces)}
	c.interfaces[i.Name] = i
	c.info.Interfaces = append(c.info.Interfaces, i)
}

// Returns the type parameters of [g], resolving them on first use, since
// their constraints may name interfaces declared after it.
func (c *checker) ifaceParams(g *genericIface) []*TypeParam {
	if !g.ready {
		g.ready = true
		defer c.enter(g.decl)()
		c.withEnv(nil, func() { g.params = c.typeParams(g.decl.TypeParams) })
	}
	return g.params
}

// Returns [g] as seen by generic code, with its own type parameters.
func (c *checker) ifaceTemplate(g *genericIface) *Interface {
	if g.tmpl == nil {
		params := c.ifaceParams(g)
		g.tmpl = c.instIface(g, paramTypes(params), nil)
	}
	return g.tmpl
}

// Returns the instance of [g] for [args], creating it on first use.
func (c *checker) instIface(g *genericIface, args []Type, at ast.Node) *Interface {
	params := c.ifaceParams(g)
	key := typeKey(args)
	if i := g.insts[key]; i != nil {
		return i
	}
	abstract := false
	for _, a := range args {
		abstract = abstract || hasTypeParam(a)
	}
	if !abstract && !c.deepEnough(at, g.decl.Name.Name) {
		return nil
	}
	defer c.enter(g.decl)()
	i := &Interface{Name: g.decl.Name.Name + "<" + typeList(args) + ">", Module: c.mod, Base: g.decl.Name.Name, Args: args, Decl: g.decl, generic: g, abstract: abstract}
	if !abstract {
		i.Decl = ast.Clone(g.decl)
		c.register(i.Decl, c.units[g.decl])
		i.Index = len(c.info.Interfaces)
		c.info.Interfaces = append(c.info.Interfaces, i)
	}
	g.insts[key] = i
	c.ifaceMembers(i)
	if !abstract {
		depth := c.depth + 1
		c.jobs = append(c.jobs, func() {
			c.runInstance(depth, g.failed, func() {
				c.withEnv(envOf(params, args), func() { c.ifaceBodies(i) })
			})
		})
	}
	return i
}

// Checks the parameter defaults and bodies of the defaults of an instance
// of a generic interface.
func (c *checker) ifaceBodies(i *Interface) {
	for _, m := range i.Methods {
		if hasBody(m.Decl) {
			c.paramDefaults(m)
			c.funcBody(m)
		}
	}
}

// Returns the instance of [g] for [args], written as [te].
func (c *checker) genericIfaceInst(te *ast.NamedType, g *genericIface, args []Type) Type {
	params := c.ifaceParams(g)
	if len(args) != len(params) {
		c.errorAt(te, "", "%s takes %s, found %d", te.Name, plural(len(params), "type argument"), len(args))
		return Invalid
	}
	for n, p := range params {
		if args[n] == Invalid {
			return Invalid
		}
		if !c.meetsConstraints(p, args[n], te.Args[n], te.Name, bindOf(params, args)) {
			return Invalid
		}
	}
	if i := c.instIface(g, args, te); i != nil {
		return i
	}
	return Invalid
}

// Returns the type names in scope in the members of [i].
func ifaceEnv(i *Interface) map[string]Type {
	if i.generic == nil {
		return nil
	}
	return envOf(i.generic.params, i.Args)
}

// Resolves the interfaces [i] extends and its members the first time it is
// needed.
func (c *checker) ifaceMembers(i *Interface) {
	defer c.enter(i.Decl)()
	if i.resolved {
		return
	}
	i.resolved = true
	c.withEnv(ifaceEnv(i), func() {
		for _, te := range i.Decl.Supers {
			j := c.ifaceRef(te, false, "an interface")
			if j == nil {
				continue
			}
			c.ifaceMembers(j)
			switch {
			case j.Extends(i):
				c.errorAt(te, "", "interface %s can't extend itself", i.Name)
				continue
			case slices.Contains(i.Supers, j):
				c.errorAt(te, "", "%s is listed twice", j.Name)
				continue
			}
			i.Supers = append(i.Supers, j)
		}
		for _, md := range i.Decl.Methods {
			m := &Func{Name: md.Name.Name, Module: i.Module, Decl: md, Iface: i}
			if c.reserved(md.Name, "a method") {
				continue
			}
			if prev := ifaceClash(i.Methods, m); prev != nil {
				c.errorAt(md.Name, "", "'%s' is already declared on line %d", m.Name, prev.Decl.Pos().Line)
				continue
			}
			if j, prev := c.inheritedClash(i, m); prev != nil {
				c.errorAt(md.Name, "", "'%s' is already required by %s", m.Name, j.Name)
				continue
			}
			i.Methods = append(i.Methods, m)
			c.signature(m)
			if hasBody(md) && !i.abstract {
				c.info.FuncOf[md] = m
				c.info.Funcs = append(c.info.Funcs, m)
			}
		}
		for _, m := range i.Methods {
			if m.Decl.Accessor != ast.Setter {
				continue
			}
			if g := ifaceProperty(i, m.Name).Get; g != nil && len(m.Params) == 1 && !Identical(g.Result, m.Params[0].Type) {
				c.errorAt(m.Decl.Params[0].Type, fmt.Sprintf("use %s, the type of the getter", g.Result), "setter '%s' takes %s, but its getter returns %s", m.Name, m.Params[0].Type, g.Result)
			}
		}
	})
}

// Returns the member of [ms] that [m] can't be declared next to: one of
// the same name, unless they are the getter and setter of a property.
func ifaceClash(ms []*Func, m *Func) *Func {
	for _, prev := range ms {
		if prev.Name != m.Name {
			continue
		}
		pair := prev.Decl.Accessor != m.Decl.Accessor && prev.Decl.Accessor != ast.Method && m.Decl.Accessor != ast.Method
		if !pair {
			return prev
		}
	}
	return nil
}

// Returns the interface that [i] extends, and its member, that [m] clashes
// with, if any.
func (c *checker) inheritedClash(i *Interface, m *Func) (*Interface, *Func) {
	for _, s := range i.Supers {
		for _, j := range s.All() {
			if prev := ifaceClash(j.Methods, m); prev != nil {
				return j, prev
			}
		}
	}
	return nil, nil
}

// Returns the interface [te] names, or nil after reporting an error that
// says it must be [what].
func (c *checker) ifaceRef(te *ast.NamedType, quiet bool, what string) *Interface {
	report := func(n ast.Node, hint, format string, args ...any) {
		if !quiet {
			c.errorAt(n, hint, format, args...)
		}
	}
	if te.Module != "" {
		return c.qualifiedIface(te, quiet)
	}
	if c.genIfaces[te.Name] != nil || len(te.Args) > 0 && c.interfaces[te.Name] != nil {
		var t Type
		if quiet {
			c.scratch(false, func() { t = c.resolveType(te) })
		} else {
			t = c.resolveType(te)
		}
		i, _ := t.(*Interface)
		return i
	}
	if i := c.interfaces[te.Name]; i != nil {
		return i
	}
	hint := "only interfaces can be listed after ':'"
	switch {
	case c.structs[te.Name] != nil || c.genStructs[te.Name] != nil:
		report(te, hint, "'%s' is a struct, not an interface", te.Name)
	case c.enums[te.Name] != nil || c.genEnums[te.Name] != nil:
		report(te, hint, "'%s' is an enum, not an interface", te.Name)
	default:
		var names []string
		for n := range c.interfaces {
			names = append(names, n)
		}
		for n := range c.genIfaces {
			names = append(names, n)
		}
		report(te, didYouMean(te.Name, names), "unknown interface '%s'", te.Name)
	}
	return nil
}

func hasBody(fd *ast.FuncDecl) bool { return fd.Body != nil || fd.ExprBody != nil }

// What a struct or enum is checked against when it declares interfaces.
type conformer struct {
	name   string
	node   ast.Node
	kind   string
	decls  []*ast.NamedType
	ifaces *[]*Interface

	// The type's members, to which defaults it doesn't override are added.
	methods *[]*Func

	// Returns the stored field called [name], or nil. Nil for enums.
	field func(name string) *Field

	// Reports whether [m] is declared by the type itself.
	owns func(m *Func) bool

	// Set for instances of generic types, which are checked through their
	// generic form, so mismatches are reported in terms of its type
	// parameters.
	quiet bool
}

// Resolves the interfaces [s] declares and checks that it implements them.
func (c *checker) conformances(s *Struct) {
	c.conform(conformer{
		name: s.Name, node: s.Decl.Name, kind: "struct", decls: s.Decl.Interfaces,
		ifaces: &s.Interfaces, methods: &s.Methods,
		field: func(name string) *Field { return s.Field(name) },
		owns:  func(m *Func) bool { return m.Owner == s },
		quiet: s.generic != nil && !s.abstract,
	})
}

// Resolves the interfaces [e] declares and checks that it implements them.
func (c *checker) enumConformances(e *Enum) {
	c.conform(conformer{
		name: e.Name, node: e.Decl.Name, kind: "enum", decls: e.Decl.Interfaces,
		ifaces: &e.Interfaces, methods: &e.Methods,
		owns:  func(m *Func) bool { return m.Enum == e },
		quiet: e.generic != nil && !e.abstract,
	})
}

func (c *checker) conform(t conformer) {
	report := func(n ast.Node, hint, format string, args ...any) {
		if !t.quiet {
			c.errorAt(n, hint, format, args...)
		}
	}
	var listed []*Interface
	for _, te := range t.decls {
		i := c.ifaceRef(te, t.quiet, "an interface")
		if i == nil {
			continue
		}
		if slices.Contains(listed, i) {
			report(te, "", "%s is listed twice", i.Name)
			continue
		}
		listed = append(listed, i)
		c.ifaceMembers(i)
		for _, j := range i.All() {
			c.ifaceMembers(j)
			if slices.Contains(*t.ifaces, j) {
				continue
			}
			*t.ifaces = append(*t.ifaces, j)
			for _, im := range j.Methods {
				c.conformMember(t, j, im, report)
			}
		}
	}
}

// Checks that the type of [t] provides member [im] of interface [i].
func (c *checker) conformMember(t conformer, i *Interface, im *Func, report func(ast.Node, string, string, ...any)) {
	kind := im.Decl.Accessor
	at := func(m *Func) ast.Node {
		if t.owns(m) {
			return m.Decl.Name
		}
		return t.node
	}
	var f *Field
	if t.field != nil && kind != ast.Method {
		f = t.field(im.Name)
	}
	m := memberOf(*t.methods, im.Name, kind)
	switch {
	case f != nil:
		if want := memberType(im); !Identical(f.Type, want) {
			report(f.Decl.Name, "", "field '%s' doesn't match %s: expected %s, found %s", f.Name, i.Name, want, f.Type)
		}
	case m == nil && hasBody(im.Decl):
		*t.methods = append(*t.methods, im)
	case m == nil:
		hint := "add " + memberExample(im)
		if kind != ast.Method && t.field != nil {
			hint += fmt.Sprintf(", or a field %s: %s", im.Name, memberType(im))
		}
		report(t.node, hint, "%s doesn't implement %s from %s", t.name, memberDesc(im), i.Name)
	case m.Iface != nil && m != im && hasBody(m.Decl) && hasBody(im.Decl):
		report(t.node, fmt.Sprintf("write its own %s to choose", memberExample(im)), "%s gets a default %s from both %s and %s", t.name, memberDesc(im), m.Iface.Name, i.Name)
	case m.Static:
		report(m.Decl.Name, "remove static", "'%s' can't be static, since it implements %s", m.Name, i.Name)
	case !Identical(methodType(m), methodType(im)):
		report(at(m), "", "'%s' doesn't match %s: expected %s, found %s", m.Name, i.Name, memberType(im), memberType(m))
	}
}

// Describes interface member [m], such as `getter 'size'`.
func memberDesc(m *Func) string {
	kind := map[ast.Accessor]string{ast.Method: "", ast.Getter: "getter ", ast.Setter: "setter "}[m.Decl.Accessor]
	return kind + "'" + m.Name + "'"
}

// Returns the type of [m] as an error shows it: a method's function type,
// or a property's type.
func memberType(m *Func) Type {
	switch m.Decl.Accessor {
	case ast.Getter:
		return m.Result
	case ast.Setter:
		return m.Params[0].Type
	}
	return methodType(m)
}

// Returns how [m] is declared, such as `fn area() -> float` or
// `get size -> int`.
func memberExample(m *Func) string {
	switch m.Decl.Accessor {
	case ast.Getter:
		return "get " + m.Name + " -> " + m.Result.String()
	case ast.Setter:
		return "set " + m.Name + "(" + m.Params[0].Name + ": " + m.Params[0].Type.String() + ")"
	}
	return methodExample(m)
}

// Returns the type of method [m] without its receiver.
func methodType(m *Func) *FuncType {
	ft := &FuncType{Result: m.Result, Throws: m.Throws}
	for _, p := range m.Params {
		ft.Params = append(ft.Params, p.Type)
	}
	return ft
}

// Returns how [m] is declared, such as `fn area() -> float`.
func methodExample(m *Func) string {
	s := "fn " + m.Name + "("
	for i, p := range m.Params {
		if i > 0 {
			s += ", "
		}
		s += p.Name + ": " + p.Type.String()
	}
	s += ")" + throwsString(m.Throws)
	if m.Result != Void {
		s += " -> " + m.Result.String()
	}
	return s
}

// Reports whether a value of type [t] can be used as interface [i].
func implements(t Type, i *Interface) bool {
	switch t := t.(type) {
	case *Interface:
		return t.Extends(i)
	case *Struct:
		return slices.Contains(t.Interfaces, i)
	case *Enum:
		return slices.Contains(t.Interfaces, i)
	case *TypeParam:
		for _, j := range t.Ifaces {
			if j.Extends(i) {
				return true
			}
		}
	}
	return false
}

// Reports whether a value of type [got] can be used where [want] is
// expected: the same type, or a struct used as an interface it implements.
func assignableType(want, got Type) bool {
	if Identical(want, got) {
		return true
	}
	i, ok := want.(*Interface)
	return ok && implements(got, i)
}

// Returns the interfaces whose members are available on a value of type
// [t]: an interface and those it extends, or those a type parameter
// requires.
func ifacesOf(t Type) []*Interface {
	var out []*Interface
	switch t := t.(type) {
	case *Interface:
		out = t.All()
	case *TypeParam:
		for _, i := range t.Ifaces {
			out = append(out, i.All()...)
		}
	}
	return out
}

// Returns the interface method called [name] available on a value of
// type [t], and the interface that declares it.
func ifaceMethod(t Type, name string) (*Interface, *Func) {
	for _, i := range ifacesOf(t) {
		if m := i.Method(name); m != nil {
			return i, m
		}
	}
	return nil, nil
}

// Returns the interface property called [name] available on a value of
// type [t], or nil. Its getter and setter may come from different
// interfaces, one extending the other.
func ifaceProperty(t Type, name string) *Property {
	p := &Property{}
	for _, i := range ifacesOf(t) {
		if q := i.Property(name); q != nil {
			p.Get = cmp.Or(p.Get, q.Get)
			p.Set = cmp.Or(p.Set, q.Set)
		}
	}
	if p.Get == nil && p.Set == nil {
		return nil
	}
	return p
}

// Checks a call of interface method [m] on [recv], or on `self` when
// [recv] is nil.
func (c *checker) ifaceCall(e *ast.CallExpr, m *Func, recv ast.Expr) Type {
	c.info.Calls[e] = &Call{Kind: InterfaceCall, Func: m, Recv: recv, Args: c.bind(e, paramSlots(m), m.Name)}
	return m.Result
}

// Returns the names of the methods available on [t], for hints.
func ifaceNames(t Type) []string {
	var names []string
	for _, i := range ifacesOf(t) {
		for _, m := range i.Methods {
			names = append(names, m.Name)
		}
	}
	return names
}

// Checks that [t] meets the constraints of type parameter [p], reporting
// an error at [at] for [callee] when it doesn't.
func (c *checker) meetsConstraints(p *TypeParam, t Type, at ast.Node, callee string, bound map[*TypeParam]Type) bool {
	for _, cst := range p.Constraints {
		if !satisfies(t, cst) {
			c.errorAt(at, constraintHint(cst), "%s needs %s to be %s, but %s isn't", callee, p.Name, cst, t)
			return false
		}
	}
	for _, i := range p.Ifaces {
		if i.abstract {
			if j, ok := c.substType(i, bound).(*Interface); ok {
				i = j
			}
		}
		if !implements(t, i) {
			hint := ""
			switch s := t.(type) {
			case *Struct:
				hint = fmt.Sprintf("declare it: struct %s: %s", s.Name, i.Name)
			case *Enum:
				hint = fmt.Sprintf("declare it: enum %s: %s", s.Name, i.Name)
			}
			c.errorAt(at, hint, "%s needs %s to implement %s, but %s doesn't", callee, p.Name, i.Name, t)
			return false
		}
	}
	return true
}

// Returns the type parameters of [params] bound to [args], leaving out
// those not known yet.
func bindOf(params []*TypeParam, args []Type) map[*TypeParam]Type {
	m := map[*TypeParam]Type{}
	for i, p := range params {
		if i < len(args) && args[i] != nil {
			m[p] = args[i]
		}
	}
	return m
}

// Returns [t] with the type parameters in [bound] replaced, creating the
// generic instances that needs.
func (c *checker) substType(t Type, bound map[*TypeParam]Type) Type {
	args := func(ts []Type) []Type {
		out := make([]Type, len(ts))
		for i, a := range ts {
			out[i] = c.substType(a, bound)
		}
		return out
	}
	switch t := t.(type) {
	case *TypeParam:
		if r, ok := bound[t]; ok {
			return r
		}
	case *List:
		return &List{Elem: c.substType(t.Elem, bound)}
	case *Map:
		return &Map{Key: c.substType(t.Key, bound), Value: c.substType(t.Value, bound)}
	case *Optional:
		return &Optional{Elem: c.substType(t.Elem, bound)}
	case *Future:
		return &Future{Elem: c.substType(t.Elem, bound), Throws: t.Throws}
	case *FuncType:
		return &FuncType{Params: args(t.Params), Result: c.substType(t.Result, bound), Throws: t.Throws}
	case *Struct:
		if t.abstract {
			if s := c.instStruct(t.generic, args(t.Args), nil); s != nil {
				return s
			}
		}
	case *Enum:
		if t.abstract {
			if e := c.instEnum(t.generic, args(t.Args), nil); e != nil {
				return e
			}
		}
	case *Interface:
		if t.abstract {
			if i := c.instIface(t.generic, args(t.Args), nil); i != nil {
				return i
			}
		}
	}
	return t
}
