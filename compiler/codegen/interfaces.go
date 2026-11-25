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
	if i.Args != nil {
		id = modTag(i.Module) + i.Base + "__" + mangleList(i.Args)
	}
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

// A struct or enum that implements interfaces, as its descriptor needs it.
type implementer struct {
	// The C name of its descriptor's suffix, and its C type.
	ident, ctype string
	t            types.Type
	ifaces       []*types.Interface
	methods      []*types.Func

	// Returns its stored field called [name], or nil. Nil for enums.
	field func(name string) *types.Field

	// Reports whether [f] is its own, rather than an interface default.
	owns func(f *types.Func) bool
}

func (g *gen) structDescriptor(s *types.Struct) {
	g.descriptor(implementer{
		ident: structIdent(s), ctype: structName(s), t: s, ifaces: s.Interfaces, methods: s.Methods,
		field: s.Field,
		owns:  func(f *types.Func) bool { return f.Owner == s },
	})
}

func (g *gen) enumDescriptor(e *types.Enum) {
	g.descriptor(implementer{
		ident: enumName(e), ctype: enumName(e), t: e, ifaces: e.Interfaces, methods: e.Methods,
		owns: func(f *types.Func) bool { return f.Enum == e },
	})
}

// Writes the method tables and descriptor of [t]. Each table entry takes
// the receiver as `void *`, so every call through a table uses the entry's
// real type: the type's own members and its fields are reached through
// thunks, while interface defaults already take `void *`.
func (g *gen) descriptor(t implementer) {
	g.line("")
	g.line("static vx_str reprv_%s(void *v) { return %s; }", t.ident, repr(t.t, "v"))
	var tables []string
	for _, i := range t.ifaces {
		var entries []string
		for slot, im := range i.Methods {
			thunk := fmt.Sprintf("th_%s_%s_%d", t.ident, ifaceName(i), slot)
			kind := im.Decl.Accessor
			var f *types.Field
			if t.field != nil && kind != ast.Method {
				f = t.field(im.Name)
			}
			var m *types.Func
			for _, x := range t.methods {
				if x.Name == im.Name && x.Decl.Accessor == kind {
					m = x
				}
			}
			switch {
			case f != nil && kind == ast.Getter:
				g.thunk(thunk, im, func(args []string) string {
					return fmt.Sprintf("return ((%s *)self)->f_%s;", t.ctype, f.Name)
				})
			case f != nil:
				g.thunk(thunk, im, func(args []string) string {
					store := fmt.Sprintf("%s *o = self; ", t.ctype)
					if f.DidSet == nil {
						return store + fmt.Sprintf("o->f_%s = %s;", f.Name, args[1])
					}
					return store + fmt.Sprintf("%s = o->f_%s; o->f_%s = %s; %s(o, old);", decl(f.Type, "old"), f.Name, f.Name, args[1], funcName(f.DidSet))
				})
			case !t.owns(m):
				entries = append(entries, "(void *)"+funcName(m))
				continue
			default:
				g.thunk(thunk, m, func(args []string) string {
					call := funcName(m) + "(" + strings.Join(args, ", ") + ");"
					if m.Result != types.Void {
						call = "return " + call
					}
					return call
				})
			}
			entries = append(entries, "(void *)"+thunk)
		}
		if len(entries) == 0 {
			entries = []string{"NULL"}
		}
		table := fmt.Sprintf("itab_%s_%s", t.ident, ifaceName(i))
		g.line("static void *const %s[] = { %s };", table, strings.Join(entries, ", "))
		tables = append(tables, fmt.Sprintf("[%d] = %s", i.Index, table))
	}
	g.line("static const vx_desc desc_%s = { reprv_%s, { %s } };", t.ident, t.ident, strings.Join(tables, ", "))
}

// Writes a table entry called [name] with the signature of [m], taking
// the receiver as `void *self`, whose body [body] gives from the names of
// the receiver and the arguments.
func (g *gen) thunk(name string, m *types.Func, body func(args []string) string) {
	params, args := []string{"void *self"}, []string{"self"}
	for n, p := range m.Params {
		params = append(params, decl(p.Type, fmt.Sprintf("a%d", n)))
		args = append(args, fmt.Sprintf("a%d", n))
	}
	if m.Throws != nil {
		params = append(params, "void **vx_err")
		args = append(args, "vx_err")
	}
	g.line("static %s { %s }", decl(m.Result, name+"("+strings.Join(params, ", ")+")"), body(args))
}

// Returns a call of method or accessor [f] on [recv], whose static type is
// [rt]. A member of an interface is called through the receiver's table
// unless the receiver's own type is known.
func (g *gen) memberCall(f *types.Func, rt types.Type, recv string, args ...string) string {
	if f.Iface != nil {
		switch rt.(type) {
		case *types.Struct, *types.Enum:
		default:
			o := g.temp()
			return fmt.Sprintf("({ void *%s = %s; ((%s)((*(const vx_desc **)%s)->itabs[%d][%d]))(%s); })",
				o, recv, fnPtrType(methodFnType(f)), o, f.Iface.Index, f.Iface.Slot(f), strings.Join(append([]string{o}, args...), ", "))
		}
	}
	return funcName(f) + "(" + strings.Join(append([]string{recv}, args...), ", ") + ")"
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
