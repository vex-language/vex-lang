package codegen

import (
	"bytes"
	"errors"
	"fmt"
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
	return execute(t, build(t, src), stdin)
}

// Compiles [src] and returns the path of the program.
func build(t *testing.T, src string) string {
	t.Helper()
	if _, err := cc.Command(); err != nil {
		t.Skip(err)
	}
	exe := filepath.Join(t.TempDir(), "main")
	if err := cc.Compile(generate(t, src), exe); err != nil {
		t.Fatal(err)
	}
	return exe
}

// Runs the program [exe] with [stdin] and the extra environment [env].
func execute(t *testing.T, exe, stdin string, env ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), env...)
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
		{"growing a list inside map and filter", `fn main() {
    var xs = [1, 2]
    let ys = xs.map(fn(x: int) -> int { xs.push(x); return x * 10 })
    let zs = xs.filter(fn(x: int) -> bool { xs.push(x); return x > 1 })
    println(ys, zs, xs.len)
}`, "", "[10, 20] [2, 2] 8\n"},
		{"invalid UTF-8", `fn main() {
    let s = input()
    println(s.len, s.chars().len, s.indexOf("b"), s.chars()[3], s.substring(2, 4))
}`, "\x80\x80ab\xe2\x82\n", "6 6 3 b ab\n"},
		{"printing cycles", `struct Node {
    name: string
    next: Node? = nil
}
fn main() {
    let a = Node(name: "a")
    let b = Node(name: "b", next: a)
    a.next = b
    println(a, [b])
    let s = Node(name: "s")
    println([Node(name: "x", next: s), Node(name: "y", next: s)])
}`, "", "Node(name: \"a\", next: Node(name: \"b\", next: Node(...))) [Node(name: \"b\", next: Node(name: \"a\", next: Node(...)))]\n" +
			"[Node(name: \"x\", next: Node(name: \"s\", next: nil)), Node(name: \"y\", next: Node(name: \"s\", next: nil))]\n"},
		{"changing a list while looping over it", `fn main() {
    var xs = [1, 2, 3]
    for x in xs {
        xs.push(x * 10)
    }
    var ys = [1, 2, 3, 4]
    for i, y in ys {
        if i == 0 {
            ys.pop()
            ys.pop()
        }
        print(y, "")
    }
    println(xs)
}`, "", "1 2 [1, 2, 3, 10, 20, 30]\n"},
		{"compound assignment reads the target first", `struct Box { n: int }
fn main() {
    let o = Box(n: 10)
    var xs = [10]
    var x = 10
    o.n += fn() -> int {
        o.n = 100
        return 1
    }()
    xs[0] += fn() -> int {
        xs[0] = 100
        return 1
    }()
    x += fn() -> int {
        x = 100
        return 1
    }()
    println(o.n, xs, x)
}`, "", "11 [11] 11\n"},
		{"inclusive range at the largest int", `fn main() {
    var n = 0
    for i in 9223372036854775806..=9223372036854775807 {
        n += 1
    }
    for i in 5..=4 { n += 100 }
    for i in 1..=4 {
        if i % 2 == 0 { continue }
        n += 10
    }
    println(n)
}`, "", "22\n"},
		{"errors stop the rest of the expression", `fn fail(s: string) throws -> int {
    throw Error(s)
}
fn note(s: string) -> int {
    println("ran {s}")
    return 1
}
fn add(a: int, b: int) -> int => a + b
fn main() {
    let x = try add(note("a"), add(fail("boom"), note("b"))) catch e {
        println(e.message)
        -1
    }
    println(x)
}`, "", "ran a\nboom\n-1\n"},
		{"errors through closures and methods", `enum Bad { tooBig(n: int), negative }
struct Box {
    limit: int

    fn check(n: int) throws(Bad) -> int {
        if n < 0 { throw .negative }
        if n > limit { throw .tooBig(n: n) }
        return n
    }
}
fn each(xs: [int], f: fn(int) throws -> int) throws -> int {
    var total = 0
    for x in xs {
        total += try f(x)
    }
    return total
}
fn main() {
    let b = Box(limit: 10)
    var seen = 0
    let sum = fn(xs: [int]) -> int => try each(xs, fn(x) {
        seen += 1
        return try b.check(x)
    }) catch e {
        println("stopped: {e.message}")
        0
    }
    println(sum([1, 2, 3]), seen)
    println(sum([4, 50, 6]), seen)
    println(sum([7, -1]), seen)
    let r = try b.check(99) catch e {
        match e {
            tooBig(n) => n * 2
            negative => 0
        }
    }
    println(r)
}`, "", "6 3\nstopped: Bad.tooBig(n: 50)\n0 5\nstopped: Bad.negative\n0 7\n198\n"},
		{"try? and nested tries", `fn find(n: int) throws -> int? {
    if n < 0 { throw Error("negative") }
    if n == 0 { return nil }
    return n
}
fn twice(n: int) throws -> int {
    let v = try find(n) ?? 5
    return v * 2
}
fn main() {
    println(try? find(-1), try? find(0), try? find(3))
    let a = try twice(-1) catch {
        try twice(4) catch { 0 }
    }
    println(a, try? twice(0) ?? 0)
}`, "", "nil nil 3\n8 10\n"},
		{"interfaces", `interface Shape {
    fn area() -> float
    fn name() -> string => "shape"
    fn describe() -> string => "{name()} with area {area()}"
}

interface Scalable {
    fn scale(by: float)
}

struct Circle: Shape, Scalable {
    r: float
    fn area() -> float => 3.0 * r * r
    fn name() -> string => "circle"
    fn scale(by: float) {
        r = r * by
    }
}

struct Square: Shape {
    s: float
    fn area() -> float => s * s
}

struct Labeled<T>: Shape {
    label: string
    value: T
    fn area() -> float => 0.0
    fn name() -> string => "{label}={value}"
}

fn total(shapes: [Shape]) -> float {
    var sum = 0.0
    for s in shapes {
        sum += s.area()
    }
    return sum
}

fn biggest<T: Shape>(xs: [T]) -> T? {
    if xs.len == 0 {
        return nil
    }
    var best = xs[0]
    for x in xs {
        if x.area() > best.area() {
            best = x
        }
    }
    return best
}

fn grow(items: [Scalable]) {
    for i in items {
        i.scale(by: 2.0)
    }
}

fn main() {
    let c = Circle(r: 1.0)
    let shapes: [Shape] = [c, Square(s: 2.0), Labeled(label: "n", value: 5)]
    for s in shapes {
        println(s.describe())
    }
    println(total(shapes))
    grow([c])
    println(c.area(), c.describe(), shapes[0].area())
    println(biggest([Square(s: 1.0), Square(s: 3.0)]), biggest(shapes))
    let one: Shape = Square(s: 4.0)
    println(one, shapes, "{one}")
    var maybe: Shape? = nil
    println(maybe?.area())
    maybe = c
    println(maybe?.area() ?? 0.0)
    let byName: [string: Shape] = ["sq": Square(s: 1.5)]
    println(byName["sq"]?.name())
    let pick: Shape = match shapes.len > 2 {
        true => Square(s: 9.0),
        false => c,
    }
    println(pick)
}`, "", "circle with area 3.0\nshape with area 4.0\nn=5 with area 0.0\n7.0\n12.0 circle with area 12.0 12.0\nSquare(s: 3.0) Circle(r: 2.0)\nSquare(s: 4.0) [Circle(r: 2.0), Square(s: 2.0), Labeled<int>(label: \"n\", value: 5)] Square(s: 4.0)\nnil\n12.0\nshape\nSquare(s: 9.0)\n"},
		{"generic functions and structs", `fn first<T>(xs: [T]) -> T? {
    if xs.len == 0 {
        return nil
    }
    return xs[0]
}

fn max<T: Comparable>(a: T, b: T) -> T {
    if a > b {
        return a
    }
    return b
}

fn count<T: Hashable>(xs: [T]) -> [T: int] {
    var m: [T: int] = [:]
    for x in xs {
        m[x] = (m[x] ?? 0) + 1
    }
    return m
}

fn apply<T, U>(xs: [T], f: fn(T) -> U) -> [U] {
    var out: [U] = []
    for x in xs {
        out.push(f(x))
    }
    return out
}

struct Box<T> {
    value: T
    fn get() -> T => value
    fn set(v: T) {
        value = v
    }
}

struct Pair<A, B> {
    a: A
    b: B
    fn swap() -> Pair<B, A> => Pair(a: b, b: a)
}

struct Stack<T> {
    items: [T] = []
    fn push(x: T) {
        items.push(x)
    }
    fn pop() -> T? {
        if items.len == 0 {
            return nil
        }
        return items.pop()
    }
}

fn empty<T>() -> [T] {
    return []
}

fn main() {
    println(first([3, 1, 2]), first(["a"]), first<int>([]))
    println(max(3, 7), max("pear", "apple"), max(1.5, 0.5))
    println(count(["a", "b", "a"]))
    println(apply([1, 2, 3], fn(x) => "#{x}"))
    let b = Box(value: "hi")
    b.set("yo")
    println(b, b.get())
    let n: Box<int> = Box(value: 5)
    println(n.value + 1)
    let p = Pair(a: 1, b: "one")
    println(p, p.swap())
    let s = Stack<string>()
    s.push("x")
    s.push("y")
    println(s.pop(), s.pop(), s.pop())
    let e: [string] = empty()
    println(e)
    let boxes = [Box(value: 1), Box(value: 2)]
    println(apply(boxes, fn(x) => x.value * 10))
}`, "", "3 a nil\n7 pear 1.5\n[\"a\": 2, \"b\": 1]\n[\"#1\", \"#2\", \"#3\"]\nBox<string>(value: \"yo\") yo\n6\nPair<int, string>(a: 1, b: \"one\") Pair<string, int>(a: \"one\", b: 1)\ny x nil\n[]\n[10, 20]\n"},
		{"generic linked list, cache and throwing callbacks", `enum Color { red, green }

struct Node<T> {
    value: T
    next: Node<T>?
}

struct List<T> {
    head: Node<T>?
    size: int = 0

    fn add(v: T) {
        head = Node(value: v, next: head)
        size += 1
    }

    fn each(f: fn(T)) {
        var n = head
        while true {
            if let cur = n {
                f(cur.value)
                n = cur.next
            } else {
                break
            }
        }
    }

    fn toList() -> [T] {
        var out: [T] = []
        each(fn(x) { out.push(x) })
        return out
    }
}

struct Cache<K: Hashable, V> {
    store: [K: V] = [:]
    hits: int = 0

    fn get(k: K, make: fn() -> V) -> V {
        if let v = store[k] {
            hits += 1
            return v
        }
        let v = make()
        store[k] = v
        return v
    }
}

fn orElse<T>(x: T?, d: T) -> T {
    if let v = x {
        return v
    }
    return d
}

fn largest<T: Comparable>(xs: [T]) -> T? {
    var best = first(xs)
    for x in xs {
        if let b = best {
            if x > b {
                best = x
            }
        }
    }
    return best
}

fn first<T>(xs: [T]) -> T? {
    if xs.len == 0 {
        return nil
    }
    return xs[0]
}

fn parseAll<T>(xs: [string], f: fn(string) throws -> T) throws -> [T] {
    var out: [T] = []
    for x in xs {
        out.push(try f(x))
    }
    return out
}

fn toInt(s: string) throws -> int {
    if s == "" {
        throw Error(message: "empty")
    }
    return int(s)
}

fn counter<T>(start: T, step: fn(T) -> T) -> fn() -> T {
    var cur = start
    return fn() -> T {
        let r = cur
        cur = step(cur)
        return r
    }
}

fn uniq<T: Hashable>(xs: [T]) -> [T] {
    var seen: [T: bool] = [:]
    var out: [T] = []
    for x in xs {
        if !seen.contains(x) {
            seen[x] = true
            out.push(x)
        }
    }
    return out
}

fn main() throws {
    let l = List<string>(head: nil)
    l.add("a")
    l.add("b")
    l.add("c")
    println(l.toList(), l.size)
    let nums = List<int>(head: nil)
    for i in 0..5 {
        nums.add(i * i)
    }
    println(nums.toList())

    let c = Cache<string, [int]>()
    println(c.get("x", fn() => [1, 2]), c.get("x", fn() => [9]), c.hits)

    let none: int? = nil
    println(orElse(none, 7), orElse(3, 7), orElse(nil, "d"))
    println(largest([3, 9, 2]), largest(["b", "a"]), largest<float>([]))
    println(try parseAll(["1", "2"], toInt))
    let r = try parseAll(["1", ""], toInt) catch e {
        println("failed:", e.message)
        []
    }
    println(r)
    let next = counter(start: 1, step: fn(x) => x * 2)
    println(next(), next(), next())
    let s = counter(start: "a", step: fn(x) => x + "a")
    println(s(), s())
    println(uniq([1, 2, 1, 3, 2]), uniq([Color.red, Color.green, Color.red]), uniq(["x", "x"]))
}`, "", "[\"c\", \"b\", \"a\"] 3\n[16, 9, 4, 1, 0]\n[1, 2] [1, 2] 1\n7 3 d\n9 b nil\n[1, 2]\nfailed: empty\n[]\n1 2 4\na aa\n[1, 2, 3] [Color.red, Color.green] [\"x\"]\n"},
		{"maps", `enum Color { red, green }
fn main() {
    var ages = ["ann": 31, "bob": 25]
    ages["cat"] = 40
    ages["ann"] = 32
    println(ages, ages.len, ages["zoe"] ?? 0, ages["bob"])
    println(ages.remove("bob"), ages.remove("bob"), ages.contains("cat"), ages.keys, ages.values)
    for name, age in ages {
        print(name, age, "")
    }
    for name in ages {
        print(name, "")
    }
    println()
    let colors: [Color: [int]] = [.green: [1], .red: []]
    println(colors, colors[.red]?.len)
    var grow = [1: 1]
    for k in grow {
        grow[k + 1] = 1
    }
    var big: [int: int] = [:]
    for i in 0..1000 {
        big[i] = i
    }
    for i in 0..1000 {
        if i % 10 != 0 {
            big.remove(i)
        }
    }
    var seen = 0
    for k, v in big {
        if seen == 0 {
            for j in 1000..1200 {
                big[j] = j
            }
        }
        seen += 1
    }
    println(grow, seen, big.len, big[990], big[1199], big[5])
    let empty: [string: bool] = [:]
    println(empty, empty.keys)
}`, "", "[\"ann\": 32, \"bob\": 25, \"cat\": 40] 3 0 25\n25 nil true [\"ann\", \"cat\"] [32, 40]\nann 32 cat 40 ann cat \n" +
			"[Color.green: [1], Color.red: []] 0\n[1: 1, 2: 1] 100 300 990 1199 nil\n[:] []\n"},
		{"conditions with temporaries", `fn two() -> int => 2
fn main() {
    let xs = [1, 2, 3]
    if xs.contains(two()) {
        println("yes")
    }
    while xs.filter(fn(x) => x > two()).len > 5 {
        println("never")
    }
    if "(" + str(two()) == "(2" {
        println("paren")
    }
}`, "", "yes\nparen\n"},
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
    let small = -big - 1
    println(big &+ 1, small &- 1, big &* 3, small / 2)
    var h = 5381
    for c in [104, 105] {
        h = h &* 33 &+ c
    }
    h &*= 1000003
    h &-= 7
    println(h)
}`, "", "3 -3 1 -1 14 20\n-9223372036854775808 9223372036854775807 9223372036854775805 -4611686018427387904\n5863463590331\n"},
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
			exe := build(t, c.src)
			out, errOut, code := execute(t, exe, c.stdin)
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, stderr:\n%s", code, errOut)
			}
			if out != c.want {
				t.Errorf("output\n got: %q\nwant: %q", out, c.want)
			}

			// A collection on every allocation finds objects the collector
			// would wrongly free.
			out, errOut, code = execute(t, exe, c.stdin, "VEX_GC_STRESS=1", "VEX_GC_VERIFY=1")
			if code != 0 || errOut != "" || out != c.want {
				t.Errorf("under VEX_GC_STRESS: exit %d, stderr %q\n got: %q\nwant: %q", code, errOut, out, c.want)
			}
		})
	}
}

func TestUncaughtErrors(t *testing.T) {
	cases := []struct{ name, src, out, err string }{
		{"interface methods", `interface Parser {
    fn parse(text: string) throws -> int
    fn parseOr(text: string, fallback: int) -> int {
        return try parse(text) catch {
            fallback
        }
    }
}

struct Plain: Parser {
    fn parse(text: string) throws -> int {
        if text == "" {
            throw Error(message: "empty input")
        }
        return int(text)
    }
}

struct Doubling: Parser {
    base: Plain = Plain()
    fn parse(text: string) throws -> int => 2 * (try base.parse(text))
}

fn main() throws {
    let parsers: [Parser] = [Plain(), Doubling()]
    for p in parsers {
        println(try p.parse("21"), p.parseOr("", -1))
    }
    let r = try parsers[1].parse("") catch e {
        println("failed:", e.message)
        0
    }
    println(r)
    println(try parsers[0].parse(""))
}`, "21 -1\n42 -1\nfailed: empty input\n0\n", "error: empty input\n"},
		{"Error", `fn main() throws {
    println("start")
    throw Error("disk full")
}`, "start\n", "error: disk full\n"},
		{"enum", `enum E { gone(id: int) }
fn get(id: int) throws(E) -> int { throw .gone(id: id) }
fn main() throws(E) {
    println(try get(7))
}`, "", "error: E.gone(id: 7)\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, code := run(t, c.src, "")
			if code != 1 || out != c.out || errOut != c.err {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
			}
		})
	}
}

func TestStackOverflow(t *testing.T) {
	if strings.Contains(os.Getenv("VEX_CFLAGS"), "sanitize") {
		t.Skip("sanitizers report stack overflows themselves")
	}
	out, errOut, code := run(t, `fn depth(n: int) -> int {
    if n < 0 { return 0 }
    // Two calls, so no compiler can turn the recursion into a loop.
    return depth(n + 1) + depth(n - 1)
}
fn main() {
    println("start")
    println(depth(0))
}`, "")
	want := "error: stack overflow: too many nested function calls, probably a function that calls itself without stopping\n"
	if code != 1 || out != "start\n" || errOut != want {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestGarbageIsFreed(t *testing.T) {
	exe := build(t, `struct Point {
    x: int
    label: string
}
enum Shape { dot(at: Point), none }
fn main() {
    var keep: [Point] = []
    var total = 0
    for i in 0..300000 {
        let p = Point(x: i, label: "point {i}")
        let s = Shape.dot(at: p)
        let f = fn() -> int => p.x + p.label.len
        total += f() + "a,b,{i}".split(",").len
        match s {
            dot(q) => total += q.x
            none => total += 0
        }
        if i % 1000 == 0 { keep.push(p) }
    }
    println(total, keep.len, keep[299].label)
}`)
	out, errOut, code := execute(t, exe, "", "VEX_GC_STATS=1")
	if code != 0 || out != "90004088890 300 point 299000\n" {
		t.Fatalf("exit %d, output %q, stderr %q", code, out, errOut)
	}
	var collections, allocated, peak, live int
	if _, err := fmt.Sscanf(errOut, "gc: %d collections, %d KB allocated, %d KB peak heap, %d KB live",
		&collections, &allocated, &peak, &live); err != nil {
		t.Fatalf("bad stats %q: %v", errOut, err)
	}
	if collections == 0 || allocated < 10*peak || peak > 16*1024 {
		t.Errorf("garbage isn't freed: %s", errOut)
	}
}

func TestRuntimeErrors(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"index", "let xs = [1, 2, 3]\nprintln(xs[5])", "index 5 is out of range for a list of length 3\n --> main.vx:3"},
		{"negative index", "var xs = [1]\nxs[-1] = 0", "index -1 is out of range for a list of length 1\n --> main.vx:3"},
		{"empty list", "let xs: [int] = []\nprintln(xs[0])", "index 0 is out of range: the list is empty\n --> main.vx:3"},
		{"divide", "let z = 0\nprintln(10 / z)", "division by zero\n --> main.vx:3"},
		{"modulo", "var z = 0\nz %= z", "division by zero in %\n --> main.vx:3"},
		{"shift left too far", "let n = 64\nprintln(1 << n)", "shift by 64, outside 0 to 63\n --> main.vx:3"},
		{"shift right negative", "let n = -1\nprintln(8 >> n)", "shift by -1, outside 0 to 63\n --> main.vx:3"},
		{"shift assign", "var x = 1\nfor i in 0..70 { x <<= i }", "shift by 64, outside 0 to 63\n --> main.vx:3"},
		{"shift past a loop range", "var s = 0\nfor i in 0..=64 { s += 1 >> i }", "shift by 64, outside 0 to 63\n --> main.vx:3"},
		{"pop", "var xs: [int] = []\nxs.pop()", "can't pop from an empty list\n --> main.vx:3"},
		{"insert past end", "var xs = [1]\nxs.insert(2, at: 3)", "insert at 3 is out of range for a list of length 1\n --> main.vx:3"},
		{"remove past end", "var xs = [1]\nlet x = xs.remove(at: 1)", "index 1 is out of range for a list of length 1\n --> main.vx:3"},
		{"slice backwards", "let xs = [1, 2, 3]\nprintln(xs.slice(2, 1))", "slice(2, 1) is out of range for a list of length 3\n --> main.vx:3"},
		{"sort changes list", "var xs = [3, 1, 2]\nxs.sort(by: fn(a, b) {\n    xs.push(0)\n    return a < b\n})", "the list changed while it was being sorted\n --> main.vx:3"},
		{"removeWhere changes list", "var xs = [3, 1, 2]\nxs.removeWhere(fn(x) {\n    xs.push(0)\n    return x > 1\n})", "the list changed while removeWhere was running\n --> main.vx:3"},
		{"sum overflow", "let xs = [9223372036854775807, 1]\nprintln(xs.sum())", "integer overflow: 9223372036854775807 + 1 doesn't fit in an int; use &+ to wrap around\n --> main.vx:3"},
		{"parse int", `println(int("12abc"))`, "can't convert \"12abc\" to int\n --> main.vx:2"},
		{"parse float", `println(float("abc"))`, "can't convert \"abc\" to float\n --> main.vx:2"},
		{"parse radix", "let r = 37\nprintln(int.parse(\"1\", radix: r))", "radix 37 is outside 2 to 36\n --> main.vx:3"},
		{"str radix", "let r = 1\nprintln(str(5, radix: r))", "radix 1 is outside 2 to 36\n --> main.vx:3"},
		{"toFixed digits", "let d = 21\nprintln(1.5.toFixed(d))", "toFixed(21): the digits must be 0 to 20\n --> main.vx:3"},
		{"int too big after parse", `println(int.parse("99999999999999999999") ?? 0, int("99999999999999999999"))`, "can't convert \"99999999999999999999\" to int: the number is too big\n --> main.vx:2"},
		{"int too big", `println(int("99999999999999999999"))`, "can't convert \"99999999999999999999\" to int: the number is too big\n --> main.vx:2"},
		{"add overflow", "let big = 9223372036854775807\nprintln(big + 1)", "integer overflow: 9223372036854775807 + 1 doesn't fit in an int; use &+ to wrap around\n --> main.vx:3"},
		{"sub overflow", "let small = -9223372036854775807 - 1\nprintln(small - 1)", "integer overflow: -9223372036854775808 - 1 doesn't fit in an int; use &- to wrap around\n --> main.vx:3"},
		{"mul overflow", "var x = 4611686018427387904\nx *= 2", "integer overflow: 4611686018427387904 * 2 doesn't fit in an int; use &* to wrap around\n --> main.vx:3"},
		{"mul overflow by variable", "let a = 3037000500\nlet b = a\nprintln(a * b)", "integer overflow: 3037000500 * 3037000500 doesn't fit in an int; use &* to wrap around\n --> main.vx:4"},
		{"negate overflow", "let small = -9223372036854775807 - 1\nprintln(-small)", "integer overflow: -(-9223372036854775808) doesn't fit in an int\n --> main.vx:3"},
		{"divide overflow", "let small = -9223372036854775807 - 1\nlet m = -1\nprintln(small / m)", "integer overflow: -9223372036854775808 / -1 doesn't fit in an int\n --> main.vx:4"},
		{"float to int", "let big = 1e30\nprintln(int(big))", "can't convert 1e+30 to int: it's out of range\n --> main.vx:3"},
		{"float to int after garbage", "for i in 0..20000 { let s = \"XXXXXXXXXXXXXXX\" + \"XXXXXXXXXXXXXXXX\" }\nprintln(int(12345678901234567890.0))", "can't convert 1.2345678901234567e+19 to int: it's out of range\n --> main.vx:3"},
		{"int with NUL", `println(int("12\u{0}34"))`, "can't convert \"12\" to int\n --> main.vx:2"},
		{"float with NUL", `println(float("1\u{0}5"))`, "can't convert \"1\" to float\n --> main.vx:2"},
		{"float hex", `println(float("0x10"))`, "can't convert \"0x10\" to float\n --> main.vx:2"},
		{"float inf", `println(float("inf"))`, "can't convert \"inf\" to float\n --> main.vx:2"},
		{"float too big", `println(float("1e999"))`, "can't convert \"1e999\" to float\n --> main.vx:2"},
		{"panic", `panic("something broke")`, "something broke\n --> main.vx:2"},
		{"panic in match", "let n = match 3 {\n    1 => 1\n    _ => panic(\"bad number\")\n}", "bad number\n --> main.vx:4"},
		{"assert", "assert(1 > 2)", "assertion failed\n --> main.vx:2"},
		{"assert message", `assert(false, "math is broken")`, "assertion failed: math is broken\n --> main.vx:2"},
		{"empty split", `println("a".split(""))`, "split separator can't be empty\n --> main.vx:2"},
		{"empty replace", `println("a".replace("", "b"))`, "replace needs text to look for, not an empty string\n --> main.vx:2"},
		{"substring past end", `println("x".substring(0, 2))`, "substring(0, 2) is out of range for a string of length 1\n --> main.vx:2"},
		{"substring backwards", `println("héllo".substring(3, 1))`, "substring(3, 1) is out of range for a string of length 5\n --> main.vx:2"},
		{"substring negative", `println("abc".substring(-1, 1))`, "substring(-1, 1) is out of range for a string of length 3\n --> main.vx:2"},
		{"any of nothing", "let fs: [Future<int>] = []\nrun(any(fs))", "any() needs at least one future\n --> main.vx:3"},

		// Checks the range analysis must keep.
		{"loop step past the end", "var m = 5\nwhile m < 10 {\n    m += 9223372036854775805\n}", "integer overflow: 5 + 9223372036854775805 doesn't fit in an int; use &+ to wrap around\n --> main.vx:4"},
		{"pushed list", "var xs = [1, 2, 3]\nxs.push(4)\nprintln(xs[4])", "index 4 is out of range for a list of length 4\n --> main.vx:4"},
		{"index after shrinking loop", "var i = 2\nwhile i >= 0 {\n    i -= 1\n}\nlet xs = [1, 2, 3]\nprintln(xs[i])", "index -1 is out of range for a list of length 3\n --> main.vx:7"},
		{"index below range", "let xs = [1, 2, 3]\nfor i in 0..3 {\n    var j = i\n    j -= 1\n    println(xs[j])\n}", "index -1 is out of range for a list of length 3\n --> main.vx:6"},
		{"index set in a later pass", "let xs = [1, 2, 3]\nvar k = 0\nvar s = 0\nfor i in 0..5 {\n    s += xs[k]\n    k = i * 2\n}", "index 4 is out of range for a list of length 3\n --> main.vx:6"},
		{"index growing with another bound", "let xs = [1, 2]\nvar a = 0\nvar b = 0\nvar s = 0\nwhile a < 2 {\n    s += xs[b]\n    a += 1\n    b += 5\n}", "index 5 is out of range for a list of length 2\n --> main.vx:7"},
		{"index set in a branch", "let xs = [1, 2]\nvar i = 0\nif xs.len > 5 {\n    i = 1\n} else {\n    i = 7\n}\nprintln(xs[i])", "index 7 is out of range for a list of length 2\n --> main.vx:9"},
		{"range end changed later", "var n = 2\nlet xs = [for i in 0..n i]\nn = 10\nvar s = 0\nfor i in 0..n {\n    s += xs[i]\n}", "index 2 is out of range for a list of length 2\n --> main.vx:7"},
		{"inclusive range", "let xs = [1, 2, 3]\nvar s = 0\nfor i in 0..=3 {\n    s += xs[i]\n}", "index 3 is out of range for a list of length 3\n --> main.vx:5"},
		{"index changed by a closure", "var i = 0\nlet f = fn() { i = 9 }\nf()\nlet xs = [1, 2]\nprintln(xs[i])", "index 9 is out of range for a list of length 2\n --> main.vx:6"},
		{"index changed in a loop body", "let xs = [1, 2, 3]\nvar i = 0\nvar s = 0\nwhile i < 3 {\n    i += 1\n    s += xs[i]\n}", "index 3 is out of range for a list of length 3\n --> main.vx:7"},
		{"list of a list loop", "let xs = [1, 2]\nvar j = 0\nvar s = 0\nfor x in [5, 6, 7] {\n    s += xs[j] + x\n    j += 1\n}", "index 2 is out of range for a list of length 2\n --> main.vx:6"},
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

func TestFrozenConstants(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"push through alias", "const xs = [1]\nfn main() {\n    var ys = xs\n    ys.push(2)\n}\n", "can't change a constant list; change a copy made with .copy()\n --> main.vx:4"},
		{"pop through alias", "const xs = [1]\nfn main() {\n    var ys = xs\n    ys.pop()\n}\n", "can't change a constant list; change a copy made with .copy()\n --> main.vx:4"},
		{"set nested list", "const grid = [[1]]\nfn main() {\n    let row = grid[0]\n    row[0] = 5\n}\n", "can't change a constant list; change a copy made with .copy()\n --> main.vx:4"},
		{"set map through alias", "const m = [\"a\": 1]\nfn main() {\n    var n = m\n    n[\"b\"] = 2\n}\n", "can't change a constant map; change a copy made with .copy()\n --> main.vx:4"},
		{"remove from map", "const m = [\"a\": 1]\nfn main() {\n    var n = m\n    n.remove(\"a\")\n}\n", "can't change a constant map; change a copy made with .copy()\n --> main.vx:4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, errOut, code := run(t, c.src, "")
			if code != 1 {
				t.Errorf("exit code %d, want 1", code)
			}
			if want := "error: " + c.want + "\n"; errOut != want {
				t.Errorf("stderr\n got: %q\nwant: %q", errOut, want)
			}
		})
	}
}

// A loop whose bounds are known has its overflow and bounds checks left
// out, and a loop whose aren't keeps them.
func TestRangeChecksLeftOut(t *testing.T) {
	c := generate(t, `fn main() {
    let limit = 1000
    var marks = [for i in 0..limit i >= 2]
    var count = 0
    for n in 2..limit {
        if marks[n] {
            count += 1
            var m = n * n
            while m < limit {
                marks[m] = false
                m += n
            }
        }
    }
    println(count)
}
`)
	c = c[strings.Index(c, "static void fn_main(void) {"):]
	c = c[:strings.Index(c, "\n}\n")]
	for _, gone := range []string{"vx_mul(v_n, v_n", "VX_AT(bool", "vx_list_mut"} {
		if strings.Contains(c, gone) {
			t.Errorf("generated code still has %q", gone)
		}
	}
	if !strings.Contains(c, "v_count = vx_add(") {
		t.Errorf("count += 1 lost its overflow check")
	}
	if strings.Count(c, "vx_add(") != 1 {
		t.Errorf("m += n kept its overflow check")
	}
}
