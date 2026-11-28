package codegen

import (
	"fmt"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/types"
)

// Bytes are a `vx_list` of one-byte elements whose storage holds no
// pointers.

// Returns C code for the built-in call [name] that creates Bytes, with
// the arguments [cs] and the result type [result].
func bytesBuiltin(name string, cs []string, result types.Type, line int) string {
	switch name {
	case "Bytes":
		return "vx_bytes_new(0, NULL)"
	case "Bytes.count":
		return fmt.Sprintf("vx_bytes_count(%s, %d)", cs[0], line)
	case "Bytes.list":
		return fmt.Sprintf("vx_bytes_from_list(%s, %d)", cs[0], line)
	}
	return fmt.Sprintf("({ vx_list *r = vx_bytes_from_hex(%s); r ? (%s){true, r} : (%s){false}; })", cs[0], ctype(result), ctype(result))
}

// Returns C code calling the built-in method of Bytes that [c] names, on
// the receiver and arguments [cs].
func bytesMember(c *types.Call, cs []string, result types.Type, line int) string {
	switch c.Builtin {
	case "push":
		return fmt.Sprintf("VX_PUSH(uint8_t, %s, vx_byte(%s, %d))", cs[0], cs[1], line)
	case "append":
		return fmt.Sprintf("vx_list_append(%s, %s)", cs[0], cs[1])
	case "copy":
		return "vx_list_copy(" + cs[0] + ")"
	case "toList":
		return "vx_bytes_list(" + cs[0] + ")"
	case "toHex":
		return "vx_bytes_hex(" + cs[0] + ")"
	}
	return fmt.Sprintf("({ vx_str r = vx_bytes_utf8(%s); r.ptr ? (%s){true, r} : (%s){false}; })", cs[0], ctype(result), ctype(result))
}

// Generates `for byte in b`, which visits the bytes there were at the
// start as ints, as a list loop does.
func (g *gen) bytesLoop(s *ast.ForStmt) {
	b, n, i := g.temp(), g.temp(), g.temp()
	g.line("{")
	g.ind++
	g.line("vx_list *%s = %s;", b, g.expr(s.Iter))
	g.line("int64_t %s = %s->len;", n, b)
	g.line("for (int64_t %s = 0; %s < %s && %s < %s->len; %s++) {", i, i, n, i, b, i)
	g.ind++
	if s.Index != nil {
		g.line("int64_t %s = %s;", g.declare(g.info.Defs[s.Index]), i)
	}
	elem := g.info.Defs[s.Elem]
	g.line("int64_t %s = ((uint8_t *)%s->data)[%s];", g.declare(elem), b, i)
	if s.Pattern != nil {
		g.letBind(s.Pattern, g.varName(elem))
	}
	g.ind--
	g.body(s.Body)
	g.line("}")
	g.ind--
	g.line("}")
}
