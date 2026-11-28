package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/types"
)

// Returns the `Json` and `JsonError` enums of the JSON module.
func (g *gen) jsonTypes() (*types.Enum, *types.Enum) {
	if g.json == nil {
		for _, e := range g.info.Enums {
			if e.Module != nil && e.Module.Path == "vex:json" {
				switch e.Name {
				case "Json":
					g.json = e
				case "JsonError":
					g.jsonErr = e
				}
			}
		}
	}
	return g.json, g.jsonErr
}

// Returns the C type of a `Json` value.
func (g *gen) jsonType() string {
	j, _ := g.jsonTypes()
	return enumName(j) + " *"
}

// Returns the tag of the `Json` case [name].
func (g *gen) jsonTag(name string) int {
	j, _ := g.jsonTypes()
	return j.Case(name).Index
}

// Returns C code making the `Json` case [name] from [args].
func (g *gen) jsonCase(name string, args ...string) string {
	j, _ := g.jsonTypes()
	return caseFunc(j.Case(name)) + "(" + strings.Join(args, ", ") + ")"
}

// Returns C code for the `Json` value of [x], a value of [t].
func (g *gen) toJSON(t types.Type, x string) string {
	if u := wrapped(t); u != nil && ownEncode(t) == nil {
		return g.toJSON(u, x)
	}
	switch t {
	case types.Int:
		return g.jsonCase("integer", x)
	case types.Float:
		return g.jsonCase("number", x)
	case types.Bool:
		return g.jsonCase("boolean", x)
	case types.String:
		return g.jsonCase("text", x)
	case types.Bytes:
		return g.jsonCase("text", "vx_bytes_base64("+x+")")
	}
	if j, _ := g.jsonTypes(); t == types.Type(j) {
		return x
	}
	return g.toJSONFunc(t) + "(" + x + ")"
}

// Returns C code adding [value], a `Json`, to the object map [m] under the
// key [key], a C string literal.
func objectSet(jt, m, key, value string) string {
	return fmt.Sprintf("{ %s x = %s; vx_str k = VX_LIT(%s); *(%s*)vx_map_set(%s, &k) = x; } ", jt, value, key, jt, m)
}

func newObject(jt string) string {
	return fmt.Sprintf("vx_map_new(VX_KEY_STR, sizeof(vx_str), VX_PM_STR, sizeof(%s), 1)", jt)
}

// Returns the name of the generated function giving a value of [t] as a
// `Json`.
func (g *gen) toJSONFunc(t types.Type) string {
	name := "tojson_" + mangle(t)
	if g.codecs[name] {
		return name
	}
	g.codecs[name] = true
	jt := g.jsonType()
	fmt.Fprintf(&g.codecDecls, "static %s%s(%s);\n", jt, name, decl(t, "v"))
	var b strings.Builder
	if m := ownEncode(t); m != nil {
		fmt.Fprintf(&g.jsonDefs, "static %s%s(%s) { return %s; }\n", jt, name, decl(t, "v"), g.memberCall(m, t, "v"))
		return name
	}
	switch t := t.(type) {
	case *types.Optional:
		fmt.Fprintf(&b, "if (!%s) return %s; return %s;", present(t, "v"), g.jsonCase("null"), g.toJSON(t.Elem, unwrap(t, "v")))
	case *types.List:
		fmt.Fprintf(&b, "vx_list *l = vx_list_new(sizeof(%s), 1, 0, NULL); for (int64_t i = 0; i < v->len; i++) { %s x = %s; vx_list_push(l, &x); } return %s;",
			jt, jt, g.toJSON(t.Elem, fmt.Sprintf("((%s*)v->data)[i]", ctype(t.Elem))), g.jsonCase("list", "l"))
	case *types.Set:
		fmt.Fprintf(&b, "vx_list *l = vx_list_new(sizeof(%s), 1, 0, NULL); vx_map_iter it = vx_map_iter_new(v); for (char *e; (e = vx_map_next(v, &it));) { %s x = %s; vx_list_push(l, &x); } return %s;",
			jt, jt, g.toJSON(t.Elem, fmt.Sprintf("*(%s*)(e + VX_MAP_KEY)", ctype(t.Elem))), g.jsonCase("list", "l"))
	case *types.Map:
		fmt.Fprintf(&b, "vx_map *m = %s; vx_map_iter it = vx_map_iter_new(v); for (char *e; (e = vx_map_next(v, &it));) { %s x = %s; *(%s*)vx_map_set(m, e + VX_MAP_KEY) = x; } return %s;",
			newObject(jt), jt, g.toJSON(t.Value, fmt.Sprintf("*(%s*)(e + v->voff)", ctype(t.Value))), jt, g.jsonCase("object", "m"))
	case *types.Struct:
		fmt.Fprintf(&b, "vx_map *m = %s; ", newObject(jt))
		for _, f := range t.Fields {
			b.WriteString(objectSet(jt, "m", cQuote(f.Name), g.toJSON(f.Type, "v->f_"+f.Name)))
		}
		fmt.Fprintf(&b, "return %s;", g.jsonCase("object", "m"))
	case *types.Enum:
		if t.Raw != nil {
			fmt.Fprintf(&b, "return %s;", g.toJSON(t.Raw, g.rawValueFunc(t)+"(v)"))
			break
		}
		b.WriteString("switch (v->tag) { ")
		for _, cs := range t.Cases {
			if len(cs.Fields) == 0 {
				fmt.Fprintf(&b, "case %d: return %s; ", cs.Index, g.jsonCase("text", "VX_LIT("+cQuote(cs.Name)+")"))
				continue
			}
			fmt.Fprintf(&b, "case %d: { vx_map *f = %s; ", cs.Index, newObject(jt))
			for _, f := range cs.Fields {
				b.WriteString(objectSet(jt, "f", cQuote(f.Name), g.toJSON(f.Type, fmt.Sprintf("v->u.c_%s.f_%s", cs.Name, f.Name))))
			}
			fmt.Fprintf(&b, "vx_map *m = %s; ", newObject(jt))
			b.WriteString(objectSet(jt, "m", cQuote(cs.Name), g.jsonCase("object", "f")))
			fmt.Fprintf(&b, "return %s; } ", g.jsonCase("object", "m"))
		}
		fmt.Fprintf(&b, "} return %s;", g.jsonCase("null"))
	default:
		panic("codegen: no JSON form for " + t.String())
	}
	fmt.Fprintf(&g.jsonDefs, "static %s%s(%s) { %s }\n", jt, name, decl(t, "v"), b.String())
	return name
}

// Declares the helpers that make a `JsonError.mismatch` at a path, with a
// reason, or saying what was expected and what [j] holds instead.
func (g *gen) jsonFailHelpers() {
	if g.codecs["vx_jfail"] {
		return
	}
	g.codecs["vx_jfail"] = true
	_, je := g.jsonTypes()
	fmt.Fprintf(&g.codecDecls, "static void *vx_jfail(const vx_jpath *p, vx_str reason) { return %s(vx_json_path(p), reason); }\n", caseFunc(je.Case("mismatch")))
	fmt.Fprintf(&g.codecDecls, "static void *vx_jwant(const vx_jpath *p, const char *want, %sj) { return vx_jfail(p, vx_json_want(want, j->tag)); }\n", g.jsonType())
	mm := je.Case("mismatch")
	fmt.Fprintf(&g.codecDecls, "static void *vx_jwithin(const vx_jpath *p, void *err) { %s *e = err; "+
		"if (!p || e->tag != %d) return err; return %s(vx_json_join(vx_json_path(p), e->u.c_mismatch.f_path), e->u.c_mismatch.f_reason); }\n",
		enumName(je), mm.Index, caseFunc(mm))
}

// Returns the `encode` that [t] writes for itself, or nil.
func ownEncode(t types.Type) *types.Func {
	switch t := t.(type) {
	case *types.Struct:
		return t.EncodeMethod()
	case *types.Enum:
		return t.EncodeMethod()
	}
	return nil
}

// Returns the `decode` that [t] writes for itself, or nil.
func ownDecode(t types.Type) *types.Func {
	switch t := t.(type) {
	case *types.Struct:
		return t.DecodeMethod()
	case *types.Enum:
		return t.DecodeMethod()
	}
	return nil
}

// Returns the name of the generated function reading a value of [t] from
// a `Json`. It takes the path to the value for errors, and stores a
// `JsonError` in its last argument when the value doesn't fit.
func (g *gen) fromJSONFunc(t types.Type) string {
	if u := wrapped(t); u != nil && ownDecode(t) == nil {
		return g.fromJSONFunc(u)
	}
	name := "fromjson_" + mangle(t)
	if g.codecs[name] {
		return name
	}
	g.codecs[name] = true
	g.jsonFailHelpers()
	jt := g.jsonType()
	sig := fmt.Sprintf("static %s(%sj, const vx_jpath *p, void **vx_err)", decl(t, name), jt)
	fmt.Fprintf(&g.codecDecls, "%s;\n", sig)
	if j, _ := g.jsonTypes(); t == types.Type(j) {
		fmt.Fprintf(&g.jsonDefs, "%s { (void)p; (void)vx_err; return j; }\n", sig)
		return name
	}

	savedB, savedInd, savedTries, savedFrames := g.b, g.ind, g.tries, g.frames
	var b strings.Builder
	g.b, g.ind, g.tries, g.frames = &b, 0, nil, nil
	defer func() { g.b, g.ind, g.tries, g.frames = savedB, savedInd, savedTries, savedFrames }()

	zero := "(" + ctype(t) + "){0}"
	want := func(desc string) {
		g.line("*vx_err = vx_jwant(p, %s, j);", cQuote(desc))
		g.line("return %s;", zero)
	}
	is := func(c string) string { return fmt.Sprintf("j->tag == %d", g.jsonTag(c)) }
	g.line("%s {", sig)
	g.ind++
	if m := ownDecode(t); m != nil {
		g.line("%s = %s(j, vx_err);", decl(t, "r"), funcName(m))
		g.line("if (*vx_err) *vx_err = vx_jwithin(p, *vx_err);")
		g.line("return r;")
		g.ind--
		g.line("}")
		g.jsonDefs.WriteString(b.String())
		return name
	}
	switch t := t.(type) {
	case *types.Optional:
		g.line("if (%s) return %s;", is("null"), zero)
		g.line("%s = %s(j, p, vx_err);", decl(t.Elem, "x"), g.fromJSONFunc(t.Elem))
		g.line("if (*vx_err) return %s;", zero)
		g.line("return %s;", wrapOpt(t, "x"))
	case *types.List:
		g.line("if (!(%s)) {", is("list"))
		g.ind++
		want("a list")
		g.ind--
		g.line("}")
		g.line("vx_list *src = j->u.c_list.f_items;")
		g.line("vx_list *l = vx_list_new(sizeof(%s), %s, 0, NULL);", ctype(t.Elem), layoutOf(t.Elem))
		g.line("for (int64_t i = 0; i < src->len; i++) {")
		g.line("    vx_jpath q = {p, {NULL, 0}, i};")
		g.line("    %s = %s(((%s*)src->data)[i], &q, vx_err);", decl(t.Elem, "x"), g.fromJSONFunc(t.Elem), jt)
		g.line("    if (*vx_err) return NULL;")
		g.line("    vx_list_push(l, &x);")
		g.line("}")
		g.line("return l;")
	case *types.Set:
		g.line("if (!(%s)) {", is("list"))
		g.ind++
		want("a list")
		g.ind--
		g.line("}")
		g.line("vx_list *src = j->u.c_list.f_items;")
		g.line("vx_map *s = %s;", g.newSet(t))
		g.line("for (int64_t i = 0; i < src->len; i++) {")
		g.line("    vx_jpath q = {p, {NULL, 0}, i};")
		g.line("    %s = %s(((%s*)src->data)[i], &q, vx_err);", decl(t.Elem, "x"), g.fromJSONFunc(t.Elem), jt)
		g.line("    if (*vx_err) return NULL;")
		g.line("    vx_map_set(s, &x);")
		g.line("}")
		g.line("return s;")
	case *types.Map:
		g.line("if (!(%s)) {", is("object"))
		g.ind++
		want("an object")
		g.ind--
		g.line("}")
		g.line("vx_map *src = j->u.c_object.f_fields;")
		g.line("vx_map *m = vx_map_new(VX_KEY_STR, sizeof(vx_str), VX_PM_STR, sizeof(%s), %s);", ctype(t.Value), layoutOf(t.Value))
		g.line("vx_map_iter it = vx_map_iter_new(src);")
		g.line("for (char *e; (e = vx_map_next(src, &it));) {")
		g.line("    vx_jpath q = {p, *(vx_str *)(e + VX_MAP_KEY), -1};")
		g.line("    %s = %s(*(%s*)(e + src->voff), &q, vx_err);", decl(t.Value, "x"), g.fromJSONFunc(t.Value), jt)
		g.line("    if (*vx_err) return NULL;")
		g.line("    *(%s*)vx_map_set(m, e + VX_MAP_KEY) = x;", ctype(t.Value))
		g.line("}")
		g.line("return m;")
	case *types.Struct:
		g.line("if (!(%s)) {", is("object"))
		g.ind++
		want("an object")
		g.ind--
		g.line("}")
		g.line("vx_map *src = j->u.c_object.f_fields;")
		// The values are read before the struct is created, so that it is
		// written only while new, without a write barrier.
		var fields []*types.Field
		var args []string
		for _, f := range t.Fields {
			if !f.ByInit {
				fields = append(fields, f)
				args = append(args, "c.f_"+f.Name)
			}
		}
		if len(fields) > 0 {
			g.line("struct { %s } c;", fieldLocals(fields))
		} else {
			g.line("(void)src;")
		}
		g.jsonFields(fields, "src", "c.f_", "NULL")
		g.line("return new_%s(%s);", structName(t), strings.Join(args, ", "))
	case *types.Enum:
		g.jsonEnum(t)
	default:
		switch t {
		case types.Int:
			g.line("if (%s) return j->u.c_integer.f_value;", is("integer"))
			want("an int")
		case types.Float:
			g.line("if (%s) return j->u.c_number.f_value;", is("number"))
			g.line("if (%s) return (double)j->u.c_integer.f_value;", is("integer"))
			want("a number")
		case types.Bool:
			g.line("if (%s) return j->u.c_boolean.f_value;", is("boolean"))
			want("a bool")
		case types.String:
			g.line("if (%s) return j->u.c_text.f_value;", is("text"))
			want("a string")
		case types.Bytes:
			g.line("if (%s) {", is("text"))
			g.line("    vx_list *b = vx_bytes_from_base64(j->u.c_text.f_value);")
			g.line("    if (b) return b;")
			g.line("    *vx_err = vx_jfail(p, VX_LIT(\"expected base64 text\"));")
			g.line("    return NULL;")
			g.line("}")
			want("a base64 string")
		default:
			if jt, _ := g.jsonTypes(); t == types.Type(jt) {
				g.line("(void)p; (void)vx_err;")
				g.line("return j;")
				break
			}
			panic("codegen: no JSON form for " + t.String())
		}
	}
	g.ind--
	g.line("}")
	g.jsonDefs.WriteString(b.String())
	return name
}

// Writes the reading of [fields] from the object map [src] into
// [dst]+name, returning [zero] when one doesn't fit. A missing field takes
// its default, or nil when optional.
func (g *gen) jsonFields(fields []*types.Field, src, dst, zero string) {
	jt := g.jsonType()
	for _, f := range fields {
		g.line("{")
		g.ind++
		g.line("vx_str k = VX_LIT(%s);", cQuote(f.Name))
		g.line("vx_jpath q = {p, k, -1};")
		g.line("%s*w = vx_map_find(%s, &k);", jt, src)
		g.line("if (w) {")
		g.line("    %s%s = %s(*w, &q, vx_err);", dst, f.Name, g.fromJSONFunc(f.Type))
		g.line("    if (*vx_err) return %s;", zero)
		g.line("} else {")
		g.ind++
		_, opt := f.Type.(*types.Optional)
		switch {
		case f.Default != nil:
			g.line("%s%s = %s;", dst, f.Name, g.expr(f.Default))
		case opt:
			g.line("%s%s = (%s){0};", dst, f.Name, ctype(f.Type))
		default:
			g.line("*vx_err = vx_jfail(&q, VX_LIT(\"missing\"));")
			g.line("return %s;", zero)
		}
		g.ind--
		g.line("}")
		g.ind--
		g.line("}")
	}
}

// Writes the body reading the enum [t]: a case without values is its name
// as a string, and a case with values an object with one key, the case's
// name, holding an object of its values.
func (g *gen) jsonEnum(t *types.Enum) {
	jt := g.jsonType()
	text, object := g.jsonTag("text"), g.jsonTag("object")
	unknown := func(s string) {
		g.line("*vx_err = vx_jfail(p, vx_json_unknown_case(%s, %s));", s, cQuote(t.Name))
		g.line("return NULL;")
	}
	if t.Raw != nil {
		g.jsonRawEnum(t)
		return
	}
	g.line("if (j->tag == %d) {", text)
	g.ind++
	g.line("vx_str s = j->u.c_text.f_value;")
	for _, cs := range t.Cases {
		if len(cs.Fields) == 0 {
			g.line("if (vx_str_eq(s, VX_LIT(%s))) return %s();", cQuote(cs.Name), caseFunc(cs))
		}
	}
	unknown("s")
	g.ind--
	g.line("}")
	g.line("if (j->tag == %d && j->u.c_object.f_fields->len == 1) {", object)
	g.ind++
	g.line("vx_map *one = j->u.c_object.f_fields;")
	g.line("vx_map_iter it = vx_map_iter_new(one);")
	g.line("char *e = vx_map_next(one, &it);")
	g.line("vx_str s = *(vx_str *)(e + VX_MAP_KEY);")
	g.line("%sinner = *(%s*)(e + one->voff);", jt, jt)
	g.line("(void)inner;")
	for _, cs := range t.Cases {
		g.line("if (vx_str_eq(s, VX_LIT(%s))) {", cQuote(cs.Name))
		g.ind++
		if len(cs.Fields) == 0 {
			g.line("return %s();", caseFunc(cs))
		} else {
			g.line("vx_jpath at = {p, s, -1};")
			g.line("if (inner->tag != %d) {", object)
			g.line("    *vx_err = vx_jwant(&at, \"an object\", inner);")
			g.line("    return NULL;")
			g.line("}")
			g.line("vx_map *src = inner->u.c_object.f_fields;")
			g.line("struct { %s } c;", fieldLocals(cs.Fields))
			g.line("{")
			g.ind++
			g.line("const vx_jpath *p = &at;")
			g.jsonFields(cs.Fields, "src", "c.f_", "NULL")
			g.ind--
			g.line("}")
			var args []string
			for _, f := range cs.Fields {
				args = append(args, "c.f_"+f.Name)
			}
			g.line("return %s(%s);", caseFunc(cs), strings.Join(args, ", "))
		}
		g.ind--
		g.line("}")
	}
	unknown("s")
	g.ind--
	g.line("}")
	g.line("*vx_err = vx_jwant(p, %s, j);", cQuote("a case of "+t.Name))
	g.line("return NULL;")
}

// Writes the body reading the enum [t] with raw values, each case written
// as its raw value.
func (g *gen) jsonRawEnum(t *types.Enum) {
	from := g.fromRawFunc(t)
	if t.Raw == types.Int {
		g.line("if (j->tag == %d) {", g.jsonTag("integer"))
		g.ind++
		g.line("%s *c = %s(j->u.c_integer.f_value);", enumName(t), from)
		g.line("if (c) return c;")
		g.line("*vx_err = vx_jfail(p, vx_str_concat(VX_LIT(%s), vx_int_str(j->u.c_integer.f_value)));", cQuote("no case of "+t.Name+" has raw value "))
	} else {
		g.line("if (j->tag == %d) {", g.jsonTag("text"))
		g.ind++
		g.line("%s *c = %s(j->u.c_text.f_value);", enumName(t), from)
		g.line("if (c) return c;")
		g.line("*vx_err = vx_jfail(p, vx_str_concat(VX_LIT(%s), vx_str_quote(j->u.c_text.f_value)));", cQuote("no case of "+t.Name+" has raw value "))
	}
	g.line("return NULL;")
	g.ind--
	g.line("}")
	g.line("*vx_err = vx_jwant(p, %s, j);", cQuote("a raw value of "+t.Name))
	g.line("return NULL;")
}

// Returns the C declarations of [fields], as struct members.
func fieldLocals(fields []*types.Field) string {
	var parts []string
	for _, f := range fields {
		parts = append(parts, decl(f.Type, "f_"+f.Name)+";")
	}
	return strings.Join(parts, " ")
}
