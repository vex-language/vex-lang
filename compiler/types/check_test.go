package types

import (
	"os"
	"strings"
	"testing"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/diag"
	"github.com/irshadio/vex-lang/compiler/parser"
)

func check(t *testing.T, src string) (*Info, diag.List) {
	t.Helper()
	f := diag.NewFile("test.vx", src)
	tree, err := parser.Parse(f)
	if err != nil {
		t.Fatalf("syntax errors in test source:\n%v\n%s", err, src)
	}
	info, err := Check(f, tree)
	if err != nil {
		return info, err.(diag.List)
	}
	return info, nil
}

// Checks [body] inside `fn main` after the declarations in [decls].
func checkMain(t *testing.T, decls, body string) (*Info, diag.List) {
	t.Helper()
	return check(t, decls+"\nfn main() {\n"+body+"\n}\n")
}

// Returns the type of each variable declared with let or var, by name.
func varTypes(info *Info) map[string]string {
	out := map[string]string{}
	for id, v := range info.Defs {
		if v.Kind == LocalVar {
			out[id.Name] = v.Type.String()
		}
	}
	return out
}

const userDecl = `
struct User {
    name: string
    age: int = 18
    tags: [string] = []

    fn greet() -> string => "Hi, I'm {name}"
    fn isAdult() -> bool => age >= 18
    fn birthday() { age += 1 }
    fn rename(to: string) { name = to }
    fn intro() -> string => greet() + "!"

    static fn guest() -> User => User(name: "Guest")
    static fn twoGuests() -> [User] => [guest(), guest()]
}
`

func TestInference(t *testing.T) {
	info, errs := checkMain(t, userDecl, `
    let a = 1
    let b = 2.5
    let c = "hi"
    let d = true
    let e = [1, 2, 3]
    let f = [[1], [2]]
    let g: [string] = []
    let h = a * 2 + 1
    let i = b * 2
    let j: float = 3
    let k = "n = {a}"
    let l = User(name: "Ann")
    let m = l.name.len
    let n = User.guest().isAdult()
    let o = c.split(",")
    let p = e.pop()
    let q = -b
    let r = 2 * b
    let s = str(a) + c.upper()
    let u = int("42") + int(2.9)
    let v = User.twoGuests()[0].tags
    let w = input("Name? ")
`)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := map[string]string{
		"a": "int", "b": "float", "c": "string", "d": "bool", "e": "[int]", "f": "[[int]]",
		"g": "[string]", "h": "int", "i": "float", "j": "float", "k": "string", "l": "User",
		"m": "int", "n": "bool", "o": "[string]", "p": "int", "q": "float", "r": "float",
		"s": "string", "u": "int", "v": "[string]", "w": "string",
	}
	got := varTypes(info)
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: got %s, want %s", name, got[name], w)
		}
	}
}

func TestIntLiteralsInFloatContext(t *testing.T) {
	info, errs := checkMain(t, "", "let x: float = 2 * 3\nlet y = 1.5 + 1")
	if errs != nil {
		t.Fatal(errs)
	}
	for e, ty := range info.Types {
		if lit, ok := e.(*ast.IntLit); ok && ty != Float {
			t.Errorf("literal %s typed %s, want float", lit.Value, ty)
		}
	}
}

func TestCalls(t *testing.T) {
	info, errs := checkMain(t, userDecl+`
fn greet(name: string, greeting: string = "Hi") -> string => "{greeting}, {name}!"
`, `
    greet("Sam")
    greet("Sam", greeting: "Welcome")
    greet(greeting: "Yo", name: "Kim")
    let u = User(name: "Ann", age: 30)
    u.birthday()
    var xs = [1]
    xs.push(2)
    println(u, xs, xs.contains(2), "x".contains("x"))
    assert(xs.len == 2)
`)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	kinds := map[CallKind]int{}
	for call, c := range info.Calls {
		kinds[c.Kind]++
		if c.Kind == FuncCall && len(c.Args) != 2 {
			t.Errorf("greet call has %d bound args, want 2 (defaults filled)", len(c.Args))
		}
		if c.Kind == FuncCall {
			if s, ok := c.Args[0].(*ast.StringLit); !ok || (s.Value != "Sam" && s.Value != "Kim") {
				t.Errorf("first bound arg of %v is %v, want the name", call, ast.Sexpr(c.Args[0]))
			}
		}
	}
	if kinds[FuncCall] != 3 || kinds[ConstructorCall] < 2 || kinds[MethodCall] < 1 ||
		kinds[StaticCall] < 1 || kinds[BuiltinCall] != 2 || kinds[MemberBuiltinCall] != 3 {
		t.Errorf("call kinds: %v", kinds)
	}
}

func TestImplicitSelf(t *testing.T) {
	info, errs := check(t, userDecl+"fn main() {}")
	if errs != nil {
		t.Fatal(errs)
	}
	fields, methods := 0, 0
	for _, u := range info.Uses {
		switch u.(type) {
		case *Field:
			fields++
		case *Func:
			methods++
		}
	}
	if fields < 4 || methods < 3 {
		t.Errorf("implicit self uses: %d fields, %d methods", fields, methods)
	}
	if len(info.Selves) != 5 {
		t.Errorf("got %d methods with self, want 5", len(info.Selves))
	}
}

func TestMissingReturnAnalysis(t *testing.T) {
	ok := []string{
		"fn f(x: int) -> int {\n return x\n}",
		"fn f(x: int) -> int {\n if x > 0 {\n  return 1\n } else {\n  return 2\n }\n}",
		"fn f(x: int) -> int {\n if x > 0 {\n  return 1\n } else if x < 0 {\n  return -1\n } else {\n  return 0\n }\n}",
		"fn f(x: int) -> int {\n while true {\n  return 1\n }\n}",
		"fn f(x: int) -> int {\n panic(\"no\")\n}",
		"fn f(x: int) -> int {\n {\n  return x\n }\n}",
	}
	for _, src := range ok {
		if _, errs := check(t, src+"\nfn main() {}"); errs != nil {
			t.Errorf("%s\nunexpected errors: %v", src, errs)
		}
	}
	bad := []string{
		"fn f(x: int) -> int {\n if x > 0 {\n  return 1\n }\n}",
		"fn f(x: int) -> int {\n while x > 0 {\n  return 1\n }\n}",
		"fn f(x: int) -> int {\n while true {\n  if x > 0 { break }\n  return 1\n }\n}",
		"fn f(x: int) -> int {\n for i in 0..x {\n  return i\n }\n}",
		"fn f(x: int) -> int {\n}",
	}
	for _, src := range bad {
		_, errs := check(t, src+"\nfn main() {}")
		if len(errs) != 1 || !strings.HasPrefix(errs[0].Message, "missing return") {
			t.Errorf("%s\nwant one missing return error, got %v", src, errs)
		}
	}
}

func TestClosures(t *testing.T) {
	info, errs := checkMain(t, `
fn apply(f: fn(int) -> int, x: int) -> int => f(x)
fn double(x: int) -> int => x * 2
fn repeat(times: int, action: fn()) {
    for i in 0..times { action() }
}
fn adder(n: int) -> fn(int) -> int => fn(x) => x + n
struct Button {
    title: string
    onTap: fn()
    fn tap() { onTap() }
}`, `
    let a = fn(x: int) -> int => x * 2
    let b = fn(x: int) => x > 0
    let c = fn() { println("hi") }
    let d: fn(int, int) -> int = fn(x, y) => x + y
    let e = apply(fn(x) => x + 1, 5)
    let f = apply(double, 5)
    let g = adder(10)(5)
    let h = [1, 2, 3].map(fn(x) => "{x}")
    let i = [1, 2, 3].filter(fn(x) => x > 1)
    let j = fn(x: int) {
        if x > 0 {
            return
        }
    }
    let k = fn(n: int) {
        return n * 2
    }
    var count = 0
    repeat(3) { count += 1 }
    let btn = Button(title: "Add") { count += 1 }
    btn.tap()
    btn.onTap()
    let handlers = [fn() {}, c]
    handlers[0]()
    let l = [1.5].map(fn(x) {
        return x > 1.0
    })
`)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := map[string]string{
		"a": "fn(int) -> int", "b": "fn(int) -> bool", "c": "fn()", "d": "fn(int, int) -> int",
		"e": "int", "f": "int", "g": "int", "h": "[string]", "i": "[int]", "j": "fn(int)",
		"k": "fn(int) -> int", "btn": "Button", "handlers": "[fn()]", "l": "[bool]",
	}
	got := varTypes(info)
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: got %s, want %s", name, got[name], w)
		}
	}
	for id, v := range info.Defs {
		if id.Name == "count" && (!v.Captured || !v.Boxed()) {
			t.Errorf("count: captured %v, boxed %v; want both", v.Captured, v.Boxed())
		}
		if id.Name == "c" && v.Boxed() {
			t.Error("c is a let and doesn't need a box")
		}
	}
}

func TestNestedCapturesPassThrough(t *testing.T) {
	info, errs := checkMain(t, "", `
    var total = 0
    let outer = fn() {
        let inner = fn() { total += 1 }
        inner()
    }
    outer()
`)
	if errs != nil {
		t.Fatal(errs)
	}
	if len(info.Captures) != 2 {
		t.Fatalf("got %d closures with captures, want 2", len(info.Captures))
	}
	for lit, vars := range info.Captures {
		if len(vars) != 1 || vars[0].Name != "total" {
			t.Errorf("closure at %s captures %v, want [total]", lit.Pos(), vars)
		}
	}
}

func TestMethodClosureCapturesSelf(t *testing.T) {
	info, errs := check(t, `
struct Counter {
    n: int = 0
    fn incrementer() -> fn() => fn() { n += 1 }
}
fn main() {}`)
	if errs != nil {
		t.Fatal(errs)
	}
	for _, vars := range info.Captures {
		if len(vars) != 1 || vars[0].Kind != SelfVar {
			t.Errorf("captures %v, want self", vars)
		}
	}
}

const shapeDecl = `
enum Shape {
    circle(radius: float)
    rect(width: float, height: float)
    empty

    fn area() -> float => match self {
        circle(r) => 3.14 * r * r
        rect(w, h) => w * h
        empty => 0.0
    }
    fn isRound() -> bool => match self {
        circle(_) => true
        _ => false
    }
    fn describe() -> string => "area {area()}"
    static fn unit() -> Shape => .rect(width: 1, height: 1)
}
enum Dir { up, down }
`

func TestEnums(t *testing.T) {
	info, errs := checkMain(t, shapeDecl+`
fn turn(d: Dir) -> Dir => match d {
    up => .down
    down => .up
}
fn grade(n: int) -> string => match n {
    100 => "perfect"
    0 => "zero"
    _ => "ok"
}`, `
    let a = Shape.circle(radius: 2)
    let b: Shape = .rect(width: 2, height: 3)
    let c = Shape.empty
    let d = Shape.unit()
    let e = a.area()
    let f = [a, b, c, .empty]
    let g = turn(.up) == Dir.down
    let h = match a {
        circle(r) => r
        _ => 0.0
    }
    match b {
        rect(w, _) => {
            println(w)
        }
        circle(r) => println(r)
        empty => println("none")
    }
    let i = match true { true => "y", false => "n" }
    let j = match "x" { "x" => 1, _ => panic("no") }
`)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := map[string]string{
		"a": "Shape", "b": "Shape", "c": "Shape", "d": "Shape", "e": "float",
		"f": "[Shape]", "g": "bool", "h": "float", "i": "string", "j": "int",
	}
	got := varTypes(info)
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: got %s, want %s", name, got[name], w)
		}
	}
	if len(info.Enums) != 2 || len(info.Matches) != 8 {
		t.Errorf("got %d enums and %d matches", len(info.Enums), len(info.Matches))
	}
}

func TestMatchTerminates(t *testing.T) {
	_, errs := check(t, shapeDecl+`
fn size(s: Shape) -> int {
    match s {
        circle(_) => { return 1 }
        rect(_, _) => { return 2 }
        empty => panic("empty")
    }
}
fn main() {}`)
	if errs != nil {
		t.Fatal(errs)
	}
}

func TestOptionals(t *testing.T) {
	info, errs := checkMain(t, `
struct User {
    name: string
    boss: User? = nil
}
fn find(id: int) -> User? => nil
fn first(xs: [int]) -> int? {
    if xs.len == 0 { return nil }
    return xs[0]
}`, `
    let a = find(1)
    let b = a?.name
    let c = a?.boss?.name ?? "none"
    let d: float? = 2
    let e = first([1]) ?? 0
    let f = a == nil
    let g: [int?] = [1, nil]
    let h = find(1) ?? find(2)
    let i: fn()? = nil
    let j = a?.name?.upper()
    if let u = a {
        let k = u.name
    }
    guard let w = find(3) else { return }
    let l = w.boss
    let m = [1, nil, 3]
    let n = [nil, nil, "x"]
`)
	if errs != nil {
		t.Fatalf("unexpected errors:\n%v", errs)
	}
	want := map[string]string{
		"a": "User?", "b": "string?", "c": "string", "d": "float?", "e": "int", "f": "bool",
		"g": "[int?]", "h": "User?", "i": "fn()?", "j": "string?", "u": "User", "k": "string",
		"w": "User", "l": "User?", "m": "[int?]", "n": "[string?]",
	}
	got := varTypes(info)
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: got %s, want %s", name, got[name], w)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name, decls, body string
		pos, msg, hint    string
	}{
		// Interfaces.
		{"missing method", "interface Shape {\n    fn area() -> float\n}\nstruct C: Shape {\n    r: float\n}", "", "4:8", "C doesn't implement 'area' from Shape", "add fn area() -> float"},
		{"mismatched method", "interface Shape {\n    fn area() -> float\n}\nstruct C: Shape {\n    fn area() -> int => 1\n}", "", "5:8", "'area' doesn't match Shape: expected fn() -> float, found fn() -> int", ""},
		{"static implementation", "interface Shape {\n    fn area() -> float\n}\nstruct C: Shape {\n    static fn area() -> float => 1.0\n}", "", "5:15", "'area' can't be static, since it implements Shape", "remove static"},
		{"unknown interface", "interface Shape {\n    fn area() -> float\n}\nstruct C: Shap {\n}", "", "4:11", "unknown interface 'Shap'", "did you mean 'Shape'?"},
		{"struct as interface", "struct D {\n}\nstruct C: D {\n}", "", "3:11", "'D' is a struct, not an interface", "a struct can only implement interfaces"},
		{"undeclared conformance", "interface Shape {\n    fn area() -> float\n}\nstruct C {\n    fn area() -> float => 1.0\n}", "let s: Shape = C()", "8:16", "expected Shape, found C", "declare it: struct C: Shape"},
		{"unknown interface method", "interface Shape {\n    fn area() -> float\n}\n", "let s: [Shape] = []\nprintln(s[0].perimeter())", "7:14", "Shape has no method 'perimeter'", ""},
		{"unconstrained method call", "interface Shape {\n    fn area() -> float\n}\nfn f<T>(x: T) -> float => x.area()", "", "4:29", "T has no method 'area'", "require it: <T: Shape>"},
		{"unmet interface constraint", "interface Shape {\n    fn area() -> float\n}\nstruct C {\n}\nfn f<T: Shape>(x: T) {\n}", "f(C())", "9:1", "f needs T to implement Shape, but C doesn't", "declare it: struct C: Shape"},
		{"interface equality", "interface Shape {\n    fn area() -> float\n}\n", "let a: [Shape] = []\nprintln(a[0] == a[0])", "7:9", "values of type Shape can't be compared with ==", ""},
		{"interface name taken", "interface Shape {\n    fn area() -> float\n}\ninterface Shape {\n}", "", "4:11", "'Shape' is already declared as an interface on line 1", ""},
		{"conflicting defaults", "interface A {\n    fn n() -> int => 1\n}\ninterface B {\n    fn n() -> int => 2\n}\nstruct C: A, B {\n}", "", "7:8", "C gets a default 'n' from both A and B", "write its own fn n(...) to choose"},
		{"creating an interface", "interface Shape {\n    fn area() -> float\n}\n", "let s = Shape()", "6:9", "Shape is an interface, so it can't be created", "create a struct that implements it: struct Name: Shape"},
		{"interface listed twice", "interface Shape {\n    fn area() -> float\n}\nstruct C: Shape, Shape {\n    fn area() -> float => 1.0\n}", "", "4:18", "Shape is listed twice", ""},

		// Generics.
		{"unordered type parameter", "fn big<T>(a: T, b: T) -> bool => a > b", "", "1:34", "values of type T can't be ordered with >", "require it: <T: Comparable>"},
		{"type parameter equality", "fn eq<T>(a: T, b: T) -> bool => a == b", "", "1:33", "values of type T can't be compared with ==", "require it: <T: Equatable>"},
		{"type parameter arithmetic", "fn add<T>(a: T) -> T => a + 1", "", "1:25", "can't use '+' with T and int", ""},
		{"unmet constraint", "fn max<T: Comparable>(a: T, b: T) -> T => a", "println(max([1], [2]))", "3:9", "max needs T to be Comparable, but [int] isn't", "Comparable types are int, float, string and type parameters that require Comparable"},
		{"unmet constraint in type", "struct Set<T: Hashable> {\n    items: [T: bool]\n}", "let s: Set<float>? = nil", "5:12", "Set needs T to be Hashable, but float isn't", "Hashable types are int, string, bool and enums whose cases carry no values"},
		{"type argument not inferred", "fn empty<T>() -> [T] => []", "let e = empty()", "3:9", "can't tell the type of T in this call to empty", "write it: empty<int>(...)"},
		{"wrong type argument count", "fn id<T>(x: T) -> T => x", "println(id<int, int>(1))", "3:12", "id takes 1 type argument, found 2", ""},
		{"generic type without arguments", "struct Box<T> {\n    v: T\n}", "let b: Box? = nil", "5:8", "Box is generic, so it needs type arguments", "write its type arguments, e.g. Box<int>"},
		{"type arguments on plain type", "struct P {\n    v: int\n}", "let p: P<int>? = nil", "5:8", "'P' is not generic, so it takes no type arguments", ""},
		{"type arguments on plain function", "fn f(x: int) {\n}", "f<int>(1)", "4:3", "'f' is not generic, so it takes no type arguments", "remove the <...>"},
		{"unknown constraint", "fn f<T: Sortable>(x: T) {\n}", "", "1:9", "unknown constraint 'Sortable'", "use an interface, or Equatable (==), Comparable (< >) or Hashable (map keys)"},
		{"misspelled constraint", "fn f<T: Comparble>(x: T) {\n}", "", "1:9", "unknown constraint 'Comparble'", "did you mean 'Comparable'?"},
		{"generic function as value", "fn id<T>(x: T) -> T => x", "let g = id", "3:9", "generic function 'id' can't be used as a value", "wrap it in a closure, e.g. fn(x: int) => id(x)"},
		{"method type parameters", "struct S {\n    fn m<T>(x: T) {\n    }\n}", "", "2:10", "methods can't have their own type parameters yet", "make it a top-level generic function"},
		{"growing instances", "fn f<T>(x: T) {\n    f([x])\n}", "f(1)", "2:5", "generic 'f' is used with types nested too deeply", "make sure generic code doesn't call itself with ever larger types, such as [T], then [[T]]"},
		{"duplicate type parameter", "fn f<T, T>(x: T) {\n}", "", "1:9", "type parameter 'T' is declared twice", ""},
		{"generic name taken", "struct Box<T> {\n    v: T\n}\nfn Box() {\n}", "", "4:4", "'Box' is already declared as a struct on line 1", ""},
		{"generic struct contains itself", "struct Node<T> {\n    next: Node<T>\n}", "", "2:11", "struct 'Node<T>' contains itself through field 'next', so it could never be created", "use a list instead, e.g. children: [Node<T>]"},
		{"instance body type error", "fn f<T>(x: T) {\n    let y: int = x\n}", "f(1)", "2:18", "expected int, found T", ""},

		// Maps.
		{"float key", "", "let m: [float: int] = [:]", "2:9", "float can't be a map key", "use int, string, bool or an enum whose cases carry no values"},
		{"optional value", "", "let m: [string: int?] = [:]", "2:17", "map values can't be optional, since a missing key already reads as nil", "remove the key instead of storing nil"},
		{"empty map without type", "", "let m = [:]", "2:9", "can't tell the type of an empty map", "give the type, e.g. let m: [string: int] = [:]"},
		{"compound assign to entry", "", "var m = [\"a\": 1]\nm[\"a\"] += 1", "3:1", "the key may be missing, so '+=' can't be used on a map entry", "write m[k] = (m[k] ?? 0) + 1"},
		{"repeated literal key", "", "let m = [\"a\": 1, \"a\": 2]", "2:18", "key \"a\" is already in this map on line 2", ""},
		{"lookup is optional", "", "let m = [\"a\": 1]\nlet x: int = m[\"a\"]", "3:14", "expected int, found int?", "it may be nil: unwrap it with if let, or give a default with ?? value"},
		{"nil map entry", "", "var m = [\"a\": 1]\nm[\"a\"] = nil", "3:10", "a map entry can't be set to nil", "remove the key instead: m.remove(key)"},
		{"map get", "", "let m = [\"a\": 1]\nm.get(\"a\")", "3:3", "[string: int] has no method 'get'", "read a key with m[key], which gives nil when it is missing"},
		{"keys called", "", "let m = [\"a\": 1]\nprintln(m.keys())", "3:11", "keys is a property, not a method", "write it without (): .keys"},
		{"wrong key type", "", "let m = [\"a\": 1]\nprintln(m[1])", "3:11", "map key: expected string, found int", `convert it with str(...) or "{...}"`},

		// Errors.
		{"call without try", "fn f() throws -> int { return 1 }", "println(f())", "3:9", "f can throw, so the call needs try", "write try before it to pass the error up, try ... catch to handle it, or try? to get nil"},
		{"try in a function that doesn't throw", "fn f() throws -> int { return 1 }", "println(try f())", "3:9", "the error can't pass up: main doesn't throw", "handle it with catch, use try?, or declare fn main(...) throws"},
		{"nothing to try", "", "let x = try 5", "2:9", "nothing after try can throw", "remove try"},
		{"throw outside throws", "", `throw Error("x")`, "2:1", "throw can only be used in a function that throws", "declare fn main(...) throws"},
		{"wrong enum passes up", "enum E { a }\nenum F { b }\nfn f() throws(E) { throw .a }\nfn g() throws(F) {\n    try f()\n}", "", "5:5", "this call throws E, but g can only throw F", "catch it here"},
		{"enum thrown as Error", "enum E { a }\nfn f() throws { throw E.a }", "", "2:23", "f throws Error, not E", `declare it throws(E), or throw Error("...")`},
		{"catch without value", "fn f() throws -> int { return 1 }", "let x = try f() catch e { println(e.message) }", "3:46", "the catch block must give a value of type int", "end it with an int value, or with return, break, continue, throw or panic(...)"},
		{"try? without value", "fn f() throws {}", "let x = try? f()", "3:9", "try? needs a call that returns a value", "use try ... catch { } to ignore the error"},
		{"throws a struct", "struct S {}\nfn f() throws(S) {}", "", "2:15", "throws(...) takes an enum, found struct S", "declare an enum of the errors, e.g. enum LoadError { notFound, denied }"},
		{"struct named Error", "struct Error {}", "", "1:8", "'Error' is the built-in error type and can't be used as a struct name", "choose another name"},
		{"closure that doesn't throw", "fn f() throws -> int { return 1 }", "let g = fn() -> int { return try f() }", "3:30", "the error can't pass up: the closure doesn't throw", "handle it with catch, use try?, or declare the closure fn(...) throws"},
		{"throwing function where none is expected", "fn f() throws -> int { return 1 }\nfn h(cb: fn() -> int) {}", "h(f)", "4:3", "argument 'cb': expected fn() -> int, found fn() throws -> int", ""},

		// Wrapping operators.
		{"wrapping float", "", "let x = 1.5 &+ 2.0", "2:9", "'&+' works only on int, found float and float", ""},
		{"wrapping string", "", `let x = "a" &* 2`, "2:9", "'&*' works only on int, found string and int", ""},
		{"wrapping assign float", "", "var f = 1.0\nf &-= 2.0", "3:1", "&-= can't be used with float", ""},

		// Names and scopes.
		{"unknown name", "", "let count = 1\nprintln(cout)", "3:9", "unknown name 'cout'", "did you mean 'count'?"},
		{"unknown function", "fn greet() {}", "gret()", "3:1", "unknown function 'gret'", "did you mean 'greet'?"},
		{"redeclared", "", "let a = 1\nlet a = 2", "3:5", "'a' is already declared on line 2", "choose another name, or assign to it without let/var"},
		{"out of scope", "", "if true {\n let x = 1\n}\nprintln(x)", "5:9", "unknown name 'x'", ""},
		{"builtin name", "", "let str = 1", "2:5", "'str' is a built-in function and can't be used as a variable name", "choose another name"},
		{"type as value", "", "let t = int", "2:9", "'int' is a type, not a value", ""},
		{"struct as value", "struct P {}", "let p = P", "3:9", "'P' is a type, not a value", "create one with P(...)"},
		{"unknown type", "struct Point {}", "let p: Piont = Point()", "3:8", "unknown type 'Piont'", "did you mean 'Point'?"},
		{"void type", "fn f() -> void {}", "", "1:11", "'void' is not a type", "leave out the return type for functions that return nothing"},
		{"duplicate function", "fn f() {}\nfn f() {}", "", "2:4", "function 'f' is already declared on line 1", ""},
		{"no main", "", "", "", "", ""},

		// let and var.
		{"assign to let", "", "let n = 1\nn = 2", "3:1", "can't change 'n': it's declared with let", "declare it with var n = ... to make it changeable"},
		{"assign to param", "fn f(n: int) { n += 1 }", "", "1:16", "can't change parameter 'n'", "copy it first: var myN = n"},
		{"assign to loop var", "", "for i in 0..3 { i = 0 }", "2:17", "can't change loop variable 'i'", ""},
		{"assign to len", "", "var xs = [1]\nxs.len = 0", "3:4", "len can't be changed", ""},
		{"assign wrong type", "", "var n = 1\nn = \"two\"", "3:5", "expected int, found string", "parse it with int(...)"},
		{"compound on bool", "", "var b = true\nb += true", "3:1", "+= can't be used with bool", ""},

		// Inference.
		{"empty list", "", "let xs = []", "2:10", "can't tell the type of an empty list", "give the type, e.g. let xs: [int] = []"},
		{"mixed list", "", `let xs = [1, "two"]`, "2:14", "list item 2: expected int, found string", "parse it with int(...)"},
		{"annotation mismatch", "", "let x: int = 2.5", "2:14", "expected int, found float", "convert it with int(...), which drops the fraction"},
		{"void value", "fn f() {}", "let x = f()", "3:9", "this expression has no value to store", ""},

		// Operators.
		{"int plus string", "", "let n = 3\nlet s = n + \" items\"", "3:9", "can't use '+' with int and string", `put the value inside the string instead: "{n} items"`},
		{"string plus call", "", "let s = \"n: \" + str(1).len", "2:9", "can't use '+' with string and int", `put the value inside the string instead, e.g. "{count} items"`},
		{"int times float", "", "let a = 2\nlet b = 1.5\nlet c = a * b", "4:9", "can't use '*' with int and float", "convert the int with float(...)"},
		{"int too big", "", "let n = 9223372036854775808", "2:9", "number 9223372036854775808 is too big for int", "the largest int is 9223372036854775807"},
		{"modulo float", "", "let x = 5.5 % 2.0", "2:9", "'%' can't be used with float", ""},
		{"compare types", "", "let b = 1 == \"1\"", "2:9", "can't compare int with string", "parse it with int(...)"},
		{"compare lists", "", "let b = [1] == [1]", "2:9", "values of type [int] can't be compared with ==", ""},
		{"and on int", "", "let b = 1 && true", "2:9", "'&&' needs bool on both sides, found int", ""},
		{"not on int", "", "let b = !1", "2:10", "'!' needs a bool, found int", ""},
		{"if int", "", "let n = 1\nif n { }", "3:4", "if condition must be bool, found int", "compare it, e.g. x != 0"},
		{"while list", "", "let xs = [1]\nwhile xs { }", "3:7", "while condition must be bool, found [int]", "check its length, e.g. xs.len > 0"},
		{"index string", "", "let xs = [1]\nlet y = xs[\"a\"]", "3:12", "index must be int, found string", "parse it with int(...)"},
		{"index int", "", "let n = 1\nlet y = n[0]", "3:9", "can't index a value of type int", ""},

		// Loops and returns.
		{"loop over int", "", "for x in 5 { }", "2:10", "can't loop over a value of type int", "loop over a list, a map or a range like 0..10"},
		{"float range", "", "for x in 0..2.5 { }", "2:13", "range bounds must be int, found float", "convert it with int(...), which drops the fraction"},
		{"break outside loop", "", "break", "2:1", "break can only be used inside a loop", ""},
		{"return value from void", "fn f() { return 1 }", "", "1:17", "f doesn't return a value", "declare the result type: fn f(...) -> int"},
		{"bare return", "fn f() -> int { return }", "", "1:17", "missing return value: f must return int", "return a value, e.g. return 0"},
		{"wrong return type", "fn f() -> string => 1", "", "1:21", "result of f: expected string, found int", `convert it with str(...) or "{...}"`},
		{"unused value", "", "let a = 1\na + 1", "3:1", "this value isn't used", "store it with let, or remove it"},
		{"main params", "", "", "", "", ""},

		// Arguments.
		{"missing argument", "fn add(a: int, b: int) -> int => a + b", "add(1)", "3:1", "missing argument 'b' in call to add", "e.g. add(b: ...)"},
		{"missing arguments", "fn add(a: int, b: int) -> int => a + b", "add()", "3:1", "missing arguments 'a' and 'b' in call to add", "e.g. add(a: ...)"},
		{"too many", "fn one(a: int) {}", "one(1, 2)", "3:8", "too many arguments: one takes 1 argument", ""},
		{"unknown named", "fn greet(name: string) {}", `greet(nmae: "x")`, "3:7", "greet has no parameter 'nmae'", "did you mean 'name'?"},
		{"duplicate named", "fn greet(name: string) {}", `greet("a", name: "b")`, "3:12", "argument 'name' is given twice", ""},
		{"positional after named", "fn f(a: int, b: int) {}", "f(a: 1, 2)", "3:9", "unnamed argument after a named one", "put unnamed arguments first"},
		{"argument type", "fn f(a: float) {}", "f(\"x\")", "3:3", "argument 'a': expected float, found string", "parse it with float(...)"},
		{"bad default", "fn f(a: int = \"x\") {}", "", "1:15", "default of 'a': expected int, found string", "parse it with int(...)"},
		{"call a variable", "", "let f = 1\nf()", "3:1", "'f' is an int, not a function", ""},
		{"trailing block", "fn f() {}", "f() { }", "3:5", "f takes no arguments, so it can't take a trailing block", ""},

		// Structs.
		{"missing field", userDecl, "let u = User(age: 3)", "18:9", "missing argument 'name' in call to User", "e.g. User(name: ...)"},
		{"unknown field", userDecl, `let u = User(name: "a")` + "\nprintln(u.nme)", "19:11", "User has no field 'nme'", "did you mean 'name'?"},
		{"unknown method", userDecl, `User(name: "a").gret()`, "18:17", "User has no method 'gret'", "did you mean 'greet'?"},
		{"method without call", userDecl, `let g = User(name: "a").greet`, "18:25", "method 'greet' must be called", "call it: greet()"},
		{"instance method on type", userDecl, "User.greet()", "18:6", "'greet' needs a User value", "call it on a value, e.g. user.greet(), or declare it static fn"},
		{"static method on value", userDecl, `User(name: "a").guest()`, "18:17", "'guest' is a static method", "call it on the type: User.guest()"},
		{"field in static", "struct S {\n n: int\n static fn f() -> int => n\n}", "", "3:26", "static method f can't use field 'n'", "static methods have no instance; pass the value in as a parameter"},
		{"self in function", "fn f() { println(self) }", "", "1:18", "'self' can only be used inside a method", ""},
		{"static method value", "struct S {\n static fn make() -> S => S()\n}", "let f = S.make", "5:11", "method 'make' must be called", "call it: S.make()"},
		{"self cycle", "struct Node {\n next: Node\n}", "", "2:8", "struct 'Node' contains itself through field 'next', so it could never be created", "use a list instead, e.g. children: [Node]"},
		{"duplicate field", "struct S {\n a: int\n a: int\n}", "", "3:2", "field 'a' is already declared on line 2", ""},

		// Closures.
		{"untyped param", "", "let f = fn(x) => x", "2:12", "can't tell the type of parameter 'x'", "write its type: fn(x: int) ..."},
		{"closure arity", "fn apply(f: fn(int) -> int) {}", "apply(fn(a, b) => a)", "3:7", "expected a function with 1 parameter, found one with 2", ""},
		{"closure result", "fn apply(f: fn(int) -> int) {}", `apply(fn(x) => "s")`, "3:16", "closure result: expected int, found string", "parse it with int(...)"},
		{"closure type", "", "let f: fn(int) -> int = fn(x: string) => 1", "2:25", "expected fn(int) -> int, found fn(string) -> int", ""},
		{"void closure returns", "", "let f = fn() {\n    let g: fn() = fn() { return 1 }\n}", "3:33", "this closure doesn't return a value", "declare the result type: fn(...) -> int { ... }"},
		{"closure missing return", "", "let f = fn(x: int) -> int {\n    if x > 0 { return 1 }\n}", "4:1", "missing return: the closure must return int", "add a return statement before the closing }"},
		{"break in closure", "", "for i in 0..3 {\n    let f = fn() { break }\n}", "3:20", "break can only be used inside a loop", ""},
		{"call non-function", "", "let n = 1\nlet x = [n][0]()", "3:9", "a value of type int can't be called", ""},
		{"value call arity", "", "let f = fn(x: int) => x\nf()", "3:1", "expected 1 argument for fn(int) -> int, found 0", ""},
		{"value call named", "", "let f = fn(x: int) => x\nf(x: 1)", "3:3", "function values don't have parameter names", "pass the arguments in order"},
		{"value call type", "", "let f = fn(x: int) => x\nf(\"a\")", "3:3", "argument 1: expected int, found string", "parse it with int(...)"},
		{"trailing needs fn", "fn f(n: int) {}", "f() { }", "3:5", "a trailing block needs the last parameter of f to be fn() or a list, but 'n' is int", ""},
		{"element type", "", `let a = [for x in [1] x, "s"]`, "2:26", "list element: expected int, found string", "parse it with int(...)"},
		{"element cond", "", "let a = [if 1 2]", "2:13", "if condition must be bool, found int", "compare it, e.g. x != 0"},
		{"spread int", "", "let a = [...5]", "2:13", "'...' needs a list or a map, found int", ""},
		{"spread list into map", "", `let m = ["a": 1, ...[2]]`, "2:21", "can't add the elements of a list to a map", ""},
		{"spread map into list", "", `let a = [1, ...["a": 1]]`, "2:16", "can't add the entries of a map to a list", "add its keys or values: ...m.keys"},
		{"spread wrong elem", "", `let a = [1, ..."x".split(",")]`, "2:16", "can't add elements of type string where int is expected", ""},
		{"spread nil", "", "let o: [int]? = nil\nlet a = [...o]", "3:13", "this [int] may be nil", "write ...? to add nothing when it is nil"},
		{"spread needs optional", "", "let a = [...?[1]]", "2:10", "'...?' needs an optional, found [int]", "write ... for a value that can't be nil"},
		{"spread in block", "", "let xs = [1]\n...xs", "3:1", "'...' can only be used in a list or map literal", "use it inside a list or map literal, e.g. [...xs, 4]"},
		{"element no value", "", `let a = [if true println("x")]`, "2:18", "this expression has no value to add", "each line of a builder block adds its value; move calls without one out of the block"},
		{"untyped element list", "", "let a = [if true nil]", "2:18", "can't tell the type of nil", "write the type, e.g. let user: User? = nil"},
		{"builder statement", "fn f(xs: [int]) {}", "f {\n    1\n    return\n}", "5:5", "a builder block can only hold elements, let, var, if and for", "move it out of the block, or into a closure"},
		{"builder void call", "fn f(xs: [int]) {}", "f {\n    println(1)\n}", "4:5", "this expression has no value to add", "each line of a builder block adds its value; move calls without one out of the block"},
		{"builder element type", "fn f(xs: [int]) {}", "f {\n    \"a\"\n}", "4:5", "list element: expected int, found string", "parse it with int(...)"},
		{"trailing twice", "fn f(cb: fn()) {}", "f(fn() {}) { }", "3:12", "the last argument of f is given twice: in the call and as a trailing block", ""},
		{"trailing on builtin", "", "println(1) { }", "2:12", "println doesn't take a trailing block", ""},
		{"map void", "", "let r = [1].map(fn(x) { println(x) })", "2:17", "map needs a function that returns a value", ""},
		{"filter type", "", "let r = [1].filter(fn(x) => x)", "2:29", "closure result: expected bool, found int", ""},
		{"compare closures", "", "let f = fn() {}\nlet b = f == f", "3:9", "values of type fn() can't be compared with ==", ""},

		// Optionals.
		{"guard break is not a return", "fn f() -> int {\n    while true {\n        guard let x = g() else { break }\n    }\n}\nfn g() -> int? => nil", "", "5:1", "missing return: f must return int", "add a return statement before the closing }"},
		{"optional field read", "struct U {\n    n: int\n}", "let u: U? = nil\nlet x = u.n + 1", "6:11", "can't read 'n' from a U?: it may be nil", "use ?.n, or unwrap it first with if let"},
		{"optional compare value", "", "let s: string? = nil\nlet b = s == \"x\"", "3:9", "can't compare string? with string", ""},
		{"optional arithmetic", "", "let n: int? = 1\nlet m = n + 1", "3:9", "can't use '+' with int? and int", ""},

		{"list of only nil", "", "let xs = [nil, nil]", "2:11", "can't tell the type of nil", "write the type, e.g. let user: User? = nil"},

		// Enums and match.
		{"unknown case", shapeDecl, "let s = Shape.circel(radius: 1)", "22:15", "Shape has no case 'circel'", "did you mean 'circle'?"},
		{"case needs values", shapeDecl, "let s = Shape.circle", "22:15", "case 'circle' carries values, so it must be called", "give its values: Shape.circle(radius: ...)"},
		{"case without values called", shapeDecl, "let s = Shape.empty()", "22:15", "case 'empty' carries no values", "write it without (): Shape.empty"},
		{"enum as value", shapeDecl, "let s = Shape", "22:9", "'Shape' is a type, not a value", "use one of its cases, e.g. Shape.circle"},
		{"enum called", shapeDecl, "let s = Shape()", "22:9", "enums are created from one of their cases", "create it from a case, e.g. Shape.circle"},
		{"implicit without type", shapeDecl, "let s = .empty", "22:9", "can't tell which enum '.empty' belongs to", "write the enum name, e.g. Shape.empty"},
		{"implicit wrong type", "", "let n: int = .zero", "2:14", "'.zero' needs an enum here, but int is expected", ""},
		{"read case value", shapeDecl, "let s = Shape.circle(radius: 1)\nprintln(s.radius)", "23:11", "Shape has no member 'radius'", "read the values a case carries with match, e.g. match v { circle(r) => r, ... }"},
		{"missing case", shapeDecl, "let s = Shape.empty\nmatch s {\n    empty => println(0)\n}", "23:1", "match doesn't handle 'circle' and 'rect'", "add the missing arms, or _ => ..."},
		{"missing one case", shapeDecl, "let s = Shape.empty\nmatch s {\n    empty => println(0)\n    rect(w, h) => println(w)\n}", "23:1", "match doesn't handle 'circle'", "add circle => ..., or _ => ..."},
		{"duplicate arm", shapeDecl, "let s = Shape.empty\nmatch s {\n    empty => println(0)\n    empty => println(1)\n    _ => println(2)\n}", "25:5", "case 'empty' is already handled on line 24", "remove one of the arms"},
		{"unreachable arm", shapeDecl, "let s = Shape.empty\nmatch s {\n    _ => println(0)\n    empty => println(1)\n}", "25:5", "this arm is never reached: _ on line 24 matches everything", "remove it, or move _ to the end"},
		{"wrong bind count", shapeDecl, "let s = Shape.empty\nmatch s {\n    rect(w) => println(w)\n    _ => println(0)\n}", "24:5", "case 'rect' carries 2 values, found 1", "write rect(width, height), using _ for values you don't need"},
		{"int needs wildcard", "", "match 3 {\n    1 => println(1)\n}", "2:1", "a match on int needs a _ arm for the other values", "add _ => ... as the last arm"},
		{"bool missing", "", "match true {\n    true => println(1)\n}", "2:1", "match doesn't handle 'false'", "add false => ..., or _ => ..."},
		{"arm types", "", "let x = match 1 {\n    1 => \"one\"\n    _ => 2\n}", "4:10", "match arms must have the same type: expected string, found int", `convert it with str(...) or "{...}"`},
		{"block in value match", "", "let x = match 1 {\n    _ => { println(1) }\n}", "3:10", "a match used as a value needs an expression after =>", "use an expression, or use the match as a statement"},
		{"match float", "", "match 1.5 {\n    _ => println(1)\n}", "2:7", "can't match on float values", "compare with if ... else instead"},
		{"match list", "", "match [1] {\n    _ => println(1)\n}", "2:7", "can't match on a value of type [int]", "use if ... else instead"},
		{"case on int", "", "match 1 {\n    one => println(1)\n    _ => println(2)\n}", "3:5", "'one' isn't a case: the match is on int", "match literal values such as 1 or \"yes\", or _"},
		{"literal type", "", "match 1 {\n    \"a\" => println(1)\n    _ => println(2)\n}", "3:5", "pattern: expected int, found string", "parse it with int(...)"},
		{"change binding", shapeDecl, "let s = Shape.empty\nmatch s {\n    circle(r) => { r = 1.0 }\n    _ => println(0)\n}", "24:20", "can't change 'r': names bound by a match pattern can't be changed", "copy it first: var myR = r"},
		{"compare data enums", shapeDecl, "let b = Shape.empty == Shape.empty", "22:9", "values of type Shape can't be compared with ==", ""},
		{"empty enum", "enum E {}", "", "1:6", "enum 'E' has no cases", "add cases, e.g. enum E { first, second }"},
		{"duplicate case", "enum E {\n a\n a\n}", "", "3:2", "case 'a' is already declared on line 2", ""},

		// Built-ins.
		{"println void", "fn f() {}", "println(f())", "3:9", "this expression has no value to print", ""},
		{"str arity", "", "let s = str()", "2:9", "str takes 1 argument, found 0", "e.g. str(x)"},
		{"int of bool", "", "let n = int(true)", "2:13", "can't convert bool to int", ""},
		{"assert type", "", "assert(1)", "2:8", "argument 'condition': expected bool, found int", ""},
		{"push type", "", "var xs = [1]\nxs.push(\"a\")", "3:9", "argument 'item': expected int, found string", "parse it with int(...)"},
		{"len call", "", "let n = \"ab\".len()", "2:14", "len is a property, not a method", "write it without (): .len"},
		{"unknown string method", "", `let s = "a".uper()`, "2:13", "string has no method 'uper'", "did you mean 'upper'?"},
	}
	for _, c := range cases {
		src := "fn main() {\n" + c.body + "\n}\n"
		if c.decls != "" {
			src = c.decls + "\n" + src
		}
		switch c.name {
		case "no main":
			src, c.pos, c.msg, c.hint = "fn start() {}", "1:1", "no main function", "add fn main() { ... } where the program starts"
		case "main params":
			src, c.pos, c.msg = "fn main(n: int) {}", "1:4", "main can't take parameters"
		}
		_, errs := check(t, src)
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

func TestOneMistakeGivesOneError(t *testing.T) {
	_, errs := checkMain(t, "", "let a = unknown\nlet b = a + 1\nlet c = b * a\nprintln(c.len, a.foo())")
	if len(errs) != 1 {
		t.Errorf("got %d errors, want 1:\n%v", len(errs), errs)
	}
}

func TestTourChecks(t *testing.T) {
	src, err := os.ReadFile("../parser/testdata/tour.vx")
	if err != nil {
		t.Fatal(err)
	}
	if _, errs := check(t, string(src)); errs != nil {
		t.Fatalf("tour.vx has type errors:\n%v", errs)
	}
}

func TestGenericErrorsReportedOnce(t *testing.T) {
	_, errs := check(t, "fn f<T>(x: T) -> int => x + 1\nfn main() {\n    f(1)\n    f(\"a\")\n    f([true])\n}\n")
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
	_, errs = check(t, "fn main<T>() {\n}\n")
	if len(errs) != 1 || errs[0].Message != "main can't be generic" {
		t.Fatalf("got %v, want main can't be generic", errs)
	}
}
