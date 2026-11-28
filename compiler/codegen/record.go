package codegen

import (
	"fmt"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
)

// A record is a C struct passed by value. Positional fields are p0, p1...
// and named ones n_<name>.

// Returns the C field holding the record field [name], `$1` or a name.
func recordField(name string) string {
	if strings.HasPrefix(name, "$") {
		var i int
		fmt.Sscanf(name[1:], "%d", &i)
		return fmt.Sprintf("p%d", i-1)
	}
	return "n_" + name
}

// Returns the C fields of [r], in the order of its FieldNames.
func recordFields(r *types.Record) []string {
	var fs []string
	for _, n := range r.FieldNames() {
		fs = append(fs, recordField(n))
	}
	return fs
}

// Writes the typedefs of the optional and record types, each after the
// value types it holds.
func (g *gen) valueTypedefs() {
	done := map[string]bool{}
	var emit func(t types.Type)
	emit = func(t types.Type) {
		name := ctype(t)
		if done[name] {
			return
		}
		switch t := t.(type) {
		case *types.Optional:
			done[name] = true
			if nullable(t) {
				return
			}
			emit(t.Elem)
			g.line("typedef struct { bool has; %s; } %s;", decl(t.Elem, "v"), name)
		case *types.Record:
			done[name] = true
			var fields []string
			for i, ft := range t.FieldTypes() {
				emit(ft)
				fields = append(fields, decl(ft, recordFields(t)[i])+";")
			}
			g.line("typedef struct { %s } %s;", strings.Join(fields, " "), name)
		case *types.Struct:
			if u := t.Underlying(); u != nil {
				emit(u)
			}
		}
	}
	for _, name := range sortedKeys(g.opts) {
		emit(g.opts[name])
	}
	for _, name := range sortedKeys(g.recs) {
		emit(g.recs[name])
	}
}

// Generates a record value. Its fields run in source order.
func (g *gen) recordLit(e *ast.RecordLit, r *types.Record) string {
	values := make([]ast.Expr, len(e.Fields))
	fields := make([]string, len(e.Fields))
	pos := 0
	for i, a := range e.Fields {
		values[i] = a.Value
		if a.Name == nil {
			fields[i] = fmt.Sprintf("p%d", pos)
			pos++
		} else {
			fields[i] = "n_" + a.Name.Name
		}
	}
	return g.ordered(values, func(cs []string) string {
		inits := make([]string, len(cs))
		for i, c := range cs {
			inits[i] = "." + fields[i] + " = " + c
		}
		return fmt.Sprintf("((%s){%s})", ctype(r), strings.Join(inits, ", "))
	})
}

// Writes the function that shows a record: `(1, "a")` or `(x: 1, y: 2)`.
func (g *gen) recordRepr(r *types.Record) {
	names, types := r.FieldNames(), r.FieldTypes()
	for i, n := range names {
		label := ", "
		if i == 0 {
			label = "("
		}
		if !strings.HasPrefix(n, "$") {
			label += n + ": "
		}
		g.line("    %s", writeLit("b", label))
		g.line("    %s", write(types[i], "b", "v."+recordField(n)))
	}
	end := ")"
	if len(names) == 1 && len(r.Positional) == 1 {
		end = ",)"
	}
	if len(names) == 0 {
		end = "()"
	}
	g.line("    %s", writeLit("b", end))
}
