package types

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vex-language/vex-lang/compiler/diag"
	"github.com/vex-language/vex-lang/compiler/modules"
)

// Checks the project in [files] and returns its warnings and errors as
// `file:line:col: message` lines, each list in the order it is shown.
func checkProject(t *testing.T, files map[string]string) (warnings, errs string) {
	t.Helper()
	fsys := fstest.MapFS{"app/package.toml": {Data: []byte("[package]\nname = \"app\"\n")}}
	for name, src := range files {
		fsys["app/"+name] = &fstest.MapFile{Data: []byte(src)}
	}
	prog, err := modules.LoadFS(fsys, "app", "app", true)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	info, err := CheckProgram(prog)
	lines := func(l diag.List) string {
		l.Sort()
		var out []string
		for _, d := range l {
			out = append(out, fmt.Sprintf("%s:%s: %s", d.File.Name, d.Pos, d.Message))
		}
		return strings.Join(out, "\n")
	}
	var list diag.List
	if err != nil && !errors.As(err, &list) {
		t.Fatal(err)
	}
	return lines(info.Warnings), lines(list)
}

func TestUnusedImports(t *testing.T) {
	lib := "pub struct User {\n    pub name: string\n}\n\npub fn hello() -> string => \"hi\"\n"
	ext := "pub extension string {\n    fn shout() -> string => upper()\n}\n"
	tests := []struct {
		main, want string
	}{
		{"import \"vex:math\"\n\nfn main() {}\n", "app/main.vx:1:1: 'vex:math' is imported but never used"},
		{"import \"vex:math\"\n\nfn main() {\n    println(math.abs(-1))\n}\n", ""},
		{"import \"vex:math\" as m\n\nfn main() {}\n", "app/main.vx:1:1: 'vex:math' is imported but never used"},
		{"import \"vex:math\" as m\n\nfn main() {\n    println(m.abs(-1))\n}\n", ""},
		{"import \"lib\"\n\nfn greet(u: lib.User) {}\n\nfn main() {}\n", ""},
		{"import \"lib\"\n\nfn main() {\n    println(\"{lib.hello()}\")\n}\n", ""},
		{"import \"ext\"\n\nfn main() {\n    println(\"a\".shout())\n}\n", ""},
		{"import \"ext\"\nimport \"lib\"\n\nfn main() {}\n",
			"app/main.vx:1:1: 'ext' is imported but never used\napp/main.vx:2:1: 'lib' is imported but never used"},
		{"for x in [1] {}\n", ""},
	}
	for _, tt := range tests {
		main := tt.main
		if !strings.Contains(main, "fn main") {
			main = "fn main() {\n    " + main + "}\n"
		}
		got, errs := checkProject(t, map[string]string{"main.vx": main, "lib/lib.vx": lib, "ext/ext.vx": ext})
		if errs != "" {
			t.Errorf("%q: errors:\n%s", tt.main, errs)
			continue
		}
		if got != tt.want {
			t.Errorf("%q:\n got %q\nwant %q", tt.main, got, tt.want)
		}
	}
}

func TestUnusedImportsInModules(t *testing.T) {
	got, errs := checkProject(t, map[string]string{
		"main.vx":    "import \"lib\"\n\nfn main() {\n    lib.hello()\n}\n",
		"lib/lib.vx": "import \"vex:strings\"\n\npub fn hello() {}\n",
	})
	if errs != "" {
		t.Fatal(errs)
	}
	if want := "app/lib/lib.vx:1:1: 'vex:strings' is imported but never used"; got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestExposedPrivateTypes(t *testing.T) {
	tests := []struct{ src, want string }{
		{"struct S {}\npub fn f() -> S => S()", "2:15: pub function 'f' exposes private struct 'S'"},
		{"struct S {}\npub fn f(s: [S?]) {}", "2:14: pub function 'f' exposes private struct 'S'"},
		{"struct S {}\nfn f() -> S => S()", ""},
		{"pub struct S {}\npub fn f() -> S => S()", ""},
		{"enum E { a }\npub struct P {\n    pub e: E\n    f: E = E.a\n}", "3:12: pub field 'e' of P exposes private enum 'E'"},
		{"enum E { a }\nstruct P {\n    pub e: E\n}", ""},
		{"struct S {}\npub struct P {\n    pub fn get() -> S => S()\n    fn hidden() -> S => S()\n}", "3:21: pub method 'get' of P exposes private struct 'S'"},
		{"struct S {}\npub enum E {\n    a(s: S)\n}", "3:10: case 'a' of pub enum E exposes private struct 'S'"},
		{"interface I {}\npub interface J: I {}", "2:18: pub interface 'J' exposes private interface 'I'"},
		{"struct S {}\npub interface J {\n    fn make() -> S\n}", "3:18: method 'make' of pub interface J exposes private struct 'S'"},
		{"struct S {}\npub typedef T = (S, int)", "2:18: pub typedef 'T' exposes private struct 'S'"},
		{"typedef T = int\npub fn f(t: T) {}", "2:13: pub function 'f' exposes private typedef 'T'"},
		{"struct Box<T> {}\npub fn f<T>(x: T) -> T => x", ""},
		{"struct S {}\npub fn f(g: fn(S) -> int) {}", "2:16: pub function 'f' exposes private struct 'S'"},
		{"struct S {}\npub extension string {\n    fn s() -> S => S()\n}", "3:15: pub method 's' of the extension exposes private struct 'S'"},
		{"struct S {}\nextension S {\n    pub fn me() -> S => self\n}", ""},
	}
	for _, tt := range tests {
		_, errs := check(t, tt.src+"\n\nfn main() {}\n")
		var got []string
		for _, d := range errs {
			got = append(got, d.Error())
		}
		sort.Strings(got)
		if g := strings.Join(got, "\n"); g != tt.want {
			t.Errorf("%q:\n got %q\nwant %q", tt.src, g, tt.want)
		}
	}
}

func TestErrorsSortedByModule(t *testing.T) {
	_, errs := checkProject(t, map[string]string{
		"main.vx":    "import \"b\"\nimport \"a\"\n\nfn main() {\n    a.f()\n    b.f()\n    let x: int = \"no\"\n}\n",
		"zeta.vx":    "fn z() -> int => \"no\"\n",
		"a/a.vx":     "pub fn f() -> int => \"no\"\n",
		"b/b.vx":     "pub fn f() -> int => \"no\"\n",
		"b/extra.vx": "fn g() -> int => \"no\"\n",
	})
	want := strings.Join([]string{
		"app/main.vx:7:18: expected int, found string",
		"app/zeta.vx:1:18: result of z: expected int, found string",
		"app/b/b.vx:1:22: result of f: expected int, found string",
		"app/b/extra.vx:1:18: result of g: expected int, found string",
		"app/a/a.vx:1:22: result of f: expected int, found string",
	}, "\n")
	if errs != want {
		t.Errorf("got\n%s\nwant\n%s", errs, want)
	}
}
