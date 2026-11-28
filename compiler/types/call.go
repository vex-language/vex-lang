package types

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// A parameter, field or built-in argument that a call argument can fill.
type slot struct {
	name string
	typ  Type
	def  ast.Expr

	// How errors refer to the argument; defaults to "argument 'name'".
	label string

	// Set for built-in arguments that may be left out.
	optional bool
}

// The signature of a built-in list or string member.
type memberSig struct {
	params []slot
	result Type
}

func (c *checker) call(e *ast.CallExpr, want Type) Type {
	awaited := c.awaited == e
	c.awaited = nil
	if f, ok := want.(*Future); ok && !awaited {
		want = f.Elem
	}
	t := c.callTarget(e, want)
	if async, throws := c.asyncCall(e); async && !awaited {
		// Without await, an async call starts a task and gives its future.
		c.info.Spawns[e] = true
		if t == Invalid {
			return Invalid
		}
		return &Future{Elem: t, Throws: throws}
	}
	c.checkThrows(e)
	return t
}

func (c *checker) callTarget(e *ast.CallExpr, want Type) Type {
	switch fn := e.Fn.(type) {
	case *ast.Ident:
		return c.callName(e, fn, want)
	case *ast.MemberExpr:
		if c.typeChain(fn) {
			c.checkArgs(e)
			return Invalid
		}
		if g := c.genEnumRef(fn.X); g != nil {
			return c.genericCaseCall(e, g, fn.Name, want)
		}
		return c.callMember(e, fn, want)
	case *ast.ImplicitMember:
		en := c.implicitEnum(fn, want)
		if en == nil {
			c.checkArgs(e)
			return Invalid
		}
		return c.caseCall(e, en, fn.Name, "."+fn.Name.Name)
	}
	return c.callValue(e)
}

// Checks a call creating struct [s].
func (c *checker) construct(e *ast.CallExpr, s *Struct) Type {
	c.inferFields(s)
	c.info.Calls[e] = &Call{Kind: ConstructorCall, Struct: s, Args: c.bind(e, fieldSlots(s), s.String())}
	return s
}

// Checks the arguments of a call that has already failed, so mistakes
// inside them are still reported.
func (c *checker) checkArgs(e *ast.CallExpr) {
	for _, a := range e.Args {
		c.expr(a.Value, nil)
	}
}

// Checks a call of a plain name: a function, constructor, built-in, or a
// method of the implicit `self`.
func (c *checker) callName(e *ast.CallExpr, id *ast.Ident, want Type) Type {
	if id.Module != "" {
		return c.moduleCall(e, id, want)
	}
	name := id.Name
	if name == "hash" && len(e.Args) > 0 && c.lookupVar(name) == nil && c.funcs[name] == nil && c.genFuncs[name] == nil && !c.ownHashWithParams() {
		return c.hashCall(e)
	}
	if name == "Channel" && c.lookupVar(name) == nil && c.structs[name] == nil && c.genStructs[name] == nil && c.funcs[name] == nil && c.enums[name] == nil {
		return c.newChannel(e, want)
	}
	if name == "Set" && c.lookupVar(name) == nil && c.funcs[name] == nil && c.builtinSet() {
		return c.newSet(e, want)
	}
	if name == "StreamController" && c.lookupVar(name) == nil && c.structs[name] == nil && c.genStructs[name] == nil && c.funcs[name] == nil && c.enums[name] == nil {
		return c.newController(e, want)
	}
	if len(e.TypeArgs) > 0 && c.genFuncs[name] == nil && c.genStructs[name] == nil && c.genIfaces[name] == nil {
		c.errorAt(e.TypeArgs[0], "remove the <...>", "'%s' is not generic, so it takes no type arguments", name)
	}
	if v := c.lookupVar(name); v != nil {
		if _, ok := v.Type.(*FuncType); ok || v.Type == Invalid {
			return c.callValue(e)
		}
		c.info.Uses[id] = v
		c.checkArgs(e)
		c.errorAt(id, "", "'%s' is %s, not a function", name, article(v.Type))
		return Invalid
	}
	if c.selfMember(name) {
		e.Fn = selfAccess(id)
		return c.call(e, want)
	}
	if owner := c.ownStruct(); owner != nil {
		if m := owner.Method(name); m != nil {
			if !m.Static && c.self == nil {
				c.checkArgs(e)
				c.errorAt(id, "static methods have no instance to call it on", "static method %s can't call method '%s'", c.fn.Name, name)
				return Invalid
			}
			if c.methodPrivate(id, m) {
				c.checkArgs(e)
				return Invalid
			}
			if m = c.methodInst(e, m, want); m == nil {
				return Invalid
			}
			c.info.Uses[id] = m
			kind := MethodCall
			if m.Static {
				kind = StaticCall
			} else {
				c.use(c.self)
			}
			c.info.Calls[e] = &Call{Kind: kind, Func: m, Args: c.bind(e, paramSlots(m), m.Name)}
			return m.Result
		}
		if f := owner.Field(name); f != nil {
			if _, ok := f.Type.(*FuncType); ok {
				return c.callValue(e)
			}
			c.checkArgs(e)
			c.errorAt(id, "", "'%s' is a field, not a method", name)
			return Invalid
		}
	}
	if c.fn != nil && c.fn.Iface != nil && c.self != nil {
		if _, m := ifaceMethod(c.fn.Iface, name); m != nil {
			c.info.Uses[id] = m
			c.use(c.self)
			return c.ifaceCall(e, m, nil)
		}
	}
	if c.self != nil {
		if t, ok := c.extCall(e, nil, id, c.self.Type, name); ok {
			c.info.Uses[id] = c.info.Calls[e].Func
			c.use(c.self)
			return t
		}
	}
	if c.fn != nil && c.fn.Enum != nil {
		if m := c.fn.Enum.Method(name); m != nil {
			if !m.Static && c.self == nil {
				c.checkArgs(e)
				c.errorAt(id, "static methods have no instance to call it on", "static method %s can't call method '%s'", c.fn.Name, name)
				return Invalid
			}
			c.info.Uses[id] = m
			kind := StaticCall
			if !m.Static {
				kind = MethodCall
				c.use(c.self)
			}
			c.info.Calls[e] = &Call{Kind: kind, Func: m, Args: c.bind(e, paramSlots(m), m.Name)}
			return m.Result
		}
	}
	if en := c.enums[name]; en != nil {
		return c.enumCall(e, en, id, "")
	}
	if i := c.interfaces[name]; i != nil {
		c.checkArgs(e)
		c.errorAt(id, fmt.Sprintf("create a struct that implements it: struct Name: %s", i.Name), "%s is an interface, so it can't be created", i.Name)
		return Invalid
	}
	if c.handles[name] != nil {
		c.checkArgs(e)
		c.errorAt(id, "get one from an extern fn", "%s is a C type, so it can't be created in Vex", name)
		return Invalid
	}
	if c.genIfaces[name] != nil {
		c.checkArgs(e)
		c.errorAt(id, fmt.Sprintf("create a struct that implements it: struct Name: %s<...>", name), "%s is an interface, so it can't be created", name)
		return Invalid
	}
	if k := c.findConst(name); k != nil && k.Lazy {
		c.checkConst(k)
		if _, ok := k.Type.(*FuncType); ok || k.Type == Invalid {
			return c.callValue(e)
		}
		c.info.Uses[id] = k
		c.checkArgs(e)
		c.errorAt(id, "", "'%s' is %s, not a function", name, article(k.Type))
		return Invalid
	}
	if asyncBuiltins[name] && c.funcs[name] == nil && c.genFuncs[name] == nil {
		return c.asyncBuiltin(e, name, want)
	}
	if builtinFuncs[name] {
		return c.callBuiltin(e, name)
	}
	if (name == "jsonOf" || name == "jsonAs") && c.mod.Path == jsonModule && c.funcs[name] == nil {
		return c.jsonBuiltin(e, name, want)
	}
	if f := c.funcs[name]; f != nil {
		c.info.Calls[e] = &Call{Kind: FuncCall, Func: f, Args: c.bind(e, paramSlots(f), f.Name)}
		return f.Result
	}
	if g := c.genFuncs[name]; g != nil {
		return c.genericFuncCall(e, g, want)
	}
	if g := c.genStructs[name]; g != nil {
		return c.genericStructCall(e, g, want)
	}
	if s := c.structs[name]; s != nil {
		return c.construct(e, s)
	}
	if _, t := c.aliasedNominal(c.mod, name); t != nil && c.lookupVar(name) == nil {
		if s, ok := t.(*Struct); ok && c.constructPrivate(e, s) {
			c.checkArgs(e)
			return Invalid
		} else if ok {
			return c.construct(e, s)
		}
		return c.enumCall(e, t.(*Enum), id, "")
	}
	if a := c.aliases[name]; a != nil && c.lookupVar(name) == nil {
		c.checkArgs(e)
		switch t := c.aliasTarget(a).(type) {
		case *Struct:
			c.errorAt(id, "create it with the struct's own name", "%s is a generic typedef, so it can't be called", name)
		case nil:
		default:
			if t != Invalid {
				c.errorAt(id, "", "%s names %s, so it can't be called", name, t)
			}
		}
		return Invalid
	}
	c.checkArgs(e)
	var names []string
	for n := range c.funcs {
		names = append(names, n)
	}
	for n := range c.structs {
		names = append(names, n)
	}
	for n := range builtinFuncs {
		names = append(names, n)
	}
	if owner := c.ownStruct(); owner != nil {
		if owner.Property(name) != nil {
			c.checkArgs(e)
			c.errorAt(id, "read it without (): "+name, "'%s' is a property, not a method", name)
			return Invalid
		}
		for _, m := range owner.Methods {
			names = append(names, m.Name)
		}
	}
	if c.pending[name] > 0 {
		c.errorAt(id, "move the declaration of "+name+" above this line", "local function '%s' is used before it is declared", name)
		return Invalid
	}
	c.errorAt(id, didYouMean(name, names), "unknown function '%s'", name)
	return Invalid
}

// Checks a call of the form `x.name(...)`.
func (c *checker) callMember(e *ast.CallExpr, m *ast.MemberExpr, want Type) Type {
	name := m.Name.Name
	if t := basicRef(m.X); t != nil {
		if name == "parse" && basicStatics[t] != nil {
			return c.callBuiltin(e, t.String()+".parse")
		}
		c.checkArgs(e)
		if _, ok := basicStatics[t][name]; ok {
			c.errorAt(m.Name, fmt.Sprintf("read it without (): %s.%s", t, name), "'%s' is a constant, not a method", name)
		} else {
			c.errorAt(m.Name, didYouMean(name, basicStaticNames(t)), "%s has no static method '%s'", t, name)
		}
		return Invalid
	}
	if tp := c.typeParamRef(m.X); tp != nil {
		return c.staticRequirementCall(e, m, tp, want)
	}
	if en := c.enumRef(m.X); en != nil {
		if method := en.Method(name); method != nil {
			if c.methodPrivate(m.Name, method) {
				c.checkArgs(e)
				return Invalid
			}
			if !method.Static {
				c.checkArgs(e)
				c.errorAt(m.Name, fmt.Sprintf("call it on a value, e.g. shape.%s(), or declare it static fn", name), "'%s' needs a %s value", name, en.Name)
				return Invalid
			}
			if method = c.methodInst(e, method, want); method == nil {
				return Invalid
			}
			c.info.Calls[e] = &Call{Kind: StaticCall, Func: method, Args: c.bind(e, paramSlots(method), en.Name+"."+name)}
			return method.Result
		}
		return c.caseCall(e, en, m.Name, en.Name+"."+name)
	}
	if s := c.structRef(m.X); s != nil {
		method := s.Method(name)
		if method != nil && c.methodPrivate(m.Name, method) {
			c.checkArgs(e)
			return Invalid
		}
		switch {
		case method == nil:
			c.checkArgs(e)
			c.errorAt(m.Name, didYouMean(name, memberNames(s)), "%s has no static method '%s'", s.Name, name)
			return Invalid
		case !method.Static:
			c.checkArgs(e)
			c.errorAt(m.Name, fmt.Sprintf("call it on a value, e.g. user.%s(), or declare it static fn", name),
				"'%s' needs a %s value", name, s.Name)
			return Invalid
		}
		if method = c.methodInst(e, method, want); method == nil {
			return Invalid
		}
		c.info.Calls[e] = &Call{Kind: StaticCall, Func: method, Args: c.bind(e, paramSlots(method), s.Name+"."+name)}
		return method.Result
	}

	recv := c.recv(m)
	if s, ok := recv.(*Struct); ok {
		method := s.Method(name)
		switch {
		case method == nil:
			if f := s.Field(name); f != nil {
				if _, ok := f.Type.(*FuncType); ok {
					return c.callValue(e)
				}
			}
			if name == "copy" && s.Field(name) == nil && s.Property(name) == nil {
				return c.copyCall(e, m, s)
			}
			if t, ok := c.extCall(e, m.X, m.Name, s, name); ok {
				return t
			}
			if s.Field(name) == nil && s.Property(name) == nil {
				if t := c.recvCall(e, m, s, want); t != nil {
					return t
				}
			}
			c.checkArgs(e)
			if s.Field(name) != nil {
				c.errorAt(m.Name, "", "'%s' is a field, not a method", name)
			} else if s.Property(name) != nil {
				c.errorAt(m.Name, "read it without ()", "'%s' is a property, not a method", name)
			} else {
				c.errorAt(m.Name, didYouMean(name, memberNames(s)), "%s has no method '%s'", s.Name, name)
			}
			return Invalid
		case method.Static:
			c.checkArgs(e)
			c.errorAt(m.Name, fmt.Sprintf("call it on the type: %s.%s()", s.Name, name), "'%s' is a static method", name)
			return Invalid
		}
		if c.methodPrivate(m.Name, method) {
			c.checkArgs(e)
			return Invalid
		}
		if method = c.methodInst(e, method, want); method == nil {
			return Invalid
		}
		c.info.Calls[e] = &Call{Kind: MethodCall, Func: method, Recv: m.X, Args: c.bind(e, paramSlots(method), name)}
		return method.Result
	}
	if r, ok := recv.(*Record); ok {
		if _, ok := r.Field(name).(*FuncType); ok {
			return c.callValue(e)
		}
	}
	if en, ok := recv.(*Enum); ok {
		method := en.Method(name)
		switch {
		case method == nil:
			if t, ok := c.extCall(e, m.X, m.Name, en, name); ok {
				return t
			}
			if t := c.recvCall(e, m, en, want); t != nil {
				return t
			}
			c.checkArgs(e)
			c.errorAt(m.Name, didYouMean(name, enumNames(en)), "%s has no method '%s'", en.Name, name)
			return Invalid
		case method.Static:
			c.checkArgs(e)
			c.errorAt(m.Name, fmt.Sprintf("call it on the type: %s.%s()", en.Name, name), "'%s' is a static method", name)
			return Invalid
		}
		if c.methodPrivate(m.Name, method) {
			c.checkArgs(e)
			return Invalid
		}
		if method = c.methodInst(e, method, want); method == nil {
			return Invalid
		}
		c.info.Calls[e] = &Call{Kind: MethodCall, Func: method, Recv: m.X, Args: c.bind(e, paramSlots(method), name)}
		return method.Result
	}
	if recv == Invalid {
		c.checkArgs(e)
		return Invalid
	}
	switch recv.(type) {
	case *Interface, *TypeParam:
		if _, method := ifaceMethod(recv, name); method != nil {
			if method.Static {
				c.checkArgs(e)
				c.errorAt(m.Name, fmt.Sprintf("call it on the type: T.%s(...)", name), "'%s' is a static requirement", name)
				return Invalid
			}
			return c.ifaceCall(e, method, m.X)
		}
		if t, ok := c.extCall(e, m.X, m.Name, recv, name); ok {
			return t
		}
		if t := c.recvCall(e, m, recv, want); t != nil {
			return t
		}
		c.checkArgs(e)
		hint := didYouMean(name, ifaceNames(recv))
		if tp, ok := recv.(*TypeParam); ok && hint == "" {
			hint = fmt.Sprintf("require an interface that has it: <%s: Name>", tp.Name)
			for _, i := range c.info.Interfaces {
				if i.Method(name) != nil {
					hint = fmt.Sprintf("require it: <%s: %s>", tp.Name, i.Name)
					break
				}
			}
		}
		c.errorAt(m.Name, c.narrowHint(m.X, hint), "%s has no method '%s'", recv, name)
		return Invalid
	}
	if len(e.TypeArgs) > 0 && name != "cast" && builtinGeneric(recv, name) {
		c.errorAt(e.TypeArgs[0], "remove the <...>", "'%s' is not generic, so it takes no type arguments", name)
	}
	if l, ok := recv.(*List); ok {
		switch name {
		case "cast":
			return c.castCall(e, m, l)
		case "map":
			return c.mapCall(e, m, l)
		case "flatMap":
			return c.flatMapCall(e, m, l)
		case "reduce":
			return c.reduceCall(e, m, l)
		}
	}
	if mt, ok := recv.(*Map); ok && name == "mapValues" {
		return c.mapValuesCall(e, m, mt)
	}
	if s, ok := recv.(*Set); ok {
		if t, ok := c.setCall(e, m, s, name); ok {
			return t
		}
	}
	sig := builtinMember(recv, name)
	if sig == nil {
		if t := c.recvCall(e, m, recv, want); t != nil {
			return t
		}
		c.checkArgs(e)
		if c.recvUnavailable(m.Name, recv, name) {
			return Invalid
		}
		if hint := mapMethodHint(recv, name); hint != "" {
			c.errorAt(m.Name, hint, "%s has no method '%s'", recv, name)
		} else if name == "len" || name == "keys" || name == "values" || name == "entries" || name == "first" || name == "last" || name == "isEmpty" || name == "isCancelled" || name == "stream" || name == "isClosed" {
			c.errorAt(m.Name, "write it without (): ."+name, "%s is a property, not a method", name)
		} else {
			c.errorAt(m.Name, didYouMean(name, builtinMemberNames(recv)), "%s has no method '%s'", recv, name)
		}
		return Invalid
	}
	if l, ok := recv.(*List); ok && l.Elem != Invalid {
		hint := ""
		switch {
		case (name == "contains" || name == "indexOf") && !comparable(l.Elem):
			if s, ok := l.Elem.(*Struct); ok {
				hint = fmt.Sprintf("define it in %s: operator ==(other: %s) -> bool", s.Name, s.Name)
			}
		case (name == "min" || name == "max" || ((name == "sort" || name == "sorted") && len(e.Args) == 0)) && !sortable(l.Elem):
			if s, ok := l.Elem.(*Struct); ok {
				hint = fmt.Sprintf("define it in %s: operator <(other: %s) -> bool", s.Name, s.Name)
			}
			if name == "sort" || name == "sorted" {
				hint = fmt.Sprintf("pass the order: %s(by: fn(a, b) => ...)", name)
			}
		case name == "sum" && l.Elem != Int && l.Elem != Float:
			c.checkArgs(e)
			c.errorAt(m.Name, "", "sum needs a list of int or float, found %s", recv)
			return Invalid
		default:
			hint = "-"
		}
		if hint != "-" {
			c.checkArgs(e)
			c.errorAt(m.Name, hint, "%s can't be used on a list of %s", name, l.Elem)
			return Invalid
		}
	}
	switch recv.(type) {
	case *List:
		switch name {
		case "push", "pop", "sort", "insert", "remove", "removeWhere", "clear":
			c.constChange(m.X, "list")
		}
	case *Map:
		switch name {
		case "remove", "getOrPut", "update":
			c.constChange(m.X, "map")
		}
	}
	c.info.Calls[e] = &Call{Kind: MemberBuiltinCall, Builtin: name, Recv: m.X, Args: c.bind(e, sig.params, name)}
	return sig.result
}

func paramSlots(f *Func) []slot {
	slots := make([]slot, len(f.Params))
	for i, p := range f.Params {
		slots[i] = slot{name: p.Name, typ: p.Type, def: p.Default}
	}
	return slots
}

// Matches the arguments of [e] to [slots] and returns the values in slot
// order, with defaults filled in. Left-out optional built-in arguments are
// nil. [callee] names the function in errors.
func (c *checker) bind(e *ast.CallExpr, slots []slot, callee string) []ast.Expr {
	out := make([]ast.Expr, len(slots))
	set := make([]bool, len(slots))
	// Set when an argument is misplaced or misnamed, so the same mistake
	// isn't also reported as a missing argument.
	next, named, misplaced := 0, false, false
	for _, a := range e.Args {
		i := -1
		switch {
		case a.Name == nil && named:
			c.errorAt(a, "put unnamed arguments first", "unnamed argument after a named one")
			misplaced = true
		case a.Name == nil && next >= len(slots):
			c.errorAt(a, "", "too many arguments: %s takes %s", callee, plural(len(slots), "argument"))
		case a.Name == nil:
			i = next
			next++
		default:
			named = true
			for j, s := range slots {
				if s.name == a.Name.Name {
					i = j
				}
			}
			if i < 0 {
				var names []string
				for _, s := range slots {
					names = append(names, s.name)
				}
				c.errorAt(a.Name, didYouMean(a.Name.Name, names), "%s has no parameter '%s'", callee, a.Name.Name)
				misplaced = true
			} else if set[i] {
				c.errorAt(a.Name, "", "argument '%s' is given twice", a.Name.Name)
				i = -1
			}
		}
		if i < 0 {
			c.expr(a.Value, nil)
			continue
		}
		t := c.expr(a.Value, slots[i].typ)
		c.assignable(slots[i].typ, t, a.Value, slots[i].describe())
		out[i], set[i] = a.Value, true
	}
	if call := c.info.Calls[e]; e.Trailing != nil && (call == nil || call.Kind != BuiltinCall) {
		if i := c.trailing(e, slots, set, callee); i >= 0 {
			lit := trailingLit(e, slots[i].typ)
			t := c.expr(lit, slots[i].typ)
			c.assignable(slots[i].typ, t, lit, slots[i].describe())
			out[i], set[i] = lit, true
		} else {
			misplaced = true
		}
	}
	var missing []string
	for i, s := range slots {
		switch {
		case set[i]:
		case s.def != nil:
			out[i] = s.def
		case !s.optional:
			missing = append(missing, s.name)
		}
	}
	if len(missing) > 0 && !misplaced {
		hint := fmt.Sprintf("e.g. %s(%s: ...)", callee, missing[0])
		if len(missing) == 1 {
			c.errorAt(e.Fn, hint, "missing argument '%s' in call to %s", missing[0], callee)
		} else {
			c.errorAt(e.Fn, hint, "missing arguments %s in call to %s", quoteList(missing), callee)
		}
	}
	return out
}

// Returns how errors refer to the argument for [s].
func (s slot) describe() string {
	if s.label != "" {
		return s.label
	}
	return "argument '" + s.name + "'"
}

// Returns the slot a trailing block fills: the last one, which must take a
// function without parameters. Reports an error and returns -1 otherwise.
func (c *checker) trailing(e *ast.CallExpr, slots []slot, set []bool, callee string) int {
	i := len(slots) - 1
	if i < 0 {
		c.errorAt(e.Trailing, "", "%s takes no arguments, so it can't take a trailing block", callee)
		return -1
	}
	_, isList := slots[i].typ.(*List)
	if _, isFunc := slots[i].typ.(*FuncType); !isList && !isFunc && slots[i].typ != Invalid {
		c.errorAt(e.Trailing, "", "a trailing block needs the last parameter of %s to be a function or a list, but '%s' is %s", callee, slots[i].name, slots[i].typ)
		return -1
	}
	if set[i] {
		c.errorAt(e.Trailing, "", "the last argument of %s is given twice: in the call and as a trailing block", callee)
		return -1
	}
	if isList {
		if len(e.BlockParams) > 0 {
			c.errorAt(e.BlockParams[0], "remove the parameters and write one element per line", "a builder block for '%s' takes no parameters", slots[i].name)
			return -1
		}
		return i
	}
	return i
}

// Returns the arguments of [e], with its trailing block, if any, as a
// closure after them.
func withBlock(e *ast.CallExpr) []*ast.Arg {
	if e.Trailing == nil {
		return e.Args
	}
	args := append([]*ast.Arg{}, e.Args...)
	return append(args, &ast.Arg{Span: e.Trailing.Span, Value: trailingLit(e, &FuncType{})})
}

// Returns the argument the trailing block of [e] gives for a parameter of
// type [t]: a builder block for a list, otherwise a closure.
func trailingLit(e *ast.CallExpr, t Type) ast.Expr {
	b := e.Trailing
	if _, ok := t.(*List); ok && len(e.BlockParams) == 0 {
		return &ast.ListLit{Span: b.Span, Body: b}
	}
	lit := &ast.FuncLit{Span: b.Span, Body: b, Block: true}
	for _, id := range e.BlockParams {
		lit.Params = append(lit.Params, &ast.Param{Span: id.Span, Name: id})
	}
	return lit
}

// Returns [t] with "a" or "an" before it: "an int", "a string".
func article(t Type) string {
	name := t.String()
	if strings.ContainsRune("aeiouAEIOU", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// Formats names as 'a', 'b' and 'c'.
func quoteList(names []string) string {
	s := ""
	for i, n := range names {
		switch {
		case i == 0:
		case i == len(names)-1:
			s += " and "
		default:
			s += ", "
		}
		s += "'" + n + "'"
	}
	return s
}

// --- Built-ins ---

// Built-in functions with fixed argument types.
var builtinSlots = map[string]struct {
	params []slot
	result Type
}{
	"input":  {[]slot{{name: "prompt", typ: String, optional: true}}, String},
	"panic":  {[]slot{{name: "message", typ: String}}, Void},
	"assert": {[]slot{{name: "condition", typ: Bool}, {name: "message", typ: String, optional: true}}, Void},
	"expect": {[]slot{{name: "condition", typ: Bool}, {name: "message", typ: String, optional: true}}, Void},

	"int.parse":   {[]slot{{name: "text", typ: String}, {name: "radix", typ: Int, optional: true}}, &Optional{Elem: Int}},
	"float.parse": {[]slot{{name: "text", typ: String}}, &Optional{Elem: Float}},
}

// Returns the built-in type [x] names, such as `int` in `int.parse`, or
// nil. Their names can't be declared again, so the name alone decides.
func basicRef(x ast.Expr) Type {
	if id, ok := x.(*ast.Ident); ok && id.Module == "" {
		return builtinTypes[id.Name]
	}
	return nil
}

// The static members of the built-in types, and their types.
var basicStatics = map[Type]map[string]Type{
	Int:   {"max": Int, "min": Int, "parse": nil},
	Float: {"infinity": Float, "nan": Float, "parse": nil},
}

// Returns the names of the static members of the built-in type [t].
func basicStaticNames(t Type) []string {
	var names []string
	for n := range basicStatics[t] {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (c *checker) callBuiltin(e *ast.CallExpr, name string) Type {
	call := &Call{Kind: BuiltinCall, Builtin: name}
	c.info.Calls[e] = call
	if e.Trailing != nil {
		c.errorAt(e.Trailing, "", "%s doesn't take a trailing block", name)
	}
	if name == "expect" && !c.testing {
		c.checkArgs(e)
		c.errorAt(e.Fn, "use it in a test block, run by vex test, or use assert", "expect only works in tests")
		return Void
	}
	if b, ok := builtinSlots[name]; ok {
		call.Args = c.bind(e, b.params, name)
		if name == "expect" && len(call.Args) > 0 && call.Args[0] != nil {
			call.Text = c.source(call.Args[0])
		}
		return b.result
	}
	if name == "str" && len(e.Args) == 2 && e.Args[1].Name != nil && e.Args[1].Name.Name == "radix" {
		call.Builtin = "str.radix"
		call.Args = c.bind(e, []slot{{name: "value", typ: Int}, {name: "radix", typ: Int}}, name)
		return String
	}
	for _, a := range e.Args {
		if a.Name != nil {
			c.errorAt(a.Name, "", "%s doesn't take named arguments", name)
		}
		call.Args = append(call.Args, a.Value)
	}

	switch name {
	case "print", "println":
		for _, a := range e.Args {
			if t := c.expr(a.Value, nil); !printable(t) {
				c.errorAt(a.Value, "", "this expression has no value to print")
			}
		}
		return Void
	}

	// str, int and float take exactly one value.
	result := map[string]Type{"str": String, "int": Int, "float": Float}[name]
	if len(e.Args) != 1 {
		c.checkArgs(e)
		c.errorAt(e.Fn, fmt.Sprintf("e.g. %s(x)", name), "%s takes 1 argument, found %d", name, len(e.Args))
		return result
	}
	arg := e.Args[0].Value
	t := c.expr(arg, nil)
	switch {
	case t == Invalid:
	case t == Void:
		c.errorAt(arg, "", "this expression has no value to convert")
	case name == "str":
	case t != Int && t != Float && t != String:
		c.errorAt(arg, "", "can't convert %s to %s", t, name)
	}
	return result
}

// Checks `list.map(fn(x) => ...)`, whose result is a list of whatever the
// function returns.
func (c *checker) mapCall(e *ast.CallExpr, m *ast.MemberExpr, l *List) Type {
	fnType := &FuncType{Params: []Type{l.Elem}}
	args := withBlock(e)
	var arg ast.Expr
	switch {
	case len(args) == 1 && args[0].Name == nil:
		arg = args[0].Value
	default:
		c.checkArgs(e)
		c.errorAt(m.Name, "e.g. xs.map(fn(x) => x * 2)", "map takes 1 function")
		return Invalid
	}
	t := c.expr(arg, fnType)
	ft, ok := t.(*FuncType)
	switch {
	case t == Invalid:
		return Invalid
	case !ok || len(ft.Params) != 1 || !Identical(ft.Params[0], l.Elem):
		c.errorAt(arg, "e.g. xs.map(fn(x) => x * 2)", "map needs a function that takes %s, found %s", l.Elem, t)
		return Invalid
	case ft.Result == Void:
		c.errorAt(arg, "", "map needs a function that returns a value")
		return Invalid
	}
	c.info.Calls[e] = &Call{Kind: MemberBuiltinCall, Builtin: "map", Recv: m.X, Args: []ast.Expr{arg}}
	return &List{Elem: ft.Result}
}

// Reports whether [name] is a built-in method of [recv], which takes no
// type arguments unless it is a list's `cast`.
func builtinGeneric(recv Type, name string) bool {
	if _, ok := recv.(*List); ok && (name == "map" || name == "flatMap" || name == "reduce") {
		return true
	}
	return builtinMember(recv, name) != nil
}

// Checks `list.cast<T>()`, a copy of the list with each element converted
// to T, which must be a type every element converts to implicitly.
func (c *checker) castCall(e *ast.CallExpr, m *ast.MemberExpr, l *List) Type {
	if len(e.TypeArgs) != 1 || len(e.Args) > 0 || e.Trailing != nil {
		c.checkArgs(e)
		c.errorAt(m.Name, "e.g. circles.cast<Shape>()", "cast takes 1 type argument and no arguments")
		return Invalid
	}
	to := c.resolveType(e.TypeArgs[0])
	if to == Invalid || l.Elem == Invalid {
		return Invalid
	}
	if !elemConverts(to, l.Elem) {
		c.errorAt(e.TypeArgs[0], elementHint(to, l.Elem, "list"), "can't cast a list of %s to a list of %s", l.Elem, to)
		return Invalid
	}
	c.info.Calls[e] = &Call{Kind: MemberBuiltinCall, Builtin: "cast", Recv: m.X}
	return &List{Elem: to}
}

// Reports whether lists of [t] can be sorted without an order given: [t]
// is ordered, or a struct that defines `<`.
func sortable(t Type) bool {
	switch t := t.(type) {
	case *Struct:
		return t.Operator("<") != nil
	case *Enum:
		if t.Operator("<") != nil {
			return true
		}
	}
	return ordered(t)
}

// Returns the type of a function telling whether one [t] goes before
// another.
func lessFunc(t Type) *FuncType { return &FuncType{Params: []Type{t, t}, Result: Bool} }

// Checks `list.flatMap(fn(x) => [...])`, whose result joins the lists the
// function returns.
func (c *checker) flatMapCall(e *ast.CallExpr, m *ast.MemberExpr, l *List) Type {
	args := withBlock(e)
	if len(args) != 1 || args[0].Name != nil {
		c.checkArgs(e)
		c.errorAt(m.Name, "e.g. xs.flatMap(fn(x) => [x, x])", "flatMap takes 1 function")
		return Invalid
	}
	arg := args[0].Value
	t := c.expr(arg, &FuncType{Params: []Type{l.Elem}})
	ft, ok := t.(*FuncType)
	if t == Invalid {
		return Invalid
	}
	var out *List
	if ok && len(ft.Params) == 1 && Identical(ft.Params[0], l.Elem) {
		out, _ = ft.Result.(*List)
	}
	if out == nil {
		c.errorAt(arg, "e.g. xs.flatMap(fn(x) => [x, x])", "flatMap needs a function that takes %s and returns a list, found %s", l.Elem, t)
		return Invalid
	}
	c.info.Calls[e] = &Call{Kind: MemberBuiltinCall, Builtin: "flatMap", Recv: m.X, Args: []ast.Expr{arg}}
	return out
}

// Checks `list.reduce(initial, fn(acc, x) => ...)`, whose result has the
// type of [initial].
func (c *checker) reduceCall(e *ast.CallExpr, m *ast.MemberExpr, l *List) Type {
	args := withBlock(e)
	if len(args) != 2 || args[0].Name != nil || args[1].Name != nil {
		c.checkArgs(e)
		c.errorAt(m.Name, "e.g. xs.reduce(0, fn(sum, x) => sum + x)", "reduce takes a starting value and a function")
		return Invalid
	}
	init, arg := args[0].Value, args[1].Value
	r := c.expr(init, nil)
	if r == Invalid {
		c.expr(arg, nil)
		return Invalid
	}
	if r == Void {
		c.errorAt(init, "", "this expression has no value to start from")
		return Invalid
	}
	want := &FuncType{Params: []Type{r, l.Elem}, Result: r}
	t := c.expr(arg, want)
	if t != Invalid && !Identical(t, want) {
		c.errorAt(arg, "e.g. xs.reduce(0, fn(sum, x) => sum + x)", "reduce needs %s, found %s", want, t)
		return Invalid
	}
	c.info.Calls[e] = &Call{Kind: MemberBuiltinCall, Builtin: "reduce", Recv: m.X, Args: []ast.Expr{init, arg}}
	return r
}

// Returns the signature of the built-in method [name] of [recv], or nil.
func builtinMember(recv Type, name string) *memberSig {
	if l, ok := recv.(*List); ok {
		switch name {
		case "push":
			return &memberSig{[]slot{{name: "item", typ: l.Elem}}, Void}
		case "pop":
			return &memberSig{nil, l.Elem}
		case "contains":
			return &memberSig{[]slot{{name: "item", typ: l.Elem}}, Bool}
		case "filter":
			return &memberSig{[]slot{{name: "keep", typ: &FuncType{Params: []Type{l.Elem}, Result: Bool}}}, &List{Elem: l.Elem}}
		case "copy", "reversed":
			return &memberSig{nil, l}
		case "sort":
			return &memberSig{[]slot{{name: "by", typ: lessFunc(l.Elem), optional: true}}, Void}
		case "sorted":
			return &memberSig{[]slot{{name: "by", typ: lessFunc(l.Elem), optional: true}}, l}
		case "insert":
			return &memberSig{[]slot{{name: "item", typ: l.Elem}, {name: "at", typ: Int}}, Void}
		case "remove":
			return &memberSig{[]slot{{name: "at", typ: Int}}, l.Elem}
		case "removeWhere":
			return &memberSig{[]slot{{name: "test", typ: &FuncType{Params: []Type{l.Elem}, Result: Bool}}}, Void}
		case "indexOf":
			return &memberSig{[]slot{{name: "item", typ: l.Elem}}, &Optional{Elem: Int}}
		case "find":
			return &memberSig{[]slot{{name: "test", typ: &FuncType{Params: []Type{l.Elem}, Result: Bool}}}, &Optional{Elem: l.Elem}}
		case "any", "all":
			return &memberSig{[]slot{{name: "test", typ: &FuncType{Params: []Type{l.Elem}, Result: Bool}}}, Bool}
		case "join":
			return &memberSig{[]slot{{name: "separator", typ: String, optional: true}}, String}
		case "clear":
			return &memberSig{nil, Void}
		case "min", "max":
			return &memberSig{nil, &Optional{Elem: l.Elem}}
		case "sum":
			return &memberSig{nil, l.Elem}
		case "slice":
			return &memberSig{[]slot{{name: "from", typ: Int}, {name: "to", typ: Int}}, l}
		}
		return nil
	}
	if _, ok := recv.(*Future); ok && name == "cancel" {
		return &memberSig{nil, Void}
	}
	if s, ok := recv.(*Stream); ok && s.Controller {
		switch name {
		case "add":
			return &memberSig{[]slot{{name: "value", typ: s.Elem}}, Void}
		case "close":
			return &memberSig{nil, Void}
		}
	}
	if ch, ok := recv.(*Channel); ok {
		switch name {
		case "send":
			return &memberSig{[]slot{{name: "value", typ: ch.Elem}}, Void}
		case "receive":
			return &memberSig{nil, &Future{Elem: &Optional{Elem: ch.Elem}}}
		case "receiveSync":
			return &memberSig{nil, &Optional{Elem: ch.Elem}}
		case "close":
			return &memberSig{nil, Void}
		}
		return nil
	}
	if s, ok := recv.(*Set); ok {
		return setMember(s, name)
	}
	if m, ok := recv.(*Map); ok {
		switch name {
		case "contains":
			return &memberSig{[]slot{{name: "key", typ: m.Key}}, Bool}
		case "remove":
			return &memberSig{[]slot{{name: "key", typ: m.Key}}, &Optional{Elem: m.Value}}
		case "copy":
			return &memberSig{nil, m}
		case "getOrPut":
			return &memberSig{[]slot{{name: "key", typ: m.Key}, {name: "defaultValue", typ: &FuncType{Result: m.Value}}}, m.Value}
		case "update":
			return &memberSig{[]slot{{name: "key", typ: m.Key}, {name: "transform", typ: &FuncType{Params: []Type{m.Value}, Result: m.Value}}}, &Optional{Elem: m.Value}}
		case "filter":
			return &memberSig{[]slot{{name: "keep", typ: &FuncType{Params: []Type{m.Key, m.Value}, Result: Bool}}}, m}
		}
		return nil
	}
	if recv == Float && name == "toFixed" {
		return &memberSig{[]slot{{name: "digits", typ: Int}}, String}
	}
	if recv == Range {
		switch name {
		case "contains":
			return &memberSig{[]slot{{name: "value", typ: Int}}, Bool}
		case "step":
			return &memberSig{[]slot{{name: "by", typ: Int}}, Range}
		case "reversed":
			return &memberSig{nil, Range}
		case "toList":
			return &memberSig{nil, &List{Elem: Int}}
		}
		return nil
	}
	if recv == String {
		switch name {
		case "upper", "lower", "capitalize", "reversed":
			return &memberSig{nil, String}
		case "contains":
			return &memberSig{[]slot{{name: "text", typ: String}}, Bool}
		case "count":
			return &memberSig{[]slot{{name: "text", typ: String}}, Int}
		case "split":
			return &memberSig{[]slot{{name: "separator", typ: String}, {name: "limit", typ: Int, optional: true}}, &List{Elem: String}}
		case "repeat":
			return &memberSig{[]slot{{name: "count", typ: Int}}, String}
		case "padStart", "padEnd":
			return &memberSig{[]slot{{name: "width", typ: Int}, {name: "with", typ: String, optional: true}}, String}
		case "replace":
			return &memberSig{[]slot{{name: "old", typ: String}, {name: "with", typ: String}}, String}
		case "trim", "trimStart", "trimEnd":
			return &memberSig{nil, String}
		case "startsWith", "endsWith":
			return &memberSig{[]slot{{name: "text", typ: String}}, Bool}
		case "indexOf", "lastIndexOf":
			return &memberSig{[]slot{{name: "text", typ: String}}, &Optional{Elem: Int}}
		case "substring":
			return &memberSig{[]slot{{name: "from", typ: Int}, {name: "to", typ: Int}}, String}
		case "chars", "lines":
			return &memberSig{nil, &List{Elem: String}}
		}
	}
	return nil
}

func builtinMemberNames(recv Type) []string {
	if _, ok := recv.(*List); ok {
		return []string{"len", "first", "last", "isEmpty", "push", "pop", "insert", "remove", "removeWhere", "clear",
			"contains", "indexOf", "find", "any", "all", "filter", "map", "flatMap", "reduce",
			"sort", "sorted", "reversed", "min", "max", "sum", "join", "slice", "copy", "cast"}
	}
	if _, ok := recv.(*Map); ok {
		return []string{"len", "isEmpty", "keys", "values", "entries", "contains", "remove", "copy",
			"getOrPut", "update", "filter", "mapValues"}
	}
	if _, ok := recv.(*Set); ok {
		return setMemberNames
	}
	if _, ok := recv.(*Channel); ok {
		return []string{"send", "receive", "receiveSync", "close"}
	}
	if _, ok := recv.(*Future); ok {
		return []string{"cancel", "isCancelled"}
	}
	if s, ok := recv.(*Stream); ok && s.Controller {
		return []string{"add", "close", "stream", "isClosed"}
	}
	if recv == Float {
		return []string{"isNaN", "isFinite", "toFixed"}
	}
	if recv == Range {
		return []string{"start", "end", "len", "isEmpty", "contains", "step", "reversed", "toList"}
	}
	if recv == String {
		return []string{"len", "isEmpty", "upper", "lower", "capitalize", "contains", "count", "split", "replace",
			"trim", "trimStart", "trimEnd", "startsWith", "endsWith", "indexOf", "lastIndexOf", "substring",
			"chars", "lines", "repeat", "padStart", "padEnd", "reversed"}
	}
	return nil
}

// Checks `int.max` and the other static members of the built-in type [t].
// `int.parse` without a call is a tear-off.
func (c *checker) basicStatic(e *ast.MemberExpr, t Type, writing bool, want Type) Type {
	name := e.Name.Name
	result, ok := basicStatics[t][name]
	switch {
	case !ok:
		c.errorAt(e.Name, didYouMean(name, basicStaticNames(t)), "%s has no static member '%s'", t, name)
		return Invalid
	case writing:
		c.errorAt(e.Name, "", "%s.%s can't be changed", t, name)
		return Invalid
	case result == nil:
		sig := &tearSig{names: []string{"text"}, types: []Type{String}, result: builtinSlots[t.String()+".parse"].result}
		return c.tearOff(e, sig, nil, func(x ast.Expr) ast.Expr { return memberAccess(x, name, e.Name.Span) }, want)
	}
	c.info.Members[e] = &Member{Builtin: t.String() + "." + name}
	return result
}
