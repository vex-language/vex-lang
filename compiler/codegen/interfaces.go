package codegen

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/types"
)

// Interface values are plain pointers to structs. A struct that implements
// interfaces starts with a pointer to its descriptor, which holds its print
// function and, for each interface, a table of its methods in the
// interface's order. Calling an interface method is two loads and a call.

func ifaceName(i *types.Interface) string {
	id := modTag(i.Module) + i.Name
	return fmt.Sprintf("I%d%s", len(id), id)
}

// Returns the type of method [m] without its receiver.
func methodFnType(m *types.Func) *types.FuncType {
	ft := &types.FuncType{Result: m.Result, Throws: m.Throws}
	for _, p := range m.Params {
		ft.Params = append(ft.Params, p.Type)
	}
	return ft
}

// Writes the descriptor type, sized for the program's interfaces.
func (g *gen) descType() {
	n := max(1, len(g.info.Interfaces))
	g.line("typedef struct { vx_str (*repr)(void *); void *const *itabs[%d]; } vx_desc;", n)
}

// Writes the method tables and descriptor of [s]. Each table entry is a
// thunk that takes the receiver as `void *`, so every call through a table
// uses the entry's real type.
func (g *gen) descriptor(s *types.Struct) {
	name := structIdent(s)
	g.line("")
	g.line("static vx_str reprv_%s(void *v) { return %s; }", name, repr(s, "v"))
	var tables []string
	for _, i := range s.Interfaces {
		var entries []string
		for slot, im := range i.Methods {
			sm := s.Method(im.Name)
			if sm.Owner != s {
				entries = append(entries, "(void *)"+funcName(sm))
				continue
			}
			thunk := fmt.Sprintf("th_%s_%s_%d", name, ifaceName(i), slot)
			params, args := []string{"void *self"}, []string{"self"}
			for n, p := range sm.Params {
				params = append(params, decl(p.Type, fmt.Sprintf("a%d", n)))
				args = append(args, fmt.Sprintf("a%d", n))
			}
			if sm.Throws != nil {
				params = append(params, "void **vx_err")
				args = append(args, "vx_err")
			}
			call := funcName(sm) + "(" + strings.Join(args, ", ") + ")"
			if sm.Result != types.Void {
				call = "return " + call
			}
			g.line("static %s { %s; }", decl(sm.Result, thunk+"("+strings.Join(params, ", ")+")"), call)
			entries = append(entries, "(void *)"+thunk)
		}
		if len(entries) == 0 {
			entries = []string{"NULL"}
		}
		table := fmt.Sprintf("itab_%s_%s", name, ifaceName(i))
		g.line("static void *const %s[] = { %s };", table, strings.Join(entries, ", "))
		tables = append(tables, fmt.Sprintf("[%d] = %s", i.Index, table))
	}
	g.line("static const vx_desc desc_%s = { reprv_%s, { %s } };", name, name, strings.Join(tables, ", "))
}

// Generates a call of an interface method through the receiver's table.
func (g *gen) ifaceCall(e *ast.CallExpr, c *types.Call, rank []int) string {
	i := c.Func.Iface
	fp := fnPtrType(methodFnType(c.Func))
	dispatch := func(recv string, cs []string) string {
		o := g.temp()
		return fmt.Sprintf("({ void *%s = %s; ((%s)((*(const vx_desc **)%s)->itabs[%d][%d]))(%s); })",
			o, recv, fp, o, i.Index, i.Slot(c.Func), g.args(e, append([]string{o}, cs...)))
	}
	if c.Recv == nil {
		return g.orderedBy(c.Args, rank, func(cs []string) string { return dispatch(g.selfRef(), cs) })
	}
	return g.orderedBy(append([]ast.Expr{c.Recv}, c.Args...), append([]int{-1}, rank...), func(cs []string) string {
		return dispatch(cs[0], cs[1:])
	})
}
