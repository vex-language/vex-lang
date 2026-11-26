package types

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
)

// The built-in constraints a type parameter can require.
var constraintNames = []string{"Equatable", "Comparable", "Hashable"}

// How deeply generic code may instantiate more generic code, which stops
// functions like `fn f<T>(x: T) { f([x]) }` from creating types forever.
const maxInstanceDepth = 32

// The most types the arguments of one instance may be made of.
const maxInstanceSize = 1024

// A generic function: its declaration, type parameters and instances.
type genericFunc struct {
	decl   *ast.FuncDecl
	params []*TypeParam
	insts  map[string]*Func

	// The function as seen by generic code, with its own type parameters.
	tmpl *Func

	// Set when checking the generic body found errors, so instances
	// don't repeat them with concrete types.
	failed bool
}

// A generic struct: its declaration, type parameters and instances.
type genericStruct struct {
	decl   *ast.StructDecl
	params []*TypeParam
	insts  map[string]*Struct
	tmpl   *Struct
	failed bool
}

// Returns the type parameters declared by [tps], reporting bad names and
// unknown constraints.
func (c *checker) typeParams(tps []*ast.TypeParam) []*TypeParam {
	var out []*TypeParam
	seen := map[string]bool{}
	env := map[string]Type{}
	for n, t := range c.env {
		env[n] = t
	}
	for _, tp := range tps {
		p := &TypeParam{Name: tp.Name.Name}
		if !c.reserved(tp.Name, "a type parameter") && seen[p.Name] {
			c.errorAt(tp.Name, "", "type parameter '%s' is declared twice", p.Name)
		}
		seen[p.Name] = true
		env[p.Name] = p
		out = append(out, p)
	}
	saved := c.env
	c.env = env
	defer func() { c.env = saved }()
	for n, tp := range tps {
		p := out[n]
		for _, id := range tp.Constraints {
			if len(id.Args) > 0 || c.genIfaces[id.Name] != nil || c.aliases[id.Name] != nil {
				switch t := c.resolveType(id).(type) {
				case *Interface:
					p.Ifaces = append(p.Ifaces, t)
				default:
					if t != Invalid && c.aliases[id.Name] != nil && len(id.Args) == 0 {
						c.errorAt(id, "use an interface", "'%s' names %s, not an interface", id.Name, t)
					} else if t != Invalid {
						c.errorAt(id, "use an interface", "'%s' is not an interface", t)
					}
				}
				continue
			}
			if id.Module != "" {
				if i := c.qualifiedIface(id, false); i != nil {
					p.Ifaces = append(p.Ifaces, i)
				}
				continue
			}
			if i := c.interfaces[id.Name]; i != nil {
				p.Ifaces = append(p.Ifaces, i)
				continue
			}
			known := false
			for _, n := range constraintNames {
				known = known || n == id.Name
			}
			if !known {
				names := append([]string(nil), constraintNames...)
				for n := range c.interfaces {
					names = append(names, n)
				}
				hint := didYouMean(id.Name, names)
				if hint == "" {
					hint = "use an interface, or Equatable (==), Comparable (< >) or Hashable (map keys)"
				}
				c.errorAt(id, hint, "unknown constraint '%s'", id.Name)
				continue
			}
			p.Constraints = append(p.Constraints, id.Name)
		}
	}
	return out
}

// Returns the type parameters of [params] mapped to [args].
func envOf(params []*TypeParam, args []Type) map[string]Type {
	env := map[string]Type{}
	for i, p := range params {
		env[p.Name] = args[i]
	}
	return env
}

func paramTypes(params []*TypeParam) []Type {
	out := make([]Type, len(params))
	for i, p := range params {
		out[i] = p
	}
	return out
}

// Runs [f] with type names resolving through [env], and no other type
// parameters in scope.
func (c *checker) withEnv(env map[string]Type, f func()) {
	saved := c.env
	c.env = env
	defer func() { c.env = saved }()
	f()
}

// Runs [f] with fresh tables for what is learned about expressions, so
// checking generic code or trying out arguments leaves nothing behind for
// the code generator. Errors are kept only when [keepErrors] is set.
func (c *checker) scratch(keepErrors bool, f func()) {
	i := c.info
	saved := *i
	i.Types = map[ast.Expr]Type{}
	i.Uses = map[*ast.Ident]any{}
	i.Defs = map[*ast.Ident]*Var{}
	i.Calls = map[*ast.CallExpr]*Call{}
	i.Members = map[*ast.MemberExpr]*Member{}
	i.Selves = map[*ast.FuncDecl]*Var{}
	i.Captures = map[*ast.FuncLit][]*Var{}
	i.CaseValues = map[ast.Expr]*Case{}
	i.Wraps = map[ast.Expr]*Optional{}
	i.ThrowingRefs = map[ast.Expr]bool{}
	i.Elements = map[*ast.ExprStmt]ast.Expr{}
	i.Chains = map[ast.Expr]bool{}
	i.TearOffs = map[*ast.MemberExpr]ast.Expr{}
	i.Matches = map[*ast.MatchExpr]*Match{}
	i.PatternTypes = map[ast.Pattern]Type{}
	i.PatternCases = map[ast.Pattern]*Case{}
	i.PatternStructs = map[*ast.CasePattern]*Struct{}
	i.PatternVars = map[ast.Pattern][]*Var{}
	i.Throwing = map[ast.Expr]Type{}
	i.Spawns = map[*ast.CallExpr]bool{}
	i.Tries = map[*ast.TryExpr]*Try{}
	errs := c.errs
	if !keepErrors {
		c.errs = nil
	}
	defer func() {
		i.Types, i.Uses, i.Defs, i.Calls, i.Members = saved.Types, saved.Uses, saved.Defs, saved.Calls, saved.Members
		i.Selves, i.Captures, i.CaseValues, i.Wraps = saved.Selves, saved.Captures, saved.CaseValues, saved.Wraps
		i.Elements, i.Spawns, i.ThrowingRefs, i.TearOffs = saved.Elements, saved.Spawns, saved.ThrowingRefs, saved.TearOffs
		i.Chains, i.Matches, i.Throwing, i.Tries = saved.Chains, saved.Matches, saved.Throwing, saved.Tries
		i.PatternTypes, i.PatternCases, i.PatternStructs, i.PatternVars = saved.PatternTypes, saved.PatternCases, saved.PatternStructs, saved.PatternVars
		if !keepErrors {
			c.errs = errs
			if r := recover(); r != nil {
				if _, ok := r.(tooMany); !ok {
					panic(r)
				}
			}
		}
	}()
	f()
}

// Reports whether [t] mentions a type parameter.
func hasTypeParam(t Type) bool {
	switch t := t.(type) {
	case *TypeParam:
		return true
	case *List:
		return hasTypeParam(t.Elem)
	case *Map:
		return hasTypeParam(t.Key) || hasTypeParam(t.Value)
	case *Optional:
		return hasTypeParam(t.Elem)
	case *Future:
		return hasTypeParam(t.Elem) || hasTypeParam(t.Throws)
	case *Channel:
		return hasTypeParam(t.Elem)
	case *Record:
		for _, f := range t.FieldTypes() {
			if hasTypeParam(f) {
				return true
			}
		}
		return false
	case *FuncType:
		for _, p := range t.Params {
			if hasTypeParam(p) {
				return true
			}
		}
		return hasTypeParam(t.Result) || hasTypeParam(t.Throws)
	case *Struct:
		return t.abstract
	case *Enum:
		return t.abstract
	case *Interface:
		return t.abstract
	}
	return false
}

// Returns a key that identifies [ts] exactly. Unlike their names, it tells
// apart type parameters that share a name, such as the T of two structs.
func typeKey(ts []Type) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = keyOf(t)
	}
	return strings.Join(parts, ", ")
}

func keyOf(t Type) string {
	switch t := t.(type) {
	case *TypeParam:
		return fmt.Sprintf("%s#%p", t.Name, t)
	case *List:
		return "[" + keyOf(t.Elem) + "]"
	case *Map:
		return "[" + keyOf(t.Key) + ": " + keyOf(t.Value) + "]"
	case *Optional:
		return keyOf(t.Elem) + "?"
	case *Channel:
		return "Channel<" + keyOf(t.Elem) + ">"
	case *Record:
		var b strings.Builder
		b.WriteString("(")
		for i, n := range t.FieldNames() {
			b.WriteString(n + ":" + keyOf(t.FieldTypes()[i]) + ",")
		}
		return b.String() + ")"
	case *Future:
		if t.Throws != nil {
			return "Future<" + keyOf(t.Elem) + ", " + keyOf(t.Throws) + ">"
		}
		return "Future<" + keyOf(t.Elem) + ">"
	case *FuncType:
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = keyOf(p)
		}
		s := "fn(" + strings.Join(parts, ", ") + ")" + throwsString(t.Throws)
		if t.Result != nil && t.Result != Void {
			s += " -> " + keyOf(t.Result)
		}
		return s
	case *Struct:
		if t.generic != nil {
			return t.Base + "<" + typeKey(t.Args) + ">"
		}
	case *Enum:
		if t.generic != nil {
			return t.Base + "<" + typeKey(t.Args) + ">"
		}
	case *Interface:
		if t.generic != nil {
			return t.Base + "<" + typeKey(t.Args) + ">"
		}
	}
	return t.String()
}

func typeList(ts []Type) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = t.String()
	}
	return strings.Join(parts, ", ")
}

// --- Declarations ---

func (c *checker) declareGenericFunc(fd *ast.FuncDecl) {
	name := fd.Name.Name
	if c.reserved(fd.Name, "a function") || c.taken(fd.Name) {
		return
	}
	g := &genericFunc{decl: fd, insts: map[string]*Func{}}
	g.params = c.typeParams(fd.TypeParams)
	c.genFuncs[name] = g
}

func (c *checker) declareGenericStruct(sd *ast.StructDecl) {
	name := sd.Name.Name
	if c.reserved(sd.Name, "a struct") || c.taken(sd.Name) {
		return
	}
	g := &genericStruct{decl: sd, insts: map[string]*Struct{}}
	g.params = c.typeParams(sd.TypeParams)
	c.genStructs[name] = g
}

// Reports, and returns true, if a top-level name is already declared.
func (c *checker) taken(id *ast.Ident) bool {
	var line int
	what := ""
	switch name := id.Name; {
	case c.funcs[name] != nil:
		line, what = c.funcs[name].Decl.Pos().Line, "a function"
	case c.genFuncs[name] != nil:
		line, what = c.genFuncs[name].decl.Pos().Line, "a function"
	case c.structs[name] != nil:
		line, what = c.structs[name].Decl.Pos().Line, "a struct"
	case c.genStructs[name] != nil:
		line, what = c.genStructs[name].decl.Pos().Line, "a struct"
	case c.genEnums[name] != nil:
		line, what = c.genEnums[name].decl.Pos().Line, "an enum"
	case c.enums[name] != nil:
		line, what = c.enums[name].Decl.Pos().Line, "an enum"
	case c.interfaces[name] != nil:
		line, what = c.interfaces[name].Decl.Pos().Line, "an interface"
	case c.genIfaces[name] != nil:
		line, what = c.genIfaces[name].decl.Pos().Line, "an interface"
	case c.handles[name] != nil:
		line, what = c.handles[name].Decl.Pos().Line, "an extern type"
	case c.aliases[name] != nil:
		line, what = c.aliases[name].decl.Pos().Line, "a typedef"
	default:
		return false
	}
	c.errorAt(id, "", "'%s' is already declared as %s on line %d", id.Name, what, line)
	return true
}

// Queues checks of every generic body with its type parameters left
// open, so mistakes are reported once, in terms of T.
func (c *checker) queueGenericBodies() {
	for _, d := range c.decls {
		back := c.enter(d)
		c.queueGenericBody(d)
		back()
	}
}

func (c *checker) queueGenericBody(d ast.Decl) {
	{
		switch d := d.(type) {
		case *ast.FuncDecl:
			g := c.genFuncs[d.Name.Name]
			if g == nil || g.decl != d {
				return
			}
			f := c.funcTemplate(g)
			c.jobs = append(c.jobs, func() {
				n := len(c.errs)
				c.scratch(true, func() {
					c.withEnv(envOf(g.params, paramTypes(g.params)), func() {
						c.paramDefaults(f)
						c.funcBody(f)
					})
				})
				g.failed = len(c.errs) > n
			})
		case *ast.EnumDecl:
			g := c.genEnums[d.Name.Name]
			if g == nil || g.decl != d {
				return
			}
			c.jobs = append(c.jobs, func() {
				n := len(c.errs)
				e := c.enumTemplate(g)
				c.scratch(true, func() {
					c.withEnv(envOf(g.params, paramTypes(g.params)), func() {
						c.enumBodies(e)
					})
				})
				g.failed = len(c.errs) > n
			})
		case *ast.StructDecl:
			g := c.genStructs[d.Name.Name]
			if g == nil || g.decl != d {
				return
			}
			c.jobs = append(c.jobs, func() {
				n := len(c.errs)
				s := c.structTemplate(g)
				c.scratch(true, func() {
					c.withEnv(envOf(g.params, paramTypes(g.params)), func() {
						c.structBodies(s)
					})
				})
				g.failed = len(c.errs) > n
			})
		case *ast.InterfaceDecl:
			g := c.genIfaces[d.Name.Name]
			if g == nil || g.decl != d {
				return
			}
			c.jobs = append(c.jobs, func() {
				n := len(c.errs)
				i := c.ifaceTemplate(g)
				c.scratch(true, func() {
					c.withEnv(envOf(g.params, i.Args), func() { c.ifaceBodies(i) })
				})
				g.failed = len(c.errs) > n
			})
		}
	}
}

// Returns [g] as seen by generic code, with its own type parameters.
func (c *checker) funcTemplate(g *genericFunc) *Func {
	if g.tmpl == nil {
		defer c.enter(g.decl)()
		g.tmpl = &Func{Name: g.decl.Name.Name, Module: c.mod, Decl: g.decl, TypeArgs: paramTypes(g.params)}
		c.withEnv(envOf(g.params, paramTypes(g.params)), func() { c.signature(g.tmpl) })
	}
	return g.tmpl
}

func (c *checker) structTemplate(g *genericStruct) *Struct {
	if g.tmpl == nil {
		g.tmpl = c.instStruct(g, paramTypes(g.params), nil)
	}
	return g.tmpl
}

// Checks the field defaults and method bodies of [s].
func (c *checker) structBodies(s *Struct) {
	defer c.enter(s.Decl)()
	c.scope = &scope{vars: map[string]*Var{}}
	for _, f := range s.Fields {
		if f.Default != nil {
			c.assignable(f.Type, c.expr(f.Default, f.Type), f.Default, "default of '"+f.Name+"'")
		}
	}
	c.scope = nil
	for _, m := range plainMethods(s.Methods) {
		c.paramDefaults(m)
		c.funcBody(m)
	}
}

// Checks the parameter defaults of [f].
func (c *checker) paramDefaults(f *Func) {
	defer c.enter(f.Decl)()
	c.scope = &scope{vars: map[string]*Var{}}
	for _, p := range f.Params {
		if p.Default != nil {
			c.assignable(p.Type, c.expr(p.Default, p.Type), p.Default, "default of '"+p.Name+"'")
		}
	}
	c.scope = nil
}

// Reports whether [f] belongs to an instance of a generic, whose body is
// checked by its own job.
func instanced(f *Func) bool {
	return f.TypeArgs != nil || f.Owner != nil && f.Owner.generic != nil || f.Enum != nil && f.Enum.generic != nil || f.Iface != nil && f.Iface.generic != nil
}

// --- Instances ---

// Returns the instance of [g] for [args], creating it on first use. [at]
// is where the instance is needed, for errors; it is nil for templates.
func (c *checker) instStruct(g *genericStruct, args []Type, at ast.Node) *Struct {
	key := typeKey(args)
	if s := g.insts[key]; s != nil {
		return s
	}
	abstract := false
	for _, a := range args {
		abstract = abstract || hasTypeParam(a)
	}
	if !c.deepEnough(at, g.decl.Name.Name, args, abstract) {
		return nil
	}
	defer c.enter(g.decl)()
	s := &Struct{Name: g.decl.Name.Name + "<" + typeList(args) + ">", Module: c.mod, Base: g.decl.Name.Name, Args: args, Decl: g.decl, generic: g, abstract: abstract}
	if !abstract {
		s.Decl = ast.Clone(g.decl)
		c.registerClone(s.Decl, g.decl)
		c.info.Structs = append(c.info.Structs, s)
	}
	g.insts[key] = s
	env := envOf(g.params, args)
	c.withEnv(env, func() { c.structMembers(s) })
	if !abstract {
		depth := c.depth + 1
		c.jobs = append(c.jobs, func() {
			c.runInstance(depth, g.failed, func() {
				c.withEnv(env, func() { c.structBodies(s) })
			})
		})
	}
	return s
}

func (c *checker) instFunc(g *genericFunc, args []Type, at ast.Node) *Func {
	key := typeKey(args)
	if f := g.insts[key]; f != nil {
		return f
	}
	abstract := false
	for _, a := range args {
		abstract = abstract || hasTypeParam(a)
	}
	if !c.deepEnough(at, g.decl.Name.Name, args, abstract) {
		return nil
	}
	defer c.enter(g.decl)()
	f := &Func{Name: g.decl.Name.Name, Module: c.mod, Decl: g.decl, TypeArgs: args}
	if !abstract {
		f.Decl = ast.Clone(g.decl)
		c.register(f.Decl, c.units[g.decl])
		c.info.FuncOf[f.Decl] = f
		c.info.Funcs = append(c.info.Funcs, f)
	}
	g.insts[key] = f
	env := envOf(g.params, args)
	c.withEnv(env, func() { c.signature(f) })
	if !abstract {
		depth := c.depth + 1
		c.jobs = append(c.jobs, func() {
			c.runInstance(depth, g.failed, func() {
				c.withEnv(env, func() {
					c.paramDefaults(f)
					c.funcBody(f)
				})
			})
		})
	}
	return f
}

// Reports whether an instance of [name] with type arguments [args] may be
// created, reporting an error at [at] when not. Instances may not nest too
// deeply, and their arguments may not grow without end, as in
// `struct Node<T> { kids: [Node<[T]>] }`.
func (c *checker) deepEnough(at ast.Node, name string, args []Type, abstract bool) bool {
	deep, size := 0, 0
	for _, a := range args {
		d, n := nesting(a)
		deep, size = max(deep, d), size+n
	}
	if deep <= maxInstanceDepth && size <= maxInstanceSize && (abstract || c.depth < maxInstanceDepth) {
		return true
	}
	if at != nil && !c.tooDeep[name] {
		if c.tooDeep == nil {
			c.tooDeep = map[string]bool{}
		}
		c.tooDeep[name] = true
		c.errorAt(at, "make sure generic code doesn't call itself with ever larger types, such as [T], then [[T]]",
			"generic '%s' is used with types nested too deeply", name)
	}
	return false
}

// Runs the checks of an instance at [depth]. When its generic body already
// had errors, the instance's errors are dropped, since they would repeat
// them with concrete types.
func (c *checker) runInstance(depth int, failed bool, f func()) {
	saved := c.depth
	c.depth = depth
	defer func() { c.depth = saved }()
	if failed {
		c.scratch(false, f)
		return
	}
	f()
}

// --- Calls ---

func (c *checker) genericFuncCall(e *ast.CallExpr, g *genericFunc, want Type) Type {
	tmpl := c.funcTemplate(g)
	name := g.decl.Name.Name
	args := c.inferTypeArgs(e, g.params, paramSlots(tmpl), tmpl.Result, want, name)
	if args == nil {
		return Invalid
	}
	f := c.instFunc(g, args, e)
	if f == nil {
		c.checkArgs(e)
		return Invalid
	}
	c.info.Calls[e] = &Call{Kind: FuncCall, Func: f, Args: c.bind(e, paramSlots(f), name)}
	return f.Result
}

func (c *checker) genericStructCall(e *ast.CallExpr, g *genericStruct, want Type) Type {
	tmpl := c.structTemplate(g)
	name := g.decl.Name.Name
	args := c.inferTypeArgs(e, g.params, fieldSlots(tmpl), tmpl, want, name)
	if args == nil {
		return Invalid
	}
	s := c.instStruct(g, args, e)
	if s == nil {
		c.checkArgs(e)
		return Invalid
	}
	c.info.Calls[e] = &Call{Kind: ConstructorCall, Struct: s, Args: c.bind(e, fieldSlots(s), name)}
	return s
}

func fieldSlots(s *Struct) []slot {
	var slots []slot
	for _, f := range s.Fields {
		slots = append(slots, slot{name: f.Name, typ: f.Type, def: f.Default})
	}
	return slots
}

// Works out the type arguments of a call of a generic [callee], from the
// type arguments written at the call or else from its arguments and the
// type the context expects. Returns nil after reporting an error.
func (c *checker) inferTypeArgs(e *ast.CallExpr, params []*TypeParam, slots []slot, result, want Type, callee string) []Type {
	return c.inferBound(e, &binding{params: params, types: map[*TypeParam]Type{}}, slots, result, want, callee)
}

// Works like [inferTypeArgs], for type parameters some of which [b]
// already binds; written type arguments are for the others.
func (c *checker) inferBound(e *ast.CallExpr, b *binding, slots []slot, result, want Type, callee string) []Type {
	params := b.params
	if len(e.TypeArgs) > 0 {
		var args []Type
		for _, te := range e.TypeArgs {
			args = append(args, c.resolveType(te))
		}
		var open []*TypeParam
		for _, p := range params {
			if b.types[p] == nil {
				open = append(open, p)
			}
		}
		if len(args) != len(open) {
			c.checkArgs(e)
			c.errorAt(e.TypeArgs[0], "", "%s takes %s, found %d", callee, plural(len(open), "type argument"), len(args))
			return nil
		}
		for i, p := range open {
			b.types[p] = args[i]
		}
	} else {
		c.scratch(false, func() { c.inferFromArgs(e, slots, b) })
		if want != nil {
			// The expected type wins where the arguments fit it, so
			// `let b: Box<Shape> = Box(v: Circle())` gives a Box<Shape>.
			w := &binding{params: params, types: map[*TypeParam]Type{}}
			w.unify(result, want)
			for p, t := range w.types {
				if got := b.types[p]; got != nil && !Identical(got, t) && assignableType(t, got) {
					b.types[p] = t
				}
			}
			b.unify(result, want)
		}
	}
	args := make([]Type, len(params))
	for i, p := range params {
		t := b.types[p]
		switch {
		case t == nil || t == Void:
			c.checkArgs(e)
			c.errorAt(e.Fn, fmt.Sprintf("write it: %s<%s>(...)", callee, typeParamExample(params)), "can't tell the type of %s in this call to %s", p.Name, callee)
			return nil
		case t == Invalid:
			c.checkArgs(e)
			return nil
		}
		if !c.meetsConstraints(p, t, e.Fn, callee, b.types) {
			c.checkArgs(e)
			return nil
		}
		args[i] = t
	}
	return args
}

func typeParamExample(params []*TypeParam) string {
	parts := make([]string, len(params))
	for i := range params {
		parts[i] = "int"
	}
	return strings.Join(parts, ", ")
}

func constraintHint(cst string) string {
	switch cst {
	case "Comparable":
		return "Comparable types are int, float, string and type parameters that require Comparable"
	case "Hashable":
		return "Hashable types are int, string, bool and enums whose cases carry no values"
	}
	return "Equatable types are int, float, bool, string and enums whose cases carry no values"
}

// Reports whether [t] meets the built-in [constraint].
func satisfies(t Type, constraint string) bool {
	switch constraint {
	case "Equatable":
		return comparable(t)
	case "Comparable":
		return ordered(t)
	case "Hashable":
		return mapKeyType(t)
	}
	return false
}

// Checks the arguments of [e] against [slots] to learn the type
// parameters in [b]. Each argument is checked with as much of its
// expected type as is already known, so closures get parameter types.
func (c *checker) inferFromArgs(e *ast.CallExpr, slots []slot, b *binding) {
	next, set := 0, make([]bool, len(slots))
	for _, a := range e.Args {
		i := -1
		if a.Name == nil {
			i = next
			next++
		} else {
			for j, s := range slots {
				if s.name == a.Name.Name {
					i = j
				}
			}
		}
		if i < 0 || i >= len(slots) {
			continue
		}
		set[i] = true
		t := c.expr(a.Value, b.partial(slots[i].typ))
		b.unify(slots[i].typ, t)
	}
	if e.Trailing != nil && len(slots) > 0 && !set[len(slots)-1] {
		s := slots[len(slots)-1]
		b.unify(s.typ, c.expr(trailingLit(e.Trailing, s.typ), b.partial(s.typ)))
	}
}

// Type arguments being worked out for a call.
type binding struct {
	params []*TypeParam
	types  map[*TypeParam]Type
}

func (b *binding) owns(p *TypeParam) bool {
	for _, q := range b.params {
		if q == p {
			return true
		}
	}
	return false
}

// Learns type parameters by matching the parameter type [p] with the
// argument type [a]. The first type found for a parameter wins.
func (b *binding) unify(p, a Type) {
	if a == nil || a == Invalid {
		return
	}
	switch p := p.(type) {
	case *TypeParam:
		if b.owns(p) && b.types[p] == nil {
			b.types[p] = a
		}
	case *List:
		if a, ok := a.(*List); ok {
			b.unify(p.Elem, a.Elem)
		}
	case *Map:
		if a, ok := a.(*Map); ok {
			b.unify(p.Key, a.Key)
			b.unify(p.Value, a.Value)
		}
	case *Optional:
		if ao, ok := a.(*Optional); ok {
			b.unify(p.Elem, ao.Elem)
		} else {
			b.unify(p.Elem, a)
		}
	case *Future:
		if a, ok := a.(*Future); ok {
			b.unify(p.Elem, a.Elem)
		}
	case *Channel:
		if a, ok := a.(*Channel); ok {
			b.unify(p.Elem, a.Elem)
		}
	case *Record:
		if a, ok := a.(*Record); ok && sameShape(p, a) {
			at := a.FieldTypes()
			for i, t := range p.FieldTypes() {
				b.unify(t, at[i])
			}
		}
	case *FuncType:
		if a, ok := a.(*FuncType); ok && len(a.Params) == len(p.Params) {
			for i := range p.Params {
				b.unify(p.Params[i], a.Params[i])
			}
			b.unify(p.Result, a.Result)
		}
	case *Struct:
		if a, ok := a.(*Struct); ok && p.generic != nil && p.generic == a.generic {
			for i := range p.Args {
				b.unify(p.Args[i], a.Args[i])
			}
		}
	case *Enum:
		if a, ok := a.(*Enum); ok && p.generic != nil && p.generic == a.generic {
			for i := range p.Args {
				b.unify(p.Args[i], a.Args[i])
			}
		}
	case *Interface:
		if p.generic == nil {
			return
		}
		// A struct or enum passed as a generic interface tells its
		// arguments through the instance it implements.
		var ifaces []*Interface
		switch a := a.(type) {
		case *Interface:
			ifaces = a.All()
		case *Struct:
			ifaces = a.Interfaces
		case *Enum:
			ifaces = a.Interfaces
		case *TypeParam:
			for _, i := range a.Ifaces {
				ifaces = append(ifaces, i.All()...)
			}
		}
		for _, i := range ifaces {
			if i.generic == p.generic {
				for n := range p.Args {
					b.unify(p.Args[n], i.Args[n])
				}
				return
			}
		}
	}
}

// Returns [t] with the known type parameters filled in, or nil if one is
// still unknown. A function type whose result alone is unknown is kept
// with a nil result, so a closure can still learn its parameter types.
func (b *binding) partial(t Type) Type {
	if ft, ok := t.(*FuncType); ok {
		out := &FuncType{Throws: ft.Throws}
		for _, p := range ft.Params {
			q := b.subst(p)
			if q == nil {
				return nil
			}
			out.Params = append(out.Params, q)
		}
		out.Result = b.subst(ft.Result)
		return out
	}
	return b.subst(t)
}

// Returns [t] with the known type parameters filled in, or nil if one is
// still unknown. Generic struct types are only filled in when they need
// no new instance.
func (b *binding) subst(t Type) Type {
	switch t := t.(type) {
	case *TypeParam:
		if !b.owns(t) {
			return t
		}
		return b.types[t]
	case *List:
		if e := b.subst(t.Elem); e != nil {
			return &List{Elem: e}
		}
		return nil
	case *Map:
		k, v := b.subst(t.Key), b.subst(t.Value)
		if k == nil || v == nil {
			return nil
		}
		return &Map{Key: k, Value: v}
	case *Optional:
		if e := b.subst(t.Elem); e != nil {
			return optionalOf(e)
		}
		return nil
	case *Future:
		if e := b.subst(t.Elem); e != nil {
			return &Future{Elem: e, Throws: t.Throws}
		}
		return nil
	case *Channel:
		if e := b.subst(t.Elem); e != nil {
			return &Channel{Elem: e}
		}
		return nil
	case *Record:
		var ts []Type
		for _, f := range t.FieldTypes() {
			s := b.subst(f)
			if s == nil {
				return nil
			}
			ts = append(ts, s)
		}
		return t.withTypes(ts)
	case *FuncType:
		out := &FuncType{Throws: t.Throws}
		for _, p := range t.Params {
			q := b.subst(p)
			if q == nil {
				return nil
			}
			out.Params = append(out.Params, q)
		}
		if out.Result = b.subst(t.Result); out.Result == nil {
			return nil
		}
		return out
	case *Struct:
		if t.generic == nil || !t.abstract {
			return t
		}
		args := make([]Type, len(t.Args))
		for i, a := range t.Args {
			if args[i] = b.subst(a); args[i] == nil {
				return nil
			}
		}
		if s := t.generic.insts[typeKey(args)]; s != nil {
			return s
		}
		return nil
	case *Enum:
		if t.generic == nil || !t.abstract {
			return t
		}
		args := make([]Type, len(t.Args))
		for i, a := range t.Args {
			if args[i] = b.subst(a); args[i] == nil {
				return nil
			}
		}
		if e := t.generic.insts[typeKey(args)]; e != nil {
			return e
		}
		return nil
	case *Interface:
		if t.generic == nil || !t.abstract {
			return t
		}
		args := make([]Type, len(t.Args))
		for i, a := range t.Args {
			if args[i] = b.subst(a); args[i] == nil {
				return nil
			}
		}
		if i := t.generic.insts[typeKey(args)]; i != nil {
			return i
		}
		return nil
	}
	return t
}

// --- Generic functions as values ---

// Returns the generic function [id] names when it is used as a value, or
// nil when the name means something else here.
func (c *checker) genFuncOf(id *ast.Ident) *genericFunc {
	if id.Module != "" {
		m := c.imported(id.Module)
		if m == nil {
			return nil
		}
		g := m.genFuncs[id.Name]
		if g != nil {
			c.private(id, m, g.decl.Pub, "function '"+id.Name+"'")
		}
		return g
	}
	if c.lookupVar(id.Name) != nil || id.Name == "self" {
		return nil
	}
	if owner := c.ownStruct(); owner != nil {
		if owner.Field(id.Name) != nil || owner.Property(id.Name) != nil || owner.Method(id.Name) != nil {
			return nil
		}
	}
	return c.genFuncs[id.Name]
}

// Checks the generic function [g] used as a value, taking its type
// arguments from the function type [want].
func (c *checker) genericFuncValue(id *ast.Ident, g *genericFunc, want Type) Type {
	tmpl := c.funcTemplate(g)
	b := &binding{params: g.params, types: map[*TypeParam]Type{}}
	if o, ok := want.(*Optional); ok {
		want = o.Elem
	}
	if ft, ok := want.(*FuncType); ok {
		b.unify(funcValue(tmpl), ft)
	}
	args := make([]Type, len(g.params))
	for i, p := range g.params {
		if args[i] = b.types[p]; args[i] == nil || args[i] == Invalid {
			c.errorAt(id, fmt.Sprintf("write its types: %s<%s>, or give the variable a function type", id.Name, typeParamExample(g.params)),
				"can't tell the type of %s for generic function '%s'", p.Name, id.Name)
			return Invalid
		}
		if !c.meetsConstraints(p, args[i], id, id.Name, bindOf(g.params, args)) {
			return Invalid
		}
	}
	return c.funcInstValue(id, g, args)
}

// Returns the instance of [g] for [args] used as a value named by [id].
func (c *checker) funcInstValue(id *ast.Ident, g *genericFunc, args []Type) Type {
	f := c.instFunc(g, args, id)
	if f == nil {
		return Invalid
	}
	c.info.Uses[id] = f
	return funcValue(f)
}

// Checks `first<int>` used as a value.
func (c *checker) typeArgsValue(e *ast.TypeArgsExpr, want Type) Type {
	id, ok := e.X.(*ast.Ident)
	var g *genericFunc
	if ok {
		g = c.genFuncOf(id)
	}
	if g == nil {
		if eg := c.genEnumRef(e.X); eg != nil {
			c.errorAt(e, fmt.Sprintf("use one of its cases, e.g. %s<...>.%s", eg.decl.Name.Name, eg.decl.Cases[0].Name.Name), "'%s' is a type, not a value", eg.decl.Name.Name)
		} else if ok && id.Module == "" && c.genIfaces[id.Name] != nil && c.lookupVar(id.Name) == nil {
			c.errorAt(e, "", "'%s' is a type, not a value", id.Name)
		} else {
			c.expr(e.X, nil)
			c.errorAt(e, "", "only generic functions can be given type arguments here")
		}
		return Invalid
	}
	var args []Type
	for _, te := range e.Args {
		args = append(args, c.resolveType(te))
	}
	if len(args) != len(g.params) {
		c.errorAt(e.Args[0], "", "%s takes %s, found %d", id.Name, plural(len(g.params), "type argument"), len(args))
		return Invalid
	}
	for i, p := range g.params {
		if args[i] == Invalid || !c.meetsConstraints(p, args[i], e.Args[i], id.Name, bindOf(g.params, args)) {
			return Invalid
		}
	}
	t := c.funcInstValue(id, g, args)
	c.info.Types[id] = t
	return t
}

// Returns how deeply types are nested inside [t], 0 for a plain type, 1
// for `[int]`, and how many types it is made of in all.
func nesting(t Type) (depth, size int) {
	var parts []Type
	switch t := t.(type) {
	case *List:
		parts = []Type{t.Elem}
	case *Map:
		parts = []Type{t.Key, t.Value}
	case *Optional:
		parts = []Type{t.Elem}
	case *Future:
		parts = []Type{t.Elem}
	case *Channel:
		parts = []Type{t.Elem}
	case *Record:
		parts = t.FieldTypes()
	case *FuncType:
		parts = append(append([]Type{}, t.Params...), t.Result)
	case *Struct:
		parts = t.Args
	case *Enum:
		parts = t.Args
	case *Interface:
		parts = t.Args
	}
	size = 1
	if len(parts) == 0 {
		return 0, size
	}
	for _, x := range parts {
		d, n := nesting(x)
		depth = max(depth, d)
		size += n
		if size > maxInstanceSize {
			break
		}
	}
	return depth + 1, size
}
