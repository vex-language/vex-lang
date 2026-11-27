package codegen

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/types"
)

// Equality and hashing. Simple values compare inline; structs, enums with
// values, records, lists, maps and optionals call a function generated the
// first time it is needed, so recursive types work.

// Returns a C condition telling whether [a] and [b], two values of [t], are
// equal.
func (g *gen) eq(t types.Type, a, b string) string {
	switch t := t.(type) {
	case *types.Enum:
		if t.Simple() {
			return fmt.Sprintf("((%s)->tag == (%s)->tag)", a, b)
		}
	case *types.Handle:
		return fmt.Sprintf("(%s == %s)", a, b)
	case *types.Struct:
		if f := t.Operator("=="); f != nil {
			return fmt.Sprintf("%s(%s, %s)", funcName(f), a, b)
		}
	}
	switch t {
	case types.String:
		return fmt.Sprintf("vx_str_eq(%s, %s)", a, b)
	case types.Int, types.Float, types.Bool:
		return fmt.Sprintf("(%s == %s)", a, b)
	}
	return fmt.Sprintf("%s(%s, %s)", g.eqFunc(t), a, b)
}

// Reports whether == on values of [t] calls a generated function.
func needsEqFunc(t types.Type) bool {
	switch t := t.(type) {
	case *types.Struct, *types.Record, *types.List, *types.Map, *types.Optional:
		return true
	case *types.Enum:
		return !t.Simple()
	}
	return false
}

// Returns the name of the generated function comparing two values of [t].
func (g *gen) eqFunc(t types.Type) string {
	name := "eq_" + mangle(t)
	if g.codecs[name] {
		return name
	}
	g.codecs[name] = true
	ct := ctype(t)
	fmt.Fprintf(&g.codecDecls, "static bool %s(%s a, %s b);\n", name, ct, ct)
	var body string
	switch t := t.(type) {
	case *types.Struct:
		parts := []string{"a == b"}
		var fields []string
		for _, f := range t.Fields {
			fields = append(fields, g.eq(f.Type, "a->f_"+f.Name, "b->f_"+f.Name))
		}
		if len(fields) > 0 {
			parts = append(parts, "("+strings.Join(fields, " && ")+")")
		} else {
			parts = append(parts, "true")
		}
		body = "return " + strings.Join(parts, " || ") + ";"
	case *types.Enum:
		var cases strings.Builder
		for _, cs := range t.Cases {
			if len(cs.Fields) == 0 {
				continue
			}
			var fields []string
			for _, f := range cs.Fields {
				x := fmt.Sprintf("u.c_%s.f_%s", cs.Name, f.Name)
				fields = append(fields, g.eq(f.Type, "a->"+x, "b->"+x))
			}
			fmt.Fprintf(&cases, "case %d: return %s; ", cs.Index, strings.Join(fields, " && "))
		}
		body = fmt.Sprintf("if (a == b) return true; if (a->tag != b->tag) return false; switch (a->tag) { %s} return true;", cases.String())
	case *types.Record:
		var fields []string
		for i, f := range t.FieldTypes() {
			x := recordFields(t)[i]
			fields = append(fields, g.eq(f, "a."+x, "b."+x))
		}
		body = "return " + strings.Join(fields, " && ") + ";"
	case *types.List:
		e := ctype(t.Elem)
		body = fmt.Sprintf("if (a == b) return true; if (a->len != b->len) return false; "+
			"for (int64_t i = 0; i < a->len; i++) if (!%s) return false; return true;",
			g.eq(t.Elem, fmt.Sprintf("((%s*)a->data)[i]", e), fmt.Sprintf("((%s*)b->data)[i]", e)))
	case *types.Map:
		v := ctype(t.Value)
		body = fmt.Sprintf("if (a == b) return true; if (a->len != b->len) return false; vx_map_iter it = vx_map_iter_new(a); "+
			"for (char *e; (e = vx_map_next(a, &it));) { %s *w = vx_map_find(b, e + VX_MAP_KEY); if (!w || !%s) return false; } return true;",
			v, g.eq(t.Value, fmt.Sprintf("*(%s*)(e + a->voff)", v), "*w"))
	case *types.Optional:
		body = fmt.Sprintf("bool x = %s, y = %s; return x == y && (!x || %s);",
			present(t, "a"), present(t, "b"), g.eq(t.Elem, unwrap(t, "a"), unwrap(t, "b")))
	default:
		panic("no equality for " + t.String())
	}
	fmt.Fprintf(&g.codecDefs, "static bool %s(%s a, %s b) { %s }\n", name, ct, ct, body)
	return name
}

// Returns C code for the 64-bit hash of [x], a value of [t]. Values that
// [gen.eq] calls equal hash the same.
func (g *gen) hashOf(t types.Type, x string) string {
	switch t := t.(type) {
	case *types.Enum:
		if m := t.HashMethod(); m != nil {
			return fmt.Sprintf("vx_mix((uint64_t)%s)", g.memberCall(m, t, x))
		}
		if t.Simple() {
			return fmt.Sprintf("vx_mix((uint64_t)(%s)->tag)", x)
		}
	case *types.Struct:
		if m := t.HashMethod(); m != nil {
			return fmt.Sprintf("vx_mix((uint64_t)%s)", g.memberCall(m, t, x))
		}
	}
	switch t {
	case types.Int, types.Bool:
		return fmt.Sprintf("vx_mix((uint64_t)(%s))", x)
	case types.Float:
		return fmt.Sprintf("vx_hash_float(%s)", x)
	case types.String:
		return fmt.Sprintf("({ vx_str hs = %s; vx_hash_bytes(hs.ptr, hs.len); })", x)
	}
	return fmt.Sprintf("%s(%s)", g.hashFunc(t), x)
}

// Returns the name of the generated function hashing a struct, enum or
// record of [t] from its fields.
func (g *gen) hashFunc(t types.Type) string {
	name := "hash_" + mangle(t)
	if g.codecs[name] {
		return name
	}
	g.codecs[name] = true
	ct := ctype(t)
	fmt.Fprintf(&g.codecDecls, "static uint64_t %s(%s v);\n", name, ct)
	var b strings.Builder
	add := func(ft types.Type, x string) { fmt.Fprintf(&b, "h = vx_hash_add(h, %s); ", g.hashOf(ft, x)) }
	switch t := t.(type) {
	case *types.Struct:
		for _, f := range t.Fields {
			add(f.Type, "v->f_"+f.Name)
		}
	case *types.Enum:
		b.WriteString("h = vx_hash_add(h, (uint64_t)v->tag); switch (v->tag) { ")
		for _, cs := range t.Cases {
			if len(cs.Fields) == 0 {
				continue
			}
			fmt.Fprintf(&b, "case %d: ", cs.Index)
			for _, f := range cs.Fields {
				add(f.Type, fmt.Sprintf("v->u.c_%s.f_%s", cs.Name, f.Name))
			}
			b.WriteString("break; ")
		}
		b.WriteString("} ")
	case *types.Record:
		for i, f := range t.FieldTypes() {
			add(f, "v."+recordFields(t)[i])
		}
	default:
		panic("no hash for " + t.String())
	}
	fmt.Fprintf(&g.codecDefs, "static uint64_t %s(%s v) { uint64_t h = 0x243f6a8885a308d3ULL; %sreturn h; }\n", name, ct, b.String())
	return name
}

// Returns C code for a new, empty map of type [m]. Keys other than ints,
// bools, strings and simple enums hash and compare through generated
// functions.
func (g *gen) newMap(m *types.Map) string {
	if kind := keyKind(m.Key); kind != "" {
		return fmt.Sprintf("vx_map_new(%s, sizeof(%s), sizeof(%s))", kind, ctype(m.Key), ctype(m.Value))
	}
	k := ctype(m.Key)
	id := mangle(m.Key)
	if !g.codecs["khash_"+id] {
		g.codecs["khash_"+id] = true
		fmt.Fprintf(&g.codecDefs, "static uint64_t khash_%s(const void *k) { return %s; }\n", id, g.hashOf(m.Key, fmt.Sprintf("(*(const %s *)k)", k)))
		fmt.Fprintf(&g.codecDefs, "static bool keq_%s(const void *a, const void *b) { return %s; }\n", id,
			g.eq(m.Key, fmt.Sprintf("(*(const %s *)a)", k), fmt.Sprintf("(*(const %s *)b)", k)))
	}
	return fmt.Sprintf("vx_map_new_custom(khash_%s, keq_%s, sizeof(%s), sizeof(%s))", id, id, k, ctype(m.Value))
}

// Returns C code for `hash(a, b, ...)` over the arguments [cs] of types [ts].
func (g *gen) hashCall(ts []types.Type, cs []string) string {
	var b strings.Builder
	b.WriteString("({ uint64_t h = 0x243f6a8885a308d3ULL; ")
	for i, c := range cs {
		fmt.Fprintf(&b, "h = vx_hash_add(h, %s); ", g.hashOf(ts[i], c))
	}
	b.WriteString("(int64_t)h; })")
	return b.String()
}
