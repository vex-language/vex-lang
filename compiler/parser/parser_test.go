package parser

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/diag"
)

func parse(t *testing.T, src string) (*ast.File, diag.List) {
	t.Helper()
	f, err := Parse(diag.NewFile("test.vx", src))
	if err != nil {
		return f, err.(diag.List)
	}
	return f, nil
}

// Parses [body] inside `fn main` and returns the S-expression of its block.
func body(t *testing.T, body string) string {
	t.Helper()
	f, errs := parse(t, "fn main() {\n"+body+"\n}")
	if errs != nil {
		t.Fatalf("unexpected errors in %q:\n%v", body, errs)
	}
	return ast.Sexpr(f.Decls[0].(*ast.FuncDecl).Body)
}

func TestExpressions(t *testing.T) {
	cases := []struct{ src, want string }{
		{"f(1 + 2 * 3)", "(call f (+ 1 (* 2 3)))"},
		{"f((1 + 2) * 3)", "(call f (* (+ 1 2) 3))"},
		{"f(a - b - c)", "(call f (- (- a b) c))"},
		{"f(a || b && c)", "(call f (|| a (&& b c)))"},
		{"f(a == b || c < d)", "(call f (|| (== a b) (< c d)))"},
		{"f(!done && -x > 0)", "(call f (&& (! done) (> (- x) 0)))"},
		{"f(a % 2 == 0)", "(call f (== (% a 2) 0))"},
		{"f(--x)", "(call f (- (- x)))"},
		{"f(1_000, 2.5e-3, true, false)", "(call f 1000 2.5e-3 true false)"},
		{`f("hi")`, `(call f "hi")`},
		{`f("Hi {name}!")`, `(call f (interp "Hi " name "!"))`},
		{`f("{a + b} and {g(c)}")`, `(call f (interp (+ a b) " and " (call g c)))`},
		{`f("{"inner {x}"}")`, `(call f (interp (interp "inner " x)))`},
		{"f([1, 2, 3])", "(call f (list 1 2 3))"},
		{"f([])", "(call f list)"},
		{"f([[1], [2, 3]])", "(call f (list (list 1) (list 2 3)))"},
		{"f(xs[0], m[i][j])", "(call f (index xs 0) (index (index m i) j))"},
		{"f(user.name.len)", "(call f (. (. user name) len))"},
		{"f(User.guest())", "(call f (call (. User guest)))"},
		{`greet("Sam", greeting: "Welcome")`, `(call greet "Sam" (greeting: "Welcome"))`},
		{`User(name: "Ann", age: 30)`, `(call User (name: "Ann") (age: 30))`},
		{"nums.push(4)", "(call (. nums push) 4)"},
		{"f()()", "(call (call f))"},
	}
	for _, c := range cases {
		got := body(t, c.src)
		want := "(block " + c.want + ")"
		if got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
}

func TestStatements(t *testing.T) {
	cases := []struct{ src, want string }{
		{"let x = 1", "(let x 1)"},
		{"var count: int = 0", "(var count: int 0)"},
		{"let names: [string] = []", "(var names: [string] list)"},
		{"x = 5", "(= x 5)"},
		{"total += n", "(+= total n)"},
		{"xs[0] -= 1", "(-= (index xs 0) 1)"},
		{"user.age *= 2", "(*= (. user age) 2)"},
		{"a /= 2\nb %= 3", "(/= a 2) (%= b 3)"},
		{"return", "return"},
		{"return x + 1", "(return (+ x 1))"},
		{"while i < 10 { i += 1 }", "(while (< i 10) (block (+= i 1)))"},
		{"break\ncontinue", "break continue"},
		{"{ let a = 1 }", "(block (let a 1))"},
		{"println(1); println(2)", "(call println 1) (call println 2)"},
	}
	for _, c := range cases {
		got := body(t, c.src)
		want := "(block " + c.want + ")"
		if strings.HasPrefix(c.src, "let names") {
			want = "(block (let names: [string] list))"
		}
		if got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
}

func TestIf(t *testing.T) {
	src := `if count > 10 {
    big()
} else if count > 5 {
    medium()
} else {
    small()
}`
	want := "(block (if (> count 10) (block (call big)) (if (> count 5) (block (call medium)) (block (call small)))))"
	if got := body(t, src); got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

func TestFor(t *testing.T) {
	cases := []struct{ src, want string }{
		{"for i in 0..5 { f(i) }", "(for i (.. 0 5) (block (call f i)))"},
		{"for i in 1..=n { f(i) }", "(for i (..= 1 n) (block (call f i)))"},
		{"for i in 0..xs.len - 1 { }", "(for i (.. 0 (- (. xs len) 1)) block)"},
		{"for fruit in fruits { f(fruit) }", "(for fruit fruits (block (call f fruit)))"},
		{"for i, fruit in fruits { }", "(for i, fruit fruits block)"},
		{"for x in items() { }", "(for x (call items) block)"},
	}
	for _, c := range cases {
		got := body(t, c.src)
		if want := "(block " + c.want + ")"; got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
}

func TestTrailingBlocks(t *testing.T) {
	cases := []struct{ src, want string }{
		{`Button("Add") { count += 1 }`, `(call Button "Add" (block (+= count 1)))`},
		{"if ready(x) { go() }", "(if (call ready x) (block (call go)))"},
		{"while more(it) { step() }", "(while (call more it) (block (call step)))"},
		{"if check(f(x) { y() }) { z() }", "(if (call check (call f x (block (call y)))) (block (call z)))"},
		{"Column(spacing: 12) {\n    Text(\"a\")\n    Text(\"b\")\n}", `(call Column (spacing: 12) (block (call Text "a") (call Text "b")))`},
	}
	for _, c := range cases {
		got := body(t, c.src)
		if want := "(block " + c.want + ")"; got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
}

func TestClosures(t *testing.T) {
	cases := []struct{ src, want string }{
		{"let double = fn(x: int) -> int => x * 2", "(let double (fn param x: int -> int (=> (* x 2))))"},
		{"let f = fn(x) => x", "(let f (fn param x (=> x)))"},
		{"let hi = fn() {\n    println(1)\n}", "(let hi (fn (block (call println 1))))"},
		{"let add: fn(int, int) -> int = fn(a, b) => a + b", "(let add: fn(int, int) -> int (fn param a param b (=> (+ a b))))"},
		{"let cb: fn() = fn() {}", "(let cb: fn() (fn block))"},
		{"xs.filter(fn(x) => x > 2)", "(call (. xs filter) (fn param x (=> (> x 2))))"},
		{"f(1)(2)", "(call (call f 1) 2)"},
		{"handlers[0]()", "(call (index handlers 0))"},
		{"if all(xs, fn(x) => x > 0) { ok() }", "(if (call all xs (fn param x (=> (> x 0)))) (block (call ok)))"},
		{"run(fn() {\n    a()\n}, 2)", "(call run (fn (block (call a))) 2)"},
	}
	for _, c := range cases {
		got := body(t, c.src)
		if want := "(block " + c.want + ")"; got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
	f, errs := parse(t, "fn apply(f: fn(int) -> int, x: int) -> fn() -> int => fn() => f(x)\nfn main() {}")
	if errs != nil {
		t.Fatal(errs)
	}
	want := "(fn apply param f: fn(int) -> int param x: int -> fn() -> int (=> (fn (=> (call f x)))))"
	if got := ast.Sexpr(f.Decls[0]); got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

func TestWarnings(t *testing.T) {
	f, err := Parse(diag.NewFile("main.vx", `fn main() {
    let a = 1;
    let b = 2; let c = 3
    if (a > 0) {
        println(a);
    }
    while (a < 0) {}
    if (a > 0) && (b > 0) {}
    if ((a)) {}
}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range f.Warnings {
		got = append(got, fmt.Sprintf("%s %s | %s", w.Pos, w.Message, w.Hint))
	}
	want := []string{
		"2:14 a ; isn't needed at the end of a line | remove it",
		"4:8 the if condition doesn't need parentheses | write if a > 0 {",
		"5:19 a ; isn't needed at the end of a line | remove it",
		"7:11 the while condition doesn't need parentheses | write while a < 0 {",
		"9:8 the if condition doesn't need parentheses | write if (a) {",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("\n got: %s\nwant: %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
}

func TestErrorHandling(t *testing.T) {
	cases := []struct{ src, want string }{
		{"let t = try load(p)", "(let t (try (call load p)))"},
		{"let n = try? parse(s) ?? 0", "(let n (?? (try? (call parse s)) 0))"},
		{"let n = try parse(s) catch { 0 }", "(let n (try (call parse s) (catch (block 0))))"},
		{"try save(x) catch e {\n    log(e)\n}", "(try (call save x) (catch e (block (call log e))))"},
		{"let v = try a.b().c(try d())", "(let v (try (call (. (call (. a b)) c) (try (call d)))))"},
		{"throw Error(\"x\")", "(throw (call Error \"x\"))"},
		{"throw .notFound(path: p)", "(throw (call .notFound (path: p)))"},
		{"let f: fn(int) throws -> int = g", "(let f: fn(int) throws -> int g)"},
		{"let h: fn() throws(E) = g", "(let h: fn() throws(E) g)"},
		{"let k = fn(x: int) throws -> int => try f(x)", "(let k (fn param x: int throws -> int (=> (try (call f x)))))"},
	}
	for _, c := range cases {
		if got, want := body(t, c.src), "(block "+c.want+")"; got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
	f, errs := parse(t, "fn load(p: string) throws -> string { return p }\nfn open() throws(E) {}")
	if errs != nil {
		t.Fatal(errs)
	}
	if got := ast.Sexpr(f.Decls[0]); !strings.Contains(got, "throws") {
		t.Errorf("load: %s", got)
	}
	if got := ast.Sexpr(f.Decls[1]); !strings.Contains(got, "throws(E)") {
		t.Errorf("open: %s", got)
	}
}

func TestOptionals(t *testing.T) {
	cases := []struct{ src, want string }{
		{"let u: User? = nil", "(let u: User? nil)"},
		{"let xs: [int?]? = nil", "(let xs: [int?]? nil)"},
		{"var f: fn()? = nil", "(var f: fn()? nil)"},
		{"var g: (fn() -> int)? = nil", "(var g: (fn() -> int)? nil)"},
		{"let h: fn() -> int? = nil", "(let h: fn() -> int? nil)"},
		{"let n = a?.b.c ?? d ?? 0", "(let n (?? (. (?. a b) c) (?? d 0)))"},
		{"let m = x ?? 1 + 2 > 3", "(let m (> (?? x (+ 1 2)) 3))"},
		{"let k = u?.greet(1)", "(let k (call (?. u greet) 1))"},
		{"if let u = find(1) {\n    use(u)\n} else if let v = w {\n}", "(if let u (call find 1) (block (call use u)) (if let v w block))"},
		{"guard let u = find(1) else { return }", "(guard let u (call find 1) (block return))"},
		{"let s = name\n    ?.upper()\n    ?? \"\"", "(let s (?? (call (?. name upper)) \"\"))"},
	}
	for _, c := range cases {
		if got, want := body(t, c.src), "(block "+c.want+")"; got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
}

func TestEnumsAndMatch(t *testing.T) {
	f, errs := parse(t, `enum Shape {
    circle(radius: float)
    rect(width: float, height: float), empty

    fn area() -> float => match self {
        circle(r) => 3.14 * r * r
        rect(w, h) => w * h
        empty => 0.0
    }
    static fn unit() -> Shape => .rect(width: 1, height: 1)
}
fn main() {}`)
	if errs != nil {
		t.Fatal(errs)
	}
	want := `(enum Shape (case circle radius: float) (case rect width: float height: float) case empty ` +
		`(fn area -> float (=> (match self (circle(r) => (* (* 3.14 r) r)) (rect(w, h) => (* w h)) (empty => 0.0)))) ` +
		`(static fn unit -> Shape (=> (call .rect (width: 1) (height: 1)))))`
	if got := ast.Sexpr(f.Decls[0]); got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	cases := []struct{ src, want string }{
		{"match n {\n    0 => zero()\n    -1 => minus()\n}", "(match n (0 => (call zero)) (-1 => (call minus)))"},
		{"match s { \"y\" => yes(), _ => {\n    no()\n} }", "(match s (\"y\" => (call yes)) (_ => (block (call no))))"},
		{"let x = match ok { true => 1, false => 0 }", "(let x (match ok (true => 1) (false => 0)))"},
		{"match f(x) {\n    some(_, v) => use(v)\n}", "(match (call f x) (some(_, v) => (call use v)))"},
		{"let d: Dir = .up", "(let d: Dir .up)"},
		{"go(.left, to: .right)", "(call go .left (to: .right))"},
	}
	for _, c := range cases {
		got := body(t, c.src)
		if want := "(block " + c.want + ")"; got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
}

func TestLineBreaksInsideBrackets(t *testing.T) {
	src := `let users = [
    User(
        name: "Ann",
        age: 30,
    ),
    User(name: "Sam"),
]
let total = a +
    b
Text("x")
    .size(24)`
	want := `(block (let users (list (call User (name: "Ann") (age: 30)) (call User (name: "Sam")))) (let total (+ a b)) (call (. (call Text "x") size) 24))`
	if got := body(t, src); got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

func TestDeclarations(t *testing.T) {
	src := `/// Adds two numbers.
fn add(a: int, b: int) -> int {
    return a + b
}

fn square(x: int) -> int => x * x

fn greet(name: string, greeting: string = "Hi") {
    println("{greeting}, {name}!")
}

fn sum(
    xs: [int],
) -> int => 0

struct User {
    name: string
    age: int = 18
    tags: [string] = [], score: float = 0.0

    fn greet() {
        println("Hi, I'm {name}")
    }

    fn isAdult() -> bool => age >= 18

    static fn guest() -> User {
        return User(name: "Guest")
    }
}

struct Empty {}
`
	f, errs := parse(t, src)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := []string{
		"(fn add param a: int param b: int -> int (block (return (+ a b))))",
		"(fn square param x: int -> int (=> (* x x)))",
		`(fn greet param name: string (param greeting: string "Hi") (block (call println (interp greeting ", " name "!"))))`,
		"(fn sum param xs: [int] -> int (=> 0))",
		`(struct User field name: string (field age: int 18) (field tags: [string] list) (field score: float 0.0) ` +
			`(fn greet (block (call println (interp "Hi, I'm " name)))) ` +
			`(fn isAdult -> bool (=> (>= age 18))) ` +
			`(static fn guest -> User (block (return (call User (name: "Guest"))))))`,
		"struct Empty",
	}
	if len(f.Decls) != len(want) {
		t.Fatalf("got %d declarations, want %d", len(f.Decls), len(want))
	}
	for i, d := range f.Decls {
		if got := ast.Sexpr(d); got != want[i] {
			t.Errorf("decl %d\n got: %s\nwant: %s", i, got, want[i])
		}
	}
}

func TestSpans(t *testing.T) {
	src := "fn main() {\n    let total = price * 2\n}"
	f, errs := parse(t, src)
	if errs != nil {
		t.Fatal(errs)
	}
	let := f.Decls[0].(*ast.FuncDecl).Body.Stmts[0].(*ast.LetStmt)
	if got := src[let.Pos().Offset:let.EndOffset()]; got != "let total = price * 2" {
		t.Errorf("let span = %q", got)
	}
	bin := let.Value.(*ast.BinaryExpr)
	if got := src[bin.Pos().Offset:bin.EndOffset()]; got != "price * 2" {
		t.Errorf("binary span = %q", got)
	}
	if bin.Pos().String() != "2:17" {
		t.Errorf("binary at %s, want 2:17", bin.Pos())
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name, src string
		pos, msg  string
		hint      string
	}{
		{"code at top level", "println(1)", "1:1", "expected 'fn', 'struct' or 'enum', found 'println'", "code goes inside a function, e.g. fn main() { ... }"},
		{"variable at top level", "let x = 1", "1:1", "variables must be declared inside a function", "move it into a function such as fn main()"},
		{"static at top level", "static fn f() {}", "1:1", "'static' is only allowed inside a struct", "use static fn inside a struct"},
		{"keyword as name", "fn for() {}", "1:4", "expected a function name, found 'for'", "for is a keyword; choose another name"},
		{"parameter without type", "fn f(a) {}", "1:7", "parameter 'a' needs a type", "write it as a: int"},
		{"colon return type", "fn f(): int {}", "1:7", "expected '->' before the return type, found ':'", "write the return type as -> int"},
		{"missing body", "fn f()\n{}", "1:7", "expected '{' or '=>' to start the body of f, found end of line", ""},
		{"bad type", "fn f(a: 5) {}", "1:9", "expected a type, found number 5", "for example int, string, [int] or fn(int) -> bool"},
		{"field without type", "struct U {\n name\n}", "2:6", "field 'name' needs a type", "write it as name: int"},
		{"let field", "struct U {\n let name: string\n}", "2:2", "fields don't use 'let'", "write fields as name: type"},
		{"unclosed struct", "struct U {\n a: int", "1:10", "unclosed struct body", "add a matching }"},
		{"let without value", "fn main() {\n let x\n}", "2:7", "'let x' needs a value", "write let x = ..."},
		{"typed var without value", "fn main() {\n var n: int\n}", "2:12", "'var n' needs a value", "give it a starting value, e.g. var n: int = ..."},
		{"missing let", "fn main() {\n x: int = 5\n}", "2:2", "missing 'let' or 'var' before 'x'", "write let x = ... or var x = ..."},
		{"go style declare", "fn main() {\n x := 5\n}", "2:2", "missing 'let' or 'var' before 'x'", "write let x = ... or var x = ..."},
		{"else on new line", "fn main() {\n if a {\n }\n else {\n }\n}", "4:2", "'else' must be on the same line as the closing }", "write } else {"},
		{"else without if", "fn main() {\n else {}\n}", "2:2", "'else' without a matching 'if'", ""},
		{"assign in condition", "fn main() {\n if a = 1 {}\n}", "2:7", "use == to compare in a condition", "= assigns a value; == compares"},
		{"brace on next line", "fn main() {\n if a\n {}\n}", "2:6", "expected '{' after the if condition, found end of line", "put { on the same line as if"},
		{"for without in", "fn main() {\n for x of xs {}\n}", "2:8", "expected 'in', found 'of'", "write for x in items { ... }"},
		{"range outside for", "fn main() {\n let r = 0..10\n}", "2:11", "ranges can only be used in for loops", "for i in 0..10 { ... }"},
		{"chained comparison", "fn main() {\n f(a < b < c)\n}", "2:10", "comparisons can't be chained", "use a < b && b < c"},
		{"two statements on a line", "fn main() {\n let a = 1 let b = 2\n}", "2:12", "expected end of statement, found 'let'", "put each statement on its own line or separate them with ;"},
		{"assign to call", "fn main() {\n f() = 1\n}", "2:2", "can't assign to this expression", "assign to a variable, list element or field"},
		{"unclosed call", "fn main() {\n f(1, 2\n}", "2:3", "unclosed '('", "add a matching )"},
		{"unclosed list", "fn main() {\n let xs = [1, 2\n}", "2:11", "unclosed '['", "add a matching ]"},
		{"missing comma", "fn main() {\n f(1 2)\n}", "2:6", "expected ',' or ')', found number 2", "add a , between items"},
		{"empty interpolation", "fn main() {\n f(\"a {} b\")\n}", "2:8", "empty {} in string", `put a value inside, or write \{ for a literal brace`},
		{"nested fn", "fn main() {\n fn inner() {}\n}", "2:2", "functions can't be declared inside other functions", "declare it at the top level, or store a closure: let inner = fn(...) { ... }"},
		{"named closure", "fn main() {\n let f = fn g() {}\n}", "2:13", "expected '(' after 'fn' in an expression", "closures have no name; store one with let name = fn(...) { ... }"},
		{"closure default", "fn main() {\n let f = fn(x = 1) {}\n}", "2:15", "closure parameters can't have default values", ""},
		{"closure without body", "fn main() {\n let f = fn(x)\n}", "2:15", "expected '{' or '=>' to start the closure body, found end of line", ""},
		{"unclosed block", "fn main() {\n f()", "1:11", "unclosed block", "add a matching }"},
		{"double optional", "fn f(x: int??) {}", "1:12", "a type can only be optional once", "one ? is enough"},
		{"throws after result", "fn f() -> int throws { return 1 }", "1:15", "throws comes before ->", "write fn f(...) throws -> ..."},
		{"throw without value", "fn f() throws {\n throw\n}", "3:1", "throw needs an error to throw", `e.g. throw Error("file not found")`},
		{"catch with try?", "fn main() {\n let n = try? f() catch { 0 }\n}", "2:19", "try? gives nil on an error, so it can't have a catch", "use try ... catch, or try? on its own"},
		{"catch without block", "fn main() {\n let n = try f() catch e\n}", "2:25", "expected '{' after catch, found end of line", "e.g. catch e { println(e.message) }"},
		{"ternary", "fn main() {\n let n = ok ? 1 : 2\n}", "2:13", "Vex has no ? : operator", "write match cond { true => a, false => b }"},
		{"lone question", "fn main() {\n let n = x?\n}", "2:11", "unexpected '?'", "use ?. to read a member, or ?? to give a default"},
		{"if var", "fn main() {\n if var x = y {}\n}", "2:5", "'if var' isn't supported", "write if let; copy it into a var inside the block"},
		{"if let no value", "fn main() {\n if let x {}\n}", "2:11", "expected '=' after 'if let x', found '{'", "write if let x = optionalValue { ... }"},
		{"guard without let", "fn main() {\n guard x else { return }\n}", "2:8", "expected 'let' after 'guard', found 'x'", "write guard let name = optionalValue else { return }"},
		{"guard without else", "fn main() {\n guard let x = y { return }\n}", "2:18", "expected 'else' after the guard value, found '{'", "add else { return } after the value"},
		{"enum in function", "fn main() {\n enum E { a }\n}", "2:2", "enums can't be declared inside functions", "declare it at the top level of the file"},
		{"case with type", "enum E {\n a: int\n}", "2:3", "enum cases don't have a type", "enum cases carry values in parentheses: a(value: int)"},
		{"case value type", "enum E {\n a(x)\n}", "2:5", "case value 'x' needs a type", "write it as x: int"},
		{"match brace", "fn main() {\n match x\n {}\n}", "2:9", "expected '{' after the match subject, found end of line", "put { on the same line as match"},
		{"match arrow", "fn main() {\n match x { a -> 1 }\n}", "2:14", "expected '=>' after the pattern, found '->'", "write pattern => result"},
		{"dotted pattern", "fn main() {\n match x { .a => 1 }\n}", "2:12", "patterns don't start with '.'", "write the case name without the dot"},
		{"bad pattern", "fn main() {\n match x { [1] => 1 }\n}", "2:12", "expected a pattern, found '['", "for example circle(r), 0, \"yes\" or _"},
		{"empty parens", "fn main() {\n f(())\n}", "2:5", "expected an expression inside ( )", ""},
	}
	for _, c := range cases {
		_, errs := parse(t, c.src)
		if len(errs) == 0 {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		e := errs[0]
		if e.Pos.String() != c.pos || e.Message != c.msg || e.Hint != c.hint {
			t.Errorf("%s:\n got: %s %q hint %q\nwant: %s %q hint %q", c.name, e.Pos, e.Message, e.Hint, c.pos, c.msg, c.hint)
		}
	}
}

func TestRecoversAndReportsSeveralErrors(t *testing.T) {
	src := `fn main() {
    let = 1
    if a = 2 { }
    f(1 2)
    println("still parsed")
}

fn other(a) {}

fn ok() {}
`
	f, errs := parse(t, src)
	if len(errs) != 4 {
		t.Fatalf("got %d errors, want 4:\n%v", len(errs), errs)
	}
	if n := len(f.Decls); n != 2 {
		t.Errorf("got %d declarations after recovery, want 2 (main, ok)", n)
	}
	main := f.Decls[0].(*ast.FuncDecl)
	if got := ast.Sexpr(main.Body.Stmts[len(main.Body.Stmts)-1]); got != `(call println "still parsed")` {
		t.Errorf("last statement after recovery: %s", got)
	}
}

func TestAssignInConditionRecovers(t *testing.T) {
	f, errs := parse(t, "fn main() {\n if a = 1 {\n  x()\n } else {\n  y()\n }\n}")
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
	if got := ast.Sexpr(f.Decls[0].(*ast.FuncDecl).Body); got != "(block (if (== a 1) (block (call x)) (block (call y))))" {
		t.Errorf("tree after recovery: %s", got)
	}
}

func TestElseOnNewLineStillParses(t *testing.T) {
	f, errs := parse(t, "fn main() {\n if a {\n  x()\n }\n else {\n  y()\n }\n}")
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if got := ast.Sexpr(f.Decls[0].(*ast.FuncDecl).Body); got != "(block (if a (block (call x)) (block (call y))))" {
		t.Errorf("tree after recovery: %s", got)
	}
}

func TestLexErrorsAreReturned(t *testing.T) {
	_, errs := parse(t, "fn main() { a & b }")
	if len(errs) != 1 || errs[0].Hint != "use && for logical and" {
		t.Errorf("got %v", errs)
	}
}

func TestTourParses(t *testing.T) {
	src, err := os.ReadFile("testdata/tour.vx")
	if err != nil {
		t.Fatal(err)
	}
	f, errs := parse(t, string(src))
	if errs != nil {
		t.Fatalf("tour.vx has errors:\n%v", errs)
	}
	if len(f.Decls) != 5 {
		t.Errorf("got %d declarations, want 5", len(f.Decls))
	}
}
