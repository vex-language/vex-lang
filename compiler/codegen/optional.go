package codegen

import (
	"fmt"
	"sort"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/lexer"
	"github.com/vex-language/vex-lang/compiler/types"
)

// Reports whether optionals of [o] are a pointer that is NULL for nil,
// rather than a struct with a flag. Struct and enum values are pointers
// that are never NULL.
func nullable(o *types.Optional) bool {
	switch o.Elem.(type) {
	case *types.Struct, *types.Enum:
		return true
	}
	return false
}

// Returns C code testing whether the optional [x] of type [o] holds a value.
func present(o *types.Optional, x string) string {
	if nullable(o) {
		return "((" + x + ") != NULL)"
	}
	return "((" + x + ").has)"
}

// Returns C code giving the value inside the non-nil optional [x].
func unwrap(o *types.Optional, x string) string {
	if nullable(o) {
		return x
	}
	return "(" + x + ").v"
}

// Returns C code giving [x] wrapped in an optional of type [o].
func wrapOpt(o *types.Optional, x string) string {
	if nullable(o) {
		return "(" + x + ")"
	}
	return fmt.Sprintf("((%s){true, %s})", ctype(o), x)
}

// Returns the optional type of [e], which the checker gave it.
func (g *gen) optOf(e ast.Expr) *types.Optional {
	return g.info.Types[e].(*types.Optional)
}

// Finds every optional type the program uses, so each gets a C struct.
func (g *gen) collectOptionals() {
	seen := map[types.Type]bool{}
	var visit func(t types.Type)
	visit = func(t types.Type) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		switch t := t.(type) {
		case *types.Optional:
			if !nullable(t) {
				g.opts[ctype(t)] = t
			}
			visit(t.Elem)
		case *types.List:
			visit(t.Elem)
		case *types.Future:
			visit(t.Elem)
		case *types.Record:
			g.recs[ctype(t)] = t
			for _, f := range t.FieldTypes() {
				visit(f)
			}
		case *types.Map:
			visit(t.Key)
			visit(t.Value)
		case *types.FuncType:
			for _, p := range t.Params {
				visit(p)
			}
			visit(t.Result)
		case *types.Struct:
			for _, f := range t.Fields {
				visit(f.Type)
			}
		case *types.Enum:
			for _, cs := range t.Cases {
				for _, f := range cs.Fields {
					visit(f.Type)
				}
			}
		}
	}
	for _, t := range g.info.Types {
		visit(t)
	}
	for _, o := range g.info.Wraps {
		visit(o)
	}
	for _, v := range g.info.Defs {
		visit(v.Type)
	}
	for _, f := range g.info.Funcs {
		for _, p := range f.Params {
			visit(p.Type)
		}
		visit(f.Result)
	}
	for _, s := range g.info.Structs {
		visit(s)
	}
	for _, e := range g.info.Enums {
		visit(e)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Writes `if let`: the bound name exists only inside the first block.
func (g *gen) ifLet(s *ast.IfStmt) {
	tmp := g.temp()
	g.line("{")
	g.ind++
	o := g.optOf(s.Cond)
	g.line("%s = %s;", decl(o, tmp), g.expr(s.Cond))
	g.line("if (%s) {", present(o, tmp))
	v := g.info.Defs[s.Bind]
	g.ind++
	g.line("%s = %s;", decl(v.Type, g.declare(v)), unwrap(o, tmp))
	g.ind--
	g.body(s.Then)
	switch e := s.Else.(type) {
	case *ast.IfStmt:
		g.line("} else {")
		g.ind++
		g.ifStmt(e)
		g.ind--
	case *ast.Block:
		g.line("} else {")
		g.body(e)
	}
	g.line("}")
	g.ind--
	g.line("}")
}

func (g *gen) nilTest(k lexer.Kind, x ast.Expr) string {
	if k == lexer.Eq {
		return "(!" + present(g.optOf(x), g.expr(x)) + ")"
	}
	return present(g.optOf(x), g.expr(x))
}

// Returns `x ?? fallback`; the fallback runs only when [x] is nil.
func (g *gen) coalesce(e *ast.BinaryExpr) string {
	tmp := g.temp()
	o := g.optOf(e.X)
	x := fmt.Sprintf("%s = %s;", decl(o, tmp), g.expr(e.X))
	value := unwrap(o, tmp)
	if _, ok := g.info.Types[e].(*types.Optional); ok {
		value = tmp
	}
	return fmt.Sprintf("({ %s %s ? %s : %s; })", x, present(o, tmp), value, g.expr(e.Y))
}

// Returns an optional chain such as `x?.name` or `x?.greet()`: nil when the
// receiver is nil, otherwise the member, wrapped in an optional if [wrap].
func (g *gen) chain(e ast.Expr, wrap bool) string {
	var x ast.Expr
	switch e := e.(type) {
	case *ast.MemberExpr:
		x = e.X
	case *ast.CallExpr:
		x = e.Fn.(*ast.MemberExpr).X
	}
	opt := g.info.Types[x].(*types.Optional)
	tmp := g.temp()
	recv := g.expr(x)

	// Generate the member as if the receiver were the value inside.
	g.recvs[x], g.chaining[e], g.info.Types[x] = unwrap(opt, tmp), true, opt.Elem
	inner := g.expr(e)
	delete(g.recvs, x)
	delete(g.chaining, e)
	g.info.Types[x] = opt

	t := g.info.Types[e]
	head := fmt.Sprintf("%s = %s;", decl(opt, tmp), recv)
	if t == types.Void {
		return fmt.Sprintf("({ %s if %s %s; })", head, present(opt, tmp), inner)
	}
	if wrap {
		inner = wrapOpt(t.(*types.Optional), inner)
	}
	return fmt.Sprintf("({ %s %s ? %s : (%s){0}; })", head, present(opt, tmp), inner, ctype(t))
}

// Returns the type of the C code [g.expr] produces for [e], which is an
// optional when the value is wrapped in one.
func (g *gen) typeOf(e ast.Expr) types.Type {
	if o := g.info.Wraps[e]; o != nil {
		return o
	}
	return g.info.Types[e]
}

// Generates `while let name = value { ... }`, which stops at nil.
func (g *gen) whileLet(s *ast.WhileStmt) {
	tmp := g.temp()
	g.line("for (;;) {")
	g.ind++
	o := g.optOf(s.Cond)
	g.line("%s = %s;", decl(o, tmp), g.expr(s.Cond))
	g.line("if (!%s) break;", present(o, tmp))
	v := g.info.Defs[s.Bind]
	g.line("%s = %s;", decl(v.Type, g.declare(v)), unwrap(o, tmp))
	g.ind--
	g.body(s.Body)
	g.line("}")
}
