package types

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// Where working out the types of a struct's untyped fields stands.
type inferState int

const (
	notInferred inferState = iota
	inferring
	inferred
)

// Reports whether a field of [s] still has no type.
func (s *Struct) untyped() bool {
	for _, f := range s.Fields {
		if f.Type == nil {
			return true
		}
	}
	return false
}

// Gives the fields of [s] declared without a type the type of their
// default value. Structs whose defaults create [s] are worked out first.
func (c *checker) inferFields(s *Struct) {
	switch s.inference {
	case inferred:
		return
	case inferring:
		for _, f := range s.Fields {
			if f.Type == nil {
				c.errorAt(f.Decl.Name, fmt.Sprintf("write its type: %s: T = ...", f.Name), "the type of '%s' can't be worked out, since its value depends on %s itself", f.Name, s.Name)
				f.Type = Invalid
			}
		}
		return
	}
	if !s.untyped() {
		s.inference = inferred
		return
	}
	s.inference = inferring
	back := c.enter(s.Decl)
	outer, fn, self, lits := c.scope, c.fn, c.self, c.lits
	c.scope, c.fn, c.self, c.lits = &scope{vars: map[string]*Var{}}, nil, nil, nil
	for _, f := range s.Fields {
		if f.Type != nil {
			continue
		}
		c.inferring = f
		t := c.expr(f.Default, nil)
		c.inferring = nil
		if f.Type == nil {
			// A cycle error may have set it already.
			f.Type, f.Inferred = t, true
		}
	}
	c.scope, c.fn, c.self, c.lits = outer, fn, self, lits
	back()
	s.inference = inferred
}

// Returns the hint for giving a type, with the example [decl] written for
// the field being worked out or for a local.
func (c *checker) typeHint(decl string) string {
	if f := c.inferring; f != nil {
		_, rest, _ := strings.Cut(decl, ":")
		return fmt.Sprintf("give the field a type, e.g. %s:%s", f.Name, rest)
	}
	return "give the type, e.g. let " + decl
}

// Sets up the `init` block of [s] and marks the `let` fields it sets.
func (c *checker) initBlock(s *Struct) {
	d := s.Decl.Init
	if d == nil {
		return
	}
	m := &Func{Name: "init", Decl: d, Owner: s}
	s.Init = m
	if !s.abstract {
		c.info.FuncOf[d] = m
		c.info.Funcs = append(c.info.Funcs, m)
	}
	c.signature(m)
	for _, f := range s.Fields {
		if f.Let && f.Default == nil {
			for _, st := range d.Body.Stmts {
				if fieldAssigned(st, f.Name) {
					f.ByInit = true
				}
			}
		}
	}
}

// Reports whether [s] is `name = ...` or `self.name = ...`.
func fieldAssigned(s ast.Stmt, name string) bool {
	a, ok := s.(*ast.AssignStmt)
	if !ok || a.Op.String() != "'='" {
		return false
	}
	switch t := a.Target.(type) {
	case *ast.Ident:
		return t.Name == name && t.Module == ""
	case *ast.MemberExpr:
		self, ok := t.X.(*ast.Ident)
		return ok && self.Name == "self" && t.Name.Name == name && !t.Optional
	}
	return false
}

// Checks that `init` sets the let fields it owns before anything reads
// them: before then it can't read them, use `self` or call a method.
func (c *checker) initOrder(s *Struct) {
	pending := map[*Field]bool{}
	for _, f := range s.Fields {
		if f.ByInit {
			pending[f] = true
		}
	}
	for _, st := range s.Init.Decl.Body.Stmts {
		if len(pending) == 0 {
			return
		}
		a, ok := st.(*ast.AssignStmt)
		var set *Field
		if ok {
			for f := range pending {
				if fieldAssigned(st, f.Name) {
					set = f
				}
			}
		}
		if set != nil {
			c.earlyUse(a.Value, pending)
			delete(pending, set)
			continue
		}
		c.earlyUse(st, pending)
	}
}

// Reports a use in [n] of a field in [pending], of `self`, or of a method
// called on self, which could read a field before `init` sets it.
func (c *checker) earlyUse(n ast.Node, pending map[*Field]bool) {
	names := func() string {
		var out []string
		for _, f := range c.fn.Owner.Fields {
			if pending[f] {
				out = append(out, f.Name)
			}
		}
		return quoteList(out)
	}
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			switch u := c.info.Uses[n].(type) {
			case *Field:
				if pending[u] {
					c.errorAt(n, "set it first in init", "'%s' is read before init sets it", u.Name)
				}
			case *Var:
				if u.Kind == SelfVar {
					c.errorAt(n, "set "+names()+" first", "self can't be used in init before it sets every let field")
					return false
				}
			case *Func:
				if u.Owner != nil && !u.Static {
					c.errorAt(n, "set "+names()+" first", "init can't call method '%s' before it sets every let field", u.Name)
				}
			}
		case *ast.MemberExpr:
			if m := c.info.Members[n]; m != nil && m.Field != nil && pending[m.Field] {
				c.errorAt(n.Name, "set it first in init", "'%s' is read before init sets it", m.Field.Name)
				return false
			}
		}
		return true
	})
}

// Reports whether the code being checked is an init block, outside any
// closure.
func (c *checker) inInit() bool {
	return c.fn != nil && c.fn.Owner != nil && c.fn == c.fn.Owner.Init && len(c.lits) == 0
}

// Checks an assignment to field [f], named at [n]: a let field changes
// only in init, and a private(set) one only in its struct's methods.
func (c *checker) fieldWrite(n ast.Node, f *Field) {
	switch {
	case f.Let:
		if c.inInit() && c.fn.Owner == f.Owner {
			return
		}
		c.errorAt(n, fmt.Sprintf("set it when creating the %s, or make a changed copy with copy(%s: ...)", f.Owner, f.Name),
			"can't change '%s': it's a let field of %s", f.Name, f.Owner)
	case f.PrivateSet:
		if c.fn != nil && sameStruct(c.fn.Owner, f.Owner) && (c.fn.Ext == nil || c.fn.Ext == f.Owner.Module) {
			return
		}
		c.errorAt(n, fmt.Sprintf("call a method of %s that changes it", f.Owner),
			"can't set '%s' outside %s: it's private(set)", f.Name, f.Owner)
	}
}

// Reports whether [a] and [b] are the same struct, or instances of the
// same generic struct.
func sameStruct(a, b *Struct) bool {
	if a == nil || b == nil {
		return false
	}
	return a == b || a.generic != nil && a.generic == b.generic
}

// Checks `x.copy(field: value, ...)`, which makes a new [s] from the
// fields of x, with the given fields changed.
func (c *checker) copyCall(e *ast.CallExpr, m *ast.MemberExpr, s *Struct) Type {
	c.inferFields(s)
	var slots []slot
	keep := map[ast.Expr]bool{}
	for _, f := range s.Fields {
		if f.ByInit {
			continue
		}
		mark := &ast.Ident{Span: e.Span, Name: f.Name}
		keep[mark] = true
		slots = append(slots, slot{name: f.Name, typ: f.Type, def: mark})
	}
	if s.Module != nil && s.Module != c.mod {
		for _, a := range e.Args {
			if a.Name == nil {
				continue
			}
			if f := s.Field(a.Name.Name); f != nil && f.Decl != nil && !f.Decl.Pub {
				c.errorAt(a.Name, fmt.Sprintf("mark it pub in %s to set it from other modules", s.Module), "field '%s' of %s is private to %s", f.Name, s, s.Module)
			}
		}
	}
	call := &Call{Kind: CopyCall, Struct: s, Recv: m.X, Args: c.bind(e, slots, s.String()+".copy")}
	for _, a := range call.Args {
		call.Keep = append(call.Keep, keep[a])
	}
	c.info.Calls[e] = call
	return s
}
