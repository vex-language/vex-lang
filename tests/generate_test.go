package tests

import (
	"bytes"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/irshadio/vex-lang/compiler/cc"
	"github.com/irshadio/vex-lang/compiler/codegen"
	"github.com/irshadio/vex-lang/compiler/diag"
	"github.com/irshadio/vex-lang/compiler/parser"
	"github.com/irshadio/vex-lang/compiler/types"
)

var (
	genCount = flag.Int("programs", 12, "number of random programs TestGeneratedPrograms runs")
	genSeed  = flag.Int64("seed", 0, "run only the random program with this seed")
)

// Compiles and runs random valid programs whose output is known in advance.
// Each program mixes ints, strings, lists, optionals, structs, enums,
// closures, loops and match, and must print exactly what the generator
// computed, both normally and while the collector runs on every allocation.
//
// Reproduce a failure with: go test ./tests -run TestGeneratedPrograms -seed N
func TestGeneratedPrograms(t *testing.T) {
	if _, err := cc.Command(); err != nil {
		t.Skip(err)
	}
	seeds := make([]int64, *genCount)
	for i := range seeds {
		seeds[i] = int64(i + 1)
	}
	if *genSeed != 0 {
		seeds = []int64{*genSeed}
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			t.Parallel()
			src, want, overflows := generateProgram(seed)
			dir := t.TempDir()
			path := filepath.Join(dir, "main.vx")
			os.WriteFile(path, []byte(src), 0o644)
			file := diag.NewFile("main.vx", src)
			tree, err := parser.Parse(file)
			if err != nil {
				t.Fatalf("generated program doesn't parse (%s):\n%v", path, err)
			}
			info, err := types.Check(file, tree)
			if err != nil {
				t.Fatalf("generated program doesn't type check (%s):\n%v", path, err)
			}
			exe := filepath.Join(dir, "main")
			if err := cc.Compile(codegen.Generate(info, "main.vx"), exe); err != nil {
				t.Fatal(err)
			}
			for _, env := range [][]string{nil, {"VEX_GC_STRESS=1", "VEX_GC_VERIFY=1"}} {
				cmd := exec.Command(exe)
				cmd.Env = append(os.Environ(), env...)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				got, err := cmd.Output()
				if overflows {
					// The program must stop at the overflow, after printing
					// everything that came before it.
					if code := cmd.ProcessState.ExitCode(); code != 1 || !strings.HasPrefix(stderr.String(), "error: integer overflow") {
						t.Fatalf("%s expected an integer overflow error, got exit %d and %q\nprogram: %s", env, code, stderr.String(), keep(t, src))
					}
				} else if err != nil {
					t.Fatalf("%s %v\n%s\nprogram: %s", env, err, stderr.String(), keep(t, src))
				}
				if string(got) != want {
					t.Fatalf("%s output differs at %s\nprogram: %s", env, firstDiff(string(got), want), keep(t, src))
				}
			}
		})
	}
}

// Saves a failing program where it survives the test, returning its path.
func keep(t *testing.T, src string) string {
	path := filepath.Join(os.TempDir(), strings.ReplaceAll(t.Name(), "/", "_")+".vx")
	os.WriteFile(path, []byte(src), 0o644)
	return path
}

func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var a, b string
		if i < len(g) {
			a = g[i]
		}
		if i < len(w) {
			b = w[i]
		}
		if a != b {
			return fmt.Sprintf("output line %d:\n got: %q\nwant: %q", i+1, a, b)
		}
	}
	return "?"
}

// --- The generator ---

type typ int

const (
	tInt typ = iota
	tBool
	tStr
	tList
	tOpt
	tPoint
	tShape
	tFnInt     // fn(int) -> int
	tFnVoid    // fn()
	valueTypes = tShape + 1
)

var typeNames = map[typ]string{
	tInt: "int", tBool: "bool", tStr: "string", tList: "[int]", tOpt: "int?", tPoint: "Point", tShape: "Shape",
}

// Values of the generated program as the generator evaluates it.
type (
	point struct {
		a int64
		s string
	}
	shape struct {
		tag int // 0 none, 1 one, 2 two
		x   int64
		s   string
	}
	fnInt  func(env, int64) int64
	fnVoid func(env)
)

// Variable values by name; names are unique, so one map serves every scope.
type env map[string]any

type expr struct {
	code string
	eval func(env) any
}

type stmt struct {
	lines []string
	exec  func(env, *strings.Builder)
}

type variable struct {
	name    string
	t       typ
	mutable bool
}

type generator struct {
	r     *rand.Rand
	scope []variable
	n     int
	depth int
}

const prelude = `struct Point {
    a: int
    s: string

    fn sum() -> int => a + s.len
}

enum Shape {
    none
    one(x: int)
    two(x: int, s: string)
}

fn even(x: int) -> int? {
    if x % 2 == 0 {
        return x
    }
    return nil
}

fn twice(f: fn(int) -> int, x: int) -> int => f(f(x))

`

// Raised by the generator's checked arithmetic where the program must stop.
type overflow struct{}

func addC(a, b int64) int64 {
	r := a + b
	if (a^r)&(b^r) < 0 {
		panic(overflow{})
	}
	return r
}

func subC(a, b int64) int64 {
	r := a - b
	if (a^b)&(a^r) < 0 {
		panic(overflow{})
	}
	return r
}

func mulC(a, b int64) int64 {
	r := a * b
	if a != 0 && (r/a != b || a == -1 && b == math.MinInt64) {
		panic(overflow{})
	}
	return r
}

func negC(a int64) int64 {
	if a == math.MinInt64 {
		panic(overflow{})
	}
	return -a
}

// The longest string a generated program may build. Strings that append to
// themselves in nested loops grow exponentially, so a program that would
// pass this is replaced.
const maxGenString = 1 << 16

// A panic value raised when a generated program builds a string longer than
// [maxGenString].
type tooBig struct{}

// Returns [s], or panics with [tooBig] if it is too long.
func capped(s string) string {
	if len(s) > maxGenString {
		panic(tooBig{})
	}
	return s
}

// Returns a random program, the output it must print, and whether it must
// then stop with an integer overflow error. A program whose strings grow
// too long is replaced by the next one from the same seed.
func generateProgram(seed int64) (src string, want string, overflows bool) {
	r := rand.New(rand.NewSource(seed))
	for {
		if src, want, overflows, ok := tryProgram(r); ok {
			return src, want, overflows
		}
	}
}

// Generates one program from [r], reporting false if it builds a string
// longer than [maxGenString].
func tryProgram(r *rand.Rand) (src string, want string, overflows bool, ok bool) {
	g := &generator{r: r}
	var stmts []stmt
	for i := 0; i < 120; i++ {
		stmts = append(stmts, g.stmt(0))
	}
	var b strings.Builder
	b.WriteString(prelude)
	b.WriteString("fn main() {\n")
	for _, s := range stmts {
		for _, l := range s.lines {
			b.WriteString("    " + l + "\n")
		}
	}
	b.WriteString("}\n")
	var out strings.Builder
	defer func() {
		switch r := recover().(type) {
		case nil:
		case overflow:
			want, overflows, ok = out.String(), true, true
		case tooBig:
			ok = false
		default:
			panic(r)
		}
	}()
	e := env{}
	src = b.String()
	for _, s := range stmts {
		s.exec(e, &out)
	}
	return src, out.String(), false, true
}

func (g *generator) name(prefix string) string {
	g.n++
	return fmt.Sprintf("%s%d", prefix, g.n)
}

func (g *generator) vars(t typ, mutable bool) []variable {
	var vs []variable
	for _, v := range g.scope {
		if v.t == t && (!mutable || v.mutable) {
			vs = append(vs, v)
		}
	}
	return vs
}

func (g *generator) pick(vs []variable) variable { return vs[g.r.Intn(len(vs))] }

func ref(name string) func(env) any { return func(e env) any { return e[name] } }

// --- Statements ---

func (g *generator) stmt(depth int) stmt {
	for {
		switch g.r.Intn(14) {
		case 0, 1, 2:
			return g.let()
		case 3, 4:
			return g.println()
		case 5:
			if s, ok := g.assign(); ok {
				return s
			}
		case 6:
			if s, ok := g.fieldAssign(); ok {
				return s
			}
		case 7:
			if depth < 2 {
				return g.forLoop(depth)
			}
		case 8:
			if depth < 2 {
				return g.ifElse(depth)
			}
		case 9:
			if depth < 2 {
				return g.ifLet(depth)
			}
		case 10:
			if depth < 2 {
				return g.matchStmt(depth)
			}
		case 11:
			if s, ok := g.closure(); ok {
				return s
			}
		case 12:
			if vs := g.vars(tFnVoid, false); len(vs) > 0 {
				v := g.pick(vs)
				return stmt{[]string{v.name + "()"}, func(e env, _ *strings.Builder) { e[v.name].(fnVoid)(e) }}
			}
		case 13:
			if vs := g.vars(tPoint, false); len(vs) > 0 {
				// An alias shares the struct: later changes show through both.
				v := g.pick(vs)
				name := g.name("p")
				g.scope = append(g.scope, variable{name, tPoint, false})
				return stmt{[]string{"let " + name + " = " + v.name}, func(e env, _ *strings.Builder) { e[name] = e[v.name] }}
			}
		}
	}
}

func (g *generator) let() stmt {
	t := typ(g.r.Intn(int(valueTypes)))
	x := g.expr(t)
	name := g.name("v")
	mutable := (t == tInt || t == tStr) && g.r.Intn(2) == 0
	kw := "let"
	if mutable {
		kw = "var"
	}
	decl := kw + " " + name
	if t == tOpt || t == tShape || g.r.Intn(4) == 0 {
		decl += ": " + typeNames[t]
	}
	if t == tOpt && g.r.Intn(4) == 0 {
		x = expr{"nil", func(env) any { return (*int64)(nil) }}
	}
	if t == tShape && g.r.Intn(2) == 0 {
		x = g.implicitShape()
	}
	g.scope = append(g.scope, variable{name, t, mutable})
	return stmt{[]string{decl + " = " + x.code}, func(e env, _ *strings.Builder) { e[name] = x.eval(e) }}
}

func (g *generator) println() stmt {
	t := typ(g.r.Intn(int(valueTypes)))
	x := g.expr(t)
	return stmt{[]string{"println(" + x.code + ")"}, func(e env, out *strings.Builder) {
		out.WriteString(show(x.eval(e)) + "\n")
	}}
}

func (g *generator) assign() (stmt, bool) {
	vs := append(g.vars(tInt, true), g.vars(tStr, true)...)
	if len(vs) == 0 {
		return stmt{}, false
	}
	v := g.pick(vs)
	x := g.expr(v.t)
	if v.t == tStr {
		if g.r.Intn(2) == 0 {
			return stmt{[]string{v.name + " += " + x.code}, func(e env, _ *strings.Builder) {
				e[v.name] = capped(e[v.name].(string) + x.eval(e).(string))
			}}, true
		}
		return stmt{[]string{v.name + " = " + x.code}, func(e env, _ *strings.Builder) { e[v.name] = x.eval(e) }}, true
	}
	switch g.r.Intn(5) {
	case 0:
		return stmt{[]string{v.name + " += " + x.code}, func(e env, _ *strings.Builder) {
			old := e[v.name].(int64)
			e[v.name] = addC(old, x.eval(e).(int64))
		}}, true
	case 1:
		return stmt{[]string{v.name + " -= " + x.code}, func(e env, _ *strings.Builder) {
			old := e[v.name].(int64)
			e[v.name] = subC(old, x.eval(e).(int64))
		}}, true
	case 2:
		return stmt{[]string{v.name + " &+= " + x.code}, func(e env, _ *strings.Builder) {
			old := e[v.name].(int64)
			e[v.name] = old + x.eval(e).(int64)
		}}, true
	case 3:
		return stmt{[]string{v.name + " &*= " + x.code}, func(e env, _ *strings.Builder) {
			old := e[v.name].(int64)
			e[v.name] = old * x.eval(e).(int64)
		}}, true
	}
	return stmt{[]string{v.name + " = " + x.code}, func(e env, _ *strings.Builder) { e[v.name] = x.eval(e) }}, true
}

func (g *generator) fieldAssign() (stmt, bool) {
	vs := g.vars(tPoint, false)
	if len(vs) == 0 {
		return stmt{}, false
	}
	v := g.pick(vs)
	if g.r.Intn(2) == 0 {
		x := g.expr(tInt)
		return stmt{[]string{v.name + ".a += " + x.code}, func(e env, _ *strings.Builder) {
			p := e[v.name].(*point)
			old := p.a
			p.a = addC(old, x.eval(e).(int64))
		}}, true
	}
	x := g.expr(tStr)
	return stmt{[]string{v.name + ".s = " + x.code}, func(e env, _ *strings.Builder) {
		p := e[v.name].(*point)
		p.s = x.eval(e).(string)
	}}, true
}

// Runs [body] statements in a new scope, returning them and restoring the
// scope afterwards.
func (g *generator) block(depth, n int, extra ...variable) []stmt {
	saved := len(g.scope)
	g.scope = append(g.scope, extra...)
	var body []stmt
	for i := 0; i < n; i++ {
		body = append(body, g.stmt(depth+1))
	}
	g.scope = g.scope[:saved]
	return body
}

func indent(body []stmt) []string {
	var lines []string
	for _, s := range body {
		for _, l := range s.lines {
			lines = append(lines, "    "+l)
		}
	}
	return lines
}

func run(body []stmt, e env, out *strings.Builder) {
	for _, s := range body {
		s.exec(e, out)
	}
}

func (g *generator) forLoop(depth int) stmt {
	i := g.name("i")
	limit := g.r.Intn(5)
	if g.r.Intn(2) == 0 {
		elems := g.expr(tList)
		body := g.block(depth, 1+g.r.Intn(3), variable{i, tInt, false})
		lines := append([]string{"for " + i + " in " + elems.code + " {"}, indent(body)...)
		return stmt{append(lines, "}"), func(e env, out *strings.Builder) {
			for _, x := range elems.eval(e).([]int64) {
				e[i] = x
				run(body, e, out)
			}
		}}
	}
	body := g.block(depth, 1+g.r.Intn(3), variable{i, tInt, false})
	lines := append([]string{fmt.Sprintf("for %s in 0..%d {", i, limit)}, indent(body)...)
	return stmt{append(lines, "}"), func(e env, out *strings.Builder) {
		for x := 0; x < limit; x++ {
			e[i] = int64(x)
			run(body, e, out)
		}
	}}
}

func (g *generator) ifElse(depth int) stmt {
	c := g.expr(tBool)
	then := g.block(depth, 1+g.r.Intn(2))
	els := g.block(depth, 1+g.r.Intn(2))
	lines := append([]string{"if " + c.code + " {"}, indent(then)...)
	lines = append(lines, "} else {")
	lines = append(append(lines, indent(els)...), "}")
	return stmt{lines, func(e env, out *strings.Builder) {
		if c.eval(e).(bool) {
			run(then, e, out)
		} else {
			run(els, e, out)
		}
	}}
}

func (g *generator) ifLet(depth int) stmt {
	o := g.expr(tOpt)
	b := g.name("u")
	then := g.block(depth, 1+g.r.Intn(2), variable{b, tInt, false})
	els := g.block(depth, 1)
	lines := append([]string{"if let " + b + " = " + o.code + " {"}, indent(then)...)
	lines = append(lines, "} else {")
	lines = append(append(lines, indent(els)...), "}")
	return stmt{lines, func(e env, out *strings.Builder) {
		if v := o.eval(e).(*int64); v != nil {
			e[b] = *v
			run(then, e, out)
		} else {
			run(els, e, out)
		}
	}}
}

func (g *generator) matchStmt(depth int) stmt {
	s := g.expr(tShape)
	x1, x2, s2 := g.name("m"), g.name("m"), g.name("m")
	none := g.block(depth, 1)
	one := g.block(depth, 1, variable{x1, tInt, false})
	two := g.block(depth, 1, variable{x2, tInt, false}, variable{s2, tStr, false})
	lines := []string{"match " + s.code + " {", "    none => {"}
	lines = append(append(lines, indent(indentBody(none))...), "    }", "    one("+x1+") => {")
	lines = append(append(lines, indent(indentBody(one))...), "    }", "    two("+x2+", "+s2+") => {")
	lines = append(append(lines, indent(indentBody(two))...), "    }", "}")
	return stmt{lines, func(e env, out *strings.Builder) {
		v := s.eval(e).(shape)
		switch v.tag {
		case 0:
			run(none, e, out)
		case 1:
			e[x1] = v.x
			run(one, e, out)
		default:
			e[x2], e[s2] = v.x, v.s
			run(two, e, out)
		}
	}}
}

func indentBody(body []stmt) []stmt {
	out := make([]stmt, len(body))
	for i, s := range body {
		out[i] = stmt{indent([]stmt{s}), s.exec}
	}
	return out
}

// Declares a closure that captures a var by reference: later changes to the
// var show inside it, and its own changes show outside.
func (g *generator) closure() (stmt, bool) {
	vs := g.vars(tInt, true)
	if len(vs) == 0 {
		return stmt{}, false
	}
	v := g.pick(vs)
	k := int64(g.r.Intn(20) - 10)
	name := g.name("f")
	if g.r.Intn(2) == 0 {
		g.scope = append(g.scope, variable{name, tFnVoid, false})
		code := fmt.Sprintf("let %s = fn() { %s += %d }", name, v.name, k)
		return stmt{[]string{code}, func(e env, _ *strings.Builder) {
			e[name] = fnVoid(func(e env) { e[v.name] = e[v.name].(int64) + k })
		}}, true
	}
	g.scope = append(g.scope, variable{name, tFnInt, false})
	code := fmt.Sprintf("let %s = fn(y: int) -> int => y * %d + %s", name, k, v.name)
	return stmt{[]string{code}, func(e env, _ *strings.Builder) {
		e[name] = fnInt(func(e env, y int64) int64 { return addC(mulC(y, k), e[v.name].(int64)) })
	}}, true
}

// --- Expressions ---

func (g *generator) expr(t typ) expr {
	g.depth++
	defer func() { g.depth-- }()
	leaf := g.depth > 3 || g.r.Intn(3) == 0
	if vs := g.vars(t, false); len(vs) > 0 && (leaf || g.r.Intn(4) == 0) {
		v := g.pick(vs)
		return expr{v.name, ref(v.name)}
	}
	switch t {
	case tInt:
		return g.intExpr(leaf)
	case tBool:
		return g.boolExpr(leaf)
	case tStr:
		return g.strExpr(leaf)
	case tList:
		return g.listExpr(leaf)
	case tOpt:
		switch g.r.Intn(3) {
		case 0:
			// Formatting and parsing in the same radix gives the int back.
			x, r := g.expr(tInt), g.r.Intn(35)+2
			return expr{fmt.Sprintf("int.parse(str(%s, radix: %d), radix: %d)", x.code, r, r), func(e env) any {
				v := x.eval(e).(int64)
				return &v
			}}
		case 1:
			s := g.expr(tStr)
			return expr{"int.parse(" + s.code + ")", func(e env) any {
				v, err := strconv.ParseInt(strings.TrimSpace(s.eval(e).(string)), 10, 64)
				if err != nil {
					return (*int64)(nil)
				}
				return &v
			}}
		}
		x := g.expr(tInt)
		return expr{"even(" + x.code + ")", func(e env) any {
			v := x.eval(e).(int64)
			if v%2 == 0 {
				return &v
			}
			return (*int64)(nil)
		}}
	case tPoint:
		a, s := g.expr(tInt), g.expr(tStr)
		return expr{"Point(a: " + a.code + ", s: " + s.code + ")", func(e env) any {
			av := a.eval(e).(int64)
			return &point{av, s.eval(e).(string)}
		}}
	case tShape:
		return g.shapeExpr("Shape")
	}
	panic("unknown type")
}

func (g *generator) shapeExpr(prefix string) expr {
	switch g.r.Intn(3) {
	case 0:
		return expr{prefix + ".none", func(env) any { return shape{} }}
	case 1:
		x := g.expr(tInt)
		return expr{prefix + ".one(x: " + x.code + ")", func(e env) any { return shape{tag: 1, x: x.eval(e).(int64)} }}
	}
	x, s := g.expr(tInt), g.expr(tStr)
	return expr{prefix + ".two(x: " + x.code + ", s: " + s.code + ")", func(e env) any {
		xv := x.eval(e).(int64)
		return shape{2, xv, s.eval(e).(string)}
	}}
}

func (g *generator) implicitShape() expr { return g.shapeExpr("") }

func intLit(v int64) expr {
	code := strconv.FormatInt(v, 10)
	if v < 0 {
		code = "(" + code + ")"
	}
	return expr{code, func(env) any { return v }}
}

func binInt(x, y expr, op string, f func(a, b int64) int64) expr {
	return expr{"(" + x.code + " " + op + " " + y.code + ")", func(e env) any {
		a := x.eval(e).(int64)
		return f(a, y.eval(e).(int64))
	}}
}

// Returns a bitwise expression; shifts move by a constant in range.
func (g *generator) bitExpr() expr {
	switch g.r.Intn(6) {
	case 0:
		return binInt(g.expr(tInt), g.expr(tInt), "&", func(a, b int64) int64 { return a & b })
	case 1:
		return binInt(g.expr(tInt), g.expr(tInt), "|", func(a, b int64) int64 { return a | b })
	case 2:
		return binInt(g.expr(tInt), g.expr(tInt), "^", func(a, b int64) int64 { return a ^ b })
	case 3:
		return binInt(g.expr(tInt), intLit(int64(g.r.Intn(64))), "<<", func(a, b int64) int64 { return a << b })
	case 4:
		return binInt(g.expr(tInt), intLit(int64(g.r.Intn(64))), ">>", func(a, b int64) int64 { return a >> b })
	}
	x := g.expr(tInt)
	return expr{"(~" + x.code + ")", func(e env) any { return ^x.eval(e).(int64) }}
}

func (g *generator) intExpr(leaf bool) expr {
	if leaf {
		return intLit(int64(g.r.Intn(2001) - 1000))
	}
	switch g.r.Intn(16) {
	case 0:
		if g.r.Intn(3) == 0 {
			return binInt(g.expr(tInt), g.expr(tInt), "&+", func(a, b int64) int64 { return a + b })
		}
		return binInt(g.expr(tInt), g.expr(tInt), "+", addC)
	case 1:
		if g.r.Intn(3) == 0 {
			return binInt(g.expr(tInt), g.expr(tInt), "&-", func(a, b int64) int64 { return a - b })
		}
		return binInt(g.expr(tInt), g.expr(tInt), "-", subC)
	case 2:
		if g.r.Intn(2) == 0 {
			return binInt(g.expr(tInt), g.expr(tInt), "&*", func(a, b int64) int64 { return a * b })
		}
		return binInt(g.expr(tInt), g.expr(tInt), "*", mulC)
	case 3:
		if g.r.Intn(2) == 0 {
			return g.bitExpr()
		}
		k := int64(g.r.Intn(9) + 2)
		if g.r.Intn(2) == 0 {
			k = -k
		}
		if g.r.Intn(2) == 0 {
			return binInt(g.expr(tInt), intLit(k), "/", func(a, b int64) int64 { return a / b })
		}
		return binInt(g.expr(tInt), intLit(k), "%", func(a, b int64) int64 { return a % b })
	case 4:
		l := g.expr(tList)
		return expr{l.code + ".len", func(e env) any { return int64(len(l.eval(e).([]int64))) }}
	case 5:
		s := g.expr(tStr)
		return expr{s.code + ".len", func(e env) any { return int64(len(s.eval(e).(string))) }}
	case 6:
		p := g.expr(tPoint)
		if g.r.Intn(2) == 0 {
			return expr{p.code + ".a", func(e env) any { return p.eval(e).(*point).a }}
		}
		return expr{p.code + ".sum()", func(e env) any {
			v := p.eval(e).(*point)
			return addC(v.a, int64(len(v.s)))
		}}
	case 7:
		o, d := g.expr(tOpt), g.expr(tInt)
		return expr{"(" + o.code + " ?? " + d.code + ")", func(e env) any {
			if v := o.eval(e).(*int64); v != nil {
				return *v
			}
			return d.eval(e)
		}}
	case 8:
		s := g.expr(tShape)
		x1, x2, s2 := g.name("m"), g.name("m"), g.name("m")
		g.scope = append(g.scope, variable{x1, tInt, false})
		one := g.expr(tInt)
		g.scope = g.scope[:len(g.scope)-1]
		g.scope = append(g.scope, variable{x2, tInt, false}, variable{s2, tStr, false})
		two := g.expr(tInt)
		g.scope = g.scope[:len(g.scope)-2]
		none := g.expr(tInt)
		code := fmt.Sprintf("match %s { none => %s, one(%s) => %s, two(%s, %s) => %s }", s.code, none.code, x1, one.code, x2, s2, two.code)
		return expr{code, func(e env) any {
			v := s.eval(e).(shape)
			switch v.tag {
			case 0:
				return none.eval(e)
			case 1:
				e[x1] = v.x
				return one.eval(e)
			}
			e[x2], e[s2] = v.x, v.s
			return two.eval(e)
		}}
	case 9:
		c, a, b := g.expr(tBool), g.expr(tInt), g.expr(tInt)
		return expr{"match " + c.code + " { true => " + a.code + ", false => " + b.code + " }", func(e env) any {
			if c.eval(e).(bool) {
				return a.eval(e)
			}
			return b.eval(e)
		}}
	case 10:
		x := g.expr(tInt)
		k := int64(g.r.Intn(7) - 3)
		y := g.name("y")
		return expr{fmt.Sprintf("twice(fn(%s) => %s * 2 + %d, %s)", y, y, k, x.code), func(e env) any {
			v := x.eval(e).(int64)
			v = addC(mulC(v, 2), k)
			return addC(mulC(v, 2), k)
		}}
	case 11:
		if vs := g.vars(tFnInt, false); len(vs) > 0 {
			f, x := g.pick(vs), g.expr(tInt)
			return expr{f.name + "(" + x.code + ")", func(e env) any { return e[f.name].(fnInt)(e, x.eval(e).(int64)) }}
		}
	case 12:
		l := g.expr(tList)
		n := g.r.Intn(3)
		return expr{fmt.Sprintf("[%s, 1, 2][%d]", l.code+".len", n), func(e env) any {
			return []int64{int64(len(l.eval(e).([]int64))), 1, 2}[n]
		}}
	case 13:
		x := g.expr(tInt)
		return expr{"(-" + x.code + ")", func(e env) any { return negC(x.eval(e).(int64)) }}
	}
	return intLit(int64(g.r.Intn(2001) - 1000))
}

func (g *generator) boolExpr(leaf bool) expr {
	if leaf {
		v := g.r.Intn(2) == 0
		return expr{strconv.FormatBool(v), func(env) any { return v }}
	}
	switch g.r.Intn(8) {
	case 0, 1:
		ops := []string{"<", "<=", ">", ">=", "==", "!="}
		op := ops[g.r.Intn(len(ops))]
		x, y := g.expr(tInt), g.expr(tInt)
		return expr{"(" + x.code + " " + op + " " + y.code + ")", func(e env) any {
			a, b := x.eval(e).(int64), y.eval(e).(int64)
			return map[string]bool{"<": a < b, "<=": a <= b, ">": a > b, ">=": a >= b, "==": a == b, "!=": a != b}[op]
		}}
	case 2:
		x, y := g.expr(tStr), g.expr(tStr)
		return expr{"(" + x.code + " == " + y.code + ")", func(e env) any { return x.eval(e).(string) == y.eval(e).(string) }}
	case 3:
		x, y := g.expr(tBool), g.expr(tBool)
		return expr{"(" + x.code + " && " + y.code + ")", func(e env) any { return x.eval(e).(bool) && y.eval(e).(bool) }}
	case 4:
		x, y := g.expr(tBool), g.expr(tBool)
		return expr{"(" + x.code + " || " + y.code + ")", func(e env) any { return x.eval(e).(bool) || y.eval(e).(bool) }}
	case 5:
		x := g.expr(tBool)
		return expr{"!" + x.code, func(e env) any { return !x.eval(e).(bool) }}
	case 6:
		l, x := g.expr(tList), g.expr(tInt)
		return expr{l.code + ".contains(" + x.code + ")", func(e env) any {
			xs := l.eval(e).([]int64)
			v := x.eval(e).(int64)
			for _, y := range xs {
				if y == v {
					return true
				}
			}
			return false
		}}
	}
	o := g.expr(tOpt)
	return expr{"(" + o.code + " == nil)", func(e env) any { return o.eval(e).(*int64) == nil }}
}

const letters = "abcdefghijklmnopqrstuvwxyz0123456789 "

func (g *generator) strExpr(leaf bool) expr {
	if leaf {
		b := make([]byte, g.r.Intn(6))
		for i := range b {
			b[i] = letters[g.r.Intn(len(letters))]
		}
		s := string(b)
		return expr{strconv.Quote(s), func(env) any { return s }}
	}
	switch g.r.Intn(6) {
	case 0, 1:
		x, y := g.expr(tStr), g.expr(tStr)
		return expr{"(" + x.code + " + " + y.code + ")", func(e env) any {
			a := x.eval(e).(string)
			return capped(a + y.eval(e).(string))
		}}
	case 2:
		x, y := g.expr(tInt), g.expr(tStr)
		return expr{`"<{` + x.code + `}|{` + y.code + `}>"`, func(e env) any {
			a := x.eval(e).(int64)
			return capped("<" + strconv.FormatInt(a, 10) + "|" + y.eval(e).(string) + ">")
		}}
	case 3:
		x := g.expr(tStr)
		return expr{x.code + ".upper()", func(e env) any { return strings.ToUpper(x.eval(e).(string)) }}
	case 4:
		p := g.expr(tPoint)
		return expr{p.code + ".s", func(e env) any { return p.eval(e).(*point).s }}
	}
	s := g.expr(tShape)
	x := g.name("m")
	code := fmt.Sprintf(`match %s { two(_, %s) => %s, _ => "other" }`, s.code, x, x)
	return expr{code, func(e env) any {
		if v := s.eval(e).(shape); v.tag == 2 {
			return v.s
		}
		return "other"
	}}
}

func (g *generator) listExpr(leaf bool) expr {
	if leaf || g.r.Intn(3) > 0 {
		n := 1 + g.r.Intn(4)
		items := make([]expr, n)
		codes := make([]string, n)
		for i := range items {
			items[i] = g.expr(tInt)
			codes[i] = items[i].code
		}
		return expr{"[" + strings.Join(codes, ", ") + "]", func(e env) any {
			xs := make([]int64, n)
			for i, x := range items {
				xs[i] = x.eval(e).(int64)
			}
			return xs
		}}
	}
	l := g.expr(tList)
	y := g.name("y")
	k := int64(g.r.Intn(5) + 2)
	if g.r.Intn(2) == 0 {
		return expr{fmt.Sprintf("%s.filter(fn(%s) => %s %% %d == 0)", l.code, y, y, k), func(e env) any {
			out := []int64{}
			for _, v := range l.eval(e).([]int64) {
				if v%k == 0 {
					out = append(out, v)
				}
			}
			return out
		}}
	}
	return expr{fmt.Sprintf("%s.map(fn(%s) => %s * %d)", l.code, y, y, k), func(e env) any {
		in := l.eval(e).([]int64)
		out := make([]int64, len(in))
		for i, v := range in {
			out[i] = mulC(v, k)
		}
		return out
	}}
}

// Formats a value the way println shows it.
func show(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return repr(v)
}

// Formats a value the way it appears inside a list or struct.
func repr(v any) string {
	switch v := v.(type) {
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		return strconv.FormatBool(v)
	case string:
		return strconv.Quote(v)
	case []int64:
		parts := make([]string, len(v))
		for i, x := range v {
			parts[i] = strconv.FormatInt(x, 10)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *int64:
		if v == nil {
			return "nil"
		}
		return strconv.FormatInt(*v, 10)
	case *point:
		return fmt.Sprintf("Point(a: %d, s: %s)", v.a, strconv.Quote(v.s))
	case shape:
		switch v.tag {
		case 0:
			return "Shape.none"
		case 1:
			return fmt.Sprintf("Shape.one(x: %d)", v.x)
		}
		return fmt.Sprintf("Shape.two(x: %d, s: %s)", v.x, strconv.Quote(v.s))
	}
	panic(fmt.Sprintf("can't show %T", v))
}
