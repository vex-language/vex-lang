package types

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// Returns the type `Self` names in the requirements of [i].
func (i *Interface) SelfType() *TypeParam {
	if i.selfType == nil {
		i.selfType = &TypeParam{Name: "Self", Self: i}
	}
	return i.selfType
}

// Reports whether [m] has a slot in its interface's method table. Static,
// generic and Self requirements are only reached through generic code.
func (m *Func) InTable() bool {
	return !m.Static && m.gen == nil && !usesSelf(m.Decl)
}

// Returns a copy of [env] in which `Self` names [t].
func withSelf(env map[string]Type, t Type) map[string]Type {
	out := map[string]Type{}
	for n, v := range env {
		out[n] = v
	}
	out["Self"] = t
	return out
}

// Returns the type `Self` names in the signature of [f], or nil.
func selfOf(f *Func) Type {
	switch {
	case f.Owner != nil:
		return f.Owner
	case f.Enum != nil:
		return f.Enum
	case f.Iface != nil && f.Ext == nil:
		return f.Iface.SelfType()
	}
	return nil
}

// Reports whether the signature of [fd] names `Self`.
func usesSelf(fd *ast.FuncDecl) bool {
	found := false
	check := func(te ast.TypeExpr) {
		if te == nil {
			return
		}
		ast.Inspect(te, func(n ast.Node) bool {
			if nt, ok := n.(*ast.NamedType); ok && nt.Name == "Self" && nt.Module == "" {
				found = true
			}
			return !found
		})
	}
	for _, p := range fd.Params {
		check(p.Type)
	}
	check(fd.Result)
	return found
}

// Reports whether [d] declares a static, generic or Self requirement.
func declConstraintOnly(d *ast.InterfaceDecl) bool {
	for _, md := range d.Methods {
		if md.Static || len(md.TypeParams) > 0 || usesSelf(md) {
			return true
		}
	}
	return false
}

// Reports whether [i] can only be used as a constraint.
func (c *checker) constraintOnly(i *Interface) bool {
	c.ifaceMembers(i)
	return i.constraint
}

// Names the kind of requirement that makes [i] constraint-only.
func constraintReason(i *Interface) string {
	for _, j := range i.All() {
		for _, md := range j.Decl.Methods {
			switch {
			case md.Static:
				return fmt.Sprintf("a static requirement ('%s')", md.Name.Name)
			case len(md.TypeParams) > 0:
				return fmt.Sprintf("a generic method ('%s')", md.Name.Name)
			case usesSelf(md):
				return fmt.Sprintf("a requirement that uses Self ('%s')", md.Name.Name)
			}
		}
	}
	return "requirements only generic code can reach"
}

// Reports requirements that can't have a default: static, generic and
// Self ones, whose defaults would need a copy per type. Returns false for
// those.
func (c *checker) requirementOK(m *Func) bool {
	if !hasBody(m.Decl) || m.InTable() {
		return true
	}
	what := "a static requirement"
	switch {
	case m.gen != nil:
		what = "a generic requirement"
	case !m.Static:
		what = "a requirement that uses Self"
	}
	c.errorAt(m.Decl.Name, "leave out the body: each type writes its own", "%s can't have a default", what)
	return false
}

// Returns [im] with `Self` replaced by [t], or [im] itself when it doesn't
// use Self.
func (c *checker) forSelf(im *Func, t Type) *Func {
	if t == nil || im.Iface == nil || !usesSelf(im.Decl) {
		return im
	}
	bound := map[*TypeParam]Type{im.Iface.SelfType(): t}
	cp := *im
	cp.Params = nil
	for _, p := range im.Params {
		cp.Params = append(cp.Params, &Param{Name: p.Name, Type: c.substType(p.Type, bound), Default: p.Default})
	}
	cp.Result = c.substType(im.Result, bound)
	return &cp
}

// Reports whether the generic methods [m] and [im] match once their type
// parameters are lined up.
func (c *checker) sameGenericMethod(m, im *Func) bool {
	mp, ip := m.gen.params, im.gen.params
	if len(mp) != len(ip) {
		return false
	}
	bound := map[*TypeParam]Type{}
	for n, p := range ip {
		if strings.Join(p.Constraints, "&") != strings.Join(mp[n].Constraints, "&") {
			return false
		}
		bound[p] = mp[n]
	}
	return Identical(methodType(m), c.substType(methodType(im), bound))
}

// Describes the signature of [m], with its type parameters if any.
func methodDesc(m *Func) string {
	s := methodType(m).String()
	if m.gen != nil {
		var names []string
		for _, p := range m.gen.params {
			names = append(names, p.Name)
		}
		s = "<" + strings.Join(names, ", ") + ">" + s
	}
	return s
}
