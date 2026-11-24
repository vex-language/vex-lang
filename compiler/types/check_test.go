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

func TestErrors(t *testing.T) {
	cases := []struct {
		name, decls, body string
		pos, msg, hint    string
	}{
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
		{"loop over int", "", "for x in 5 { }", "2:10", "can't loop over a value of type int", "loop over a list or a range like 0..10"},
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
		{"call a variable", "", "let f = 1\nf()", "3:1", "'f' is a int, not a function", ""},
		{"trailing block", "fn f() {}", "f() { }", "3:5", "trailing blocks are reserved for UI components", "pass the values as arguments instead"},

		// Structs.
		{"missing field", userDecl, "let u = User(age: 3)", "18:9", "missing argument 'name' in call to User", "e.g. User(name: ...)"},
		{"unknown field", userDecl, `let u = User(name: "a")` + "\nprintln(u.nme)", "19:11", "User has no field 'nme'", "did you mean 'name'?"},
		{"unknown method", userDecl, `User(name: "a").gret()`, "18:17", "User has no method 'gret'", "did you mean 'greet'?"},
		{"method without call", userDecl, `let g = User(name: "a").greet`, "18:25", "method 'greet' must be called", "call it: greet()"},
		{"instance method on type", userDecl, "User.greet()", "18:6", "'greet' needs a User value", "call it on a value, e.g. user.greet(), or declare it static fn"},
		{"static method on value", userDecl, `User(name: "a").guest()`, "18:17", "'guest' is a static method", "call it on the type: User.guest()"},
		{"field in static", "struct S {\n n: int\n static fn f() -> int => n\n}", "", "3:26", "static method f can't use field 'n'", "static methods have no instance; pass the value in as a parameter"},
		{"self in function", "fn f() { println(self) }", "", "1:18", "'self' can only be used inside a method", ""},
		{"self cycle", "struct Node {\n next: Node\n}", "", "2:8", "struct 'Node' contains itself through field 'next', so it could never be created", "use a list instead, e.g. children: [Node]"},
		{"duplicate field", "struct S {\n a: int\n a: int\n}", "", "3:2", "field 'a' is already declared on line 2", ""},

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
