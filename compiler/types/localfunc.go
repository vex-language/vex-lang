package types

import (
	"github.com/vex-language/vex-lang/compiler/ast"
)

// Checks a function declared inside another. Its name is declared before
// its body is checked, so the body can call it.
func (c *checker) localFunc(s *ast.LocalFuncStmt) {
	d := s.Decl
	if len(d.TypeParams) > 0 {
		c.errorAt(d.TypeParams[0], "declare it at the top level", "local functions can't have type parameters yet")
		return
	}
	ft := &FuncType{Async: d.Async, Result: Void}
	for _, p := range d.Params {
		if p.Default != nil {
			c.errorAt(p.Default, "remove the default, or declare the function at the top level", "parameters of local functions can't have default values")
		}
		ft.Params = append(ft.Params, c.resolveType(p.Type))
	}
	if d.Result != nil {
		ft.Result = c.resolveType(d.Result)
	}
	if d.Throws != nil {
		ft.Throws = c.throwsType(d.Throws)
	}
	if c.pending[d.Name.Name] > 0 {
		c.pending[d.Name.Name]--
		defer func() { c.pending[d.Name.Name]++ }()
	}
	c.declare(d.Name, ft, false, FuncVar)
	if d.Body == nil && d.ExprBody == nil {
		c.errorAt(d.Name, "add a body: fn "+d.Name.Name+"() { ... }", "local function '%s' has no body", d.Name.Name)
		return
	}
	if c.localNames == nil {
		c.localNames = map[*ast.FuncLit]string{}
	}
	c.localNames[s.Lit] = d.Name.Name
	c.expr(s.Lit, ft)
}
