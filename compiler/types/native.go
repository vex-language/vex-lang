package types

import (
	"fmt"
	"github.com/vex-language/vex-lang/compiler/ast"
	"strings"
)

// The sized C numbers that extern and export signatures may use, and
// their C types. They are int or float in Vex code.
var cNumbers = map[string]string{
	"int8": "int8_t", "int16": "int16_t", "int32": "int32_t", "int64": "int64_t",
	"uint8": "uint8_t", "uint16": "uint16_t", "uint32": "uint32_t", "uint64": "uint64_t",
	"float32": "float", "float64": "double",
}

func (c *checker) declareHandle(d *ast.ExternTypeDecl) {
	if c.reserved(d.Name, "an extern type") || c.taken(d.Name) {
		return
	}
	if cNumbers[d.Name.Name] != "" {
		c.errorAt(d.Name, "choose another name", "'%s' is a C number type", d.Name.Name)
		return
	}
	h := &Handle{Name: d.Name.Name, Module: c.mod, Decl: d, CName: d.CName}
	if h.CName == "" {
		h.CName = h.Name
	}
	c.handles[h.Name] = h
}

// Returns the type written as [te] and, in the signature of an extern or
// export function when [native] is set, the C type of a sized number.
func (c *checker) nativeType(te ast.TypeExpr, native bool) (Type, string) {
	if ot, ok := te.(*ast.OptionalType); ok && native {
		if nt, ok := ot.Elem.(*ast.NamedType); ok && nt.Module == "" && len(nt.Args) == 0 && cNumbers[nt.Name] != "" && c.handles[nt.Name] == nil {
			c.errorAt(te, fmt.Sprintf("use %s, and a separate value to say whether it is set", nt.Name), "%s? can't cross into C, since a C number has no nil", nt.Name)
			return Invalid, ""
		}
	}
	if nt, ok := te.(*ast.NamedType); ok && native && nt.Module == "" && len(nt.Args) == 0 && cNumbers[nt.Name] != "" && c.handles[nt.Name] == nil {
		ct := cNumbers[nt.Name]
		if ct == "float" || ct == "double" {
			return Float, ct
		}
		return Int, ct
	}
	return c.resolveType(te), ""
}

// Reports types in the signature of extern or export function [f] that
// can't cross into C.
func (c *checker) nativeTypes(f *Func) {
	what, hint := "passed to C", "C functions take int, float, bool, string, string?, sized numbers such as int32, extern types and fn(...) callbacks"
	if f.Decl.Export {
		switch name := f.Decl.Name.Name; {
		case name == "vex_poll" || name == "vex_release":
			c.errorAt(f.Decl.Name, "use another name", "'%s' is already exported by the Vex runtime", name)
		case cLibraryNames[name]:
			c.errorAt(f.Decl.Name, "use another name, such as my_"+name, "export function '%s' would clash with the C library function of that name", name)
		}
		what, hint = "taken from C", "export functions take int, float, bool, string, string?, sized numbers such as int32 and extern types"
	}
	for i, p := range f.Params {
		at := f.Decl.Params[i].Type
		if ft, ok := p.Type.(*FuncType); ok && f.Decl.Extern {
			c.callbackType(ft, at)
			continue
		}
		if p.Type != Invalid && !cValue(p.Type) {
			c.errorAt(at, hint, "%s can't be %s", p.Type, what)
		}
	}
	_, future := f.Result.(*Future)
	switch r := f.Result; {
	case r == Void || r == Invalid:
	case future && f.Decl.Extern && strings.HasPrefix(f.Decl.CName, "vx_"):
		// Runtime functions, used by the standard library, may give futures.
	case f.Decl.Export && (r == String || Identical(r, &Optional{Elem: String})):
		c.errorAt(f.Decl.Result, "return a handle, or let C pass in a buffer", "export functions can't return strings yet, since C would hold memory Vex manages")
	case !cValue(r):
		what := "returned from C"
		if f.Decl.Export {
			what = "returned to C"
		}
		c.errorAt(f.Decl.Result, hint, "%s can't be %s", r, what)
	}
	if f.Decl.Export {
		if f.Owner != nil || f.Enum != nil {
			c.errorAt(f.Decl.Name, "move it to the top level", "only top-level functions can be exported")
		}
		if f.Name == "main" {
			c.errorAt(f.Decl.Name, "", "main can't be exported; it is where the program starts")
		}
		if strings.HasPrefix(f.Name, "vx_") {
			c.errorAt(f.Decl.Name, "choose a name that doesn't start with vx_", "names starting with vx_ are kept for the Vex runtime")
		}
	}
}

// Reports whether values of [t] can be passed to and from C.
func cValue(t Type) bool {
	switch t := t.(type) {
	case *Handle:
		return true
	case *Optional:
		_, h := t.Elem.(*Handle)
		return h || t.Elem == String
	}
	switch t {
	case Int, Float, Bool, String, Invalid:
		return true
	}
	return false
}

// Reports what callback type [ft], at [at], uses that C can't call with.
func (c *checker) callbackType(ft *FuncType, at ast.Node) {
	const hint = "callbacks take int, float, bool, string, string? and extern types, and return int, float, bool or an extern type"
	if ft.Throws != nil {
		c.errorAt(at, "catch errors inside the callback", "callbacks passed to C can't throw")
	}
	for _, p := range ft.Params {
		if _, ok := p.(*FuncType); ok || !cValue(p) {
			c.errorAt(at, hint, "C can't pass %s to a callback", p)
			return
		}
	}
	if r := ft.Result; r != Void && r != nil && (!cValue(r) || r == String || Identical(r, &Optional{Elem: String})) {
		c.errorAt(at, hint, "a callback passed to C can't return %s", r)
	}
}
