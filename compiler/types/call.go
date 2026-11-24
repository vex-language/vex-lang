package types

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
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
	switch fn := e.Fn.(type) {
	case *ast.Ident:
		return c.callName(e, fn)
	case *ast.MemberExpr:
		return c.callMember(e, fn)
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

// Checks the arguments of a call that has already failed, so mistakes
// inside them are still reported.
func (c *checker) checkArgs(e *ast.CallExpr) {
	for _, a := range e.Args {
		c.expr(a.Value, nil)
	}
}

// Checks a call of a plain name: a function, constructor, built-in, or a
// method of the implicit `self`.
func (c *checker) callName(e *ast.CallExpr, id *ast.Ident) Type {
	name := id.Name
	if v := c.lookupVar(name); v != nil {
		if _, ok := v.Type.(*FuncType); ok || v.Type == Invalid {
			return c.callValue(e)
		}
		c.info.Uses[id] = v
		c.checkArgs(e)
		c.errorAt(id, "", "'%s' is %s, not a function", name, article(v.Type))
		return Invalid
	}
	if owner := c.ownStruct(); owner != nil {
		if m := owner.Method(name); m != nil {
			if !m.Static && c.self == nil {
				c.checkArgs(e)
				c.errorAt(id, "static methods have no instance to call it on", "static method %s can't call method '%s'", c.fn.Name, name)
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
		c.checkArgs(e)
		c.errorAt(id, caseHint("create it from a case, e.g. ", en, true), "enums are created from one of their cases")
		return Invalid
	}
	if builtinFuncs[name] {
		return c.callBuiltin(e, name)
	}
	if f := c.funcs[name]; f != nil {
		c.info.Calls[e] = &Call{Kind: FuncCall, Func: f, Args: c.bind(e, paramSlots(f), f.Name)}
		return f.Result
	}
	if s := c.structs[name]; s != nil {
		var slots []slot
		for _, f := range s.Fields {
			slots = append(slots, slot{name: f.Name, typ: f.Type, def: f.Default})
		}
		c.info.Calls[e] = &Call{Kind: ConstructorCall, Struct: s, Args: c.bind(e, slots, s.Name)}
		return s
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
		for _, m := range owner.Methods {
			names = append(names, m.Name)
		}
	}
	c.errorAt(id, didYouMean(name, names), "unknown function '%s'", name)
	return Invalid
}

// Checks a call of the form `x.name(...)`.
func (c *checker) callMember(e *ast.CallExpr, m *ast.MemberExpr) Type {
	name := m.Name.Name
	if en := c.enumRef(m.X); en != nil {
		if method := en.Method(name); method != nil {
			if !method.Static {
				c.checkArgs(e)
				c.errorAt(m.Name, fmt.Sprintf("call it on a value, e.g. shape.%s(), or declare it static fn", name), "'%s' needs a %s value", name, en.Name)
				return Invalid
			}
			c.info.Calls[e] = &Call{Kind: StaticCall, Func: method, Args: c.bind(e, paramSlots(method), en.Name+"."+name)}
			return method.Result
		}
		return c.caseCall(e, en, m.Name, en.Name+"."+name)
	}
	if s := c.structRef(m.X); s != nil {
		method := s.Method(name)
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
			c.checkArgs(e)
			if s.Field(name) != nil {
				c.errorAt(m.Name, "", "'%s' is a field, not a method", name)
			} else {
				c.errorAt(m.Name, didYouMean(name, memberNames(s)), "%s has no method '%s'", s.Name, name)
			}
			return Invalid
		case method.Static:
			c.checkArgs(e)
			c.errorAt(m.Name, fmt.Sprintf("call it on the type: %s.%s()", s.Name, name), "'%s' is a static method", name)
			return Invalid
		}
		c.info.Calls[e] = &Call{Kind: MethodCall, Func: method, Recv: m.X, Args: c.bind(e, paramSlots(method), name)}
		return method.Result
	}
	if en, ok := recv.(*Enum); ok {
		method := en.Method(name)
		switch {
		case method == nil:
			c.checkArgs(e)
			c.errorAt(m.Name, didYouMean(name, enumNames(en)), "%s has no method '%s'", en.Name, name)
			return Invalid
		case method.Static:
			c.checkArgs(e)
			c.errorAt(m.Name, fmt.Sprintf("call it on the type: %s.%s()", en.Name, name), "'%s' is a static method", name)
			return Invalid
		}
		c.info.Calls[e] = &Call{Kind: MethodCall, Func: method, Recv: m.X, Args: c.bind(e, paramSlots(method), name)}
		return method.Result
	}
	if recv == Invalid {
		c.checkArgs(e)
		return Invalid
	}
	if l, ok := recv.(*List); ok && name == "map" {
		return c.mapCall(e, m, l)
	}
	sig := builtinMember(recv, name)
	if sig == nil {
		c.checkArgs(e)
		if name == "len" {
			c.errorAt(m.Name, "write it without (): .len", "len is a property, not a method")
		} else {
			c.errorAt(m.Name, didYouMean(name, builtinMemberNames(recv)), "%s has no method '%s'", recv, name)
		}
		return Invalid
	}
	if name == "contains" {
		if l, ok := recv.(*List); ok && !comparable(l.Elem) && l.Elem != Invalid {
			c.checkArgs(e)
			c.errorAt(m.Name, "", "contains can't be used on a list of %s", l.Elem)
			return Invalid
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
			lit := &ast.FuncLit{Span: e.Trailing.Span, Body: e.Trailing}
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
	if set[i] {
		c.errorAt(e.Trailing, "", "the last argument of %s is given twice: in the call and as a trailing block", callee)
		return -1
	}
	if ft, ok := slots[i].typ.(*FuncType); !ok || len(ft.Params) > 0 {
		c.errorAt(e.Trailing, "", "a trailing block needs the last parameter of %s to be fn(), but '%s' is %s", callee, slots[i].name, slots[i].typ)
		return -1
	}
	return i
}

// Returns [t] with "a" or "an" before it: "an int", "a string".
func article(t Type) string {
	name := t.String()
	if strings.ContainsRune("aeiou", rune(name[0])) {
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
}

func (c *checker) callBuiltin(e *ast.CallExpr, name string) Type {
	call := &Call{Kind: BuiltinCall, Builtin: name}
	c.info.Calls[e] = call
	if e.Trailing != nil {
		c.errorAt(e.Trailing, "", "%s doesn't take a trailing block", name)
	}
	if b, ok := builtinSlots[name]; ok {
		call.Args = c.bind(e, b.params, name)
		return b.result
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
	var arg ast.Expr
	switch {
	case len(e.Args) == 1 && e.Args[0].Name == nil && e.Trailing == nil:
		arg = e.Args[0].Value
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
		}
		return nil
	}
	if recv == String {
		switch name {
		case "upper", "lower":
			return &memberSig{nil, String}
		case "contains":
			return &memberSig{[]slot{{name: "text", typ: String}}, Bool}
		case "split":
			return &memberSig{[]slot{{name: "separator", typ: String}}, &List{Elem: String}}
		}
	}
	return nil
}

func builtinMemberNames(recv Type) []string {
	if _, ok := recv.(*List); ok {
		return []string{"len", "push", "pop", "contains", "filter", "map"}
	}
	if recv == String {
		return []string{"len", "upper", "lower", "contains", "split"}
	}
	return nil
}
