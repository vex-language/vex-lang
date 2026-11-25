package types

import "github.com/irshadio/vex-lang/compiler/ast"

// The type parameters and instances of a method with its own type
// parameters, such as `fn map<U>(f: fn(T) -> U) -> Box<U>`.
type genericMethod struct {
	params []*TypeParam
	insts  map[string]*Func
	failed bool

	// The type names in scope in its signature, and whether the signature
	// has been resolved. It is resolved on first use, since resolving
	// `Box<U>` in it creates another instance with its own generic method.
	env   map[string]Type
	ready bool
}

// Reports whether [args] are exactly [params], as for a generic template.
func isTemplate(args []Type, params []*TypeParam) bool {
	return typeKey(args) == typeKey(paramTypes(params))
}

// Returns [methods] without those that have their own type parameters,
// whose bodies are checked through their instances.
func plainMethods(methods []*Func) []*Func {
	var out []*Func
	for _, m := range methods {
		if m.gen == nil {
			out = append(out, m)
		}
	}
	return out
}

// Sets up [m] as a generic method when it declares type parameters,
// returning false otherwise. The body is checked once in terms of its
// type parameters when [checkBody] is set, for a non-generic owner or the
// template of a generic one.
func (c *checker) genericMethod(m *Func, abstractOwner, checkBody bool) bool {
	if len(m.Decl.TypeParams) == 0 || m.Decl.Accessor != ast.Method {
		return false
	}
	gm := &genericMethod{params: c.typeParams(m.Decl.TypeParams), insts: map[string]*Func{}}
	m.gen, m.TypeArgs = gm, paramTypes(gm.params)
	env := withParams(c.env, gm.params, m.TypeArgs)
	gm.env = env
	if checkBody {
		c.methodSignature(m)
		c.jobs = append(c.jobs, func() {
			n := len(c.errs)
			c.scratch(true, func() {
				c.withEnv(env, func() {
					c.paramDefaults(m)
					c.funcBody(m)
				})
			})
			gm.failed = len(c.errs) > n
		})
	}
	return true
}

// Resolves the signature of the generic method [m] if not done yet.
func (c *checker) methodSignature(m *Func) {
	if gm := m.gen; !gm.ready {
		gm.ready = true
		c.withEnv(gm.env, func() { c.signature(m) })
	}
}

// Returns a copy of [env] with [params] mapped to [args].
func withParams(env map[string]Type, params []*TypeParam, args []Type) map[string]Type {
	out := map[string]Type{}
	for n, t := range env {
		out[n] = t
	}
	for i, p := range params {
		out[p.Name] = args[i]
	}
	return out
}

// Returns the type names in scope in the methods of [m]'s owner: the type
// arguments of a generic struct or enum instance.
func ownerEnv(m *Func) map[string]Type {
	switch {
	case m.Owner != nil && m.Owner.generic != nil:
		return envOf(m.Owner.generic.params, m.Owner.Args)
	case m.Enum != nil && m.Enum.generic != nil:
		return envOf(m.Enum.generic.params, m.Enum.Args)
	}
	return map[string]Type{}
}

// Returns the method that the call [e] runs: [m] itself, or the instance
// of a generic method for the type arguments written at the call or
// worked out from the arguments and [want]. Returns nil after reporting an
// error.
func (c *checker) methodInst(e *ast.CallExpr, m *Func, want Type) *Func {
	if m.gen == nil {
		if len(e.TypeArgs) > 0 {
			c.errorAt(e.TypeArgs[0], "remove the <...>", "'%s' is not generic, so it takes no type arguments", m.Name)
		}
		return m
	}
	c.methodSignature(m)
	args := c.inferTypeArgs(e, m.gen.params, paramSlots(m), m.Result, want, m.Name)
	if args == nil {
		return nil
	}
	f := c.instMethod(m, args, e)
	if f == nil {
		c.checkArgs(e)
	}
	return f
}

// Returns the instance of the generic method [m] for [args], creating it
// on first use.
func (c *checker) instMethod(m *Func, args []Type, at ast.Node) *Func {
	gm := m.gen
	key := typeKey(args)
	if f := gm.insts[key]; f != nil {
		return f
	}
	abstract := m.Owner != nil && m.Owner.abstract || m.Enum != nil && m.Enum.abstract
	for _, a := range args {
		abstract = abstract || hasTypeParam(a)
	}
	if !c.deepEnough(at, m.Name, args, abstract) {
		return nil
	}
	f := &Func{Name: m.Name, Module: m.Module, Decl: m.Decl, Owner: m.Owner, Enum: m.Enum, Static: m.Static, TypeArgs: args}
	if !abstract {
		f.Decl = ast.Clone(m.Decl)
		c.units[f.Decl] = c.units[m.Decl]
		c.info.FuncOf[f.Decl] = f
		c.info.Funcs = append(c.info.Funcs, f)
	}
	gm.insts[key] = f
	env := withParams(ownerEnv(m), gm.params, args)
	c.withEnv(env, func() { c.signature(f) })
	if !abstract {
		depth := c.depth + 1
		c.jobs = append(c.jobs, func() {
			c.runInstance(depth, gm.failed, func() {
				c.withEnv(env, func() {
					c.paramDefaults(f)
					c.funcBody(f)
				})
			})
		})
	}
	return f
}
