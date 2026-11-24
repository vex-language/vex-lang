package codegen

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irshadio/vex-lang/compiler/cc"
	"github.com/irshadio/vex-lang/compiler/diag"
	"github.com/irshadio/vex-lang/compiler/parser"
	"github.com/irshadio/vex-lang/compiler/types"
)

func generate(t *testing.T, src string) string {
	t.Helper()
	f := diag.NewFile("main.vx", src)
	tree, err := parser.Parse(f)
	if err != nil {
		t.Fatalf("syntax errors:\n%v", err)
	}
	info, err := types.Check(f, tree)
	if err != nil {
		t.Fatalf("type errors:\n%v", err)
	}
	return Generate(info, "main.vx")
}

// Compiles and runs [src] with [stdin], returning its output and exit code.
func run(t *testing.T, src, stdin string) (stdout, stderr string, code int) {
	t.Helper()
	if _, err := cc.Command(); err != nil {
		t.Skip(err)
	}
	exe := filepath.Join(t.TempDir(), "main")
	if err := cc.Compile(generate(t, src), exe); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return out.String(), errOut.String(), code
}

func TestPrograms(t *testing.T) {
	cases := []struct {
		name, src, stdin, want string
	}{
		{"hello", `fn main() { println("Hello, world!") }`, "", "Hello, world!\n"},
		{"optionals", `struct User {
    name: string
    boss: User? = nil

    fn greet() -> string => "hi {name}"
}

fn find(id: int) -> User? {
    if id == 1 {
        return User(name: "ann", boss: User(name: "bob"))
    }
    return nil
}

fn main() {
    let a = find(1)
    let b = find(2)
    let n: int? = 5
    let f: float? = 2
    let none: string? = nil
    println(a?.name ?? "nobody", b?.name ?? "nobody")
    println(a?.boss?.name, b?.boss?.name, a?.greet())
    println(n, f, none, n ?? 0, none ?? "empty")
    println(a == nil, b == nil, nil != a)
    if let u = a {
        println("found", u.name)
    }
    if let u = b {
        println("never")
    } else if let m = n {
        println("n is", m)
    } else {
        println("no")
    }
    for id in [1, 2] {
        guard let u = find(id) else {
            println("no user", id)
            continue
        }
        println(u.greet())
    }
    let xs: [int?] = [1, nil, 3]
    var total = 0
    for x in xs {
        total += x ?? 100
    }
    println(xs, total)
    var later: User? = nil
    later = a
    println(later?.name?.upper(), later?.name?.len)
}`, "", "ann nobody\nbob nil hi ann\n5 2.0 nil 5 empty\nfalse true true\nfound ann\nn is 5\nhi ann\nno user 2\n[1, nil, 3] 104\nANN 3\n"},
		{"optional edge cases", `struct Node {
    value: int
    next: Node? = nil

    fn log() {
        println("node", value)
    }
}

enum Event {
    tap(label: string?)
    none
}

fn greet(name: string? = nil) -> string => "hello {name ?? "stranger"}"

fn length(head: Node?) -> int {
    var n = 0
    var cur = head
    while cur != nil {
        n += 1
        cur = cur?.next
    }
    return n
}

fn pick(i: int) -> int? => match i {
    0 => nil
    _ => i * 10
}

fn tick(s: string) -> int? {
    print(s, "")
    return 1
}

fn main() {
    let list = Node(value: 1, next: Node(value: 2, next: Node(value: 3)))
    println(length(list), length(nil))
    list.next?.log()
    let empty: Node? = nil
    empty?.log()
    println(greet(), greet(name: "ann"), "{empty?.value}")

    var handler: fn()? = nil
    println(handler == nil)
    handler = fn() { println("handled") }
    if let h = handler {
        h()
    }
    let label = fn(e: Event) -> string => match e {
        tap(l) => l ?? "unlabelled"
        none => "-"
    }
    println(label(.tap(label: "ok")), label(.tap(label: nil)), label(.none))
    println(pick(0), pick(4), pick(0) ?? pick(2) ?? 0)

    var count: int? = nil
    count = 3
    println(count, (count ?? 0) + 1)
    println(tick("a") ?? 0, tick("b") ?? tick("c") ?? 9)
    let words: [string?] = ["x", nil]
    println(words, words[1] ?? "?")
    var maybe = words[0]
    maybe = nil
    println(maybe)
}`, "", "3 0\nnode 2\nhello stranger hello ann nil\ntrue\nhandled\nok unlabelled -\nnil 40 20\n3 4\na b 1 1\n[\"x\", nil] ?\nnil\n"},
		{"enums", `enum Shape {
    circle(radius: float)
    rect(width: float, height: float)
    empty

    fn area() -> float => match self {
        circle(r) => 3.14 * r * r
        rect(w, h) => w * h
        empty => 0.0
    }

    fn name() -> string => match self {
        circle(_) => "circle"
        rect(_, _) => "rect"
        empty => "empty"
    }

    fn describe() -> string => "{name()} with area {area()}"

    static fn square(side: float) -> Shape => .rect(width: side, height: side)
}

enum Dir { up, down, left, right }

enum Result {
    ok(value: int)
    failed(reason: string)
}

fn turn(d: Dir) -> Dir => match d {
    up => .right
    right => .down
    down => .left
    left => .up
}

fn parse(text: string) -> Result {
    if text == "" {
        return .failed(reason: "empty input")
    }
    return .ok(value: int(text))
}

fn grade(score: int) -> string => match score {
    100 => "perfect"
    0 => "zero"
    _ => "{score} points"
}

fn main() {
    let shapes = [Shape.circle(radius: 1), .rect(width: 2, height: 3), Shape.empty, Shape.square(2)]
    for s in shapes {
        println(s.describe())
    }
    println(shapes)

    var d = Dir.up
    for i in 0..5 {
        print(d, "")
        d = turn(d)
    }
    println()
    println(d == .right, d != Dir.up)

    for text in ["42", ""] {
        match parse(text) {
            ok(v) => println("parsed", v)
            failed(why) => {
                println("error:", why)
            }
        }
    }

    println(grade(100), grade(0), grade(7))

    var total = 0
    for n in [1, 2, 3, 4, 5, 6] {
        match n % 3 {
            0 => continue
            _ => total += n
        }
        if n == 5 { break }
    }
    println("total", total)
    let flag = match total > 5 { true => "big", false => "small" }
    println(flag)
}`, "", "circle with area 3.14\nrect with area 6.0\nempty with area 0.0\nrect with area 4.0\n[Shape.circle(radius: 1.0), Shape.rect(width: 2.0, height: 3.0), Shape.empty, Shape.rect(width: 2.0, height: 2.0)]\nDir.up Dir.right Dir.down Dir.left Dir.up \ntrue true\nparsed 42\nerror: empty input\nperfect zero 7 points\ntotal 12\nbig\n"},
		{"enum edge cases", `enum Dir { up, down }

enum Tree {
    leaf
    node(left: Tree, value: int, right: Tree)

    fn sum() -> int => match self {
        leaf => 0
        node(l, v, r) => l.sum() + v + r.sum()
    }
}

struct Player {
    name: string
    facing: Dir
}

fn insert(t: Tree, v: int) -> Tree {
    match t {
        leaf => return .node(left: .leaf, value: v, right: .leaf)
        node(l, x, r) => {
            if v < x {
                return .node(left: insert(l, v), value: x, right: r)
            }
            return .node(left: l, value: x, right: insert(r, v))
        }
    }
}

fn main() {
    let dirs = [Dir.up, Dir.up]
    println(dirs.contains(.down), dirs.contains(Dir.up))
    let p = Player(name: "ana", facing: .down)
    println(p, "facing {p.facing}")
    let t = Tree.node(left: .node(left: .leaf, value: 1, right: .leaf), value: 2, right: .leaf)
    println(t.sum())
    var tree = Tree.leaf
    for v in [5, 3, 8, 1] {
        tree = insert(tree, v)
    }
    println(tree.sum(), tree)
    let f = fn(d: Dir) -> string => match d { up => "^", down => "v" }
    println(f(.up), f(p.facing))
    let cmd = "go"
    match cmd {
        "stop" => println("stopping")
        "go" => println("going")
        _ => println("?")
    }
}`, "", "false true\nPlayer(name: \"ana\", facing: Dir.down) facing Dir.down\n3\n17 Tree.node(left: Tree.node(left: Tree.node(left: Tree.leaf, value: 1, right: Tree.leaf), value: 3, right: Tree.leaf), value: 5, right: Tree.node(left: Tree.leaf, value: 8, right: Tree.leaf))\n^ v\ngoing\n"},
		{"match order and panic arms", `enum Op { add, neg }
fn log(s: string) -> int {
    print(s, "")
    return 1
}
fn main() {
    let o = Op.add
    let n = log("a") + match o {
        add => log("b")
        neg => panic("no")
    } + log("c")
    println(n)
}`, "", "a b c 3\n"},
		{"print values", `fn main() {
    println(1, 2.5, true, "text", [1, 2], ["a", "b"], [[1.0], []])
    print("no newline")
    print("\n")
    println()
}`, "", "1 2.5 true text [1, 2] [\"a\", \"b\"] [[1.0], []]\nno newline\n\n"},
		{"floats", `fn main() {
    println(3.0, 0.1 + 0.2, 1.0 / 3.0, 1e20, -0.5, 10 / 4.0, 2 * 1.5)
    println(10.0, 500.0, 123456.0, 1200.5, 0.001, 0.0, -20.0, 1e16, 1e17, 1e-5, 1e-7, 12.56636)
}`, "", "3.0 0.30000000000000004 0.3333333333333333 1e+20 -0.5 2.5 3.0\n" +
			"10.0 500.0 123456.0 1200.5 0.001 0.0 -20.0 10000000000000000.0 1e+17 0.00001 1e-07 12.56636\n"},
		{"int arithmetic", `fn main() {
    println(7 / 2, -7 / 2, 7 % 3, -7 % 3, 2 + 3 * 4, (2 + 3) * 4)
    let big = 9223372036854775807
    println(big + 1)
}`, "", "3 -3 1 -1 14 20\n-9223372036854775808\n"},
		{"strings", `fn main() {
    let s = "héllo, wörld"
    println(s.len, s.upper(), "ABC".lower(), s.contains("wör"), s.contains("x"))
    println("a,b,,c".split(","), "abc" < "abd", "b" > "abc", "x" == "x", "x" != "y")
    var t = "a"
    t += "b"
    println(t + "c", "tab\there", "quote \"q\"", "brace \{x\}")
}`, "", "12 HéLLO, WöRLD abc true false\n[\"a\", \"b\", \"\", \"c\"] true true true true\nabc tab\there quote \"q\" brace {x}\n"},
		{"interpolation", `struct P {
    x: int
    y: int = 2
}
fn main() {
    let n = 3
    let names = ["a", "b"]
    println("n={n}, next={n + 1}, f={1.5}, b={n > 2}, list={names}, p={P(x: 1)}, s={"in"}")
    println("{n}{n}")
}`, "", "n=3, next=4, f=1.5, b=true, list=[\"a\", \"b\"], p=P(x: 1, y: 2), s=in\n33\n"},
		{"conversions", `fn main() {
    println(str(42) + str(1.5) + str(true), int("  -12 "), int(3.99), int(-3.99), float(2), float(" 2.5"))
}`, "", "421.5true -12 3 -3 2.0 2.5\n"},
		{"control flow", `fn classify(n: int) -> string {
    if n < 0 {
        return "negative"
    } else if n == 0 {
        return "zero"
    } else {
        return "positive"
    }
}
fn main() {
    for n in [-5, 0, 5] { print(classify(n), "") }
    println()
    var i = 0
    while true {
        i += 1
        if i % 2 == 0 { continue }
        if i > 7 { break }
        print(i, "")
    }
    println()
    for i in 0..3 { print(i) }
    for i in 3..=5 { print(i) }
    for i in 5..3 { print("never") }
    println()
    for i, s in ["x", "y"] { print("{i}{s} ") }
    println()
}`, "", "negative zero positive \n1 3 5 7 \n012345\n0x 1y \n"},
		{"functions", `fn add(a: int, b: int) -> int => a + b
fn greet(name: string, greeting: string = "Hi", mark: string = "!") -> string => "{greeting}, {name}{mark}"
fn fib(n: int) -> int {
    if n < 2 { return n }
    return fib(n - 1) + fib(n - 2)
}
fn log(msg: string) { println("log: {msg}") }
fn main() {
    println(add(2, 3), fib(20))
    println(greet("Sam"))
    println(greet("Sam", mark: "?"))
    println(greet(mark: ".", name: "Kim", greeting: "Yo"))
    log("done")
}`, "", "5 6765\nHi, Sam!\nHi, Sam?\nYo, Kim.\nlog: done\n"},
		{"structs", `struct User {
    name: string
    age: int = 18
    tags: [string] = []

    fn greet() -> string => "Hi, I'm {name}"
    fn birthday() {
        age += 1
        tags.push("older")
    }
    fn isAdult() -> bool => age >= 18
    fn describe() -> string => "{greet()} ({age})"
    static fn guest() -> User => User(name: "Guest", age: 0)
}
fn rename(u: User, to: string) { u.name = to }
fn main() {
    let u = User(name: "Ann", age: 30)
    println(u)
    u.birthday()
    println(u.age, u.tags, u.isAdult(), u.describe())
    let alias = u
    rename(alias, "Annie")
    println(u.name)
    let g = User.guest()
    println(g, g.isAdult())
    let users = [u, g]
    users[1].age = 21
    println(g.age, users.len)
}`, "", "User(name: \"Ann\", age: 30, tags: [])\n31 [\"older\"] true Hi, I'm Ann (31)\nAnnie\nUser(name: \"Guest\", age: 0, tags: []) false\n21 2\n"},
		{"empty struct and nested", `struct Empty {}
struct Line {
    from: Point
    to: Point
}
struct Point {
    x: float
    y: float
}
fn main() {
    println(Empty())
    println(Line(from: Point(x: 0, y: 0), to: Point(x: 1.5, y: 2)))
}`, "", "Empty()\nLine(from: Point(x: 0.0, y: 0.0), to: Point(x: 1.5, y: 2.0))\n"},
		{"lists", `fn main() {
    var xs = [3, 1, 2]
    xs.push(10)
    let last = xs.pop()
    xs[0] = 7
    xs[1] += 5
    println(xs, last, xs.len, xs.contains(7), xs.contains(3))
    var grid = [[1, 2], [3]]
    grid[1].push(4)
    grid.push([])
    println(grid, grid[1][1])
    let words = ["a", "b"]
    println(words.contains("b"))
    var total = 0
    for x in xs { total += x }
    println(total)
}`, "", "[7, 6, 2] 10 3 true false\n[[1, 2], [3, 4], []] 4\ntrue\n15\n"},
		{"shadowing", `fn main() {
    let x = 1
    if true {
        let x = x + 1
        println(x)
        if true {
            let x = x * 10
            println(x)
        }
    }
    println(x)
}`, "", "2\n20\n1\n"},
		{"evaluation order", `fn step(name: string, v: int) -> int {
    print(name)
    return v
}
fn pair(a: int, b: int) -> int => a * 10 + b
struct Counter {
    n: int = 0
    fn next() -> int {
        n += 1
        return n
    }
}
fn main() {
    println(" ", step("a", 1) + step("b", 2))
    println(" ", pair(b: step("b", 2), a: step("a", 1)))
    let c = Counter()
    println([c.next(), c.next(), c.next()], "{c.next()}{c.next()}", c.n)
    var xs = [0]
    xs[0] = step("v", xs.len)
    println(" ", xs)
}`, "", "ab  3\nba  12\n[1, 2, 3] 45 5\nv  [1]\n"},
		{"assign while growing", `fn grow(xs: [int]) -> int {
    for i in 0..100 { xs.push(i) }
    return 1
}
fn main() {
    var xs = [0]
    xs[0] += grow(xs)
    println(xs[0], xs.len)
}`, "", "1 101\n"},
		{"input", `fn main() {
    let name = input("Name? ")
    let age = int(input())
    println("{name} is {age}")
    println(input() == "")
}`, "Ann\n30\n", "Name? Ann is 30\ntrue\n"},
		{"closures", `fn apply(f: fn(int) -> int, x: int) -> int => f(x)
fn double(x: int) -> int => x * 2
fn repeat(times: int, action: fn()) {
    for i in 0..times { action() }
}
fn adder(n: int) -> fn(int) -> int => fn(x) => x + n

fn counter() -> fn() -> int {
    var n = 0
    return fn() -> int {
        n += 1
        return n
    }
}

struct Button {
    title: string
    onTap: fn()
    fn tap() { onTap() }
}

struct Counter {
    count: int = 0
    fn incrementer() -> fn() => fn() { count += 1 }
}

fn main() {
    println(apply(fn(x) => x + 1, 5), apply(double, 5), adder(10)(5))
    var count = 0
    repeat(3) { count += 1 }
    println("count:", count)
    let btn = Button(title: "Add") { count += 10 }
    btn.tap()
    btn.onTap()
    println("count:", count)

    let next = counter()
    let other = counter()
    println(next(), next(), next(), other())

    let nums = [1, 2, 3, 4, 5, 6]
    println(nums.filter(fn(x) => x % 2 == 0), nums.map(fn(x) => x * x), nums.map(fn(x) => "#{x}"))

    let c = Counter()
    let inc = c.incrementer()
    inc()
    inc()
    println(c.count)

    var total = 0
    let outer = fn() {
        let inner = fn() { total += 100 }
        inner()
        inner()
    }
    outer()
    println(total)

    var actions: [fn()] = []
    for i in 0..3 {
        actions.push(fn() { print(i, "") })
    }
    for a in actions { a() }
    println()
    println(double, fn(s: string) => s.len)
}
`, "", "6 10 15\ncount: 3\ncount: 23\n1 2 3 1\n[2, 4, 6] [1, 4, 9, 16, 25, 36] [\"#1\", \"#2\", \"#3\", \"#4\", \"#5\", \"#6\"]\n2\n200\n0 1 2 \n<fn(int) -> int> <fn(string) -> int>\n"},
		{"closure evaluation order", `fn step(name: string, v: int) -> int {
    print(name)
    return v
}
fn main() {
    let pick = fn(n: int) -> fn(int) -> int {
        print("p")
        return fn(x) => x + n
    }
    println(" ", pick(1)(step("a", 2)))
    var fs: [fn(int) -> int] = []
    fs.push(fn(x) => x * 3)
    println(fs[0](4), [fn() => 1, fn() => 2][1]())
}`, "", "pa  3\n12 2\n"},
		{"names that are C keywords", `struct char {
    double: int
}
fn unsigned(auto: int) -> int => auto * 2
fn main() {
    let long = char(double: 4)
    let register = unsigned(long.double)
    println(register, long)
}`, "", "8 char(double: 4)\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, errOut, code := run(t, c.src, c.stdin)
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr:\n%s", code, errOut)
			}
			if out != c.want {
				t.Errorf("output\n got: %q\nwant: %q", out, c.want)
			}
		})
	}
}

func TestRuntimeErrors(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"index", "let xs = [1, 2, 3]\nprintln(xs[5])", "index 5 is out of range for a list of length 3\n --> main.vx:3"},
		{"negative index", "var xs = [1]\nxs[-1] = 0", "index -1 is out of range for a list of length 1\n --> main.vx:3"},
		{"empty list", "let xs: [int] = []\nprintln(xs[0])", "index 0 is out of range: the list is empty\n --> main.vx:3"},
		{"divide", "let z = 0\nprintln(10 / z)", "division by zero\n --> main.vx:3"},
		{"modulo", "var z = 0\nz %= z", "division by zero in %\n --> main.vx:3"},
		{"pop", "var xs: [int] = []\nxs.pop()", "can't pop from an empty list\n --> main.vx:3"},
		{"parse int", `println(int("12abc"))`, "can't convert \"12abc\" to int\n --> main.vx:2"},
		{"parse float", `println(float("abc"))`, "can't convert \"abc\" to float\n --> main.vx:2"},
		{"int too big", `println(int("99999999999999999999"))`, "can't convert \"99999999999999999999\" to int: the number is too big\n --> main.vx:2"},
		{"float to int", "let big = 1e30\nprintln(int(big))", "can't convert 1e+30 to int: it's out of range\n --> main.vx:3"},
		{"panic", `panic("something broke")`, "something broke\n --> main.vx:2"},
		{"panic in match", "let n = match 3 {\n    1 => 1\n    _ => panic(\"bad number\")\n}", "bad number\n --> main.vx:4"},
		{"assert", "assert(1 > 2)", "assertion failed\n --> main.vx:2"},
		{"assert message", `assert(false, "math is broken")`, "assertion failed: math is broken\n --> main.vx:2"},
		{"empty split", `println("a".split(""))`, "split separator can't be empty\n --> main.vx:2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, errOut, code := run(t, "fn main() {\n"+c.body+"\n}\n", "")
			if code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
			if want := "error: " + c.want + "\n"; errOut != want {
				t.Errorf("stderr\n got: %q\nwant: %q", errOut, want)
			}
			if out != "" {
				t.Errorf("unexpected output %q", out)
			}
		})
	}
}

func TestOutputBeforeErrorIsKept(t *testing.T) {
	out, _, code := run(t, "fn main() {\n println(\"before\")\n panic(\"stop\")\n}", "")
	if code != 1 || out != "before\n" {
		t.Errorf("exit %d, output %q", code, out)
	}
}

// Compiles generated code with strict warnings, to catch sloppy C that the
// normal build would hide.
func TestGeneratedCodeIsWarningFree(t *testing.T) {
	command, err := cc.Command()
	if err != nil {
		t.Skip(err)
	}
	if out, _ := exec.Command(command[0], "--version").Output(); !bytes.Contains(out, []byte("clang")) {
		t.Skip("strict warning flags are tuned for clang")
	}
	for _, path := range []string{"../parser/testdata/tour.vx", "../../examples/enums.vx", "../../examples/closures.vx", "../../examples/optionals.vx"} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		cfile := filepath.Join(dir, "main.c")
		if err := os.WriteFile(cfile, []byte(generate(t, string(src))), 0o644); err != nil {
			t.Fatal(err)
		}
		args := append(command[1:len(command):len(command)], "-std=gnu11", "-fwrapv", "-Wall", "-Wextra", "-Werror",
			"-Wno-unused-function", "-Wno-unused-parameter", "-Wno-unused-variable", "-c", cfile, "-o", filepath.Join(dir, "main.o"))
		if out, err := exec.Command(command[0], args...).CombinedOutput(); err != nil {
			t.Fatalf("warnings in code generated for %s:\n%s", path, out)
		}
	}
}
