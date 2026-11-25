package codegen

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/types"
)

// Returns a C condition testing whether [x] matches [p]. Each name the
// pattern binds is assigned to its temporary as the test runs, so it is
// set once the condition holds.
func (g *gen) patCond(p ast.Pattern, x string) string {
	t := g.info.PatternTypes[p]
	switch p := p.(type) {
	case *ast.NamePattern:
		if cs := g.info.PatternCases[p]; cs != nil {
			return fmt.Sprintf("%s->tag == %d", x, cs.Index)
		}
		if k, ok := g.info.Uses[p.Name].(*types.Const); ok {
			v := g.constRef(k)
			switch kt := k.Type.(type) {
			case *types.Enum:
				return fmt.Sprintf("%s->tag == (%s)->tag", x, v)
			case *types.Record:
				return equal(kt, x, v)
			}
			if k.Type == types.String {
				return fmt.Sprintf("vx_str_eq(%s, %s)", x, v)
			}
			return fmt.Sprintf("%s == %s", x, v)
		}
		return g.patBind(g.info.Defs[p.Name], x)
	case *ast.CasePattern:
		if cs := g.info.PatternCases[p]; cs != nil {
			conds := []string{fmt.Sprintf("%s->tag == %d", x, cs.Index)}
			for i, a := range p.Args {
				f := cs.Fields[i]
				if a.Name != nil {
					for _, cf := range cs.Fields {
						if cf.Name == a.Name.Name {
							f = cf
						}
					}
				}
				conds = append(conds, g.patCond(a.Pattern, fmt.Sprintf("%s->u.c_%s.f_%s", x, cs.Name, f.Name)))
			}
			return joinConds(conds)
		}
		var conds []string
		for _, a := range p.Args {
			conds = append(conds, g.patCond(a.Pattern, fmt.Sprintf("%s->f_%s", x, a.Name.Name)))
		}
		return joinConds(conds)
	case *ast.RecordPattern:
		var conds []string
		pos := 0
		for _, f := range p.Fields {
			name := ""
			if f.Name == nil {
				pos++
				name = fmt.Sprintf("$%d", pos)
			} else {
				name = f.Name.Name
			}
			conds = append(conds, g.patCond(f.Pattern, x+"."+recordField(name)))
		}
		return joinConds(conds)
	case *ast.ListPattern:
		elem := t.(*types.List).Elem
		rest := -1
		for i, e := range p.Elems {
			if _, ok := e.(*ast.RestPattern); ok {
				rest = i
			}
		}
		fixed := len(p.Elems)
		conds := []string{fmt.Sprintf("%s->len == %d", x, fixed)}
		if rest >= 0 {
			fixed--
			conds[0] = fmt.Sprintf("%s->len >= %d", x, fixed)
		}
		for i, e := range p.Elems {
			switch {
			case i == rest:
				r := e.(*ast.RestPattern)
				if r.Bind != nil {
					slice := fmt.Sprintf("vx_list_new(sizeof(%s), %s->len - %d, ((%s*)%s->data) + %d)", ctype(elem), x, fixed, ctype(elem), x, i)
					conds = append(conds, g.patBind(g.info.Defs[r.Bind], slice))
				}
			case rest >= 0 && i > rest:
				conds = append(conds, g.patCond(e, fmt.Sprintf("((%s*)%s->data)[%s->len - %d]", ctype(elem), x, x, len(p.Elems)-i)))
			default:
				conds = append(conds, g.patCond(e, fmt.Sprintf("((%s*)%s->data)[%d]", ctype(elem), x, i)))
			}
		}
		return joinConds(conds)
	case *ast.LiteralPattern:
		if t == types.String {
			return fmt.Sprintf("vx_str_eq(%s, %s)", x, g.expr(p.Value))
		}
		return fmt.Sprintf("%s == %s", x, g.expr(p.Value))
	case *ast.RangePattern:
		op := "<"
		if p.Inclusive {
			op = "<="
		}
		return fmt.Sprintf("(%s >= %s && %s %s %s)", x, g.expr(p.Lo), x, op, g.expr(p.Hi))
	case *ast.RelationalPattern:
		op := strings.Trim(p.Op.String(), "'")
		if t == types.String {
			return fmt.Sprintf("(vx_str_cmp(%s, %s) %s 0)", x, g.expr(p.Value), op)
		}
		return fmt.Sprintf("(%s %s %s)", x, op, g.expr(p.Value))
	case *ast.OrPattern:
		alts := make([]string, len(p.Alts))
		for i, a := range p.Alts {
			alts[i] = "(" + g.patCond(a, x) + ")"
		}
		return "(" + strings.Join(alts, " || ") + ")"
	}
	return "1"
}

// Returns the condition storing [x] in the temporary of [v].
func (g *gen) patBind(v *types.Var, x string) string {
	if v == nil {
		return "1"
	}
	return fmt.Sprintf("(%s = %s, 1)", g.patTemps[v], x)
}

func joinConds(conds []string) string {
	var out []string
	for _, c := range conds {
		if c != "1" {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "1"
	}
	return strings.Join(out, " && ")
}

// Returns declarations of the temporaries for [vars]. Until the variables
// are bound, uses of them read the temporaries, as a guard does.
func (g *gen) patTempDecls(vars []*types.Var) []string {
	if g.patTemps == nil {
		g.patTemps = map[*types.Var]string{}
	}
	var out []string
	for _, v := range vars {
		t := g.temp()
		g.patTemps[v] = t
		g.vals[v] = t
		out = append(out, fmt.Sprintf("%s = {0};", decl(v.Type, t)))
	}
	return out
}

// Returns the statements binding [vars] to their temporaries.
func (g *gen) patBinds(vars []*types.Var) []string {
	var out []string
	for _, v := range vars {
		value := g.patTemps[v]
		if v.Boxed() {
			name := g.declare(v)
			out = append(out, fmt.Sprintf("%s = vx_alloc(sizeof(%s)); *%s = %s;", declPtr(v.Type, name), ctype(v.Type), name, value))
			g.vals[v], g.boxes[v] = "(*"+name+")", name
			continue
		}
		out = append(out, fmt.Sprintf("%s = %s;", decl(v.Type, g.declare(v)), value))
	}
	return out
}

// Writes the code binding the names of [p], a pattern that matches every
// value, taking apart [x].
func (g *gen) letBind(p ast.Pattern, x string) {
	vars := g.info.PatternVars[p]
	for _, d := range g.patTempDecls(vars) {
		g.line("%s", d)
	}
	if c := g.patCond(p, x); c != "1" {
		g.line("(void)(%s);", c)
	}
	for _, b := range g.patBinds(vars) {
		g.line("%s", b)
	}
}

// Generates `let pattern = value`.
func (g *gen) letPattern(s *ast.LetStmt) {
	t := g.info.Types[s.Value]
	tmp := g.temp()
	g.line("%s = %s;", decl(t, tmp), g.expr(s.Value))
	g.letBind(s.Pattern, tmp)
}

// Writes `if value case pattern when guard { ... } else { ... }`.
func (g *gen) ifCase(s *ast.IfStmt) {
	tmp := g.temp()
	g.line("{")
	g.ind++
	g.line("%s = %s;", decl(g.info.Types[s.Cond], tmp), g.expr(s.Cond))
	vars := g.info.PatternVars[s.Case]
	for _, d := range g.patTempDecls(vars) {
		g.line("%s", d)
	}
	c := g.patCond(s.Case, tmp)
	if s.Guard != nil {
		c = joinConds([]string{c, "(" + g.expr(s.Guard) + ")"})
	}
	g.line("if (%s) {", c)
	g.ind++
	for _, b := range g.patBinds(vars) {
		g.line("%s", b)
	}
	g.ind--
	g.body(s.Then)
	switch e := s.Else.(type) {
	case *ast.IfStmt:
		g.line("} else {")
		g.ind++
		g.ifStmt(e)
		g.ind--
	case *ast.Block:
		g.line("} else {")
		g.body(e)
	}
	g.line("}")
	g.ind--
	g.line("}")
}
