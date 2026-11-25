package codegen

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/types"
)

// Values sent between isolates are encoded by a snd_ function for their
// type and decoded by a rcv_ function, both generated on first use.

// Returns the mangled name of [t], making sure its encoder and decoder
// are generated.
func (g *gen) codec(t types.Type) string {
	m := mangle(t)
	if g.codecs[m] {
		return m
	}
	g.codecs[m] = true
	ct := ctype(t)
	fmt.Fprintf(&g.codecDecls, "static void snd_%s(vx_enc *e, %s);\n", m, decl(t, "v"))
	fmt.Fprintf(&g.codecDecls, "static %s rcv_%s(vx_dec *d);\n", ct, m)
	var snd, rcv strings.Builder
	switch t := t.(type) {
	case *types.Optional:
		e := g.codec(t.Elem)
		fmt.Fprintf(&snd, "vx_enc_put(e, &v.has, 1); if (v.has) snd_%s(e, v.v);", e)
		fmt.Fprintf(&rcv, "%s r = {0}; vx_dec_get(d, &r.has, 1); if (r.has) r.v = rcv_%s(d); return r;", ct, e)
	case *types.List:
		e := g.codec(t.Elem)
		fmt.Fprintf(&snd, "if (vx_enc_ref(e, v)) return; vx_enc_i64(e, v->len); "+
			"for (int64_t i = 0; i < v->len; i++) snd_%s(e, ((%s *)v->data)[i]);", e, ctype(t.Elem))
		fmt.Fprintf(&rcv, "vx_list *o = vx_dec_ref(d); if (o) return o; int64_t n = vx_dec_i64(d); "+
			"o = vx_list_new(sizeof(%s), 0, NULL); vx_dec_obj(d, o); "+
			"for (int64_t i = 0; i < n; i++) { %s = rcv_%s(d); vx_list_push(o, &x); } return o;", ctype(t.Elem), decl(t.Elem, "x"), e)
	case *types.Map:
		k, v := g.codec(t.Key), g.codec(t.Value)
		fmt.Fprintf(&snd, "if (vx_enc_ref(e, v)) return; vx_enc_i64(e, v->len); vx_map_iter it = vx_map_iter_new(v); "+
			"for (char *en; (en = vx_map_next(v, &it));) { snd_%s(e, *(%s *)(en + VX_MAP_KEY)); snd_%s(e, *(%s *)(en + v->voff)); }",
			k, ctype(t.Key), v, ctype(t.Value))
		fmt.Fprintf(&rcv, "vx_map *o = vx_dec_ref(d); if (o) return o; int64_t n = vx_dec_i64(d); o = %s; vx_dec_obj(d, o); "+
			"for (int64_t i = 0; i < n; i++) { %s = rcv_%s(d); %s = rcv_%s(d); *(%s *)vx_map_set(o, &k) = x; } return o;",
			newMap(t), decl(t.Key, "k"), k, decl(t.Value, "x"), v, ctype(t.Value))
	case *types.Record:
		fmt.Fprintf(&rcv, "%s r;", ct)
		for i, ft := range t.FieldTypes() {
			c, f := g.codec(ft), recordFields(t)[i]
			fmt.Fprintf(&snd, " snd_%s(e, v.%s);", c, f)
			fmt.Fprintf(&rcv, " r.%s = rcv_%s(d);", f, c)
		}
		rcv.WriteString(" return r;")
	case *types.Struct:
		name := structName(t)
		snd.WriteString("if (vx_enc_ref(e, v)) return;")
		fmt.Fprintf(&rcv, "%s *o = vx_dec_ref(d); if (o) return o; o = vx_alloc(sizeof(%s)); ", name, name)
		if len(t.Interfaces) > 0 {
			fmt.Fprintf(&rcv, "o->vx_t = &desc_%s; ", structIdent(t))
		}
		rcv.WriteString("vx_dec_obj(d, o);")
		for _, f := range t.Fields {
			c := g.codec(f.Type)
			fmt.Fprintf(&snd, " snd_%s(e, v->f_%s);", c, f.Name)
			fmt.Fprintf(&rcv, " o->f_%s = rcv_%s(d);", f.Name, c)
		}
		rcv.WriteString(" return o;")
	case *types.Enum:
		name := enumName(t)
		snd.WriteString("if (vx_enc_ref(e, v)) return; vx_enc_put(e, &v->tag, 4); switch (v->tag) {")
		fmt.Fprintf(&rcv, "%s *o = vx_dec_ref(d); if (o) return o; o = vx_alloc(sizeof(%s)); ", name, name)
		if len(t.Interfaces) > 0 {
			fmt.Fprintf(&rcv, "o->vx_t = &desc_%s; ", name)
		}
		rcv.WriteString("vx_dec_obj(d, o); vx_dec_get(d, &o->tag, 4); switch (o->tag) {")
		for _, cs := range t.Cases {
			if len(cs.Fields) == 0 {
				continue
			}
			fmt.Fprintf(&snd, " case %d:", cs.Index)
			fmt.Fprintf(&rcv, " case %d:", cs.Index)
			for _, f := range cs.Fields {
				c := g.codec(f.Type)
				fmt.Fprintf(&snd, " snd_%s(e, v->u.c_%s.f_%s);", c, cs.Name, f.Name)
				fmt.Fprintf(&rcv, " o->u.c_%s.f_%s = rcv_%s(d);", cs.Name, f.Name, c)
			}
			snd.WriteString(" break;")
			rcv.WriteString(" break;")
		}
		snd.WriteString(" }")
		rcv.WriteString(" } return o;")
	default:
		if t == types.String {
			snd.WriteString("vx_enc_str(e, v);")
			rcv.WriteString("return vx_dec_str(d);")
		} else {
			// Numbers, bools and channels are copied as they are.
			snd.WriteString("vx_enc_put(e, &v, sizeof v);")
			fmt.Fprintf(&rcv, "%s v; vx_dec_get(d, &v, sizeof v); return v;", ct)
		}
	}
	fmt.Fprintf(&g.codecDefs, "static void snd_%s(vx_enc *e, %s) { %s }\n", m, decl(t, "v"), snd.String())
	fmt.Fprintf(&g.codecDefs, "static %s rcv_%s(vx_dec *d) { %s }\n", ct, m, rcv.String())
	return m
}

// Returns the name of the function that decodes a message holding a
// value of [t] into a future's value; as an optional of [t] when [opt].
func (g *gen) filler(t types.Type, opt bool) string {
	m := g.codec(t)
	name := "fil_" + m
	if opt {
		name = "filo_" + m
	}
	if g.codecs[name] {
		return name
	}
	g.codecs[name] = true
	if opt {
		fmt.Fprintf(&g.codecDefs, "static void %s(const char *buf, void *out) { vx_dec d = {buf, NULL, 0, 0}; %s = rcv_%s(&d); "+
			"%s *o = out; o->has = true; o->v = v; }\n", name, decl(t, "v"), m, ctype(&types.Optional{Elem: t}))
	} else {
		fmt.Fprintf(&g.codecDefs, "static void %s(const char *buf, void *out) { vx_dec d = {buf, NULL, 0, 0}; *(%s *)out = rcv_%s(&d); }\n",
			name, ctype(t), m)
	}
	return name
}

// Generates `isolate.run(f)` or `isolate.spawn(f)`. The values the
// closure captured are encoded here; a generated entry function decodes
// them on the new isolate into a fresh environment and calls the closure.
func (g *gen) isolateCall(e *ast.CallExpr, c *types.Call) string {
	arg := c.Args[0]
	ft := g.info.Types[arg].(*types.FuncType)
	g.nisos++
	entry := fmt.Sprintf("iso%d", g.nisos)
	enc := g.temp()
	var pre strings.Builder
	var body strings.Builder
	fmt.Fprintf(&body, "static void %s(vx_dec *d, vx_inbox *parent, vx_future *f) {", entry)
	var callee string
	args := []string{}
	if lit, ok := arg.(*ast.FuncLit); ok {
		code := g.expr(arg)
		lam, caps := g.litNames[lit], g.info.Captures[lit]
		fv := g.temp()
		fmt.Fprintf(&pre, "vx_fn %s = %s; vx_enc %s = {0}; ", fv, code, enc)
		if len(caps) > 0 {
			fmt.Fprintf(&body, " struct env_%s *env = vx_alloc(sizeof(struct env_%s));", lam, lam)
			for i, v := range caps {
				m := g.codec(v.Type)
				field := fmt.Sprintf("((struct env_%s *)%s.env)->c%d", lam, fv, i)
				if v.Boxed() {
					fmt.Fprintf(&pre, "snd_%s(&%s, *%s); ", m, enc, field)
					fmt.Fprintf(&body, " env->c%d = vx_alloc(sizeof(%s)); *env->c%d = rcv_%s(d);", i, ctype(v.Type), i, m)
				} else {
					fmt.Fprintf(&pre, "snd_%s(&%s, %s); ", m, enc, field)
					fmt.Fprintf(&body, " env->c%d = rcv_%s(d);", i, m)
				}
			}
			args = append(args, "env")
		} else {
			args = append(args, "NULL")
		}
		callee = lam
	} else {
		fmt.Fprintf(&pre, "vx_enc %s = {0}; ", enc)
		callee = funcName(g.info.Uses[arg.(*ast.Ident)].(*types.Func))
	}
	if ft.Throws != nil {
		body.WriteString(" void *err = NULL;")
		args = append(args, "&err")
	}
	call := callee + "(" + strings.Join(args, ", ") + ")"
	run := c.Builtin == "isolate.run"
	if run && ft.Result != types.Void {
		fmt.Fprintf(&body, " %s = %s;", decl(ft.Result, "r"), call)
	} else {
		fmt.Fprintf(&body, " %s;", call)
	}
	if run {
		body.WriteString(" vx_enc e = {0};")
		if ft.Throws != nil {
			m := g.codec(ft.Throws)
			fmt.Fprintf(&body, " if (err) { snd_%s(&e, (%s)err); vx_post(parent, f, vx_enc_done(&e), %s, true); return; }",
				m, ctype(ft.Throws), g.filler(ft.Throws, false))
		}
		fill := "NULL"
		if ft.Result != types.Void {
			fmt.Fprintf(&body, " snd_%s(&e, r);", g.codec(ft.Result))
			fill = g.filler(ft.Result, false)
		}
		fmt.Fprintf(&body, " vx_post(parent, f, vx_enc_done(&e), %s, false);", fill)
	} else {
		body.WriteString(" (void)parent; (void)f;")
	}
	body.WriteString(" }\n")
	g.codecDefs.WriteString(body.String())
	if run {
		return fmt.Sprintf("({ %svx_isolate_run(%s, &%s, %s); })", pre.String(), entry, enc, valueSize(ft.Result))
	}
	return fmt.Sprintf("({ %svx_isolate_start(%s, &%s, NULL); })", pre.String(), entry, enc)
}

// Generates a call of a channel's method.
func (g *gen) channelCall(e *ast.CallExpr, c *types.Call, ch *types.Channel, cs []string, line int) string {
	switch c.Builtin {
	case "send":
		x, en := g.temp(), g.temp()
		return fmt.Sprintf("({ vx_chan *%s_c = %s; %s = %s; vx_enc %s = {0}; snd_%s(&%s, %s); vx_chan_send(%s_c, &%s, %d); })",
			x, cs[0], decl(ch.Elem, x), cs[1], en, g.codec(ch.Elem), en, x, x, en, line)
	case "receive":
		return fmt.Sprintf("vx_chan_receive(%s, %s, sizeof(%s))", cs[0], g.filler(ch.Elem, true), ctype(&types.Optional{Elem: ch.Elem}))
	case "receiveSync":
		b, r := g.temp(), g.temp()
		return fmt.Sprintf("({ char *%s = vx_chan_receive_sync(%s); %s %s = {0}; if (%s) { %s(%s, &%s); free(%s); } %s; })",
			b, cs[0], ctype(&types.Optional{Elem: ch.Elem}), r, b, g.filler(ch.Elem, true), b, r, b, r)
	}
	return fmt.Sprintf("vx_chan_close(%s)", cs[0])
}
