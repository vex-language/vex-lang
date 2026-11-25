package types

import (
	"fmt"

	"github.com/irshadio/vex-lang/compiler/ast"
)

// A type alias: another name for the type it is declared with.
type typeAlias struct {
	decl   *ast.TypeAliasDecl
	params []*TypeParam

	// The aliased type, with [params] left open; nil until resolved.
	target    Type
	resolving bool
}

// Registers the alias [d], whose type is resolved when first used.
func (c *checker) declareAlias(d *ast.TypeAliasDecl) {
	if c.reserved(d.Name, "a type") || c.taken(d.Name) || c.ifaceTaken(d.Name) {
		return
	}
	a := &typeAlias{decl: d}
	if len(d.TypeParams) > 0 {
		a.params = c.typeParams(d.TypeParams)
	}
	c.aliases[d.Name.Name] = a
}

// Resolves every alias, so mistakes in one are reported even when it
// isn't used.
func (c *checker) checkAliases() {
	for _, d := range c.decls {
		if d, ok := d.(*ast.TypeAliasDecl); ok {
			back := c.enter(d)
			if a := c.aliases[d.Name.Name]; a != nil && a.decl == d {
				c.aliasTarget(a)
			}
			back()
		}
	}
}

// Returns the type [a] names, with its type parameters left open.
func (c *checker) aliasTarget(a *typeAlias) Type {
	if a.target != nil {
		return a.target
	}
	if a.resolving {
		return nil
	}
	defer c.enter(a.decl)()
	a.resolving = true
	env := map[string]Type{}
	for _, p := range a.params {
		env[p.Name] = p
	}
	saved := c.env
	c.env = env
	a.target = c.resolveType(a.decl.Type)
	c.env = saved
	a.resolving = false
	return a.target
}

// Returns the type that alias [a], written as [te], stands for.
func (c *checker) aliasType(te *ast.NamedType, a *typeAlias) Type {
	name := te.Name
	if te.Module != "" {
		name = te.Module + "." + name
	}
	if len(a.params) == 0 && len(te.Args) > 0 {
		c.errorAt(te, "", "'%s' is not generic, so it takes no type arguments", name)
		return Invalid
	}
	if len(a.params) > 0 && len(te.Args) == 0 {
		c.errorAt(te, fmt.Sprintf("write its type arguments, e.g. %s<%s>", name, typeParamExample(a.params)), "%s is generic, so it needs type arguments", name)
		return Invalid
	}
	args := c.typeArgs(te)
	t := c.aliasTarget(a)
	if t == nil {
		c.errorAt(te, "use a struct or enum for a type that contains itself", "typedef %s refers to itself", a.decl.Name.Name)
		return Invalid
	}
	if len(a.params) == 0 || t == Invalid {
		return t
	}
	if len(args) != len(a.params) {
		c.errorAt(te, "", "%s takes %s, found %d", name, plural(len(a.params), "type argument"), len(args))
		return Invalid
	}
	bound := bindOf(a.params, args)
	for i, p := range a.params {
		if args[i] == Invalid || !c.meetsConstraints(p, args[i], te.Args[i], name, bound) {
			return Invalid
		}
	}
	return c.substType(t, bound)
}
