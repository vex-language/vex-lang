package codegen

import (
	"fmt"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/lexer"
	"github.com/vex-language/vex-lang/compiler/types"
)

// Returns C code telling whether [a] goes before [b], two values of [t],
// by the `<` of [t].
func (g *gen) less(t types.Type, a, b string) string {
	switch s := t.(type) {
	case *types.Struct:
		return g.operator(lexer.Lt, s.Operator("<"), t, a, b)
	case *types.Enum:
		if f := s.Operator("<"); f != nil {
			return g.operator(lexer.Lt, f, t, a, b)
		}
	}
	return binop(lexer.Lt, t, a, b, 0)
}

// Returns the name of a C comparison for [vx_list_sort] over values of
// [t]: by their `<`, or with [by] set, by the closure passed as the
// context. Each is generated once.
func (g *gen) lessFunc(t types.Type, by bool) string {
	name := "less_" + mangle(t)
	if by {
		name = "lessby_" + mangle(t)
	}
	if g.codecs[name] {
		return name
	}
	g.codecs[name] = true
	ct := ctype(t)
	a, b := fmt.Sprintf("(*(%s *)a)", ct), fmt.Sprintf("(*(%s *)b)", ct)
	var body string
	if by {
		ft := &types.FuncType{Params: []types.Type{t, t}, Result: types.Bool}
		body = fmt.Sprintf("vx_fn *f = ctx; return ((%s)f->fn)(f->env, %s, %s);", fnPtrType(ft), a, b)
	} else {
		body = "(void)ctx; return " + g.less(t, a, b) + ";"
	}
	fmt.Fprintf(&g.codecDefs, "static bool %s(void *ctx, const void *a, const void *b) { %s }\n", name, body)
	return name
}

// Returns C code for the built-in list member [c] of [e] on a list of [l],
// with [cs] the receiver and arguments, or "" if [c] isn't one of these.
// Members that call a function visit the elements there were at the start
// and stop early if some are removed, as a for loop does.
func (g *gen) listMember(e *ast.CallExpr, c *types.Call, l *types.List, cs []string, line int) string {
	elem := ctype(l.Elem)
	list, i, x := g.temp(), g.temp(), g.temp()
	at := fmt.Sprintf("((%s*)%s->data)[%s]", elem, list, i)
	// The start of a loop over the elements, and a call of the function
	// value [f] of type [ft] with [args].
	loop := func(n string) string {
		return fmt.Sprintf("int64_t %s = %s->len; for (int64_t %s = 0; %s < %s && %s < %s->len; %s++) { %s = %s; ",
			n, list, i, i, n, i, list, i, decl(l.Elem, x), at)
	}
	call := func(f string, arg ast.Expr, args ...string) string {
		s := fmt.Sprintf("((%s)%s.fn)(%s.env", fnPtrType(g.info.Types[arg].(*types.FuncType)), f, f)
		for _, a := range args {
			s += ", " + a
		}
		return s + ")"
	}
	switch c.Builtin {
	case "sort", "sorted":
		start := fmt.Sprintf("vx_list_mut(%s, %d)", cs[0], line)
		if c.Builtin == "sorted" {
			start = "vx_list_copy(" + cs[0] + ")"
		}
		result := ""
		if c.Builtin == "sorted" {
			result = " " + list + ";"
		}
		if c.Args[0] == nil {
			return fmt.Sprintf("({ vx_list *%s = %s; vx_list_sort(%s, %s, NULL, %d);%s })", list, start, list, g.lessFunc(l.Elem, false), line, result)
		}
		f := g.temp()
		return fmt.Sprintf("({ vx_list *%s = %s; vx_fn %s = %s; vx_list_sort(%s, %s, &%s, %d);%s })",
			list, start, f, cs[1], list, g.lessFunc(l.Elem, true), f, line, result)
	case "reversed":
		return "vx_list_reversed(" + cs[0] + ")"
	case "insert":
		return fmt.Sprintf("({ vx_list *%s = vx_list_mut(%s, %d); %s = %s; vx_list_insert(%s, &%s, %s, %d); })",
			list, cs[0], line, decl(l.Elem, x), cs[1], list, x, cs[2], line)
	case "remove":
		return fmt.Sprintf("({ %s; vx_list_remove_at(vx_list_mut(%s, %d), %s, %d, &%s); %s; })", decl(l.Elem, x), cs[0], line, cs[1], line, x, x)
	case "removeWhere":
		f, n, k := g.temp(), g.temp(), g.temp()
		barrier := ""
		if !flat(l.Elem) {
			barrier = fmt.Sprintf(" vx_remember(%s->data);", list)
		}
		changed := fmt.Sprintf("if (%s->len != %s) vx_fail(%d, \"the list changed while removeWhere was running\");", list, n, line)
		return fmt.Sprintf("({ vx_list *%s = vx_list_mut(%s, %d); vx_fn %s = %s; int64_t %s = %s->len, %s = 0; "+
			"for (int64_t %s = 0; %s < %s; %s++) { %s = %s; bool keep = !%s; %s if (keep) ((%s*)%s->data)[%s++] = %s; } %s->len = %s;%s })",
			list, cs[0], line, f, cs[1], n, list, k,
			i, i, n, i, decl(l.Elem, x), at, call(f, c.Args[0], x), changed, elem, list, k, x, list, k, barrier)
	case "indexOf":
		v, r := g.temp(), g.temp()
		return fmt.Sprintf("({ vx_list *%s = %s; %s = %s; int64_t %s = -1; "+
			"for (int64_t %s = 0; %s < %s->len; %s++) if (%s) { %s = %s; break; } (%s){%s >= 0, %s}; })",
			list, cs[0], decl(l.Elem, v), cs[1], r, i, i, list, i, g.eq(l.Elem, at, v), r, i, ctype(g.info.Types[e]), r, r)
	case "find":
		f, n, r, o := g.temp(), g.temp(), g.temp(), g.optOf(e)
		return fmt.Sprintf("({ vx_list *%s = %s; vx_fn %s = %s; %s %s = {0}; %sif (%s) { %s = %s; break; } } %s; })",
			list, cs[0], f, cs[1], ctype(o), r, loop(n), call(f, c.Args[0], x), r, wrapOpt(o, x), r)
	case "any", "all":
		f, n, r := g.temp(), g.temp(), g.temp()
		test, start := call(f, c.Args[0], x), "false"
		if c.Builtin == "all" {
			test, start = "!"+test, "true"
		}
		return fmt.Sprintf("({ vx_list *%s = %s; vx_fn %s = %s; bool %s = %s; %sif (%s) { %s = !%s; break; } } %s; })",
			list, cs[0], f, cs[1], r, start, loop(n), test, r, start, r)
	case "join":
		// Showing an element may collect, after which the parts array is
		// old, so each write goes through the barrier.
		sep, parts, k := g.temp(), g.temp(), g.temp()
		separator := `VX_LIT("")`
		if c.Args[0] != nil {
			separator = cs[1]
		}
		return fmt.Sprintf("({ vx_list *%s = %s; vx_str %s = %s; vx_str *%s = vx_alloc(sizeof(vx_str) * (size_t)(2 * %s->len + 1)); int %s = 0; "+
			"for (int64_t %s = 0; %s < %s->len; %s++) { if (%s > 0) %s[%s++] = %s; %s[%s++] = %s; vx_remember(%s); } vx_str_join(%s, %s); })",
			list, cs[0], sep, separator, parts, list, k,
			i, i, list, i, i, parts, k, sep, parts, k, show(l.Elem, at), parts, k, parts)
	case "clear":
		return fmt.Sprintf("((void)(vx_list_mut(%s, %d)->len = 0))", cs[0], line)
	case "min", "max":
		best, r, o := g.temp(), g.temp(), g.optOf(e)
		better := g.less(l.Elem, x, best)
		if c.Builtin == "max" {
			better = g.less(l.Elem, best, x)
		}
		return fmt.Sprintf("({ vx_list *%s = %s; %s %s = {0}; if (%s->len > 0) { %s = ((%s*)%s->data)[0]; "+
			"for (int64_t %s = 1; %s < %s->len; %s++) { %s = %s; if (%s) %s = %s; } %s = %s; } %s; })",
			list, cs[0], ctype(o), r, list, decl(l.Elem, best), elem, list,
			i, i, list, i, decl(l.Elem, x), at, better, best, x, r, wrapOpt(o, best), r)
	case "sum":
		s := g.temp()
		add := fmt.Sprintf("%s = vx_add(%s, %s, %d)", s, s, at, line)
		if l.Elem == types.Float {
			add = fmt.Sprintf("%s += %s", s, at)
		}
		return fmt.Sprintf("({ vx_list *%s = %s; %s %s = 0; for (int64_t %s = 0; %s < %s->len; %s++) %s; %s; })",
			list, cs[0], elem, s, i, i, list, i, add, s)
	case "slice":
		return fmt.Sprintf("vx_list_slice(%s, %s, %s, %d)", cs[0], cs[1], cs[2], line)
	case "flatMap":
		f, n, out := g.temp(), g.temp(), g.temp()
		res := g.info.Types[e].(*types.List)
		return fmt.Sprintf("({ vx_list *%s = %s; vx_fn %s = %s; vx_list *%s = %s; %svx_list_append(%s, %s); } %s; })",
			list, cs[0], f, cs[1], out, newList(res.Elem, "0", "0", "NULL"), loop(n), out, call(f, c.Args[0], x), out)
	case "reduce":
		acc, f, n := g.temp(), g.temp(), g.temp()
		r := g.info.Types[e]
		return fmt.Sprintf("({ vx_list *%s = %s; %s = %s; vx_fn %s = %s; %s%s = %s; } %s; })",
			list, cs[0], decl(r, acc), cs[1], f, cs[2], loop(n), acc, call(f, c.Args[1], acc, x), acc)
	}
	return ""
}

// Returns C code for the list property [name] of [x], a list of [l]
// read by [e], or "" if [name] isn't one of these.
func (g *gen) listProperty(e *ast.MemberExpr, name string, l *types.List, x string) string {
	switch name {
	case "isEmpty":
		return "((" + x + ")->len == 0)"
	case "first", "last":
		list, o := g.temp(), g.optOf(e)
		i := "0"
		if name == "last" {
			i = list + "->len - 1"
		}
		return fmt.Sprintf("({ vx_list *%s = %s; %s->len > 0 ? %s : (%s){0}; })",
			list, x, list, wrapOpt(o, fmt.Sprintf("((%s*)%s->data)[%s]", ctype(l.Elem), list, i)), ctype(o))
	}
	return ""
}

// Returns [x], the C code of [e], converted to the type the checker chose
// for it when a list or map is used as one with other element types.
func (g *gen) converted(e ast.Expr, x string) string {
	if to := g.info.Converts[e]; to != nil {
		return g.convert(to, g.info.Types[e], x)
	}
	return x
}

// Returns C code converting [x], a value of [from], to [to]. Lists and maps
// are copied, so the original can't be changed through the copy.
func (g *gen) convert(to, from types.Type, x string) string {
	if types.Identical(to, from) {
		return x
	}
	switch t := to.(type) {
	case *types.Optional:
		f, ok := from.(*types.Optional)
		if !ok {
			return wrapOpt(t, g.convert(t.Elem, from, x))
		}
		tmp := g.temp()
		return fmt.Sprintf("({ %s = %s; %s ? %s : (%s){0}; })", decl(f, tmp), x, present(f, tmp),
			wrapOpt(t, g.convert(t.Elem, f.Elem, unwrap(f, tmp))), ctype(t))
	case *types.List, *types.Map:
		return g.convertFunc(to, from) + "(" + x + ")"
	}
	// Interface values and functions share their C value.
	return x
}

// Returns the name of a C function copying a list or map of [from] into
// one of [to], converting each element. Each is generated once.
func (g *gen) convertFunc(to, from types.Type) string {
	name := "conv_" + mangle(from) + "__" + mangle(to)
	if g.codecs[name] {
		return name
	}
	g.codecs[name] = true
	var body string
	switch t := to.(type) {
	case *types.List:
		f := from.(*types.List)
		v := g.convert(t.Elem, f.Elem, fmt.Sprintf("((%s *)src->data)[i]", ctype(f.Elem)))
		body = fmt.Sprintf("vx_list *l = %s; for (int64_t i = 0; i < src->len; i++) %s; return l;",
			newList(t.Elem, "0", "src->len", "NULL"), push(t.Elem, "l", v))
		fmt.Fprintf(&g.codecDefs, "static vx_list *%s(vx_list *src) { %s }\n", name, body)
	case *types.Map:
		f := from.(*types.Map)
		v := g.convert(t.Value, f.Value, fmt.Sprintf("(*(%s *)(e + src->voff))", ctype(f.Value)))
		body = fmt.Sprintf("vx_map *m = vx_map_new(src->kind, src->ksz, sizeof(%s)); m->khash = src->khash; m->keq = src->keq; "+
			"for (int64_t i = 0; i < src->used; i++) { char *e = VX_MAP_ENTRY(src, i); if (VX_MAP_SEQ(e) == 0) continue; "+
			"%s = %s; *(%s *)vx_map_set(m, e + VX_MAP_KEY) = v; } return m;",
			ctype(t.Value), decl(t.Value, "v"), v, ctype(t.Value))
		fmt.Fprintf(&g.codecDefs, "static vx_map *%s(vx_map *src) { %s }\n", name, body)
	}
	return name
}
