// Package codegen translates a checked Vex program into C.
package codegen

import (
	_ "embed"
	"fmt"
	"math"
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
	// The index of the source file being translated, for runtime errors.
	file int

	info *types.Info
	b    *strings.Builder
	ind  int

	// The function or closure being generated: C code reading each variable
	// (an lvalue), the address of each boxed variable, the names in use and
	// the method's `self`, if any.
	vals  map[*types.Var]string
	boxes map[*types.Var]string

	// What is known about its ints and list lengths, to leave out checks
	// that can't fail.
	rng *ranges

	// Set while generating the values of constants, whose lists and maps
	// are frozen.
	constMode bool

	// The temporaries pattern variables are stored in while their pattern
	// is tested.
	patTemps map[*types.Var]string
	used     map[string]bool
	tmp      int
	self     *types.Var

	// Lists and structs that need a function to show their values.
	reprs map[string]types.Type

	// Closures: environment structs and prototypes, and their bodies, with
	// the C function of each.
	lamDecls strings.Builder
	lams     strings.Builder
	nlams    int
	litNames map[*ast.FuncLit]string

	// Encoders and decoders of values sent between isolates, by mangled
	// type, the entry functions of new isolates, and the comparisons that
	// sort lists.
	codecs     map[string]bool
	codecDecls strings.Builder

	// The C variable holding the value of each `if` used as a value, while
	// its branches are written.
	ifResults map[*ast.IfExpr]string
	codecDefs strings.Builder
	nisos     int

	// The C variable each collection being built adds its elements to.
	builds map[ast.Expr]string
	fills  map[ast.Expr]fill

	// Named functions used as values, which need a wrapper.
	fnValues map[*types.Func]bool

	// Functions used as throwing function values, which get a wrapper
	// taking the error slot.
	throwingValues map[*types.Func]bool

	// Optional and record types, which each need a C struct, by name.
	opts map[string]*types.Optional
	recs map[string]*types.Record

	// While an optional chain is generated: C code standing for its
	// receiver, and the chains being generated.
	recvs    map[ast.Expr]string
	chaining map[ast.Expr]bool

	// The result type and thrown type of the function or closure being
	// generated, and the try expressions around the current expression.
	result types.Type
	throws types.Type
	tries  []tryGen
}

// Returns the C source of the program described by [info]. [srcName] is
// the file name shown in runtime errors.
func Generate(info *types.Info, srcName string) string {
	g := &gen{info: info, reprs: map[string]types.Type{}, fnValues: map[*types.Func]bool{}, throwingValues: map[*types.Func]bool{},
		opts: map[string]*types.Optional{}, recs: map[string]*types.Record{}, recvs: map[ast.Expr]string{}, chaining: map[ast.Expr]bool{},
		builds: map[ast.Expr]string{}, fills: map[ast.Expr]fill{}, litNames: map[*ast.FuncLit]string{}, codecs: map[string]bool{},
		ifResults: map[*ast.IfExpr]string{}}
	g.collectOptionals()
	for _, t := range info.Types {
		g.needRepr(t)
	}
	for _, s := range info.Structs {
		g.needRepr(s)
	}
	for _, e := range info.Enums {
		g.needRepr(e)
	}

	// Function bodies come first, since they discover the closures and
	// function values whose declarations must precede them.
	var defs strings.Builder
	g.b = &defs
	for _, s := range info.Structs {
		g.constructor(s)
	}
	for _, e := range info.Enums {
		for _, cs := range e.Cases {
			g.caseConstructor(cs)
		}
	}
	for _, name := range g.reprNames() {
		g.repr(name, g.reprs[name])
	}
	for _, f := range info.Funcs {
		g.function(f)
	}
	for _, f := range info.Funcs {
		if g.fnValues[f] {
			g.fnValueWrapper(f, false)
		}
		if g.throwingValues[f] {
			g.fnValueWrapper(f, true)
		}
		if f.Async {
			g.spawnWrapper(f)
		}
	}
	consts := g.constsInit(info)

	var out strings.Builder
	g.b, g.ind = &out, 0
	g.b.WriteString(runtime)
	g.line("")
	g.line("// --- Program ---")
	g.line("")
	for _, s := range info.Structs {
		g.line("typedef struct %s %s;", structName(s), structName(s))
	}
	for _, e := range info.Enums {
		g.line("typedef struct %s %s;", enumName(e), enumName(e))
	}
	g.valueTypedefs()
	g.descType()
	for _, s := range info.Structs {
		g.line("struct %s {", structName(s))
		if len(s.Interfaces) > 0 {
			g.line("    const vx_desc *vx_t;")
		}
		for _, f := range s.Fields {
			g.line("    %s;", decl(f.Type, "f_"+f.Name))
		}
		g.line("};")
	}
	for _, e := range info.Enums {
		g.enumStruct(e)
	}
	g.line("")
	for _, s := range info.Structs {
		g.line("static %s;", constructorSig(s))
	}
	for _, e := range info.Enums {
		for _, cs := range e.Cases {
			g.line("static %s;", caseSig(cs))
		}
	}
	for _, name := range g.reprNames() {
		g.line("static vx_str repr_%s(%s);", name, decl(g.reprs[name], "v"))
		if showsInline(g.reprs[name]) {
			g.line("static vx_str show_%s(%s);", name, decl(g.reprs[name], "v"))
		}
	}
	for _, f := range info.Funcs {
		g.line("static %s;", g.signature(f, false))
		if f.Async {
			g.line("static %s;", spawnSig(f))
		}
	}
	// Record equality may run a struct's == operator, declared above.
	g.recordEquals()
	g.b.WriteString(g.lamDecls.String())
	for _, s := range info.Structs {
		if len(s.Interfaces) > 0 {
			g.structDescriptor(s)
		}
	}
	for _, e := range info.Enums {
		if len(e.Interfaces) > 0 {
			g.enumDescriptor(e)
		}
	}
	g.b.WriteString(g.codecDecls.String())
	g.b.WriteString(g.codecDefs.String())
	g.constsStruct(info)
	g.b.WriteString(defs.String())
	g.b.WriteString(g.lams.String())
	g.b.WriteString(consts)
	g.line("")
	names := []string{cQuote(srcName)}
	if len(info.Files) > 1 {
		names = names[:0]
		for _, f := range info.Files {
			names = append(names, cQuote(f.Name))
		}
	}
	g.line("static const char *const vx_srcs[] = {%s};", strings.Join(names, ", "))
	g.line("")
	g.line("int main(int argc, char **argv) {")
	g.line("    vx_argc = argc;")
	g.line("    vx_argv = argv;")
	g.line("    vx_init(__builtin_frame_address(0));")
	g.line("    vx_files = vx_srcs;")
	if consts != "" {
		g.line("    vx_consts_init();")
	}
	switch main := mainFunc(info); {
	case main != nil && main.Async:
		g.line("    vx_future *f = sp_fn_main();")
		g.line("    vx_loop();")
		if main.Throws != nil {
			g.line("    if (f->state == 2) vx_uncaught(%s);", errorMessage(info, main.Throws, "f->err"))
		} else {
			g.line("    (void)f;")
		}
	case main != nil && main.Throws != nil:
		g.line("    void *err = NULL;")
		g.line("    fn_main(&err);")
		g.line("    if (err) vx_uncaught(%s);", errorMessage(info, main.Throws, "err"))
		g.line("    vx_loop();")
	default:
		g.line("    fn_main();")
		g.line("    vx_loop();")
	}
	g.line("    return 0;")
	g.line("}")
	return g.b.String()
}

// Writes one line at the current indentation.
// Returns the line of [n] as passed to the runtime: the line number, with
// the index of its file in the bits above [lineBits].
func (g *gen) lineOf(n ast.Node) int { return g.file<<lineBits | n.Pos().Line }

const lineBits = 20

func (g *gen) line(format string, args ...any) {
	if format != "" {
		g.b.WriteString(strings.Repeat("    ", g.ind))
		fmt.Fprintf(g.b, format, args...)
	}
	g.b.WriteByte('\n')
}

// --- Names and types ---

func structName(s *types.Struct) string { return "S_" + structIdent(s) }

// Returns a C-safe name for [s]: its name, with the type arguments of a
// generic instance spelled out, as in `Box__int`.
func structIdent(s *types.Struct) string {
	if s.Args == nil {
		return modTag(s.Module) + cid(s.Name)
	}
	return modTag(s.Module) + cid(s.Base) + "__" + mangleList(s.Args)
}

// Returns the prefix that keeps the C names of module [m] apart from those
// of other modules. It starts with a digit, which no Vex name can, so it
// can't collide with names of the root module.
func modTag(m *types.Module) string {
	if m == nil || m.Index == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d", m.Index)
	for _, r := range m.Name {
		if r < 128 && (r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	b.WriteByte('_')
	return b.String()
}

// Returns the suffix that tells apart the instances of a generic method.
func typeArgsTag(f *types.Func) string {
	if f.TypeArgs == nil {
		return ""
	}
	return "__" + mangleList(f.TypeArgs)
}

func mangleList(ts []types.Type) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = mangle(t)
	}
	return strings.Join(parts, "_")
}

// Returns the C name of a function or method. Methods include the length of
// the struct name so `A_b.c` and `A.b_c` can't collide.
func funcName(f *types.Func) string {
	if f.Owner != nil {
		owner := structIdent(f.Owner)
		// Accessors share their property's name, so each kind has its own
		// prefix.
		prefix := map[ast.Accessor]string{ast.Method: "m", ast.Getter: "g", ast.Setter: "s", ast.Observer: "o", ast.Operator: "p"}[f.Decl.Accessor]
		name := cid(f.Name)
		if f.Decl.Accessor == ast.Operator {
			name = operatorNames[f.Name]
		}
		return fmt.Sprintf("%s%d%s_%s", prefix, len(owner), owner, name) + typeArgsTag(f)
	}
	if f.Enum != nil {
		id := enumIdent(f.Enum)
		return fmt.Sprintf("m%d%s_%s", len(id), id, cid(f.Name)) + typeArgsTag(f)
	}
	if f.Recv != nil {
		// Extensions of built-in types: the receiver's type tells apart
		// members of the same name, and the accessor kind a getter.
		recv := mangle(f.Recv)
		prefix := map[ast.Accessor]string{ast.Method: "x", ast.Getter: "xg"}[f.Decl.Accessor]
		return fmt.Sprintf("%s%s%d%s_%s", prefix, modTag(f.Module), len(recv), recv, cid(f.Name)) + typeArgsTag(f)
	}
	if f.TypeArgs != nil {
		return "fn_" + modTag(f.Module) + cid(f.Name) + "__" + mangleList(f.TypeArgs)
	}
	if f.Iface != nil {
		id := strings.TrimPrefix(ifaceName(f.Iface), "I")
		prefix := map[ast.Accessor]string{ast.Method: "i", ast.Getter: "ig", ast.Setter: "is"}[f.Decl.Accessor]
		return prefix + id + "_" + cid(f.Name)
	}
	return "fn_" + modTag(f.Module) + cid(f.Name)
}

// The C names of the operators a struct can define.
var operatorNames = map[string]string{
	"+": "plus", "-": "minus", "*": "times", "/": "div", "%": "rem", "unary -": "neg",
	"==": "eq", "<": "lt", "[]": "get", "[]=": "set",
}

// Returns [name], a name from the program, for use inside a C name. Each
// `_` becomes `_u`, so the result never holds `__`, which separates the
// type arguments of generic instances: `id__int` stays apart from `id<int>`.
func cid(name string) string { return strings.ReplaceAll(name, "_", "_u") }

func ctype(t types.Type) string {
	switch t := t.(type) {
	case *types.List:
		return "vx_list *"
	case *types.Map:
		return "vx_map *"
	case *types.Interface, *types.Handle:
		return "void *"
	case *types.Future:
		return "vx_future *"
	case *types.Channel:
		return "vx_chan *"
	case *types.Record:
		return "rec_" + mangle(t)
	case *types.Struct:
		return structName(t) + " *"
	case *types.FuncType:
		return "vx_fn"
	case *types.Enum:
		return enumName(t) + " *"
	case *types.Optional:
		if nullable(t) {
			return ctype(t.Elem)
		}
		return "opt_" + mangle(t.Elem)
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

// Returns a C declaration of [name] as a pointer to a value of type [t].
func declPtr(t types.Type, name string) string {
	c := ctype(t)
	if strings.HasSuffix(c, "*") {
		return c + "*" + name
	}
	return c + " *" + name
}

// Returns the C type of a pointer to a closure function of type [ft].
func fnPtrType(ft *types.FuncType) string {
	params := []string{"void *"}
	for _, p := range ft.Params {
		params = append(params, ctype(p))
	}
	if ft.Throws != nil {
		params = append(params, "void **")
	}
	return ctype(ft.Result) + " (*)(" + strings.Join(params, ", ") + ")"
}

// Returns a name for [t] usable inside C identifiers, such as `L_int`.
// Struct names and parameter lists carry their length, so different types
// never share a name.
func mangle(t types.Type) string {
	switch t := t.(type) {
	case *types.List:
		return "L_" + mangle(t.Elem)
	case *types.Map:
		return "M_" + mangle(t.Key) + "_" + mangle(t.Value)
	case *types.Interface:
		return ifaceName(t)
	case *types.Struct:
		id := structIdent(t)
		return fmt.Sprintf("S%d%s", len(id), id)
	case *types.Enum:
		id := enumIdent(t)
		return fmt.Sprintf("E%d%s", len(id), id)
	case *types.Optional:
		return "O_" + mangle(t.Elem)
	case *types.Handle:
		id := modTag(t.Module) + t.Name
		return fmt.Sprintf("H%d%s", len(id), id)
	case *types.Channel:
		return "K_" + mangle(t.Elem)
	case *types.Record:
		var b strings.Builder
		fmt.Fprintf(&b, "R%d_%d", len(t.Positional), len(t.Named))
		for _, p := range t.Positional {
			b.WriteString("_" + mangle(p))
		}
		for _, f := range t.Named {
			fmt.Fprintf(&b, "_%d%s_%s", len(f.Name), f.Name, mangle(f.Type))
		}
		return b.String() + "_E"
	case *types.Future:
		if t.Throws != nil {
			return "U2_" + mangle(t.Elem) + "_" + mangle(t.Throws)
		}
		return "U1_" + mangle(t.Elem)
	case *types.FuncType:
		parts := []string{fmt.Sprintf("F%d", len(t.Params))}
		if t.Throws != nil {
			parts[0] = fmt.Sprintf("T%d", len(t.Params))
		}
		for _, p := range t.Params {
			parts = append(parts, mangle(p))
		}
		parts = append(parts, mangle(t.Result))
		if t.Throws != nil {
			parts = append(parts, mangle(t.Throws))
		}
		return strings.Join(parts, "_")
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
	g.vals[v] = name
	return name
}

// Returns C code reading (or assigning) the variable [v].
func (g *gen) varName(v *types.Var) string {
	if c, ok := g.vals[v]; ok {
		return c
	}
	return "self"
}

// Returns C code for the current method's `self`.
// Returns the type of `self`, or nil when it isn't known here.
func (g *gen) selfType() types.Type {
	if g.self != nil {
		return g.self.Type
	}
	return nil
}

func (g *gen) selfRef() string {
	if g.self != nil {
		return g.varName(g.self)
	}
	return "self"
}

// Starts generating a new function or closure body into [b].
func (g *gen) enter(b *strings.Builder) {
	g.b, g.ind = b, 0
	g.vals, g.boxes, g.used, g.tmp, g.self = map[*types.Var]string{}, map[*types.Var]string{}, map[string]bool{}, 0, nil
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
	if len(s.Interfaces) > 0 {
		g.line("    o->vx_t = &desc_%s;", structIdent(s))
	}
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
	if f.Enum != nil && !f.Static {
		params = append(params, enumName(f.Enum)+" *self")
	}
	if f.Iface != nil {
		params = append(params, "void *self")
	}
	if f.Recv != nil {
		params = append(params, decl(f.Recv, "self"))
	}
	for i, p := range f.Params {
		name := "v_" + p.Name
		if define {
			name = g.declare(g.info.Defs[f.Decl.Params[i].Name])
		}
		params = append(params, decl(p.Type, name))
	}
	if f.Throws != nil {
		params = append(params, "void **vx_err")
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	return decl(f.Result, funcName(f)+"("+strings.Join(params, ", ")+")")
}

func (g *gen) function(f *types.Func) {
	if f.Decl.Extern {
		g.enter(g.b)
		g.externFunction(f)
		return
	}
	if f.Decl.Export {
		defer g.exportFunction(f)
	}
	g.file = g.info.FileOf[f.Decl]
	g.enter(g.b)
	g.result, g.throws, g.tries = f.Result, f.Throws, nil
	if f.Decl.ExprBody != nil {
		g.rng = newRanges(g.info, f.Decl.ExprBody)
	} else {
		g.rng = newRanges(g.info, f.Decl.Body)
	}
	if self := g.info.Selves[f.Decl]; self != nil {
		g.self = self
		g.vals[self] = "self"
	}
	g.line("")
	g.line("static %s {", g.signature(f, true))
	g.ind++
	if e := f.Decl.ExprBody; e != nil {
		if f.Result == types.Void || types.IsPanic(e) {
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
	case *types.Map:
		if _, ok := g.reprs[mangle(t)]; !ok {
			g.reprs[mangle(t)] = t
			g.needRepr(t.Key)
			g.needRepr(t.Value)
		}
	case *types.Interface:
		g.reprs[mangle(t)] = t
	case *types.Struct:
		if _, ok := g.reprs[mangle(t)]; !ok {
			g.reprs[mangle(t)] = t
			for _, f := range t.Fields {
				g.needRepr(f.Type)
			}
		}
	case *types.Optional:
		if _, ok := g.reprs[mangle(t)]; !ok {
			g.reprs[mangle(t)] = t
			g.needRepr(t.Elem)
		}
	case *types.Record:
		if _, ok := g.reprs[mangle(t)]; !ok {
			g.reprs[mangle(t)] = t
			for _, f := range t.FieldTypes() {
				g.needRepr(f)
			}
		}
	case *types.Enum:
		if _, ok := g.reprs[mangle(t)]; !ok {
			g.reprs[mangle(t)] = t
			for _, cs := range t.Cases {
				for _, f := range cs.Fields {
					g.needRepr(f.Type)
				}
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
	switch t := t.(type) {
	case *types.List, *types.Map, *types.Struct, *types.Enum, *types.Optional, *types.Interface, *types.Record:
		return fmt.Sprintf("repr_%s(%s)", mangle(t), x)
	case *types.FuncType:
		return "VX_LIT(" + cQuote("<"+t.String()+">") + ")"
	case *types.Handle:
		return "vx_handle_str(" + cQuote(t.CName) + ", " + x + ")"
	case *types.Future, *types.Channel:
		return "VX_LIT(" + cQuote("<"+t.String()+">") + ")"
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
	if showsInline(t) {
		return fmt.Sprintf("show_%s(%s)", mangle(t), x)
	}
	return repr(t, x)
}

// Reports whether printing [t] on its own differs from showing it inside a
// list: a `string?` prints without quotes.
func showsInline(t types.Type) bool {
	o, ok := t.(*types.Optional)
	return ok && o.Elem == types.String
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
	case *types.Interface:
		g.line("    return (*(const vx_desc **)v)->repr(v);")
	case *types.Record:
		g.recordRepr(t)
	case *types.Map:
		g.line("    if (v->len == 0) return VX_LIT(\"[:]\");")
		g.line("    vx_str s = VX_LIT(\"[\");")
		g.line("    vx_map_iter it = vx_map_iter_new(v);")
		g.line("    bool first = true;")
		g.line("    for (char *e; (e = vx_map_next(v, &it)); first = false) {")
		g.line("        if (!first) s = vx_str_concat(s, VX_LIT(\", \"));")
		g.line("        s = vx_str_concat(s, %s);", repr(t.Key, fmt.Sprintf("*(%s*)(e + VX_MAP_KEY)", ctype(t.Key))))
		g.line("        s = vx_str_concat(s, VX_LIT(\": \"));")
		g.line("        s = vx_str_concat(s, %s);", repr(t.Value, fmt.Sprintf("*(%s*)(e + v->voff)", ctype(t.Value))))
		g.line("    }")
		g.line("    return vx_str_concat(s, VX_LIT(\"]\"));")
	case *types.Struct:
		g.line("    if (vx_repr_enter(v)) return VX_LIT(%s);", cQuote(t.Name+"(...)"))
		g.line("    vx_str s = VX_LIT(%s);", cQuote(t.Name+"("))
		for i, f := range t.Fields {
			sep := ""
			if i > 0 {
				sep = ", "
			}
			g.line("    s = vx_str_concat(s, VX_LIT(%s));", cQuote(sep+f.Name+": "))
			g.line("    s = vx_str_concat(s, %s);", repr(f.Type, "v->f_"+f.Name))
		}
		g.line("    vx_repr_leave(v);")
		g.line("    return vx_str_concat(s, VX_LIT(\")\"));")
	case *types.Optional:
		g.line("    return %s ? %s : VX_LIT(\"nil\");", present(t, "v"), repr(t.Elem, unwrap(t, "v")))
		if showsInline(t) {
			g.line("}")
			g.line("")
			g.line("static vx_str show_%s(%s) {", name, decl(t, "v"))
			g.line("    return v.has ? v.v : VX_LIT(\"nil\");")
		}
	case *types.Enum:
		g.line("    switch (v->tag) {")
		for _, cs := range t.Cases {
			name := t.Name + "." + cs.Name
			if len(cs.Fields) == 0 {
				g.line("    case %d: return VX_LIT(%s);", cs.Index, cQuote(name))
				continue
			}
			g.line("    case %d: {", cs.Index)
			g.line("        vx_str s = VX_LIT(%s);", cQuote(name+"("))
			for i, f := range cs.Fields {
				sep := ""
				if i > 0 {
					sep = ", "
				}
				g.line("        s = vx_str_concat(s, VX_LIT(%s));", cQuote(sep+f.Name+": "))
				g.line("        s = vx_str_concat(s, %s);", repr(f.Type, fmt.Sprintf("v->u.c_%s.f_%s", cs.Name, f.Name)))
			}
			g.line("        return vx_str_concat(s, VX_LIT(\")\"));")
			g.line("    }")
		}
		g.line("    }")
		g.line("    __builtin_unreachable();")
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
	// A statement expression `({ ... })` keeps its parentheses, and a
	// string literal may hold unbalanced ones, so neither is touched.
	if !strings.HasPrefix(c, "(") || !strings.HasSuffix(c, ")") || strings.HasPrefix(c, "({") || strings.Contains(c, `"`) {
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
	r := g.rng
	if r == nil || g.constMode {
		g.emitStmt(s)
		return
	}
	switch s := s.(type) {
	case *ast.LetStmt:
		r.forget(s)
		g.emitStmt(s)
		r.forget(s)
		if s.Pattern == nil {
			r.declare(g.info.Defs[s.Name], s.Value)
		}
	case *ast.AssignStmt:
		// The new value's range is worked out before the assignment drops
		// what was known about the target.
		v := r.local(s.Target)
		next, known := anyInt, false
		nested := len(r.assigned(s.Value)) > 0
		if nested {
			r.forget(s)
		}
		if v != nil && !nested && g.info.Types[s.Target] == types.Int && g.info.Operators[s] == nil {
			if s.Op == lexer.Assign {
				next, known = r.of(s.Value), true
			} else if k, ok := compoundOps[s.Op]; ok {
				next, known = combine(k, r.of(s.Target), r.of(s.Value))
			}
		}
		g.emitStmt(s)
		r.forget(s)
		if known {
			r.vals[v] = next
		}
	case *ast.WhileStmt:
		if s.Bind != nil {
			r.forget(s)
			g.emitStmt(s)
			r.forget(s)
			break
		}
		saved := r.save()
		r.enterLoop(s)
		head := "while (" + cond(g.expr(s.Cond)) + ")"
		r.assume(s.Cond)
		g.block(head, s.Body)
		r.vals = saved
		r.forget(s)
	case *ast.ForStmt:
		saved := r.save()
		if _, ok := s.Iter.(*ast.RangeExpr); !ok {
			// A range loop does this itself once its ends are read.
			r.enterLoop(s)
		}
		g.forStmt(s)
		r.vals = saved
		r.forget(s)
	default:
		r.forget(s)
		g.emitStmt(s)
		r.forget(s)
	}
}

// Generates the statement [s].
func (g *gen) emitStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.Block:
		g.block("", s)
	case *ast.LetStmt:
		if s.Pattern != nil {
			g.letPattern(s)
			break
		}
		g.bindVar(g.info.Defs[s.Name], g.expr(s.Value))
	case *ast.AssignStmt:
		g.assign(s)
	case *ast.GuardStmt:
		tmp := g.temp()
		g.line("%s = %s;", decl(g.info.Types[s.Value], tmp), g.expr(s.Value))
		o := g.optOf(s.Value)
		g.line("if (!%s) {", present(o, tmp))
		g.body(s.Else)
		g.line("}")
		v := g.info.Defs[s.Bind]
		g.line("%s = %s;", decl(v.Type, g.declare(v)), unwrap(o, tmp))
	case *ast.ExprStmt:
		if lit, ok := g.info.Elements[s]; ok {
			g.element(s.X, lit)
			break
		}
		if e := g.info.IfValues[s]; e != nil {
			if r := g.ifResults[e]; r != "" {
				g.line("%s = %s;", r, g.expr(s.X))
			} else {
				g.line("%s;", g.expr(s.X))
			}
			break
		}
		if m, ok := s.X.(*ast.MatchExpr); ok && g.info.Matches[m].Stmt {
			g.matchStmt(m)
			break
		}
		g.line("%s;", g.expr(s.X))
	case *ast.IfStmt:
		g.ifStmt(s)
	case *ast.WhileStmt:
		if s.Bind != nil {
			g.whileLet(s)
			break
		}
		g.block("while ("+cond(g.expr(s.Cond))+")", s.Body)
	case *ast.ForStmt:
		g.forStmt(s)
	case *ast.ReturnStmt:
		if s.Value == nil {
			g.line("return;")
		} else {
			g.line("return %s;", g.expr(s.Value))
		}
	case *ast.ThrowStmt:
		g.line("*vx_err = %s;", g.expr(s.Value))
		g.line("%s", zeroReturn(g.result))
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
	if s.Bind != nil {
		g.ifLet(s)
		return
	}
	if s.Case != nil {
		g.ifCase(s)
		return
	}
	g.line("if (%s) {", cond(g.expr(s.Cond)))
	for {
		g.body(s.Then)
		switch e := s.Else.(type) {
		case *ast.IfStmt:
			if e.Bind != nil || e.Case != nil {
				g.line("} else {")
				g.ind++
				g.ifStmt(e)
				g.ind--
				g.line("}")
				return
			}
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

// Records, before the body of the loop [s] over the range [r], what is known
// about the loop variable. The range's ends were read before the loop.
func (g *gen) loopRange(s *ast.ForStmt, r *ast.RangeExpr) {
	if g.rng == nil {
		return
	}
	lo, hi := g.rng.of(r.Lo), g.rng.of(r.Hi)
	g.rng.enterLoop(s)
	if !r.Inclusive {
		if hi.hi == math.MinInt64 {
			return
		}
		hi.hi--
	}
	if lo.lo <= hi.hi {
		g.rng.vals[g.info.Defs[s.Elem]] = interval{lo.lo, hi.hi}
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
		if r.Inclusive {
			// Testing for the end after each pass, not with <=, lets a range
			// end at the largest int.
			done := g.temp()
			head := fmt.Sprintf("for (int64_t %s = %s, %s = %s, %s = %s > %s; !%s; %s = %s == %s, %s++)", v, g.expr(r.Lo), hi, g.expr(r.Hi), done, v, hi, done, done, v, hi, v)
			g.loopRange(s, r)
			g.block(head, s.Body)
			return
		}
		head := fmt.Sprintf("for (int64_t %s = %s, %s = %s; %s %s %s; %s++)", v, g.expr(r.Lo), hi, g.expr(r.Hi), v, cmp, hi, v)
		g.loopRange(s, r)
		g.block(head, s.Body)
		return
	}
	// Items added during the loop aren't visited, so pushing inside the
	// loop can't make it endless; removed items end it early.
	if m, ok := g.info.Types[s.Iter].(*types.Map); ok {
		g.mapLoop(s, m)
		return
	}
	elem := g.info.Defs[s.Elem]
	list, n, i := g.temp(), g.temp(), g.temp()
	g.line("{")
	g.ind++
	g.line("vx_list *%s = %s;", list, g.expr(s.Iter))
	g.line("int64_t %s = %s->len;", n, list)
	g.line("for (int64_t %s = 0; %s < %s && %s < %s->len; %s++) {", i, i, n, i, list, i)
	g.ind++
	if s.Index != nil {
		g.line("int64_t %s = %s;", g.declare(g.info.Defs[s.Index]), i)
	}
	g.line("%s = ((%s*)%s->data)[%s];", decl(elem.Type, g.declare(elem)), ctype(elem.Type), list, i)
	if s.Pattern != nil {
		g.letBind(s.Pattern, g.varName(elem))
	}
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

	lexer.AmpPlusAssign:  lexer.AmpPlus,
	lexer.AmpMinusAssign: lexer.AmpMinus,
	lexer.AmpStarAssign:  lexer.AmpStar,

	lexer.AmpAssign:   lexer.Amp,
	lexer.PipeAssign:  lexer.Pipe,
	lexer.CaretAssign: lexer.Caret,
	lexer.ShlAssign:   lexer.Shl,
	lexer.ShrAssign:   lexer.Shr,
}

// Generates an assignment. The target's list and index (or object) are
// evaluated first, then the value, and only then is the element located, so
// a value that grows the list can't leave a stale pointer.
func (g *gen) assign(s *ast.AssignStmt) {
	t := g.info.Types[s.Target]
	var setup []string
	var target, recv string
	// Whether the target is in an object, which the write barrier records,
	// and what it records: the object, the list's elements, or the target
	// itself.
	inHeap, barrier := true, ""
	switch x := s.Target.(type) {
	case *ast.IndexExpr:
		if set := g.info.IndexSets[x]; set != nil {
			g.indexSet(s, x, set)
			return
		}
		if _, ok := g.info.Types[x.X].(*types.Map); ok {
			m, k := g.temp(), g.temp()
			setup = append(setup,
				fmt.Sprintf("vx_map *%s = %s;", m, g.expr(x.X)),
				fmt.Sprintf("%s = %s;", decl(g.info.Types[x.Index], k), g.expr(x.Index)))
			target = fmt.Sprintf("(*(%s*)vx_map_set(vx_map_mut(%s, %d), &%s))", ctype(t), m, g.lineOf(x), k)
			break
		}
		list, idx := g.temp(), g.temp()
		setup = append(setup,
			fmt.Sprintf("vx_list *%s = %s;", list, g.expr(x.X)),
			fmt.Sprintf("int64_t %s = %s;", idx, g.expr(x.Index)))
		barrier = list + "->data"
		if g.rng != nil && !g.constMode && g.rng.inBounds(x.X, x.Index) {
			// A list that keeps its length was made by a literal here, so
			// it isn't a constant's.
			target = fmt.Sprintf("(((%s *)%s->data)[%s])", ctype(t), list, idx)
			break
		}
		target = fmt.Sprintf("(*VX_AT(%s, vx_list_mut(%s, %d), %s, %d))", ctype(t), list, g.lineOf(x), idx, g.lineOf(x))
	case *ast.MemberExpr:
		obj := g.temp()
		setup = append(setup, fmt.Sprintf("%s = %s;", decl(g.info.Types[x.X], obj), g.expr(x.X)))
		if p := g.info.Members[x].Property; p != nil {
			g.setProperty(s, obj, g.info.Types[x.X], p, setup)
			return
		}
		target = fmt.Sprintf("%s->f_%s", obj, x.Name.Name)
		recv, barrier = obj, obj
	default:
		if id, ok := s.Target.(*ast.Ident); ok {
			switch g.info.Uses[id].(type) {
			case *types.Property:
				g.setProperty(s, g.selfRef(), g.selfType(), g.info.Uses[id].(*types.Property), nil)
				return
			case *types.Field:
				recv = g.selfRef()
				barrier = recv
			case *types.Var:
				inHeap = g.info.Uses[id].(*types.Var).Boxed()
			}
		}
		target = g.expr(s.Target)
	}
	store := func(value string) {
		if !inHeap || flat(t) {
			g.line("%s = %s;", target, value)
			return
		}
		if barrier != "" {
			g.line("%s = %s;", target, value)
			g.line("vx_remember(%s);", barrier)
			return
		}
		p := g.temp()
		g.line("{ %s *%s = &%s; *%s = %s; vx_remember(%s); }", ctype(t), p, target, p, value, p)
	}
	observer := g.info.Observers[s]
	op, compound := compoundOps[s.Op]
	// A compound assignment reads the target before the value runs, as
	// x += f() reads x first.
	old := target
	if compound {
		old = g.temp()
		setup = append(setup, fmt.Sprintf("%s = %s;", decl(t, old), target))
	}
	value := g.expr(s.Value)
	if setup != nil || observer != nil {
		v := g.temp()
		setup = append(setup, fmt.Sprintf("%s = %s;", decl(g.valueType(s), v), value))
		value = v
	}
	if compound {
		value = g.compound(s, op, t, old, value)
	}
	if setup == nil {
		store(value)
		return
	}
	g.line("{")
	g.ind++
	for _, l := range setup {
		g.line("%s", l)
	}
	if observer != nil {
		prev := g.temp()
		g.line("%s = %s;", decl(t, prev), target)
		store(value)
		g.line("%s(%s, %s);", funcName(observer), recv, prev)
	} else {
		store(value)
	}
	g.ind--
	g.line("}")
}

// Generates `x[i] = v` with the `[]=` operator [set] of x's struct. A
// compound assignment reads the old value with its `[]` operator first.
func (g *gen) indexSet(s *ast.AssignStmt, x *ast.IndexExpr, set *types.Func) {
	rt, t := g.info.Types[x.X], g.info.Types[s.Target]
	recv, idx, v := g.temp(), g.temp(), g.temp()
	g.line("{")
	g.ind++
	g.line("%s = %s;", decl(rt, recv), g.expr(x.X))
	g.line("%s = %s;", decl(set.Params[0].Type, idx), g.expr(x.Index))
	value := g.expr(s.Value)
	if op, compound := compoundOps[s.Op]; compound {
		old := g.temp()
		g.line("%s = %s;", decl(t, old), g.memberCall(g.info.Operators[x], rt, recv, idx))
		g.line("%s = %s;", decl(g.valueType(s), v), value)
		value = g.compound(s, op, t, old, v)
	}
	g.line("%s;", g.memberCall(set, rt, recv, idx, value))
	g.ind--
	g.line("}")
}

// Generates an assignment to the property [p] of [recv], after the lines
// in [setup]. A compound assignment reads it with the getter first.
func (g *gen) setProperty(s *ast.AssignStmt, recv string, rt types.Type, p *types.Property, setup []string) {
	t := g.info.Types[s.Target]
	op, compound := compoundOps[s.Op]
	var old string
	if compound {
		old = g.temp()
		setup = append(setup, fmt.Sprintf("%s = %s;", decl(t, old), g.memberCall(p.Get, rt, recv)))
	}
	v := g.temp()
	setup = append(setup, fmt.Sprintf("%s = %s;", decl(g.valueType(s), v), g.expr(s.Value)))
	value := v
	if compound {
		value = g.compound(s, op, t, old, v)
	}
	g.line("{")
	g.ind++
	for _, l := range setup {
		g.line("%s", l)
	}
	g.line("%s;", g.memberCall(p.Set, rt, recv, value))
	g.ind--
	g.line("}")
}

// --- Closures ---

// Generates the C function for the closure [e] and returns C code creating
// its value. Captured variables are copied into a heap environment; boxed
// ones are shared through their address.
func (g *gen) funcLit(e *ast.FuncLit) string {
	ft := g.info.Types[e].(*types.FuncType)
	caps := g.info.Captures[e]
	g.nlams++
	name := fmt.Sprintf("lam%d", g.nlams)
	env := "env_" + name
	g.litNames[e] = name

	// Values for the environment, read in the enclosing context.
	inits := make([]string, len(caps))
	for i, v := range caps {
		if v.Boxed() {
			inits[i] = g.boxes[v]
		} else {
			inits[i] = g.varName(v)
		}
	}

	outer, ind, vals, boxes, used, tmp, self, rng := g.b, g.ind, g.vals, g.boxes, g.used, g.tmp, g.self, g.rng
	result, throws, tries := g.result, g.throws, g.tries
	var body strings.Builder
	g.enter(&body)
	if e.ExprBody != nil {
		g.rng = newRanges(g.info, e.ExprBody)
	} else {
		g.rng = newRanges(g.info, e.Body)
	}
	g.result, g.throws, g.tries = ft.Result, ft.Throws, nil
	params := []string{"void *env_"}
	for i, p := range e.Params {
		params = append(params, decl(ft.Params[i], g.declare(g.info.Defs[p.Name])))
	}
	if ft.Throws != nil {
		params = append(params, "void **vx_err")
	}
	sig := decl(ft.Result, name+"("+strings.Join(params, ", ")+")")
	g.line("")
	g.line("static %s {", sig)
	g.ind++
	if len(caps) > 0 {
		g.line("struct %s *_env = env_;", env)
	}
	for i, v := range caps {
		field := fmt.Sprintf("_env->c%d", i)
		if v.Boxed() {
			g.vals[v], g.boxes[v] = "(*"+field+")", field
		} else {
			g.vals[v] = field
		}
		if v.Kind == types.SelfVar {
			g.self = v
		}
	}
	if e.ExprBody != nil {
		if ft.Result == types.Void || types.IsPanic(e.ExprBody) {
			g.line("%s;", g.expr(e.ExprBody))
		} else {
			g.line("return %s;", g.expr(e.ExprBody))
		}
	} else {
		for _, st := range e.Body.Stmts {
			g.stmt(st)
		}
		if ft.Result != types.Void {
			g.line("__builtin_unreachable();")
		}
	}
	g.ind--
	g.line("}")
	g.b, g.ind, g.vals, g.boxes, g.used, g.tmp, g.self, g.rng = outer, ind, vals, boxes, used, tmp, self, rng
	g.result, g.throws, g.tries = result, throws, tries

	if len(caps) > 0 {
		fmt.Fprintf(&g.lamDecls, "struct %s {", env)
		for i, v := range caps {
			field := fmt.Sprintf("c%d", i)
			if v.Boxed() {
				fmt.Fprintf(&g.lamDecls, " %s;", declPtr(v.Type, field))
			} else {
				fmt.Fprintf(&g.lamDecls, " %s;", decl(v.Type, field))
			}
		}
		g.lamDecls.WriteString(" };\n")
	}
	fmt.Fprintf(&g.lamDecls, "static %s;\n", sig)
	g.lams.WriteString(body.String())

	if len(caps) == 0 {
		return fmt.Sprintf("((vx_fn){(void *)%s, NULL})", name)
	}
	ptr := g.temp()
	var b strings.Builder
	fmt.Fprintf(&b, "({ struct %s *%s = vx_alloc(sizeof(struct %s)); ", env, ptr, env)
	for i, init := range inits {
		fmt.Fprintf(&b, "%s->c%d = %s; ", ptr, i, init)
	}
	fmt.Fprintf(&b, "(vx_fn){(void *)%s, %s}; })", name, ptr)
	return b.String()
}

// Writes `fnv_NAME`, which lets the top-level function [f] be called
// through a function value, or with [throwing] `fntv_NAME`, which takes
// the error slot of a throwing function value that [f] never fills.
func (g *gen) fnValueWrapper(f *types.Func, throwing bool) {
	params := []string{"void *env_"}
	var args []string
	for i, p := range f.Params {
		params = append(params, decl(p.Type, fmt.Sprintf("a%d", i)))
		args = append(args, fmt.Sprintf("a%d", i))
	}
	if f.Throws != nil {
		params = append(params, "void **vx_err")
		args = append(args, "vx_err")
	}
	name := "fnv_"
	if throwing {
		params = append(params, "void **vx_err")
		name = "fntv_"
	}
	sig := decl(f.Result, name+funcName(f)+"("+strings.Join(params, ", ")+")")
	fmt.Fprintf(&g.lamDecls, "static %s;\n", sig)
	call := funcName(f) + "(" + strings.Join(args, ", ") + ")"
	if f.Result != types.Void {
		call = "return " + call
	}
	fmt.Fprintf(&g.lams, "\nstatic %s {\n    %s;\n}\n", sig, call)
}

// --- Expressions ---

// Reports whether evaluating [e] may call a function, so its order relative
// to other calls matters.
func (g *gen) hasCall(e ast.Expr) bool {
	if g.info.Operators[e] != nil {
		return true
	}
	switch e := e.(type) {
	case *ast.CallExpr, *ast.TryExpr, *ast.AwaitExpr:
		return true
	case *ast.UnaryExpr:
		return g.hasCall(e.X)
	case *ast.BinaryExpr:
		return g.hasCall(e.X) || g.hasCall(e.Y)
	case *ast.IndexExpr:
		return g.hasCall(e.X) || g.hasCall(e.Index)
	case *ast.MemberExpr:
		if m := g.info.Members[e]; m != nil && m.Property != nil {
			return true
		}
		return g.hasCall(e.X)
	case *ast.ListLit:
		if e.Body != nil {
			return true
		}
		for _, x := range e.Elems {
			if g.hasCall(x) {
				return true
			}
		}
	case *ast.MapLit:
		if e.Body != nil {
			return true
		}
		for i, k := range e.Keys {
			if g.hasCall(k) || g.hasCall(e.Values[i]) {
				return true
			}
		}
	case *ast.InterpolatedString:
		for _, x := range e.Exprs {
			if g.hasCall(x) {
				return true
			}
		}
	case *ast.MatchExpr, *ast.IfExpr:
		// Branches may run calls, so these are ordered like one.
		return true
	case *ast.TypeTest:
		return g.hasCall(e.X)
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
			if g.hasCall(e) {
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
		fmt.Fprintf(&b, "%s = %s; ", decl(g.typeOf(e), cs[i]), g.expr(e))
	}
	b.WriteString(render(cs))
	b.WriteString("; })")
	return b.String()
}

func (g *gen) expr(e ast.Expr) string {
	if f, ok := g.info.FileOf[e]; ok && f != g.file {
		saved := g.file
		g.file = f
		defer func() { g.file = saved }()
	}
	if s, ok := g.recvs[e]; ok {
		return s
	}
	if o := g.info.Wraps[e]; o != nil {
		return wrapOpt(o, g.value(e))
	}
	if wrap, ok := g.info.Chains[e]; ok && !g.chaining[e] {
		return g.chain(e, wrap)
	}
	return g.value(e)
}

// Returns the C code of [e] itself, before any conversion to an optional.
func (g *gen) value(e ast.Expr) string {
	t := g.info.Types[e]
	switch e := e.(type) {
	case *ast.IntLit:
		v, _ := strconv.ParseInt(e.Value, 0, 64)
		if t == types.Float {
			return strconv.FormatInt(v, 10) + ".0"
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
			if u.Narrows != nil {
				return fmt.Sprintf("((%s)%s)", ctype(u.Type), g.varName(u.Narrows))
			}
			return g.varName(u)
		case *types.Field:
			return g.selfRef() + "->f_" + u.Name
		case *types.Property:
			return g.memberCall(u.Get, g.selfType(), g.selfRef())
		case *types.SelfMember:
			return g.expr(u.Expr)
		case *types.Func:
			if g.info.ThrowingRefs[e] {
				g.throwingValues[u] = true
				return "((vx_fn){(void *)fntv_" + funcName(u) + ", NULL})"
			}
			g.fnValues[u] = true
			return "((vx_fn){(void *)fnv_" + funcName(u) + ", NULL})"
		case *types.Const:
			return g.constRef(u)
		}
	case *ast.TypeArgsExpr:
		return g.expr(e.X)
	case *ast.FuncLit:
		return g.funcLit(e)
	case *ast.ImplicitMember:
		return caseFunc(g.info.CaseValues[e]) + "()"
	case *ast.TryExpr:
		return g.tryExpr(e)
	case *ast.AwaitExpr:
		return g.await(e)
	case *ast.RecordLit:
		return g.recordLit(e, t.(*types.Record))
	case *ast.MatchExpr:
		return g.matchExpr(e)
	case *ast.IfExpr:
		return g.ifValue(e)
	case *ast.TypeTest:
		return g.typeTest(e)
	case *ast.ListLit:
		if e.Body != nil {
			return g.collection(e, e.Body, t)
		}
		elem := t.(*types.List).Elem
		if len(e.Elems) == 0 {
			if g.constMode {
				return "vx_list_freeze(" + newList(elem, "0", "0", "NULL") + ")"
			}
			return newList(elem, "0", "0", "NULL")
		}
		return g.ordered(e.Elems, func(cs []string) string {
			l := newList(elem, strconv.Itoa(len(cs)), "0", fmt.Sprintf("(%s[]){%s}", ctype(elem), strings.Join(cs, ", ")))
			if g.constMode {
				return "vx_list_freeze(" + l + ")"
			}
			return l
		})
	case *ast.UnaryExpr:
		if f := g.info.Operators[e]; f != nil {
			return g.memberCall(f, g.info.Types[e.X], g.expr(e.X))
		}
		if types.IsMinInt(e) && t == types.Int {
			return "INT64_MIN"
		}
		if _, lit := e.X.(*ast.IntLit); e.Op == lexer.Minus && t == types.Int && !lit {
			return fmt.Sprintf("vx_neg(%s, %d)", g.expr(e.X), g.lineOf(e))
		}
		return "(" + op(e.Op) + g.expr(e.X) + ")"
	case *ast.NilLit:
		return "((" + ctype(t) + "){0})"
	case *ast.BinaryExpr:
		if e.Op == lexer.AndAnd || e.Op == lexer.OrOr {
			return fmt.Sprintf("(%s %s %s)", g.expr(e.X), op(e.Op), g.expr(e.Y))
		}
		if e.Op == lexer.QuestionQuestion {
			return g.coalesce(e)
		}
		if _, isNil := e.Y.(*ast.NilLit); isNil && (e.Op == lexer.Eq || e.Op == lexer.NotEq) {
			return g.nilTest(e.Op, e.X)
		}
		if _, isNil := e.X.(*ast.NilLit); isNil && (e.Op == lexer.Eq || e.Op == lexer.NotEq) {
			return g.nilTest(e.Op, e.Y)
		}
		operand := g.info.Types[e.X]
		if f := g.info.Operators[e]; f != nil {
			return g.ordered([]ast.Expr{e.X, e.Y}, func(cs []string) string {
				return g.operator(e.Op, f, operand, cs[0], cs[1])
			})
		}
		if operand == types.Int && g.rng != nil && g.rng.safe(e.Op, e.X, e.Y) {
			return g.ordered([]ast.Expr{e.X, e.Y}, func(cs []string) string {
				return unchecked(e.Op, cs[0], cs[1])
			})
		}
		return g.ordered([]ast.Expr{e.X, e.Y}, func(cs []string) string {
			return binop(e.Op, operand, cs[0], cs[1], g.lineOf(e))
		})
	case *ast.CallExpr:
		return g.call(e)
	case *ast.IndexExpr:
		if m, ok := g.info.Types[e.X].(*types.Map); ok {
			return g.ordered([]ast.Expr{e.X, e.Index}, func(cs []string) string {
				k, p := g.temp(), g.temp()
				return fmt.Sprintf("({ %s = %s; %s *%s = vx_map_find(%s, &%s); %s ? %s : (%s){0}; })",
					decl(m.Key, k), cs[1], ctype(m.Value), p, cs[0], k, p, wrapOpt(t.(*types.Optional), "*"+p), ctype(t))
			})
		}
		if f := g.info.Operators[e]; f != nil {
			return g.ordered([]ast.Expr{e.X, e.Index}, func(cs []string) string {
				return g.memberCall(f, g.info.Types[e.X], cs[0], cs[1])
			})
		}
		if g.rng != nil && g.rng.inBounds(e.X, e.Index) {
			return g.ordered([]ast.Expr{e.X, e.Index}, func(cs []string) string {
				return fmt.Sprintf("(((%s *)%s->data)[%s])", ctype(t), cs[0], cs[1])
			})
		}
		return g.ordered([]ast.Expr{e.X, e.Index}, func(cs []string) string {
			return fmt.Sprintf("(*VX_AT(%s, %s, %s, %d))", ctype(t), cs[0], cs[1], g.lineOf(e))
		})
	case *ast.MapLit:
		if e.Body != nil {
			return g.collection(e, e.Body, t)
		}
		if g.constMode {
			return "vx_map_freeze(" + g.mapLit(e, t.(*types.Map)) + ")"
		}
		return g.mapLit(e, t.(*types.Map))
	case *ast.KnownExpr:
		return g.expr(e.X)
	case *ast.MemberExpr:
		if f := g.info.TearOffs[e]; f != nil {
			return g.expr(f)
		}
		if cs := g.info.CaseValues[e]; cs != nil {
			return caseFunc(cs) + "()"
		}
		m := g.info.Members[e]
		if m.Const != nil {
			return g.constRef(m.Const)
		}
		switch m.Builtin {
		case "int.max":
			return "INT64_MAX"
		case "int.min":
			return "INT64_MIN"
		case "float.infinity":
			return "((double)INFINITY)"
		case "float.nan":
			return "((double)NAN)"
		case "isEmpty", "first", "last":
			return g.listProperty(e, m.Builtin, g.info.Types[e.X].(*types.List), g.expr(e.X))
		case "isNaN":
			return "((bool)isnan(" + g.expr(e.X) + "))"
		case "isFinite":
			return "((bool)isfinite(" + g.expr(e.X) + "))"
		}
		x := g.expr(e.X)
		if m.RecordField != "" {
			return fmt.Sprintf("(%s).%s", x, recordField(m.RecordField))
		}
		if m.Property != nil {
			return g.memberCall(m.Property.Get, g.info.Types[e.X], x)
		}
		if m.Field != nil {
			return fmt.Sprintf("%s->f_%s", x, m.Field.Name)
		}
		if g.info.Types[e.X] == types.String {
			return "vx_str_len(" + x + ")"
		}
		switch m.Builtin {
		case "keys":
			return "vx_map_list(" + x + ", false)"
		case "values":
			return "vx_map_list(" + x + ", true)"
		}
		return x + "->len"
	}
	panic(fmt.Sprintf("codegen: unexpected expression %T at %s", e, e.Pos()))
}

func op(k lexer.Kind) string { return strings.Trim(k.String(), "'") }

// Returns C code applying the binary operator [k] to [a] and [b] with
// operator [f] of struct [t]. `!=` negates `==`, and the other orderings
// come from `<`.
func (g *gen) operator(k lexer.Kind, f *types.Func, t types.Type, a, b string) string {
	switch k {
	case lexer.NotEq, lexer.GtEq:
		return "(!" + g.memberCall(f, t, a, b) + ")"
	case lexer.Gt:
		return g.memberCall(f, t, b, a)
	case lexer.LtEq:
		return "(!" + g.memberCall(f, t, b, a) + ")"
	}
	return g.memberCall(f, t, a, b)
}

// Returns the type of the C value assigned by [s]: the target's, or for a
// struct's compound operator, its parameter's.
func (g *gen) valueType(s *ast.AssignStmt) types.Type {
	if f := g.info.Operators[s]; f != nil {
		return f.Params[0].Type
	}
	return g.info.Types[s.Target]
}

// Returns C code applying the compound assignment [s], whose operator is
// [k], to the old value [old] of type [t] and the assigned value [v].
func (g *gen) compound(s *ast.AssignStmt, k lexer.Kind, t types.Type, old, v string) string {
	if f := g.info.Operators[s]; f != nil {
		return g.memberCall(f, t, old, v)
	}
	if t == types.Int && g.rng != nil && g.rng.safe(k, s.Target, s.Value) {
		return unchecked(k, old, v)
	}
	return binop(k, t, old, v, g.lineOf(s))
}

// Returns C code applying the int operator [k] to [a] and [b] without
// checks, for operands the range analysis proved safe.
func unchecked(k lexer.Kind, a, b string) string {
	if k == lexer.Shl {
		return fmt.Sprintf("VX_SHL(%s, %s)", a, b)
	}
	return fmt.Sprintf("(%s %s %s)", a, op(k), b)
}

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
	if _, ok := t.(*types.Enum); ok {
		return fmt.Sprintf("((%s)->tag %s (%s)->tag)", a, op(k), b)
	}
	if r, ok := t.(*types.Record); ok {
		if k == lexer.NotEq {
			return "(!" + equal(r, a, b) + ")"
		}
		return "(" + equal(r, a, b) + ")"
	}
	if t == types.Int {
		switch k {
		case lexer.Plus:
			return fmt.Sprintf("vx_add(%s, %s, %d)", a, b, line)
		case lexer.Minus:
			return fmt.Sprintf("vx_sub(%s, %s, %d)", a, b, line)
		case lexer.Star:
			return fmt.Sprintf("vx_mul(%s, %s, %d)", a, b, line)
		case lexer.AmpPlus:
			return fmt.Sprintf("VX_WRAP(%s, +, %s)", a, b)
		case lexer.AmpMinus:
			return fmt.Sprintf("VX_WRAP(%s, -, %s)", a, b)
		case lexer.AmpStar:
			return fmt.Sprintf("VX_WRAP(%s, *, %s)", a, b)
		case lexer.Slash:
			return fmt.Sprintf("vx_div(%s, %s, %d)", a, b, line)
		case lexer.Percent:
			return fmt.Sprintf("vx_mod(%s, %s, %d)", a, b, line)
		case lexer.Shl:
			return fmt.Sprintf("vx_shl(%s, %s, %d)", a, b, line)
		case lexer.Shr:
			return fmt.Sprintf("vx_shr(%s, %s, %d)", a, b, line)
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
	code := g.callCode(e)
	if thrown := g.info.Throwing[e]; thrown != nil {
		return g.throwingCall(e, code, thrown)
	}
	return code
}

func (g *gen) callCode(e *ast.CallExpr) string {
	c := g.info.Calls[e]
	line := g.lineOf(e)
	rank := sourceRank(e, c.Args)
	switch c.Kind {
	case types.FuncCall, types.StaticCall:
		return g.orderedBy(c.Args, rank, func(cs []string) string {
			return g.callee(e, c.Func) + "(" + g.args(e, cs) + ")"
		})
	case types.ConstructorCall:
		return g.orderedBy(c.Args, rank, func(cs []string) string {
			return "new_" + structName(c.Struct) + "(" + strings.Join(cs, ", ") + ")"
		})
	case types.CaseCall:
		return g.orderedBy(c.Args, rank, func(cs []string) string {
			return caseFunc(c.Case) + "(" + strings.Join(cs, ", ") + ")"
		})
	case types.MethodCall:
		if c.Recv == nil {
			return g.orderedBy(c.Args, rank, func(cs []string) string {
				return g.callee(e, c.Func) + "(" + g.args(e, append([]string{g.selfRef()}, cs...)) + ")"
			})
		}
		return g.orderedBy(append([]ast.Expr{c.Recv}, c.Args...), append([]int{-1}, rank...), func(cs []string) string {
			return g.callee(e, c.Func) + "(" + g.args(e, cs) + ")"
		})
	case types.InterfaceCall:
		return g.ifaceCall(e, c, rank)
	case types.BuiltinCall:
		switch c.Builtin {
		case "all", "any", "run":
			return g.asyncBuiltin(e, c)
		case "isolate.run", "isolate.spawn":
			return g.isolateCall(e, c)
		case "Channel":
			return "vx_chan_new()"
		}
		return g.builtin(c, g.info.Types[e], line)
	case types.MemberBuiltinCall:
		return g.memberBuiltin(e, c, line)
	case types.ValueCall:
		fp := fnPtrType(g.info.Types[c.Callee].(*types.FuncType))
		return g.ordered(append([]ast.Expr{c.Callee}, c.Args...), func(cs []string) string {
			f := g.temp()
			args := ""
			for _, a := range cs[1:] {
				args += ", " + a
			}
			if g.info.Throwing[e] != nil {
				args += ", &" + g.tries[len(g.tries)-1].err
			}
			return fmt.Sprintf("({ vx_fn %s = %s; ((%s)%s.fn)(%s.env%s); })", f, cs[0], fp, f, f, args)
		})
	}
	panic("codegen: unknown call kind")
}

func (g *gen) builtin(c *types.Call, result types.Type, line int) string {
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
		case "str.radix":
			return fmt.Sprintf("vx_int_str_radix(%s, %s, %d)", cs[0], cs[1], line)
		case "int.parse", "float.parse":
			t, v := ctype(result), g.temp()
			if c.Builtin == "int.parse" {
				return fmt.Sprintf("({ int64_t %s; bool ok = vx_int_parse(%s, %s, %d, &%s); (%s){ok, %s}; })", v, cs[0], arg(1, "10"), line, v, t, v)
			}
			return fmt.Sprintf("({ double %s; bool ok = vx_float_parse(%s, &%s); (%s){ok, %s}; })", v, cs[0], v, t, v)
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

func (g *gen) memberBuiltin(e *ast.CallExpr, c *types.Call, line int) string {
	recv := g.info.Types[c.Recv]
	return g.ordered(append([]ast.Expr{c.Recv}, c.Args...), func(cs []string) string {
		if ch, ok := recv.(*types.Channel); ok {
			return g.channelCall(e, c, ch, cs, line)
		}
		if recv == types.Float && c.Builtin == "toFixed" {
			return fmt.Sprintf("vx_float_fixed(%s, %s, %d)", cs[0], cs[1], line)
		}
		if m, ok := recv.(*types.Map); ok {
			k := g.temp()
			switch c.Builtin {
			case "contains":
				return fmt.Sprintf("({ %s = %s; vx_map_find(%s, &%s) != NULL; })", decl(m.Key, k), cs[1], cs[0], k)
			case "remove":
				r, v, o := g.temp(), g.temp(), g.optOf(e)
				return fmt.Sprintf("({ %s = %s; %s %s = {0}; %s; if (vx_map_remove(vx_map_mut(%s, %d), &%s, &%s)) %s = %s; %s; })",
					decl(m.Key, k), cs[1], ctype(o), r, decl(m.Value, v), cs[0], line, k, v, r, wrapOpt(o, v), r)
			case "copy":
				return "vx_map_copy(" + cs[0] + ")"
			}
		}
		if l, ok := recv.(*types.List); ok {
			if s := g.listMember(e, c, l, cs, line); s != "" {
				return s
			}
			elem := ctype(l.Elem)
			switch c.Builtin {
			case "push":
				return push(l.Elem, fmt.Sprintf("vx_list_mut(%s, %d)", cs[0], line), cs[1])
			case "pop":
				return fmt.Sprintf("(*(%s*)vx_list_pop(vx_list_mut(%s, %d), %d))", elem, cs[0], line, line)
			case "copy":
				return "vx_list_copy(" + cs[0] + ")"
			case "filter", "map":
				list, f, i, x, out := g.temp(), g.temp(), g.temp(), g.temp(), g.temp()
				ft := g.info.Types[c.Args[0]].(*types.FuncType)
				call := fmt.Sprintf("((%s)%s.fn)(%s.env, %s)", fnPtrType(ft), f, f, x)
				resultElem := ft.Result
				body := fmt.Sprintf("if (%s) %s;", call, push(l.Elem, out, x))
				if c.Builtin == "map" {
					body = push(resultElem, out, call) + ";"
				} else {
					resultElem = l.Elem
				}
				// Like a for loop, it visits the elements there were at the
				// start, and stops early if some are removed.
				n := g.temp()
				capacity := "0"
				if c.Builtin == "map" {
					capacity = n
				}
				return fmt.Sprintf("({ vx_list *%s = %s; vx_fn %s = %s; int64_t %s = %s->len; vx_list *%s = %s; "+
					"for (int64_t %s = 0; %s < %s && %s < %s->len; %s++) { %s = ((%s*)%s->data)[%s]; %s } %s; })",
					list, cs[0], f, cs[1], n, list, out, newList(resultElem, "0", capacity, "NULL"),
					i, i, n, i, list, i, decl(l.Elem, x), elem, list, i, body, out)
			case "contains":
				list, v, i, found := g.temp(), g.temp(), g.temp(), g.temp()
				item := fmt.Sprintf("((%s*)%s->data)[%s]", elem, list, i)
				eq := equal(l.Elem, item, v)
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
		case "replace":
			return fmt.Sprintf("vx_str_replace(%s, %s, %s, %d)", cs[0], cs[1], cs[2], line)
		case "trim", "chars", "lines":
			return fmt.Sprintf("vx_str_%s(%s)", c.Builtin, cs[0])
		case "trimStart", "trimEnd", "startsWith", "endsWith":
			name := map[string]string{"trimStart": "trim_start", "trimEnd": "trim_end", "startsWith": "starts_with", "endsWith": "ends_with"}[c.Builtin]
			return fmt.Sprintf("vx_str_%s(%s)", name, strings.Join(cs, ", "))
		case "indexOf":
			i := g.temp()
			return fmt.Sprintf("({ int64_t %s = vx_str_index_of(%s, %s); (%s){%s >= 0, %s}; })", i, cs[0], cs[1], ctype(g.info.Types[e]), i, i)
		case "substring":
			return fmt.Sprintf("vx_str_substring(%s, %s, %s, %d)", cs[0], cs[1], cs[2], line)
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

// Declares the local variable [v] with the C value [value], on the heap
// when closures share it.
func (g *gen) bindVar(v *types.Var, value string) {
	if v.Boxed() {
		name := g.declare(v)
		g.line("%s = vx_alloc(sizeof(%s));", declPtr(v.Type, name), ctype(v.Type))
		g.line("*%s = %s;", name, value)
		if !flat(v.Type) {
			g.line("vx_remember(%s);", name)
		}
		g.vals[v], g.boxes[v] = "(*"+name+")", name
		return
	}
	g.line("%s = %s;", decl(v.Type, g.declare(v)), value)
}
