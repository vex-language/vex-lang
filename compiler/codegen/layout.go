package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/types"
)

// The collector scans heap objects precisely: each allocation passes the
// layout of its words as a mask with one bit per 8-byte word that may hold
// a pointer (see VX_LAYOUT in the runtime). The masks are C constant
// expressions built with offsetof, so the C compiler works out where each
// field lands.

// Returns the C constant expression for the layout of a value of type [t].
func layoutOf(t types.Type) string {
	if t == types.Void {
		return "0"
	}
	return cLayout(ctype(t))
}

// Returns the C constant expression for the layout of a value of the C
// type [c].
func cLayout(c string) string {
	c = strings.TrimSpace(c)
	switch {
	case strings.HasSuffix(c, "*"), c == "vx_str":
		return "1"
	case c == "vx_fn":
		return "VX_PM_FN"
	case strings.HasPrefix(c, "rec_"), strings.HasPrefix(c, "opt_"):
		return "VXPM_" + c
	}
	switch c {
	case "int64_t", "double", "bool", "vx_range", "int32_t", "uint64_t", "void":
		return "0"
	}
	return fmt.Sprintf("VX_ONES(sizeof(%s))", c)
}

// A field of a C struct, for working out its layout.
type cField struct {
	// The member designator passed to offsetof, such as `f_name` or
	// `u.c_circle.f_r`.
	name  string
	ctype string
}

// Returns the fields of the C declarations [decls], each like
// `vx_list **c1` or `int64_t n;`.
func cFields(decls []string) []cField {
	var out []cField
	for _, d := range decls {
		d = strings.TrimSuffix(strings.TrimSpace(d), ";")
		i := len(d)
		for i > 0 && (d[i-1] == '_' || d[i-1] >= 'a' && d[i-1] <= 'z' || d[i-1] >= 'A' && d[i-1] <= 'Z' || d[i-1] >= '0' && d[i-1] <= '9') {
			i--
		}
		out = append(out, cField{d[i:], d[:i]})
	}
	return out
}

// Returns the C constant expression for the layout of the C struct type
// [name] with [fields].
func structLayout(name string, fields []cField) string {
	var parts []string
	for _, f := range fields {
		if m := cLayout(f.ctype); m != "0" {
			parts = append(parts, fmt.Sprintf("VX_FIELD(%s, %s, %s)", name, f.name, m))
		}
	}
	if len(parts) == 0 {
		return "0"
	}
	return fmt.Sprintf("VX_LAYOUT(%s, %s)", name, strings.Join(parts, " | "))
}

// Writes the macro VXPM_[name] for the layout of the C struct type [name]
// with the fields declared in [decls].
func (g *gen) layoutMacro(name string, decls []string) {
	g.line("#define VXPM_%s %s", name, structLayout(name, cFields(decls)))
}

// Returns C code allocating a zeroed value of type [t] on the heap, as for
// a captured variable.
func boxAlloc(t types.Type) string {
	return fmt.Sprintf("vx_alloc_obj(sizeof(%s), %s)", ctype(t), layoutOf(t))
}
