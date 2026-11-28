package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/types"
)

// A generator's C function returns an Iterable object holding its
// arguments. Each iterator the object gives runs the body afresh as a
// task, which takes the iterator as `vx_yg` and pauses at each yield.

// Writes the generator [f]: its body, its Iterable object and descriptor,
// and the function that makes the object.
func (g *gen) generatorFunction(f *types.Func) {
	name := funcName(f)
	it := f.Result.(*types.Interface)
	iter := types.IteratorOf(it)
	elem := it.Args[0]
	g.iteratorDesc(iter, elem)

	g.result, g.yields, g.generating = types.Void, elem, true
	defer func() { g.generating = false }()
	g.rng = newRanges(g.info, f.Decl.Body)
	if self := g.info.Selves[f.Decl]; self != nil {
		g.self = self
		g.vals[self] = "self"
	}
	params := append([]string{"vx_gen *vx_yg"}, g.params(f, true)...)
	g.line("")
	g.line("static void bd_%s(%s) {", name, strings.Join(params, ", "))
	g.ind++
	g.enterFrame(traceName(f))
	g.stmtsIn(f.Decl.Body.Stmts, nil)
	g.ind--
	g.line("}")

	ts, ns := spawnParams(f)
	fields := []string{"const vx_desc *vx_t;"}
	args := []string{"g"}
	var sig []string
	for i := range ts {
		fields = append(fields, ts[i]+" "+ns[i]+";")
		args = append(args, "a->"+ns[i])
		sig = append(sig, ts[i]+" "+ns[i])
	}
	if len(sig) == 0 {
		sig = []string{"void"}
	}
	g.line("typedef struct { %s } gi_%s;", strings.Join(fields, " "), name)
	g.line("static void en_%s(void *p) {", name)
	g.line("    vx_gen *g = p;")
	g.line("    gi_%s *a = g->src;", name)
	g.line("    bd_%s(%s);", name, strings.Join(args, ", "))
	g.line("}")
	g.line("static void *git_%s(void *self) { return vx_gen_new(self, en_%s, &desc_gen_%s, %d); }", name, name, ifaceName(iter), g.lineOf(f.Decl))
	g.line("static void wrv_gi_%s(vx_buf *b, void *v) { (void)v; %s }", name, writeLit("b", it.String()))
	g.line("static void *const itab_gi_%s[] = { %s };", name, tableOf(it, map[string]string{"iterator": "git_" + name}))
	g.line("static const vx_desc desc_gi_%s = { wrv_gi_%s, { [%d] = itab_gi_%s } };", name, name, it.Index, name)
	g.line("static void *%s(%s) {", name, strings.Join(sig, ", "))
	g.line("    gi_%s *a = vx_alloc(sizeof(gi_%s));", name, name)
	g.line("    a->vx_t = &desc_gi_%s;", name)
	for _, n := range ns {
		g.line("    a->%s = %s;", n, n)
	}
	g.line("    return a;")
	g.line("}")
}

// Writes, once per element type, the descriptor of the iterators that
// generators of [elem] give, as values of [iter], an `Iterator<T>`.
func (g *gen) iteratorDesc(iter *types.Interface, elem types.Type) {
	id := ifaceName(iter)
	if g.genDescs == nil {
		g.genDescs = map[string]bool{}
	}
	if g.genDescs[id] {
		return
	}
	g.genDescs[id] = true
	opt := iter.Method("next").Result.(*types.Optional)
	none := "NULL"
	if !nullable(opt) {
		none = fmt.Sprintf("((%s){0})", ctype(opt))
	}
	g.line("")
	g.line("static %s {", decl(opt, "gnx_"+id+"(void *self)"))
	g.line("    %s;", decl(elem, "v"))
	g.line("    return vx_gen_next(self, &v) ? %s : %s;", wrapOpt(opt, "v"), none)
	g.line("}")
	g.line("static void wrv_gen_%s(vx_buf *b, void *v) { (void)v; %s }", id, writeLit("b", iter.String()))
	g.line("static void *const itab_gen_%s[] = { %s };", id, tableOf(iter, map[string]string{"next": "gnx_" + id}))
	g.line("static const vx_desc desc_gen_%s = { wrv_gen_%s, { [%d] = itab_gen_%s } };", id, id, iter.Index, id)
}

// Returns the entries of the method table of [i], filled from [fns], the
// C functions of its methods by name.
func tableOf(i *types.Interface, fns map[string]string) string {
	var entries []string
	for _, m := range i.Methods {
		if fn := fns[m.Name]; fn != "" && m.InTable() {
			entries = append(entries, "(void *)"+fn)
		} else {
			entries = append(entries, "NULL")
		}
	}
	if len(entries) == 0 {
		return "NULL"
	}
	return strings.Join(entries, ", ")
}
