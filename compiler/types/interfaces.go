package types

import (
	"fmt"

	"github.com/irshadio/vex-lang/compiler/ast"
)

func (c *checker) declareInterface(d *ast.InterfaceDecl) {
	if c.reserved(d.Name, "an interface") || c.taken(d.Name) {
		return
	}
	i := &Interface{Name: d.Name.Name, Module: c.mod, Decl: d, Index: len(c.info.Interfaces)}
	c.interfaces[i.Name] = i
	c.info.Interfaces = append(c.info.Interfaces, i)
}

// Resolves the method signatures of [i] the first time it is needed.
func (c *checker) ifaceMembers(i *Interface) {
	defer c.enter(i.Decl)()
	if i.resolved {
		return
	}
	i.resolved = true
	c.withEnv(nil, func() {
		for _, md := range i.Decl.Methods {
			m := &Func{Name: md.Name.Name, Decl: md, Iface: i}
			if c.reserved(md.Name, "a method") {
				continue
			}
			if prev := i.Method(m.Name); prev != nil {
				c.errorAt(md.Name, "", "method '%s' is already declared on line %d", m.Name, prev.Decl.Pos().Line)
				continue
			}
			i.Methods = append(i.Methods, m)
			c.signature(m)
			if hasBody(md) {
				c.info.FuncOf[md] = m
				c.info.Funcs = append(c.info.Funcs, m)
			}
		}
	})
}

func hasBody(fd *ast.FuncDecl) bool { return fd.Body != nil || fd.ExprBody != nil }

// Resolves the interfaces [s] declares and checks that it implements
// them. Defaults it doesn't override are added to its methods.
func (c *checker) conformances(s *Struct) {
	// Instances of generic structs are checked through their generic
	// form, which reports any mismatch in terms of its type parameters.
	quiet := s.generic != nil && !s.abstract
	report := func(n ast.Node, hint, format string, args ...any) {
		if !quiet {
			c.errorAt(n, hint, format, args...)
		}
	}
	for _, id := range s.Decl.Interfaces {
		i := c.interfaces[id.Name]
		if id.Module != "" {
			i = c.qualifiedIface(id, quiet)
			if i == nil {
				continue
			}
		}
		if i == nil {
			switch {
			case c.structs[id.Name] != nil || c.genStructs[id.Name] != nil:
				report(id, "a struct can only implement interfaces", "'%s' is a struct, not an interface", id.Name)
			case c.enums[id.Name] != nil:
				report(id, "a struct can only implement interfaces", "'%s' is an enum, not an interface", id.Name)
			default:
				var names []string
				for n := range c.interfaces {
					names = append(names, n)
				}
				report(id, didYouMean(id.Name, names), "unknown interface '%s'", id.Name)
			}
			continue
		}
		dup := false
		for _, j := range s.Interfaces {
			dup = dup || j == i
		}
		if dup {
			report(id, "", "%s is listed twice", i.Name)
			continue
		}
		c.ifaceMembers(i)
		s.Interfaces = append(s.Interfaces, i)
		for _, im := range i.Methods {
			sm := s.Method(im.Name)
			switch {
			case sm == nil && hasBody(im.Decl):
				s.Methods = append(s.Methods, im)
			case sm == nil:
				report(s.Decl.Name, fmt.Sprintf("add %s", methodExample(im)), "%s doesn't implement '%s' from %s", s.Name, im.Name, i.Name)
			case sm.Iface != nil && sm != im && hasBody(sm.Decl) && hasBody(im.Decl):
				report(s.Decl.Name, fmt.Sprintf("write its own fn %s(...) to choose", im.Name), "%s gets a default '%s' from both %s and %s", s.Name, im.Name, sm.Iface.Name, i.Name)
			case sm.Static:
				report(sm.Decl.Name, "remove static", "'%s' can't be static, since it implements %s", sm.Name, i.Name)
			case !Identical(methodType(sm), methodType(im)):
				at := ast.Node(s.Decl.Name)
				if sm.Owner == s {
					at = sm.Decl.Name
				}
				report(at, "", "'%s' doesn't match %s: expected %s, found %s", sm.Name, i.Name, methodType(im), methodType(sm))
			}
		}
	}
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
		return t == i
	case *Struct:
		for _, j := range t.Interfaces {
			if j == i {
				return true
			}
		}
	case *TypeParam:
		for _, j := range t.Ifaces {
			if j == i {
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

// Returns the interface methods available on a value of type [t]: those
// of an interface, or of the interfaces a type parameter requires.
func ifaceMethod(t Type, name string) (*Interface, *Func) {
	switch t := t.(type) {
	case *Interface:
		if m := t.Method(name); m != nil {
			return t, m
		}
	case *TypeParam:
		for _, i := range t.Ifaces {
			if m := i.Method(name); m != nil {
				return i, m
			}
		}
	}
	return nil, nil
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
	add := func(i *Interface) {
		for _, m := range i.Methods {
			names = append(names, m.Name)
		}
	}
	switch t := t.(type) {
	case *Interface:
		add(t)
	case *TypeParam:
		for _, i := range t.Ifaces {
			add(i)
		}
	}
	return names
}

// Checks that [t] meets the constraints of type parameter [p], reporting
// an error at [at] for [callee] when it doesn't.
func (c *checker) meetsConstraints(p *TypeParam, t Type, at ast.Node, callee string) bool {
	for _, cst := range p.Constraints {
		if !satisfies(t, cst) {
			c.errorAt(at, constraintHint(cst), "%s needs %s to be %s, but %s isn't", callee, p.Name, cst, t)
			return false
		}
	}
	for _, i := range p.Ifaces {
		if !implements(t, i) {
			hint := ""
			if s, ok := t.(*Struct); ok {
				hint = fmt.Sprintf("declare it: struct %s: %s", s.Name, i.Name)
			}
			c.errorAt(at, hint, "%s needs %s to implement %s, but %s doesn't", callee, p.Name, i.Name, t)
			return false
		}
	}
	return true
}
