package types

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
)

// A record type: positional fields, read as `$1`, `$2`..., and named
// fields, kept sorted by name, since their order doesn't matter.
type Record struct {
	Positional []Type
	Named      []RecordField
}

// A named field of a record.
type RecordField struct {
	Name string
	Type Type
}

// Returns the type as written: `(int, string)`, `({int x, int y})` or
// `(int, {string name})`.
func (r *Record) String() string {
	var parts []string
	for _, p := range r.Positional {
		parts = append(parts, p.String())
	}
	if len(r.Named) > 0 {
		named := make([]string, len(r.Named))
		for i, f := range r.Named {
			named[i] = f.Type.String() + " " + f.Name
		}
		parts = append(parts, "{"+strings.Join(named, ", ")+"}")
	}
	if len(r.Positional) == 1 && len(r.Named) == 0 {
		return "(" + parts[0] + ",)"
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// Returns the type of the field [name], `$1` for the first positional
// one, or nil when there is none.
func (r *Record) Field(name string) Type {
	if strings.HasPrefix(name, "$") {
		// Only the plain spelling names a field: `$1`, not `$01`.
		if i, err := strconv.Atoi(name[1:]); err == nil && i >= 1 && i <= len(r.Positional) && name[1:] == strconv.Itoa(i) {
			return r.Positional[i-1]
		}
		return nil
	}
	for _, f := range r.Named {
		if f.Name == name {
			return f.Type
		}
	}
	return nil
}

// Returns the names of the fields, positional ones first.
func (r *Record) FieldNames() []string {
	var names []string
	for i := range r.Positional {
		names = append(names, fmt.Sprintf("$%d", i+1))
	}
	for _, f := range r.Named {
		names = append(names, f.Name)
	}
	return names
}

// Returns the types of the fields, in the order of [FieldNames].
func (r *Record) FieldTypes() []Type {
	ts := append([]Type{}, r.Positional...)
	for _, f := range r.Named {
		ts = append(ts, f.Type)
	}
	return ts
}

// Returns a record with the same shape as [r] and field types [ts], in
// the order of [FieldTypes].
func (r *Record) withTypes(ts []Type) *Record {
	out := &Record{Positional: ts[:len(r.Positional):len(r.Positional)]}
	for i, f := range r.Named {
		out.Named = append(out.Named, RecordField{f.Name, ts[len(r.Positional)+i]})
	}
	return out
}

// Reports whether [a] and [b] have the same field names.
func sameShape(a, b *Record) bool {
	if len(a.Positional) != len(b.Positional) || len(a.Named) != len(b.Named) {
		return false
	}
	for i := range a.Named {
		if a.Named[i].Name != b.Named[i].Name {
			return false
		}
	}
	return true
}

// Returns the record type written as [te].
func (c *checker) recordType(te *ast.RecordType) Type {
	r := &Record{}
	bad := false
	for _, p := range te.Positional {
		t := c.resolveType(p)
		bad = bad || t == Invalid
		r.Positional = append(r.Positional, t)
	}
	seen := map[string]bool{}
	for _, f := range te.Named {
		if seen[f.Name.Name] {
			c.errorAt(f.Name, "give each field its own name", "field '%s' is written twice", f.Name.Name)
			bad = true
		}
		seen[f.Name.Name] = true
		if strings.HasPrefix(f.Name.Name, "$") {
			c.errorAt(f.Name, "choose a name that starts with a letter", "names starting with $ are the positional fields of records")
			bad = true
		}
		t := c.resolveType(f.Type)
		bad = bad || t == Invalid
		r.Named = append(r.Named, RecordField{f.Name.Name, t})
	}
	for _, t := range r.FieldTypes() {
		if t == Void {
			c.errorAt(te, "", "a record field can't be void")
			bad = true
		}
	}
	if bad {
		return Invalid
	}
	sort.Slice(r.Named, func(i, j int) bool { return r.Named[i].Name < r.Named[j].Name })
	return r
}

// Checks a record value. [want] gives the field types their context when
// it is a record of the same shape.
func (c *checker) recordLit(e *ast.RecordLit, want Type) Type {
	shape := &Record{}
	seen := map[string]bool{}
	bad := false
	for _, a := range e.Fields {
		if a.Name == nil {
			shape.Positional = append(shape.Positional, nil)
			continue
		}
		if seen[a.Name.Name] {
			c.errorAt(a.Name, "give each field its own name", "field '%s' is given twice", a.Name.Name)
			bad = true
		}
		seen[a.Name.Name] = true
		shape.Named = append(shape.Named, RecordField{Name: a.Name.Name})
	}
	sort.Slice(shape.Named, func(i, j int) bool { return shape.Named[i].Name < shape.Named[j].Name })
	hint, _ := want.(*Record)
	if hint != nil && !sameShape(hint, shape) {
		hint = nil
	}
	pos := 0
	for _, a := range e.Fields {
		var h Type
		if a.Name == nil {
			if hint != nil {
				h = hint.Positional[pos]
			}
			t := c.expr(a.Value, h)
			shape.Positional[pos] = c.recordValue(a.Value, t)
			pos++
			continue
		}
		if hint != nil {
			h = hint.Field(a.Name.Name)
		}
		t := c.recordValue(a.Value, c.expr(a.Value, h))
		for i := range shape.Named {
			if shape.Named[i].Name == a.Name.Name && shape.Named[i].Type == nil {
				shape.Named[i].Type = t
				break
			}
		}
	}
	for _, t := range shape.FieldTypes() {
		if t == Invalid || t == nil {
			bad = true
		}
	}
	if bad {
		return Invalid
	}
	// A record whose fields fit the expected record takes its type, so
	// `let r: (Shape, int) = (Circle(), 1)` holds a Shape.
	if hint != nil && !Identical(hint, shape) && fitsRecord(hint, shape) {
		return hint
	}
	return shape
}

// Reports whether each field of [got] can be used as the field of [want]
// with the same position or name.
func fitsRecord(want, got *Record) bool {
	wt, gt := want.FieldTypes(), got.FieldTypes()
	for i := range wt {
		if !assignableType(wt[i], gt[i]) {
			return false
		}
	}
	return true
}

// Returns the type [t] of a record field's value [x], reporting a value
// that is missing.
func (c *checker) recordValue(x ast.Expr, t Type) Type {
	if t == Void {
		c.errorAt(x, "", "this expression has no value to store in a record")
		return Invalid
	}
	if _, ok := x.(*ast.NilLit); ok && t == Invalid {
		return Invalid
	}
	return t
}

// Returns a pattern matching every field of [r], for hints.
func patternFor(r *Record) string {
	var parts []string
	for i := range r.Positional {
		parts = append(parts, fmt.Sprintf("v%d", i+1))
	}
	for _, f := range r.Named {
		parts = append(parts, ":"+f.Name)
	}
	if len(parts) == 1 && len(r.Positional) == 1 {
		return "(" + parts[0] + ",)"
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
