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
		{"f(a === b && c !== d)", "(call f (&& (=== a b) (!== c d)))"},
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
		{"f(flags & HIDDEN != 0)", "(call f (!= (& flags HIDDEN) 0))"},
		{"f(a | b ^ c & d)", "(call f (^ (| a b) (& c d)))"},
		{"f(a + b << 2, a * b >> 1)", "(call f (+ a (<< b 2)) (* a (>> b 1)))"},
		{"f(x >> 16 & 0xFF)", "(call f (& (>> x 16) 0xFF))"},
		{"f(if ok { 1 } else { 2 })", "(call f (if ok (block 1) (block 2)))"},
		{"f(v is Text)", "(call f (is v Text))"},
		{"f(v is Text && v.ok)", "(call f (&& (is v Text) (. v ok)))"},
		{"f(v as? Text ?? d)", "(call f (?? (as? v Text) d))"},
		{"f(a + b is Box<int>)", "(call f (is (+ a b) Box<int>))"},
		{"f(match v { t: Text => t, [i: Image, ...] => i, _ => d })", "(call f (match v (t: Text => t) ([i: Image, ...] => i) (_ => d)))"},
		{"f(if a { 1 } else if b { 2 } else { 3 })", "(call f (if a (block 1) (if b (block 2) (block 3))))"},
		{"f(1 + if ok { 2 } else { 3 } * 4)", "(call f (+ 1 (* (if ok (block 2) (block 3)) 4)))"},
		{"f(if let x = m { x } else { 0 })", "(call f (if let x m (block x) (block 0)))"},
		{"f(~a & b, -~a)", "(call f (& (~ a) b) (- (~ a)))"},
		{"f(a > b, a >= b)", "(call f (> a b) (>= a b))"},
		{"f(a >> b > c)", "(call f (> (>> a b) c))"},
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
		{"a &= 1\nb |= 2\nc ^= 3", "(&= a 1) (|= b 2) (^= c 3)"},
		{"a <<= 1\nb >>= 2", "(<<= a 1) (>>= b 2)"},
		{"let b: Box<Box<int>>= x", "(let b: Box<Box<int>> x)"},
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

func TestCollectionElements(t *testing.T) {
	cases := []struct{ src, want string }{
		{"let a = [1, if ok 2 else 3]", `(let a (list 1 (if ok (block 2) (block 3))))`},
		{"let a = [for x in xs x * 2, ...ys, ...?zs]", `(let a (list (for x xs (block (* x 2))) (... ys) (...? zs)))`},
		{"let a = [if a 1 else if b 2 else 3]", `(let a (list (if a (block 1) (if b (block 2) (block 3)))))`},
		{"let a = [if ok [1] else -1, if ok (2), if f(x) -1]", `(let a (list (if ok (block (list 1)) (block (- 1))) (if ok (block 2)) (if (call f x) (block (- 1)))))`},
		{"let a = [if ok n -1, if ok xs[0]]", `(let a (list (if ok (block (- n 1))) (if ok (block (index xs 0)))))`},
		{"let a = [for i, x in xs if x > 0 i]", `(let a (list (for i, x xs (block (if (> x 0) (block i))))))`},
		{"let a = [\n    if ok\n        1\n    else\n        2,\n]", `(let a (list (if ok (block 1) (block 2))))`},
		{"let a = [if let n = maybe n]", `(let a (list (if let n maybe (block n))))`},
		{"let m = [\"a\": 1, if ok \"b\": 2, for k in ks k: 0, ...base]", `(let m (map (: "a" 1) (if ok (block (: "b" 2))) (for k ks (block (: k 0))) (... base)))`},
		{"let a = [...xs]", `(let a (list (... xs)))`},
		{"Column {\n    Text(\"a\")\n    ...rest\n}", `(call Column (block (call Text "a") (... rest)))`},
		{"f(x) { a }", `(call f x (block a))`},
	}
	for _, c := range cases {
		if got, want := body(t, c.src), "(block "+c.want+")"; got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
}

func TestMaps(t *testing.T) {
	cases := []struct{ src, want string }{
		{"let m: [string: int] = [:]", "(let m: [string: int] map)"},
		{"let m = [\"a\": 1, \"b\": 2]", "(let m (map (: \"a\" 1) (: \"b\" 2)))"},
		{"let m = [\n    1: [2],\n    3: [],\n]", "(let m (map (: 1 (list 2)) (: 3 list)))"},
		{"let n: [[int: bool]] = [[:]]", "(let n: [[int: bool]] (list map))"},
		{"m[k] = v", "(= (index m k) v)"},
		{"for k, v in m {\n}", "(for k, v m block)"},
	}
	for _, c := range cases {
		if got, want := body(t, c.src), "(block "+c.want+")"; got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
}

func TestGenerics(t *testing.T) {
	src := `fn first<T>(xs: [T]) -> T? => nil
fn max<T: Comparable, U: Equatable & Hashable>(a: T, b: U) -> T => a
struct Box<T> {
    value: T
    fn get() -> T => value
}
enum Result<T> {
    ok(value: T)
    fn map<U>(f: fn(T) -> U) -> Result<U> => .ok(f(value))
}
fn wrap(b: Box<[Box<int>]>, m: [string: Pair<int, bool>]) {
}
`
	f, errs := parse(t, src)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := []string{
		"(fn first<T> param xs: [T] -> T? (=> nil))",
		"(fn max<T: Comparable, U: Equatable & Hashable> param a: T param b: U -> T (=> a))",
		"(struct Box<T> field value: T (fn get -> T (=> value)))",
		"(enum Result<T> (case ok value: T) (fn map<U> param f: fn(T) -> U -> Result<U> (=> (call .ok (call f value)))))",
		"(fn wrap param b: Box<[Box<int>]> param m: [string: Pair<int, bool>] block)",
	}
	for i, d := range f.Decls {
		if got := ast.Sexpr(d); got != want[i] {
			t.Errorf("decl %d\n got: %s\nwant: %s", i, got, want[i])
		}
	}
	cases := []struct{ src, want string }{
		{"first<int>(xs)", "(call<int> first xs)"},
		{"Box<[string]>(value: [])", "(call<[string]> Box (value: list))"},
		{"Pair<int, Box<int>>(a: 1)", "(call<int, Box<int>> Pair (a: 1))"},
		{"println(a < b, c > (d))", "(call println (< a b) (> c d))"},
		{"x = a<b", "(= x (< a b))"},
		{"x = f<a>(b) && c", "(= x (&& (call<a> f b) c))"},
		{"let g = first<string>", "(let g (<string> first))"},
		{"let g = first<string>\nprintln(g)", "(let g (<string> first)) (call println g)"},
		{"let r = Result<int>.ok(1)", "(let r (call (. (<int> Result) ok) 1))"},
		{"let b = box.map<float>(f)", "(let b (call<float> (. box map) f))"},
		{"let c = a < b\nprintln(c)", "(let c (< a b)) (call println c)"},
	}
	for _, c := range cases {
		if got, want := body(t, c.src), "(block "+c.want+")"; got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
}

func TestProperties(t *testing.T) {
	src := `struct Slider {
    _value: float = 0
    count: int = 0 {
        didSet {
            redraw()
        }
    }
    get: int = 1
    pub get value -> float => _value
    set value(v: float) {
        _value = v
    }
    fn get() {}
}
`
	f, errs := parse(t, src)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := `(struct Slider (field _value: float 0) (field count: int 0) (field get: int 1) (didSet count param oldValue: int (block (call redraw))) (pub get value -> float (=> _value)) (set value param v: float (block (= _value v))) (fn get block))`
	if got := ast.Sexpr(f.Decls[0]); got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
}

func TestInterfaces(t *testing.T) {
	src := `interface Shape {
    fn area() -> float
    fn name() -> string => "shape"
    fn scale(by: float) throws
}
struct Circle: Shape, Named {
    r: float
}
struct Box<T>: Shape {
}
interface Store<T: Hashable>: Sized, m.Named {
    get size -> int
    set size(value: int)
    fn get(key: string) -> T?
}
enum Suit: Drawable, Store<Suit> {
    hearts
    get name -> string => "suit"
}
fn largest<T: Ordered<T> & Equatable>(xs: [T]) -> T? => nil
`
	f, errs := parse(t, src)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := []string{
		`(interface Shape (fn area -> float) (fn name -> string (=> "shape")) (fn scale param by: float throws))`,
		"(struct Circle: Shape, Named field r: float)",
		"struct Box<T>: Shape",
		"(interface Store<T: Hashable>: Sized, m.Named (get size -> int) (set size param value: int) (fn get param key: string -> T?))",
		`(enum Suit: Drawable, Store<Suit> case hearts (get name -> string (=> "suit")))`,
		"(fn largest<T: Ordered<T> & Equatable> param xs: [T] -> T? (=> nil))",
	}
	for i, d := range f.Decls {
		if got := ast.Sexpr(d); got != want[i] {
			t.Errorf("decl %d\n got: %s\nwant: %s", i, got, want[i])
		}
	}
}

func TestModules(t *testing.T) {
	src := `import "vex:math"
import "models" as m

pub struct User: shapes.Shape {
    pub name: string
    age: int = 0
    home: m.Place
    pub fn greet() -> string => "hi"
    pub static fn guest() -> User => User(name: "guest")
}
pub enum Kind {
    a
    pub fn label() -> string => "k"
}
pub interface Named {
    fn name() -> string
}
pub fn sort<T: m.Ordered>(xs: [m.Box<T>]) -> m.Box<int>? => nil
fn main() {
    let b = m.Box<int>(value: 1)
    println(math.max(1, 2), m.Kind.a)
}
`
	f, errs := parse(t, src)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	if len(f.Imports) != 2 || f.Imports[0].Path != "vex:math" || f.Imports[0].As != nil || f.Imports[1].As.Name != "m" {
		t.Fatalf("imports: %+v", f.Imports)
	}
	want := []string{
		`(pub struct User: shapes.Shape pub field name: string (field age: int 0) field home: m.Place (pub fn greet -> string (=> "hi")) (pub static fn guest -> User (=> (call User (name: "guest")))))`,
		`(pub enum Kind case a (pub fn label -> string (=> "k")))`,
		"(pub interface Named (fn name -> string))",
		"(pub fn sort<T: m.Ordered> param xs: [m.Box<T>] -> m.Box<int>? (=> nil))",
		"(fn main (block (let b (call<int> (. m Box) (value: 1))) (call println (call (. math max) 1 2) (. (. m Kind) a))))",
	}
	for i, d := range f.Decls {
		if got := ast.Sexpr(d); got != want[i] {
			t.Errorf("decl %d\n got: %s\nwant: %s", i, got, want[i])
		}
	}
}

func TestExtern(t *testing.T) {
	src := `extern fn strlen(s: string) -> int
pub extern fn sqrt(x: float) -> float
extern fn cpow(x: float, y: float) -> float as "pow"
extern fn log(msg: string)
extern type Db
pub extern type View as "UIView"
extern fn on_tap(v: View, handler: fn(int)) -> int32
export fn vex_frame(t: float) -> int => 0
`
	f, errs := parse(t, src)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := []string{
		"(extern fn strlen param s: string -> int)",
		"(pub extern fn sqrt param x: float -> float)",
		"(extern fn cpow as pow param x: float param y: float -> float)",
		"(extern fn log param msg: string)",
		"extern type Db",
		"pub extern type View as UIView",
		"(extern fn on_tap param v: View param handler: fn(int) -> int32)",
		"(export fn vex_frame param t: float -> int (=> 0))",
	}
	for i, d := range f.Decls {
		if got := ast.Sexpr(d); got != want[i] {
			t.Errorf("decl %d\n got: %s\nwant: %s", i, got, want[i])
		}
	}
}

func TestTypeAliases(t *testing.T) {
	src := `typedef Json = [string: string?]
pub typedef Handler = fn(int) throws -> bool
typedef Pair<T> = (T, T)
typedef Cache<K: Hashable> = [K: [string]]
typedef Point = shapes.Point
`
	f, errs := parse(t, src)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := []string{
		"typedef Json = [string: string?]",
		"pub typedef Handler = fn(int) throws -> bool",
		"typedef Pair<T> = (T, T)",
		"typedef Cache<K: Hashable> = [K: [string]]",
		"typedef Point = shapes.Point",
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

func TestExtensions(t *testing.T) {
	src := `extension string {
    fn shout() -> string => upper()
    get isBlank -> bool => trim().len == 0
}
pub extension<T: Comparable> [T] {
    fn largest() -> T? => nil
}
extension shapes.Point {
    static fn origin() -> Point => Point(x: 0)
    static const dimensions = 2
}
`
	f, errs := parse(t, src)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := []string{
		"(extension string (fn shout -> string (=> (call upper))) (get isBlank -> bool (=> (== (. (call trim) len) 0))))",
		"(pub extension<T: Comparable> [T] (pub fn largest -> T? (=> nil)))",
		"(extension shapes.Point (const dimensions 2) (static fn origin -> Point (=> (call Point (x: 0)))))",
	}
	if len(f.Decls) != len(want) {
		t.Fatalf("got %d declarations, want %d", len(f.Decls), len(want))
	}
	for i, d := range f.Decls {
		if got := ast.Sexpr(d); got != want[i] {
			t.Errorf("decl %d\n got: %s\nwant: %s", i, got, want[i])
		}
	}
	for _, m := range f.Decls[0].(*ast.ExtensionDecl).Methods {
		if ast.TypeString(m.Recv) != "string" {
			t.Errorf("%s: receiver %v, want string", m.Name.Name, m.Recv)
		}
	}
}

func TestGlobals(t *testing.T) {
	src := `var count = 0
pub let names: [string] = load()
struct Router {
    static var stack: [string] = []
    pub static let home = "/"
}
`
	f, errs := parse(t, src)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := []string{
		"(var count 0)",
		"(pub let names: [string] (call load))",
		"(struct Router (var stack: [string] list) (pub let home \"/\"))",
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

func TestOperators(t *testing.T) {
	src := `struct Vec {
    operator: int
    pub operator +(other: Vec) -> Vec => other
    operator -() -> Vec => self
    operator ==(other: Vec) -> bool { return true }
    operator [](i: int) -> int => i
    operator []=(i: int, v: int) {}
}
`
	f, errs := parse(t, src)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	s := f.Decls[0].(*ast.StructDecl)
	if len(s.Fields) != 1 || s.Fields[0].Name.Name != "operator" {
		t.Errorf("got fields %v, want one named operator", s.Fields)
	}
	want := []string{
		"(pub operator + param other: Vec -> Vec (=> other))",
		"(operator unary - -> Vec (=> self))",
		"(operator == param other: Vec -> bool (block (return true)))",
		"(operator [] param i: int -> int (=> i))",
		"(operator []= param i: int param v: int block)",
	}
	if len(s.Methods) != len(want) {
		t.Fatalf("got %d operators, want %d", len(s.Methods), len(want))
	}
	for i, m := range s.Methods {
		if m.Accessor != ast.Operator {
			t.Errorf("%s: accessor %v, want Operator", m.Name.Name, m.Accessor)
		}
		if got := ast.Sexpr(m); got != want[i] {
			t.Errorf("operator %d\n got: %s\nwant: %s", i, got, want[i])
		}
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
		{"code at top level", "println(1)", "1:1", "expected 'fn', 'struct', 'enum' or 'interface', found 'println'", "code goes inside a function, e.g. fn main() { ... }"},
		{"top-level variable without value", "var x: int", "1:11", "'var x' needs a value", "give it a starting value, e.g. var x: int = ..."},
		{"static without member", "struct S {\n    static x = 1\n}", "2:12", "expected 'fn', 'const', 'let' or 'var' after 'static', found 'x'", ""},
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
		{"getter params", "struct R {\n get a(x: int) -> int => 1\n}", "2:7", "getters take no parameters", "write get a -> T, or make it a method: fn a(...)"},
		{"getter type", "struct R {\n get a => 1\n}", "2:8", "getter 'a' needs a result type", "write get a -> int => ..."},
		{"setter params", "struct R {\n set a(x: int, y: int) {}\n}", "2:7", "setter 'a' takes exactly one parameter, the new value", "write set a(value: int) { ... }"},
		{"setter result", "struct R {\n set a(x: int) -> int {}\n}", "2:16", "setters don't return a value", "remove the result type"},
		{"willSet", "struct R {\n n: int = 0 {\n  willSet {}\n }\n}", "3:3", "expected 'didSet' in the block of field 'n', found 'willSet'", "use didSet, which has the old value in oldValue"},
		{"enum setter", "enum E {\n a\n set b(v: int) {}\n}", "3:2", "enums can't have setters, since their values never change", "use a method that returns a new value"},
		{"element missing", "fn main() {\n let a = [if ok]\n}", "2:16", "expected an element after the condition or loop, found ']'", "e.g. [if ok 1 else 2] or [for x in xs x * 2]"},
		{"spread outside literal", "fn main() {\n let a = ...xs\n}", "2:10", "'...' can only be used in a list or map literal", "use it inside a list or map literal, e.g. [...xs, 4]"},
		{"entry in element list", "fn main() {\n let a = [1, if ok \"k\": 2]\n}", "2:23", "unexpected ':' in a list", "a list holds values, a map holds key: value pairs"},
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
		{"map entry without value", "fn main() {\n let m = [\"a\": 1, 2]\n}", "2:20", "expected ':' after a map key, found ']'", "write key: value"},
		{"colon in a list", "fn main() {\n let m = [1, \"a\": 2]\n}", "2:17", "unexpected ':' in a list", "a list holds values, a map holds key: value pairs"},
		{"interface field", "interface I {\n    x: int\n}", "2:5", "interfaces can't have fields", "require a getter instead, e.g. get name -> string"},
		{"interface static method", "interface I {\n    static fn x()\n}", "2:5", "interfaces can't have static methods", ""},
		{"extern body", "extern fn f() {\n}", "1:11", "extern functions have no body", "remove the body; the function is written in C"},
		{"extern expression body", "extern fn f() -> int => 1", "1:11", "extern functions have no body", "remove the body; the function is written in C"},
		{"extern generic", "extern fn f<T>(x: T)", "1:13", "extern functions can't be generic", "C has no generics; declare one extern fn per type"},
		{"extern throws", "extern fn f() throws", "1:15", "extern functions can't throw", "C functions report failure through their result; check it in a Vex wrapper that throws"},
		{"extern without fn", "extern struct S {}", "1:8", "expected 'fn' after 'extern', found 'struct'", "write extern fn name(...)"},
		{"extern type generic", "extern type Db<T>", "1:15", "extern types can't be generic", "declare one extern type per C type"},
		{"extern type body", "extern type Db {}", "1:16", "extern types have no body", "C structs are reached through extern fn calls on the handle"},
		{"export generic", "export fn f<T>(x: T) {}", "1:13", "export functions can't be generic", "C has no generics; export one fn per type"},
		{"export throws", "export fn f() throws {}", "1:15", "export functions can't throw", "catch errors inside and return a status instead"},
		{"extern bad C name", "extern fn f() as \"no-dash\"", "1:18", "'no-dash' isn't a valid C name", "use letters, digits and _, not starting with a digit"},
		{"extern C name without quotes", "extern fn f() as g", "1:18", "expected the C name in quotes, found 'g'", `e.g. as "c_name"`},
		{"extern method", "struct S {\n    extern fn f()\n}", "2:5", "expected a field or method, found 'extern'", ""},
		{"import after decl", "fn main() {}\nimport \"vex:math\"", "2:1", "imports go at the top of the file", "move it above the first declaration"},
		{"import without path", "import math", "1:8", "expected the path of a module in quotes, found 'math'", `e.g. import "vex:math" or import "models"`},
		{"empty import", "import \"\"", "1:8", "empty import path", `e.g. import "vex:math" or import "models"`},
		{"import as nothing", "import \"m\" as", "1:14", "expected a name for the module after 'as', found end of file", ""},
		{"pub twice", "pub pub fn f() {}", "1:5", "unexpected 'pub'", "one pub is enough"},
		{"pub member twice", "struct S {\n    pub pub x: int\n}", "2:9", "unexpected 'pub'", "one pub is enough"},
		{"pub case", "enum E {\n    pub a\n}", "2:9", "cases can't be marked pub", "remove pub: cases are public whenever their enum is"},
		{"pub interface method", "interface I {\n    pub fn x()\n}", "2:5", "interface methods can't be marked pub", "remove pub: interface methods are public whenever the interface is"},
		{"method without body", "struct S {\n    fn x()\n}", "2:11", "expected '{' or '=>' to start the body of x, found end of line", ""},
		{"empty type parameters", "fn f<>() {\n}", "1:5", "empty type parameter list", "remove the <>, or name a type parameter: <T>"},
		{"ternary", "fn main() {\n let n = ok ? 1 : 2\n}", "2:13", "Vex has no ? : operator", "write if cond { a } else { b }"},
		{"lone question", "fn main() {\n let n = x?\n}", "2:11", "unexpected '?'", "use ?. to read a member, or ?? to give a default"},
		{"if var", "fn main() {\n if var x = y {}\n}", "2:5", "'if var' isn't supported", "write if let; copy it into a var inside the block"},
		{"if let no value", "fn main() {\n if let x {}\n}", "2:11", "expected '=' after 'if let x', found '{'", "write if let x = optionalValue { ... }"},
		{"guard without let", "fn main() {\n guard x else { return }\n}", "2:8", "expected 'let' after 'guard', found 'x'", "write guard let name = optionalValue else { return }"},
		{"guard without else", "fn main() {\n guard let x = y { return }\n}", "2:18", "expected 'else' after the guard value, found '{'", "add else { return } after the value"},
		{"enum in function", "fn main() {\n enum E { a }\n}", "2:2", "enums can't be declared inside functions", "declare it at the top level of the file"},
		{"field in extension", "extension string {\n    count: int\n}", "2:5", "extensions can't add fields", "compute it instead: get count -> T { ... }"},
		{"public field in extension", "extension string {\n    pub count: int\n}", "2:9", "extensions can't add fields", "compute it instead: get count -> T { ... }"},
		{"unclosed extension", "extension string {\n    fn f() {}\n", "1:18", "unclosed extension body", "add a matching }"},
		{"static operator", "struct A {\n    static operator +(o: A) -> A => o\n}", "2:12", "operators can't be static", "remove static: self is the left operand"},
		{"not-equal operator", "struct A {\n    operator !=(o: A) -> bool => true\n}", "2:14", "'!=' can't be defined", "define == instead: != is its opposite"},
		{"greater operator", "struct A {\n    operator >=(o: A) -> bool => true\n}", "2:14", "'>=' can't be defined", "define < instead: >= follows from it"},
		{"unknown operator", "struct A {\n    operator &&(o: A) -> A => o\n}", "2:14", "expected an operator after 'operator', found '&&'", "operators are + - * / % == < [] and []="},
		{"binary operator arity", "struct A {\n    operator *() -> A => self\n}", "2:15", "operator '*' takes 1 parameter, found 0", "write operator *(other: T) -> T"},
		{"index set arity", "struct A {\n    operator []=(i: int) {}\n}", "2:17", "operator '[]=' takes 2 parameters, found 1", "write operator []=(index: int, value: T)"},
		{"generic operator", "struct A {\n    operator +<T>(o: T) -> A => self\n}", "2:15", "operators can't take type parameters", ""},
		{"throwing operator", "struct A {\n    operator +(o: A) throws -> A => o\n}", "2:22", "operators can't throw", "make it a method that throws instead"},
		{"enum operator", "enum E {\n    a\n    operator +(o: E) -> E => a\n}", "3:5", "enums can't define operators yet", "use a method, or a struct that holds the enum"},
		{"interface operator", "interface I {\n    operator ==(o: I) -> bool\n}", "2:5", "interfaces can't declare operators", "require a method instead, and define the operator in each struct"},
		{"typedef in function", "fn main() {\n typedef T = int\n}", "2:2", "typedefs can't be declared inside functions", "declare it at the top level of the file"},
		{"typedef without =", "typedef T int", "1:11", "expected '=' after the type's name, found 'int'", "write typedef T = the type it names, e.g. typedef T = [string: int]"},
		{"typedef without name", "typedef = int", "1:9", "expected a name for the type, found '='", ""},
		{"case with type", "enum E {\n a: int\n}", "2:3", "enum cases don't have a type", "enum cases carry values in parentheses: a(value: int)"},
		{"case value type", "enum E {\n a(x)\n}", "2:5", "case value 'x' needs a type", "write it as x: int"},
		{"match brace", "fn main() {\n match x\n {}\n}", "2:9", "expected '{' after the match subject, found end of line", "put { on the same line as match"},
		{"match arrow", "fn main() {\n match x { a -> 1 }\n}", "2:14", "expected '=>' after the pattern, found '->'", "write pattern => result"},
		{"dotted pattern", "fn main() {\n match x { .a => 1 }\n}", "2:12", "patterns don't start with '.'", "write the case name without the dot"},
		{"as without ?", "fn main() {\n let t = v as Text\n}", "2:12", "Vex has only as?, not as", "write as? T, which gives nil when the value isn't a T"},
		{"bad pattern", "fn main() {\n match x { + => 1 }\n}", "2:12", "expected a pattern, found '+'", "for example circle(r), (a, b), [first, ...rest], 0, \"yes\" or _"},
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
	_, errs := parse(t, "fn main() { a # b }")
	if len(errs) != 1 || errs[0].Hint != "comments start with //" {
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

func TestAsync(t *testing.T) {
	cases := []struct{ src, want string }{
		{"let x = await f()", "(let x (await (call f)))"},
		{"let x = try await f()", "(let x (try (await (call f))))"},
		{"let x = try? await f()", "(let x (try? (await (call f))))"},
		{"await sleep(1)", "(await (call sleep 1))"},
		{"let x = await a.b().c", "(let x (await (. (call (. a b)) c)))"},
		{"let x = -await f()", "(let x (- (await (call f))))"},
		{"while let x = f() { g(x) }", "(while let x (call f) (block (call g x)))"},
	}
	for _, c := range cases {
		got := body(t, c.src)
		want := "(block " + c.want + ")"
		if got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
	f, errs := parse(t, "async fn load() -> int { return 1 }\nstruct S {\n    pub async fn m() {}\n    static async fn n() {}\n}")
	if errs != nil {
		t.Fatal(errs)
	}
	if fn := f.Decls[0].(*ast.FuncDecl); !fn.Async {
		t.Errorf("load: Async not set")
	}
	for _, m := range f.Decls[1].(*ast.StructDecl).Methods {
		if !m.Async {
			t.Errorf("%s: Async not set", m.Name.Name)
		}
	}
	for _, src := range []string{"async struct S {}", "interface I {\n    async fn f()\n}", "export async fn f() {}"} {
		if _, errs := parse(t, src); errs == nil {
			t.Errorf("%q: expected an error", src)
		}
	}
}

func TestRecords(t *testing.T) {
	cases := []struct{ src, want string }{
		{"let r = (1, 2)", "(let r (record 1 2))"},
		{"let r = (1,)", "(let r (record 1))"},
		{"let r = (1)", "(let r 1)"},
		{"let r = (x: 1, y: 2)", "(let r (record (x: 1) (y: 2)))"},
		{"let r = (1, name: \"a\")", `(let r (record 1 (name: "a")))`},
		{"f(r.$1, r.x)", "(call f (. r $1) (. r x))"},
		{"let (a, b) = f()", "(let (a, b) (call f))"},
		{"var (min: lo, max: _) = f()", "(var (min: lo, max: _) (call f))"},
		{"let (:min, :max) = f()", "(let (:min, :max) (call f))"},
		{"for (k, v) in xs { g(k) }", "(for (k, v) xs (block (call g k)))"},
		{"for i, (k, v) in xs { g(k) }", "(for i, (k, v) xs (block (call g k)))"},
	}
	for _, c := range cases {
		got := body(t, c.src)
		want := "(block " + c.want + ")"
		if got != want {
			t.Errorf("%s\n got: %s\nwant: %s", c.src, got, want)
		}
	}
	for src, want := range map[string]string{
		"fn f() -> (int, string) {}":        "(int, string)",
		"fn f() -> ({int x, int y}) {}":     "({int x, int y})",
		"fn f() -> (int, {string name}) {}": "(int, {string name})",
		"fn f() -> (int,) {}":               "(int,)",
		"fn f() -> (int) {}":                "int",
	} {
		f, errs := parse(t, src)
		if errs != nil {
			t.Fatalf("%s: %v", src, errs)
		}
		if got := ast.TypeString(f.Decls[0].(*ast.FuncDecl).Result); got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}
