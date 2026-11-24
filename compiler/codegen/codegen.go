// Package codegen translates a checked Vex program into C.
package codegen

import (
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/lexer"
	"github.com/irshadio/vex-lang/compiler/types"
)

//go:embed runtime/vexrt.h
var runtime string

type gen struct {
	info *types.Info
	b    strings.Builder
	ind  int

	// C names of the variables of the function being generated.
	names map[*types.Var]string
	used  map[string]bool
	tmp   int

	// Lists and structs that need a function to show their values.
	reprs map[string]types.Type
}

// Returns the C source of the program described by [info]. [srcName] is
// the file name shown in runtime errors.
func Generate(info *types.Info, srcName string) string {
	g := &gen{info: info, reprs: map[string]types.Type{}}
	for _, t := range info.Types {
		g.needRepr(t)
	}
	for _, s := range info.Structs {
		g.needRepr(s)
	}

	g.b.WriteString(runtime)
	g.line("")
	g.line("// --- Program ---")
	g.line("")
	for _, s := range info.Structs {
		g.line("typedef struct %s %s;", structName(s), structName(s))
	}
	for _, s := range info.Structs {
		g.line("struct %s {", structName(s))
		for _, f := range s.Fields {
			g.line("    %s f_%s;", ctype(f.Type), f.Name)
		}
		g.line("};")
	}
	g.line("")
	for _, s := range info.Structs {
		g.line("static %s;", constructorSig(s))
	}
	for _, name := range g.reprNames() {
		g.line("static vx_str repr_%s(%s);", name, decl(g.reprs[name], "v"))
	}
	for _, f := range info.Funcs {
		g.line("static %s;", g.signature(f, false))
	}
	for _, s := range info.Structs {
		g.constructor(s)
	}
	for _, name := range g.reprNames() {
		g.repr(name, g.reprs[name])
	}
	for _, f := range info.Funcs {
		g.function(f)
	}
	g.line("")
	g.line("int main(void) {")
	g.line("    vx_file = %s;", cQuote(srcName))
	g.line("    fn_main();")
	g.line("    return 0;")
	g.line("}")
	return g.b.String()
}

// Writes one line at the current indentation.
func (g *gen) line(format string, args ...any) {
	if format != "" {
		g.b.WriteString(strings.Repeat("    ", g.ind))
		fmt.Fprintf(&g.b, format, args...)
	}
	g.b.WriteByte('\n')
}

// --- Names and types ---

func structName(s *types.Struct) string { return "S_" + s.Name }

// Returns the C name of a function or method. Methods include the length of
// the struct name so `A_b.c` and `A.b_c` can't collide.
func funcName(f *types.Func) string {
	if f.Owner != nil {
		return fmt.Sprintf("m%d%s_%s", len(f.Owner.Name), f.Owner.Name, f.Name)
	}
	return "fn_" + f.Name
}

func ctype(t types.Type) string {
	switch t := t.(type) {
	case *types.List:
		return "vx_list *"
	case *types.Struct:
		return structName(t) + " *"
	}
	switch t {
	case types.Int:
		return "int64_t"
	case types.Float:
		return "double"
	case types.Bool:
		return "bool"
	case types.String:
		return "vx_str"
	}
	return "void"
}

// Returns a C declaration of [name] with type [t].
func decl(t types.Type, name string) string {
	c := ctype(t)
	if strings.HasSuffix(c, "*") {
		return c + name
	}
	return c + " " + name
}

// Returns a name for [t] usable inside C identifiers, such as `L_int`.
func mangle(t types.Type) string {
	switch t := t.(type) {
	case *types.List:
		return "L_" + mangle(t.Elem)
	case *types.Struct:
		return structName(t)
	}
	if t == types.String {
		return "str"
	}
	return t.String()
}

// Gives a C name to a new variable, unique within the current function.
func (g *gen) declare(v *types.Var) string {
	base := "v_" + v.Name
	name := base
	for n := 2; g.used[name]; n++ {
		name = fmt.Sprintf("%s_%d", base, n)
	}
	g.used[name] = true
	g.names[v] = name
	return name
}

func (g *gen) varName(v *types.Var) string {
	if v.Kind == types.SelfVar {
		return "self"
	}
	return g.names[v]
}

// Returns a fresh name for a temporary value.
func (g *gen) temp() string {
	g.tmp++
	return fmt.Sprintf("_t%d", g.tmp)
}

// --- Declarations ---

func constructorSig(s *types.Struct) string {
	var params []string
	for _, f := range s.Fields {
		params = append(params, decl(f.Type, "f_"+f.Name))
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	return fmt.Sprintf("%s *new_%s(%s)", structName(s), structName(s), strings.Join(params, ", "))
}

func (g *gen) constructor(s *types.Struct) {
	g.line("")
	g.line("static %s {", constructorSig(s))
	g.line("    %s *o = vx_alloc(sizeof(%s));", structName(s), structName(s))
	for _, f := range s.Fields {
		g.line("    o->f_%s = f_%s;", f.Name, f.Name)
	}
	g.line("    return o;")
	g.line("}")
}

// Returns the C signature of [f], declaring its parameters when [define] is set.
func (g *gen) signature(f *types.Func, define bool) string {
	var params []string
	if f.Owner != nil && !f.Static {
		params = append(params, structName(f.Owner)+" *self")
	}
	for i, p := range f.Params {
		name := "v_" + p.Name
		if define {
			name = g.declare(g.info.Defs[f.Decl.Params[i].Name])
		}
		params = append(params, decl(p.Type, name))
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	return decl(f.Result, funcName(f)+"("+strings.Join(params, ", ")+")")
}

func (g *gen) function(f *types.Func) {
	g.names, g.used, g.tmp = map[*types.Var]string{}, map[string]bool{}, 0
	g.line("")
	g.line("static %s {", g.signature(f, true))
	g.ind++
	if e := f.Decl.ExprBody; e != nil {
		if f.Result == types.Void {
			g.line("%s;", g.expr(e))
		} else {
			g.line("return %s;", g.expr(e))
		}
	} else {
		for _, s := range f.Decl.Body.Stmts {
			g.stmt(s)
		}
		if f.Result != types.Void {
			g.line("__builtin_unreachable();")
		}
	}
	g.ind--
	g.line("}")
}

// --- Showing values ---

// Records that values of [t] may be shown, along with the types inside it.
func (g *gen) needRepr(t types.Type) {
	switch t := t.(type) {
	case *types.List:
		if _, ok := g.reprs[mangle(t)]; !ok {
			g.reprs[mangle(t)] = t
			g.needRepr(t.Elem)
		}
	case *types.Struct:
		if _, ok := g.reprs[mangle(t)]; !ok {
			g.reprs[mangle(t)] = t
			for _, f := range t.Fields {
				g.needRepr(f.Type)
			}
		}
	}
}

func (g *gen) reprNames() []string {
	names := make([]string, 0, len(g.reprs))
	for n := range g.reprs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Returns C code giving [x] as it appears inside a list or struct, with
// strings quoted.
func repr(t types.Type, x string) string {
	switch t.(type) {
	case *types.List, *types.Struct:
		return fmt.Sprintf("repr_%s(%s)", mangle(t), x)
	}
	switch t {
	case types.Int:
		return "vx_int_str(" + x + ")"
	case types.Float:
		return "vx_float_str(" + x + ")"
	case types.Bool:
		return "vx_bool_str(" + x + ")"
	case types.String:
		return "vx_str_quote(" + x + ")"
	}
	return "VX_LIT(\"\")"
}

// Returns C code giving [x] as printed: strings as they are, everything
// else as in [repr].
func show(t types.Type, x string) string {
	if t == types.String {
		return x
	}
	return repr(t, x)
}

func (g *gen) repr(name string, t types.Type) {
	g.line("")
	g.line("static vx_str repr_%s(%s) {", name, decl(t, "v"))
	switch t := t.(type) {
	case *types.List:
		g.line("    vx_str s = VX_LIT(\"[\");")
		g.line("    for (int64_t i = 0; i < v->len; i++) {")
		g.line("        if (i > 0) s = vx_str_concat(s, VX_LIT(\", \"));")
		g.line("        s = vx_str_concat(s, %s);", repr(t.Elem, fmt.Sprintf("((%s*)v->data)[i]", ctype(t.Elem))))
		g.line("    }")
		g.line("    return vx_str_concat(s, VX_LIT(\"]\"));")
	case *types.Struct:
		g.line("    vx_str s = VX_LIT(%s);", cQuote(t.Name+"("))
		for i, f := range t.Fields {
			sep := ""
			if i > 0 {
				sep = ", "
			}
			g.line("    s = vx_str_concat(s, VX_LIT(%s));", cQuote(sep+f.Name+": "))
			g.line("    s = vx_str_concat(s, %s);", repr(f.Type, "v->f_"+f.Name))
		}
		g.line("    return vx_str_concat(s, VX_LIT(\")\"));")
	}
	g.line("}")
}

// --- Statements ---

// Writes [header] followed by the block [b] in braces.
func (g *gen) block(header string, b *ast.Block) {
	if header == "" {
		g.line("{")
	} else {
		g.line("%s {", header)
	}
	g.body(b)
	g.line("}")
}

// Returns a condition for `if (...)`, without a redundant outer pair of
// parentheses.
func cond(c string) string {
	if !strings.HasPrefix(c, "(") || !strings.HasSuffix(c, ")") {
		return c
	}
	depth := 0
	for i, r := range c {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && i != len(c)-1 {
				return c
			}
		}
	}
	return c[1 : len(c)-1]
}

func (g *gen) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.Block:
		g.block("", s)
	case *ast.LetStmt:
		v := g.info.Defs[s.Name]
		value := g.expr(s.Value)
		g.line("%s = %s;", decl(v.Type, g.declare(v)), value)
	case *ast.AssignStmt:
		g.assign(s)
	case *ast.ExprStmt:
		g.line("%s;", g.expr(s.X))
	case *ast.IfStmt:
		g.ifStmt(s)
	case *ast.WhileStmt:
		g.block("while ("+cond(g.expr(s.Cond))+")", s.Body)
	case *ast.ForStmt:
		g.forStmt(s)
	case *ast.ReturnStmt:
		if s.Value == nil {
			g.line("return;")
		} else {
			g.line("return %s;", g.expr(s.Value))
		}
	case *ast.BreakStmt:
		g.line("break;")
	case *ast.ContinueStmt:
		g.line("continue;")
	}
}

// Writes the statements of [b] one level deeper, without braces.
func (g *gen) body(b *ast.Block) {
	g.ind++
	for _, s := range b.Stmts {
		g.stmt(s)
	}
	g.ind--
}

// Writes an if statement, with `else if` chains on the closing brace line.
func (g *gen) ifStmt(s *ast.IfStmt) {
	g.line("if (%s) {", cond(g.expr(s.Cond)))
	for {
		g.body(s.Then)
		switch e := s.Else.(type) {
		case *ast.IfStmt:
			g.line("} else if (%s) {", cond(g.expr(e.Cond)))
			s = e
			continue
		case *ast.Block:
			g.line("} else {")
			g.body(e)
		}
		g.line("}")
		return
	}
}

func (g *gen) forStmt(s *ast.ForStmt) {
	if r, ok := s.Iter.(*ast.RangeExpr); ok {
		v := g.declare(g.info.Defs[s.Elem])
		hi := g.temp()
		cmp := "<"
		if r.Inclusive {
			cmp = "<="
		}
		g.block(fmt.Sprintf("for (int64_t %s = %s, %s = %s; %s %s %s; %s++)", v, g.expr(r.Lo), hi, g.expr(r.Hi), v, cmp, hi, v), s.Body)
		return
	}
	elem := g.info.Defs[s.Elem]
	list, i := g.temp(), g.temp()
	g.line("{")
	g.ind++
	g.line("vx_list *%s = %s;", list, g.expr(s.Iter))
	g.line("for (int64_t %s = 0; %s < %s->len; %s++) {", i, i, list, i)
	g.ind++
	if s.Index != nil {
		g.line("int64_t %s = %s;", g.declare(g.info.Defs[s.Index]), i)
	}
	g.line("%s = ((%s*)%s->data)[%s];", decl(elem.Type, g.declare(elem)), ctype(elem.Type), list, i)
	g.ind--
	g.body(s.Body)
	g.line("}")
	g.ind--
	g.line("}")
}

// The binary operator applied by each compound assignment.
var compoundOps = map[lexer.Kind]lexer.Kind{
	lexer.PlusAssign:    lexer.Plus,
	lexer.MinusAssign:   lexer.Minus,
	lexer.StarAssign:    lexer.Star,
	lexer.SlashAssign:   lexer.Slash,
	lexer.PercentAssign: lexer.Percent,
}

// Generates an assignment. The target's list and index (or object) are
// evaluated first, then the value, and only then is the element located, so
// a value that grows the list can't leave a stale pointer.
func (g *gen) assign(s *ast.AssignStmt) {
	t := g.info.Types[s.Target]
	var setup []string
	var target string
	switch x := s.Target.(type) {
	case *ast.IndexExpr:
		list, idx := g.temp(), g.temp()
		setup = append(setup,
			fmt.Sprintf("vx_list *%s = %s;", list, g.expr(x.X)),
			fmt.Sprintf("int64_t %s = %s;", idx, g.expr(x.Index)))
		target = fmt.Sprintf("(*(%s*)vx_list_at(%s, %s, %d))", ctype(t), list, idx, x.Pos().Line)
	case *ast.MemberExpr:
		obj := g.temp()
		setup = append(setup, fmt.Sprintf("%s = %s;", decl(g.info.Types[x.X], obj), g.expr(x.X)))
		target = fmt.Sprintf("%s->f_%s", obj, x.Name.Name)
	default:
		target = g.expr(s.Target)
	}
	value := g.expr(s.Value)
	if setup != nil {
		v := g.temp()
		setup = append(setup, fmt.Sprintf("%s = %s;", decl(t, v), value))
		value = v
	}
	if op, ok := compoundOps[s.Op]; ok {
		value = binop(op, t, target, value, s.Pos().Line)
	}
	if setup == nil {
		g.line("%s = %s;", target, value)
		return
	}
	g.line("{")
	g.ind++
	for _, l := range setup {
		g.line("%s", l)
	}
	g.line("%s = %s;", target, value)
	g.ind--
	g.line("}")
}

// --- Expressions ---

// Reports whether evaluating [e] may call a function, so its order relative
// to other calls matters.
func hasCall(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.CallExpr:
		return true
	case *ast.UnaryExpr:
		return hasCall(e.X)
	case *ast.BinaryExpr:
		return hasCall(e.X) || hasCall(e.Y)
	case *ast.IndexExpr:
		return hasCall(e.X) || hasCall(e.Index)
	case *ast.MemberExpr:
		return hasCall(e.X)
	case *ast.ListLit:
		for _, x := range e.Elems {
			if hasCall(x) {
				return true
			}
		}
	case *ast.InterpolatedString:
		for _, x := range e.Exprs {
			if hasCall(x) {
				return true
			}
		}
	}
	return false
}

// Generates [es] so they run left to right, and passes their C code to
// [render]. C leaves the order of arguments unspecified, and a call can
// change what another argument reads, so when any of them calls a function
// they are first stored in temporaries. Nil entries stay empty.
func (g *gen) ordered(es []ast.Expr, render func(cs []string) string) string {
	return g.orderedBy(es, nil, render)
}

// Like [ordered], but stores the temporaries in the order of [rank]: es[i]
// runs before es[j] when rank[i] < rank[j]. A nil rank keeps [es] order.
func (g *gen) orderedBy(es []ast.Expr, rank []int, render func(cs []string) string) string {
	calls, n := 0, 0
	for _, e := range es {
		if e != nil {
			n++
			if hasCall(e) {
				calls++
			}
		}
	}
	cs := make([]string, len(es))
	if calls == 0 || n < 2 {
		for i, e := range es {
			if e != nil {
				cs[i] = g.expr(e)
			}
		}
		return render(cs)
	}
	var b strings.Builder
	order := make([]int, len(es))
	for i := range order {
		order[i] = i
	}
	if rank != nil {
		sort.SliceStable(order, func(a, b int) bool { return rank[order[a]] < rank[order[b]] })
	}
	b.WriteString("({ ")
	for _, i := range order {
		e := es[i]
		if e == nil {
			continue
		}
		cs[i] = g.temp()
		fmt.Fprintf(&b, "%s = %s; ", decl(g.info.Types[e], cs[i]), g.expr(e))
	}
	b.WriteString(render(cs))
	b.WriteString("; })")
	return b.String()
}

func (g *gen) expr(e ast.Expr) string {
	t := g.info.Types[e]
	switch e := e.(type) {
	case *ast.IntLit:
		v, _ := strconv.ParseInt(e.Value, 0, 64)
		if t == types.Float {
			return strconv.FormatInt(v, 10) + ".0"
		}
		if v == -1<<63 {
			return "INT64_MIN"
		}
		return "INT64_C(" + strconv.FormatInt(v, 10) + ")"
	case *ast.FloatLit:
		return e.Value
	case *ast.BoolLit:
		return strconv.FormatBool(e.Value)
	case *ast.StringLit:
		return "VX_LIT(" + cQuote(e.Value) + ")"
	case *ast.InterpolatedString:
		return g.interpolated(e)
	case *ast.Ident:
		switch u := g.info.Uses[e].(type) {
		case *types.Var:
			return g.varName(u)
		case *types.Field:
			return "self->f_" + u.Name
		}
	case *ast.ListLit:
		elem := t.(*types.List).Elem
		if len(e.Elems) == 0 {
			return fmt.Sprintf("vx_list_new(sizeof(%s), 0, NULL)", ctype(elem))
		}
		return g.ordered(e.Elems, func(cs []string) string {
			return fmt.Sprintf("vx_list_new(sizeof(%s), %d, (%s[]){%s})", ctype(elem), len(cs), ctype(elem), strings.Join(cs, ", "))
		})
	case *ast.UnaryExpr:
		return "(" + op(e.Op) + g.expr(e.X) + ")"
	case *ast.BinaryExpr:
		if e.Op == lexer.AndAnd || e.Op == lexer.OrOr {
			return fmt.Sprintf("(%s %s %s)", g.expr(e.X), op(e.Op), g.expr(e.Y))
		}
		operand := g.info.Types[e.X]
		return g.ordered([]ast.Expr{e.X, e.Y}, func(cs []string) string {
			return binop(e.Op, operand, cs[0], cs[1], e.Pos().Line)
		})
	case *ast.CallExpr:
		return g.call(e)
	case *ast.IndexExpr:
		return g.ordered([]ast.Expr{e.X, e.Index}, func(cs []string) string {
			return fmt.Sprintf("(*(%s*)vx_list_at(%s, %s, %d))", ctype(t), cs[0], cs[1], e.Pos().Line)
		})
	case *ast.MemberExpr:
		m := g.info.Members[e]
		x := g.expr(e.X)
		if m.Field != nil {
			return fmt.Sprintf("%s->f_%s", x, m.Field.Name)
		}
		if g.info.Types[e.X] == types.String {
			return "vx_str_len(" + x + ")"
		}
		return x + "->len"
	}
	panic(fmt.Sprintf("codegen: unexpected expression %T at %s", e, e.Pos()))
}

func op(k lexer.Kind) string { return strings.Trim(k.String(), "'") }

// Returns C code applying the binary operator [k] to [a] and [b], whose
// type is [t].
func binop(k lexer.Kind, t types.Type, a, b string, line int) string {
	if t == types.String {
		switch k {
		case lexer.Plus:
			return fmt.Sprintf("vx_str_concat(%s, %s)", a, b)
		case lexer.Eq:
			return fmt.Sprintf("vx_str_eq(%s, %s)", a, b)
		case lexer.NotEq:
			return fmt.Sprintf("(!vx_str_eq(%s, %s))", a, b)
		default:
			return fmt.Sprintf("(vx_str_cmp(%s, %s) %s 0)", a, b, op(k))
		}
	}
	if t == types.Int {
		switch k {
		case lexer.Slash:
			return fmt.Sprintf("vx_div(%s, %s, %d)", a, b, line)
		case lexer.Percent:
			return fmt.Sprintf("vx_mod(%s, %s, %d)", a, b, line)
		}
	}
	return fmt.Sprintf("(%s %s %s)", a, op(k), b)
}

func (g *gen) interpolated(e *ast.InterpolatedString) string {
	return g.ordered(e.Exprs, func(cs []string) string {
		var parts []string
		for i, text := range e.Texts {
			if text != "" {
				parts = append(parts, "VX_LIT("+cQuote(text)+")")
			}
			if i < len(cs) {
				parts = append(parts, show(g.info.Types[e.Exprs[i]], cs[i]))
			}
		}
		return fmt.Sprintf("vx_str_join(%d, (vx_str[]){%s})", len(parts), strings.Join(parts, ", "))
	})
}

// Returns the position in the source of each bound argument of [e], so
// named arguments run in the order they are written. Defaults come last.
func sourceRank(e *ast.CallExpr, bound []ast.Expr) []int {
	rank := make([]int, len(bound))
	for i, b := range bound {
		rank[i] = len(e.Args) + i
		for j, a := range e.Args {
			if a.Value == b {
				rank[i] = j
			}
		}
	}
	return rank
}

func (g *gen) call(e *ast.CallExpr) string {
	c := g.info.Calls[e]
	line := e.Pos().Line
	rank := sourceRank(e, c.Args)
	switch c.Kind {
	case types.FuncCall, types.StaticCall:
		return g.orderedBy(c.Args, rank, func(cs []string) string {
			return funcName(c.Func) + "(" + strings.Join(cs, ", ") + ")"
		})
	case types.ConstructorCall:
		return g.orderedBy(c.Args, rank, func(cs []string) string {
			return "new_" + structName(c.Struct) + "(" + strings.Join(cs, ", ") + ")"
		})
	case types.MethodCall:
		if c.Recv == nil {
			return g.orderedBy(c.Args, rank, func(cs []string) string {
				return funcName(c.Func) + "(" + strings.Join(append([]string{"self"}, cs...), ", ") + ")"
			})
		}
		return g.orderedBy(append([]ast.Expr{c.Recv}, c.Args...), append([]int{-1}, rank...), func(cs []string) string {
			return funcName(c.Func) + "(" + strings.Join(cs, ", ") + ")"
		})
	case types.BuiltinCall:
		return g.builtin(c, line)
	case types.MemberBuiltinCall:
		return g.memberBuiltin(c, line)
	}
	panic("codegen: unknown call kind")
}

func (g *gen) builtin(c *types.Call, line int) string {
	return g.ordered(c.Args, func(cs []string) string {
		arg := func(i int, fallback string) string {
			if i < len(cs) && cs[i] != "" {
				return cs[i]
			}
			return fallback
		}
		switch c.Builtin {
		case "print", "println":
			if len(cs) == 0 {
				return fmt.Sprintf("vx_print(%t, 0, NULL)", c.Builtin == "println")
			}
			parts := make([]string, len(cs))
			for i, x := range cs {
				parts[i] = show(g.info.Types[c.Args[i]], x)
			}
			return fmt.Sprintf("vx_print(%t, %d, (vx_str[]){%s})", c.Builtin == "println", len(parts), strings.Join(parts, ", "))
		case "input":
			return "vx_input(" + arg(0, `VX_LIT("")`) + ")"
		case "panic":
			return fmt.Sprintf("vx_panic(%s, %d)", cs[0], line)
		case "assert":
			return fmt.Sprintf("vx_assert(%s, %s, %d)", cs[0], arg(1, `VX_LIT("")`), line)
		case "str":
			return show(g.info.Types[c.Args[0]], cs[0])
		}
		from := g.info.Types[c.Args[0]]
		switch {
		case c.Builtin == "int" && from == types.Float:
			return fmt.Sprintf("vx_float_to_int(%s, %d)", cs[0], line)
		case c.Builtin == "int" && from == types.String:
			return fmt.Sprintf("vx_str_to_int(%s, %d)", cs[0], line)
		case c.Builtin == "float" && from == types.Int:
			return "((double)" + cs[0] + ")"
		case c.Builtin == "float" && from == types.String:
			return fmt.Sprintf("vx_str_to_float(%s, %d)", cs[0], line)
		}
		return cs[0]
	})
}

func (g *gen) memberBuiltin(c *types.Call, line int) string {
	recv := g.info.Types[c.Recv]
	return g.ordered(append([]ast.Expr{c.Recv}, c.Args...), func(cs []string) string {
		if l, ok := recv.(*types.List); ok {
			elem := ctype(l.Elem)
			switch c.Builtin {
			case "push":
				item := g.temp()
				return fmt.Sprintf("({ %s = %s; vx_list_push(%s, &%s); })", decl(l.Elem, item), cs[1], cs[0], item)
			case "pop":
				return fmt.Sprintf("(*(%s*)vx_list_pop(%s, %d))", elem, cs[0], line)
			case "contains":
				list, v, i, found := g.temp(), g.temp(), g.temp(), g.temp()
				item := fmt.Sprintf("((%s*)%s->data)[%s]", elem, list, i)
				eq := item + " == " + v
				if l.Elem == types.String {
					eq = fmt.Sprintf("vx_str_eq(%s, %s)", item, v)
				}
				return fmt.Sprintf("({ vx_list *%s = %s; %s = %s; bool %s = false; "+
					"for (int64_t %s = 0; %s < %s->len; %s++) if (%s) { %s = true; break; } %s; })",
					list, cs[0], decl(l.Elem, v), cs[1], found, i, i, list, i, eq, found, found)
			}
		}
		switch c.Builtin {
		case "upper", "lower":
			return fmt.Sprintf("vx_str_%s(%s)", c.Builtin, cs[0])
		case "contains":
			return fmt.Sprintf("vx_str_contains(%s, %s)", cs[0], cs[1])
		case "split":
			return fmt.Sprintf("vx_str_split(%s, %s, %d)", cs[0], cs[1], line)
		}
		panic("codegen: unknown built-in member " + c.Builtin)
	})
}

// Returns [s] as a C string literal. Bytes outside printable ASCII use
// octal escapes, which never run into the following character.
func cQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '?':
			b.WriteString(`\?`)
		case c >= 0x20 && c < 0x7f:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "\\%03o", c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
