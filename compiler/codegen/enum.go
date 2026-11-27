package codegen

import (
	"fmt"
	"strings"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/types"
)

func enumName(e *types.Enum) string { return "E_" + enumIdent(e) }

// Returns a C-safe name for [e], tagged with its module.
func enumIdent(e *types.Enum) string {
	if e.Args == nil {
		return modTag(e.Module) + cid(e.Name)
	}
	return modTag(e.Module) + cid(e.Base) + "__" + mangleList(e.Args)
}

// Returns the name of the function creating values of [cs].
func caseFunc(cs *types.Case) string {
	id := enumIdent(cs.Enum)
	return fmt.Sprintf("case%d%s_%s", len(id), id, cid(cs.Name))
}

func caseSig(cs *types.Case) string {
	var params []string
	for _, f := range cs.Fields {
		params = append(params, decl(f.Type, "f_"+f.Name))
	}
	if len(params) == 0 {
		params = []string{"void"}
	}
	return fmt.Sprintf("%s *%s(%s)", enumName(cs.Enum), caseFunc(cs), strings.Join(params, ", "))
}

// Writes the C struct of [e]: a tag naming the case, and a union with the
// values of each case that carries any.
func (g *gen) enumStruct(e *types.Enum) {
	g.line("struct %s {", enumName(e))
	if len(e.Interfaces) > 0 {
		g.line("    const vx_desc *vx_t;")
	}
	g.line("    int32_t tag;")
	if !e.Simple() {
		g.line("    union {")
		for _, cs := range e.Cases {
			if len(cs.Fields) == 0 {
				continue
			}
			var fields []string
			for _, f := range cs.Fields {
				fields = append(fields, decl(f.Type, "f_"+f.Name)+";")
			}
			g.line("        struct { %s } c_%s;", strings.Join(fields, " "), cs.Name)
		}
		g.line("    } u;")
	}
	g.line("};")
}

func (g *gen) caseConstructor(cs *types.Case) {
	e := enumName(cs.Enum)
	g.line("")
	g.line("static %s {", caseSig(cs))
	g.line("    %s *o = vx_alloc(sizeof(%s));", e, e)
	if len(cs.Enum.Interfaces) > 0 {
		g.line("    o->vx_t = &desc_%s;", e)
	}
	g.line("    o->tag = %d;", cs.Index)
	for _, f := range cs.Fields {
		g.line("    o->u.c_%s.f_%s = f_%s;", cs.Name, f.Name, f.Name)
	}
	g.line("    return o;")
	g.line("}")
}

// Returns the declarations of the temporaries of every arm of [m].
func (g *gen) armTemps(m *ast.MatchExpr) []string {
	var out []string
	for _, a := range g.info.Matches[m].Arms {
		out = append(out, g.patTempDecls(a.Vars)...)
	}
	return out
}

// Returns the opening of arm [i]: `if (...) {`, `} else if (...) {`, or
// `} else {` for the last arm, which the checker guarantees matches
// whatever the earlier arms didn't. The arm's bindings follow.
func (g *gen) armHeader(m *ast.MatchExpr, i int, subject string) string {
	arm := m.Arms[i]
	vars := g.info.Matches[m].Arms[i].Vars
	c := g.patCond(arm.Pattern, subject)
	var head string
	if i == len(m.Arms)-1 {
		head = "{"
		if i > 0 {
			head = "} else {"
		}
		if len(vars) > 0 && c != "1" {
			head += " (void)(" + c + ");"
		}
	} else {
		if arm.Guard != nil {
			c = joinConds([]string{c, "(" + g.expr(arm.Guard) + ")"})
		}
		head = "if (" + c + ") {"
		if i > 0 {
			head = "} else " + head
		}
	}
	for _, b := range g.patBinds(vars) {
		head += " " + b
	}
	return head
}

// Writes a match used as a statement.
func (g *gen) matchStmt(m *ast.MatchExpr) {
	if k := g.info.Matches[m].Known; k >= 0 {
		if arm := m.Arms[k]; arm.Block != nil {
			g.block("", arm.Block)
		} else {
			g.line("%s;", g.expr(arm.Expr))
		}
		return
	}
	subject := g.temp()
	g.line("{")
	g.ind++
	g.line("%s = %s;", decl(g.info.Types[m.Subject], subject), g.expr(m.Subject))
	for _, d := range g.armTemps(m) {
		g.line("%s", d)
	}
	for i, arm := range m.Arms {
		g.line("%s", g.armHeader(m, i, subject))
		g.ind++
		if arm.Block != nil {
			g.block("", arm.Block)
		} else {
			g.line("%s;", g.expr(arm.Expr))
		}
		g.ind--
	}
	g.line("}")
	g.ind--
	g.line("}")
}

// Returns a match used as a value, as a statement expression.
func (g *gen) matchExpr(m *ast.MatchExpr) string {
	t := g.info.Types[m]
	if k := g.info.Matches[m].Known; k >= 0 {
		x := g.expr(m.Arms[k].Expr)
		switch {
		case t == types.Void:
			return "({ " + x + "; })"
		case g.info.Matches[m].Arms[k].NoValue:
			r := g.temp()
			return fmt.Sprintf("({ %s; %s; %s; })", decl(t, r), x, r)
		}
		r := g.temp()
		return fmt.Sprintf("({ %s = %s; %s; })", decl(t, r), x, r)
	}
	subject, result := g.temp(), g.temp()
	var b strings.Builder
	fmt.Fprintf(&b, "({ %s = %s; ", decl(g.info.Types[m.Subject], subject), g.expr(m.Subject))
	if t != types.Void {
		fmt.Fprintf(&b, "%s; ", decl(t, result))
	}
	for _, d := range g.armTemps(m) {
		b.WriteString(d + " ")
	}
	for i, arm := range m.Arms {
		b.WriteString(g.armHeader(m, i, subject) + " ")
		if t == types.Void || g.info.Matches[m].Arms[i].NoValue {
			fmt.Fprintf(&b, "%s; ", g.expr(arm.Expr))
		} else {
			fmt.Fprintf(&b, "%s = %s; ", result, g.expr(arm.Expr))
		}
	}
	b.WriteString("} ")
	if t != types.Void {
		b.WriteString(result + "; ")
	}
	b.WriteString("})")
	return b.String()
}
