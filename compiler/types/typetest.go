package types

import (
	"fmt"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/lexer"
)

// Checks `x is T` or `x as? T`.
func (c *checker) typeTest(e *ast.TypeTest) Type {
	xt := c.expr(e.X, nil)
	op := "is"
	if e.Cast {
		op = "as?"
	}
	target := c.testTarget(e.Type, xt, op, e.X)
	if target != nil {
		c.info.TypeTests[e] = target
	}
	if e.Cast {
		if target == nil {
			return Invalid
		}
		return &Optional{Elem: target}
	}
	return Bool
}

// Resolves [te], the type a value of type [subject] is tested for, and
// returns it, or nil after reporting why it can't be tested. [what] names
// the test in messages and [at] is the tested value.
func (c *checker) testTarget(te ast.TypeExpr, subject Type, what string, at ast.Node) Type {
	target := c.resolveType(te)
	if target == Invalid || subject == Invalid {
		return nil
	}
	iface, ok := subject.(*Interface)
	if !ok {
		hint := "it is always " + article(subject)
		if o, opt := subject.(*Optional); opt {
			if _, ok := o.Elem.(*Interface); ok {
				hint = "unwrap it first, e.g. with if let"
			}
		}
		c.errorAt(at, hint, "'%s' tests an interface value, but this is %s", what, subject)
		return nil
	}
	switch target.(type) {
	case *Struct, *Enum:
		if !implements(target, iface) {
			c.errorAt(te, "", "%s doesn't implement %s, so a %s value is never one", target, iface, iface)
			return nil
		}
	case *Interface:
		if implements(iface, target.(*Interface)) {
			c.errorAt(te, "", "every %s is a %s, so this always holds", iface, target)
			return nil
		}
	default:
		c.errorAt(te, "", "'%s' tests for a struct, enum or interface, found %s", what, target)
		return nil
	}
	return target
}

// A name that a condition shows to have a narrower type.
type narrowing struct {
	v *Var
	t Type
}

// Returns the names [cond] narrows when it holds: each `name is T` it
// requires, directly or through `&&`. Only names that can't change are
// narrowed.
func (c *checker) narrowings(cond ast.Expr) []narrowing {
	switch e := cond.(type) {
	case *ast.BinaryExpr:
		if e.Op == lexer.AndAnd {
			return append(c.narrowings(e.X), c.narrowings(e.Y)...)
		}
	case *ast.TypeTest:
		id, ok := e.X.(*ast.Ident)
		target := c.info.TypeTests[e]
		if !ok || e.Cast || target == nil {
			return nil
		}
		if v, ok := c.info.Uses[id].(*Var); ok {
			return []narrowing{{v, target}}
		}
	}
	return nil
}

// Opens a scope in which the names [cond] narrows have their narrower
// types; the caller pops it.
func (c *checker) pushNarrowed(cond ast.Expr) {
	ns := c.narrowings(cond)
	c.push()
	for _, n := range ns {
		if n.v.Mutable {
			if c.scope.tested == nil {
				c.scope.tested = map[string]Type{}
			}
			c.scope.tested[n.v.Name] = n.t
			continue
		}
		root := n.v
		if root.Narrows != nil {
			root = root.Narrows
		}
		c.scope.vars[root.Name] = &Var{Name: root.Name, Type: n.t, Kind: root.Kind, Decl: root.Decl, Narrows: root, lit: root.lit}
	}
}

// Returns a hint for a member missing from the interface value [x] when
// [x] is a var that a type test couldn't narrow, or [fallback].
func (c *checker) narrowHint(x ast.Expr, fallback string) string {
	id, ok := x.(*ast.Ident)
	if !ok {
		return fallback
	}
	v, ok := c.info.Uses[id].(*Var)
	if !ok || !v.Mutable {
		return fallback
	}
	for s := c.scope; s != nil; s = s.parent {
		if t, ok := s.tested[v.Name]; ok {
			return fmt.Sprintf("'%s' is a var, so 'is' can't narrow it; write if let x = %s as? %s", v.Name, v.Name, t)
		}
	}
	return fallback
}

// Checks a typed pattern `t: Text` against a value of type [t].
func (c *checker) typedPattern(p *ast.TypedPattern, t Type, b *binder) {
	target := c.testTarget(p.Type, t, "a typed pattern", p)
	if target == nil {
		c.bindInvalid(p, b)
		return
	}
	c.info.PatternTests[p] = target
	c.bindName(b, p.Name, target)
}
