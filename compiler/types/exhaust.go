package types

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
)

// A pattern reduced to what matters for coverage.
type spat struct {
	kind  spatKind
	key   string
	args  []*spat
	rest  int
	alts  []*spat
	shown string
}

type spatKind int

const (
	sWild spatKind = iota
	sCtor
	sList
	sOpaque
	sOr
)

var wild = &spat{kind: sWild}

type patRow struct {
	pats []*spat
}

// Reduces [p], which tests a value of type [t], for coverage checks.
func (c *checker) lower(p ast.Pattern, t Type) *spat {
	switch p := p.(type) {
	case *ast.NamePattern:
		if cs := c.info.PatternCases[p]; cs != nil {
			return &spat{kind: sCtor, key: cs.Name, args: wilds(len(cs.Fields))}
		}
		if k, ok := c.info.Uses[p.Name].(*Const); ok {
			if v, ok := k.Value.(bool); ok && t == Bool {
				return &spat{kind: sCtor, key: strconv.FormatBool(v)}
			}
			return &spat{kind: sOpaque, key: "const " + k.String()}
		}
	case *ast.CasePattern:
		if cs := c.info.PatternCases[p]; cs != nil {
			args := wilds(len(cs.Fields))
			for i, a := range p.Args {
				j := i
				if a.Name != nil {
					j = fieldIndex(cs.Fields, a.Name.Name)
				}
				if j >= 0 && j < len(args) {
					args[j] = c.lower(a.Pattern, cs.Fields[j].Type)
				}
			}
			return &spat{kind: sCtor, key: cs.Name, args: args}
		}
		if s := c.info.PatternStructs[p]; s != nil {
			args := wilds(len(s.Fields))
			for _, a := range p.Args {
				if a.Name == nil {
					continue
				}
				for j, f := range s.Fields {
					if f.Name == a.Name.Name {
						args[j] = c.lower(a.Pattern, f.Type)
					}
				}
			}
			return &spat{kind: sCtor, key: "struct", args: args}
		}
	case *ast.RecordPattern:
		r, ok := t.(*Record)
		if !ok {
			return wild
		}
		names, types := r.FieldNames(), r.FieldTypes()
		args := wilds(len(names))
		pos := 0
		for _, f := range p.Fields {
			name := ""
			if f.Name == nil {
				pos++
				name = fmt.Sprintf("$%d", pos)
			} else {
				name = f.Name.Name
			}
			for j, n := range names {
				if n == name {
					args[j] = c.lower(f.Pattern, types[j])
				}
			}
		}
		return &spat{kind: sCtor, key: "record", args: args}
	case *ast.ListPattern:
		l, ok := t.(*List)
		if !ok {
			return wild
		}
		s := &spat{kind: sList, rest: -1}
		for _, e := range p.Elems {
			if _, ok := e.(*ast.RestPattern); ok {
				if s.rest < 0 {
					s.rest = len(s.args)
				}
				continue
			}
			s.args = append(s.args, c.lower(e, l.Elem))
		}
		return s
	case *ast.LiteralPattern:
		key := ast.PatternString(p)
		if t == Bool {
			return &spat{kind: sCtor, key: key}
		}
		return &spat{kind: sOpaque, key: key}
	case *ast.RangePattern, *ast.RelationalPattern:
		return &spat{kind: sOpaque, key: fmt.Sprintf("%p", p)}
	case *ast.OrPattern:
		s := &spat{kind: sOr}
		for _, a := range p.Alts {
			s.alts = append(s.alts, c.lower(a, t))
		}
		return s
	}
	return wild
}

func wilds(n int) []*spat {
	out := make([]*spat, n)
	for i := range out {
		out[i] = wild
	}
	return out
}

func fieldIndex(fs []*Field, name string) int {
	for i, f := range fs {
		if f.Name == name {
			return i
		}
	}
	return -1
}

// A constructor of a type with finitely many shapes.
type ctor struct {
	key   string
	types []Type
	names []string

	// For lists: the length, and whether it stands for every longer
	// length too.
	length int
	open   bool
}

type exhauster struct{ c *checker }

func (c *checker) exhaust() exhauster { return exhauster{c} }

// Returns the constructors of [t], or nil when it has too many to list.
// For lists the lengths that matter come from the patterns in [col].
func ctors(t Type, col []*spat) []ctor {
	switch t := t.(type) {
	case *Enum:
		out := make([]ctor, len(t.Cases))
		for i, cs := range t.Cases {
			k := ctor{key: cs.Name}
			for _, f := range cs.Fields {
				k.types = append(k.types, f.Type)
				k.names = append(k.names, f.Name)
			}
			out[i] = k
		}
		return out
	case *Record:
		return []ctor{{key: "record", types: t.FieldTypes(), names: t.FieldNames()}}
	case *Struct:
		k := ctor{key: "struct"}
		for _, f := range t.Fields {
			k.types = append(k.types, f.Type)
			k.names = append(k.names, f.Name)
		}
		return []ctor{k}
	case *List:
		longest := 0
		var walk func(*spat)
		walk = func(s *spat) {
			switch s.kind {
			case sList:
				longest = max(longest, len(s.args))
			case sOr:
				for _, a := range s.alts {
					walk(a)
				}
			}
		}
		for _, s := range col {
			walk(s)
		}
		out := make([]ctor, longest+2)
		for n := range out {
			k := ctor{key: "list", length: n, open: n == longest+1}
			for range n {
				k.types = append(k.types, t.Elem)
			}
			out[n] = k
		}
		return out
	}
	if t == Bool {
		return []ctor{{key: "true"}, {key: "false"}}
	}
	return nil
}

// Expands or-patterns at the head of each row.
func expand(rows []patRow) []patRow {
	var out []patRow
	for _, r := range rows {
		if len(r.pats) > 0 && r.pats[0].kind == sOr {
			for _, a := range r.pats[0].alts {
				pats := append([]*spat{a}, r.pats[1:]...)
				out = append(out, expand([]patRow{{pats}})...)
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// Returns the arguments of [s] when it matches values built by [k], or
// false when it can't.
func specialize(s *spat, k ctor) ([]*spat, bool) {
	switch s.kind {
	case sWild:
		return wilds(len(k.types)), true
	case sCtor:
		if s.key == k.key {
			return s.args, true
		}
	case sList:
		if k.key != "list" {
			return nil, false
		}
		if s.rest < 0 {
			if len(s.args) == k.length && !k.open {
				return s.args, true
			}
			return nil, false
		}
		if len(s.args) > k.length {
			return nil, false
		}
		out := append([]*spat{}, s.args[:s.rest]...)
		out = append(out, wilds(k.length-len(s.args))...)
		return append(out, s.args[s.rest:]...), true
	case sOpaque:
		if s.key == k.key {
			return nil, true
		}
	}
	return nil, false
}

func specializeRows(rows []patRow, k ctor) []patRow {
	var out []patRow
	for _, r := range expand(rows) {
		if args, ok := specialize(r.pats[0], k); ok {
			out = append(out, patRow{append(append([]*spat{}, args...), r.pats[1:]...)})
		}
	}
	return out
}

// Returns values, one per column of types [ts], that no row matches, or
// nil when the rows cover every value.
func (e exhauster) missing(rows []patRow, ts []Type) []string {
	if len(ts) == 0 {
		if len(rows) == 0 {
			return []string{}
		}
		return nil
	}
	rows = expand(rows)
	col := make([]*spat, len(rows))
	any := false
	for i, r := range rows {
		col[i] = r.pats[0]
		any = any || r.pats[0].kind != sWild
	}
	ks := ctors(ts[0], col)
	if !any || ks == nil {
		var rest []patRow
		for _, r := range rows {
			if r.pats[0].kind == sWild {
				rest = append(rest, patRow{r.pats[1:]})
			}
		}
		if w := e.missing(rest, ts[1:]); w != nil {
			return append([]string{"_"}, w...)
		}
		return nil
	}
	for _, k := range ks {
		n := len(k.types)
		w := e.missing(specializeRows(rows, k), append(append([]Type{}, k.types...), ts[1:]...))
		if w != nil {
			return append([]string{show(ts[0], k, w[:n])}, w[n:]...)
		}
	}
	return nil
}

// Reports whether some value matched by [q] isn't matched by any row.
func (e exhauster) useful(rows []patRow, q []*spat, ts []Type) bool {
	if len(ts) == 0 || len(rows) == 0 {
		return len(rows) == 0
	}
	h := q[0]
	if h.kind == sOr {
		for _, a := range h.alts {
			if e.useful(rows, append([]*spat{a}, q[1:]...), ts) {
				return true
			}
		}
		return false
	}
	rows = expand(rows)
	col := []*spat{h}
	for _, r := range rows {
		col = append(col, r.pats[0])
	}
	ks := ctors(ts[0], col)
	// A wildcard over rows that all start with wildcards needs only the
	// rest of the columns. Expanding it into each constructor would never
	// end for a type that contains itself.
	if ks != nil && h.kind == sWild && allWild(rows) {
		ks = nil
	}
	if ks == nil {
		k := ctor{key: h.key}
		if h.kind == sWild {
			var rest []patRow
			for _, r := range rows {
				if r.pats[0].kind == sWild {
					rest = append(rest, patRow{r.pats[1:]})
				}
			}
			return e.useful(rest, q[1:], ts[1:])
		}
		return e.useful(specializeRows(rows, k), q[1:], ts[1:])
	}
	if h.kind == sOpaque {
		// A value the compiler can't see, such as a constant enum, may
		// match any of the constructors.
		return true
	}
	for _, k := range ks {
		args, ok := specialize(h, k)
		if !ok {
			continue
		}
		sub := append(append([]*spat{}, args...), q[1:]...)
		if e.useful(specializeRows(rows, k), sub, append(append([]Type{}, k.types...), ts[1:]...)) {
			return true
		}
	}
	return false
}

// Reports whether every row starts with a wildcard.
func allWild(rows []patRow) bool {
	for _, r := range rows {
		if r.pats[0].kind != sWild {
			return false
		}
	}
	return true
}

// Shows a value built by [k] from [args], for messages.
func show(t Type, k ctor, args []string) string {
	switch k.key {
	case "record", "struct":
		var parts []string
		wild := true
		for i, a := range args {
			wild = wild && a == "_"
			switch {
			case k.key == "struct" && a == "_":
			case strings.HasPrefix(k.names[i], "$"):
				parts = append(parts, a)
			default:
				parts = append(parts, k.names[i]+": "+a)
			}
		}
		if wild {
			return "_"
		}
		if k.key == "struct" {
			return t.(*Struct).Name + "(" + strings.Join(parts, ", ") + ")"
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case "list":
		if k.open {
			args = append(args, "...")
		}
		return "[" + strings.Join(args, ", ") + "]"
	}
	if len(args) == 0 {
		return k.key
	}
	return k.key + "(" + strings.Join(args, ", ") + ")"
}
