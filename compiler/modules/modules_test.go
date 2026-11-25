package modules

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/irshadio/vex-lang/compiler/diag"
)

const project = "[package]\nname = \"app\"\n"

// Loads the project in [files] and returns its errors as
// `file:line:col: message` lines.
func loadErrors(t *testing.T, files map[string]string) string {
	t.Helper()
	fsys := fstest.MapFS{}
	for name, src := range files {
		fsys["app/"+name] = &fstest.MapFile{Data: []byte(src)}
	}
	_, err := LoadFS(fsys, "app", "app", true)
	if err == nil {
		return ""
	}
	var list diag.List
	if !errors.As(err, &list) {
		return err.Error()
	}
	list.Sort()
	var out []string
	for _, d := range list {
		name := ""
		if d.File != nil {
			name = d.File.Name + ":"
		}
		out = append(out, fmt.Sprintf("%s%s: %s", name, d.Pos, d.Message))
	}
	return strings.Join(out, "\n")
}

func TestLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"app/package.toml":  {Data: []byte(project)},
		"app/main.vx":       {Data: []byte("import \"models\"\nimport \"vex:math\"\nfn main() {}\n")},
		"app/extra.vx":      {Data: []byte("fn helper() {}\n")},
		"app/models/a.vx":   {Data: []byte("import \"../util\" as u\npub fn f() {}\n")},
		"app/util/util.vx":  {Data: []byte("import \"../models\"\npub fn g() {}\n")},
		"app/notes/todo.md": {Data: []byte("not code")},
	}
	prog, err := LoadFS(fsys, "app", "app", true)
	if err != nil {
		t.Fatal(err)
	}
	if prog.Name != "app" || !prog.Root.Root || len(prog.Root.Files) != 2 {
		t.Fatalf("root: name %q, files %d", prog.Name, len(prog.Root.Files))
	}
	var got []string
	for _, m := range prog.Modules {
		got = append(got, fmt.Sprintf("%s=%s(%d)", m.Path, m.Name, len(m.Files)))
	}
	// The root comes first, then modules in the order they are imported;
	// util and models import each other.
	if want := "=main(2) models=models(1) vex:math=math(1) ../util=util(1)"; strings.Join(got, " ") != want {
		t.Errorf("modules\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	models := prog.Modules[1]
	if imp := models.Imports[models.Files[0]]["u"]; imp == nil || imp.Module != prog.Modules[3] {
		t.Errorf("models doesn't import util as u")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no project file", map[string]string{"main.vx": "fn main() {}"},
			"no package.toml in app; create one with:\n\n[package]\nname = \"app\""},
		{"no source files", map[string]string{"package.toml": project}, "no .vx files in app"},
		{"unknown std module", map[string]string{"package.toml": project, "main.vx": "import \"vex:nope\"\n"},
			"app/main.vx:1:8: unknown module 'vex:nope'"},
		{"package imports", map[string]string{"package.toml": project, "main.vx": "import \"package:ui\"\n"},
			"app/main.vx:1:8: package imports aren't supported yet"},
		{"unknown scheme", map[string]string{"package.toml": project, "main.vx": "import \"http:x\"\n"},
			"app/main.vx:1:8: unknown import scheme in 'http:x'"},
		{"absolute", map[string]string{"package.toml": project, "main.vx": "import \"/etc\"\n"},
			"app/main.vx:1:8: import paths must be relative"},
		{"outside", map[string]string{"package.toml": project, "main.vx": "import \"../other\"\n"},
			"app/main.vx:1:8: '../other' is outside the project"},
		{"own folder", map[string]string{"package.toml": project, "main.vx": "import \".\"\n"},
			"app/main.vx:1:8: a module can't import its own folder"},
		{"missing folder", map[string]string{"package.toml": project, "main.vx": "import \"models\"\n"},
			"app/main.vx:1:8: no folder 'models'"},
		{"empty folder", map[string]string{"package.toml": project, "main.vx": "import \"docs\"\n", "docs/a.md": "x"},
			"app/main.vx:1:8: folder 'docs' has no .vx files"},
		{"keyword name", map[string]string{"package.toml": project, "main.vx": "import \"as\"\n", "as/a.vx": "pub fn f() {}"},
			"app/main.vx:1:8: 'as' can't be used as a name in code"},
		{"bad name", map[string]string{"package.toml": project, "main.vx": "import \"my-lib\"\n", "my-lib/a.vx": "pub fn f() {}"},
			"app/main.vx:1:8: 'my-lib' can't be used as a name in code"},
		{"renamed keyword", map[string]string{"package.toml": project, "main.vx": "import \"as\" as helpers\nfn main() {}", "as/a.vx": "pub fn f() {}"}, ""},
		{"twice", map[string]string{"package.toml": project, "main.vx": "import \"vex:math\"\nimport \"m/math\"\n", "m/math/a.vx": "pub fn f() {}"},
			"app/main.vx:2:8: 'math' is already imported on line 1"},
		{"std imports folders", map[string]string{"package.toml": project, "main.vx": "import \"vex:math\"\nfn main() {}"}, ""},
		{"syntax error in module", map[string]string{"package.toml": project, "main.vx": "import \"m\"\n", "m/a.vx": "fn {"},
			"app/m/a.vx:1:4: expected a function name, found '{'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := loadErrors(t, tt.files); got != tt.want {
				t.Errorf("\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestLoadFile(t *testing.T) {
	fsys := fstest.MapFS{
		"dir/hello.vx":     {Data: []byte("import \"vex:strings\"\nimport \"lib\"\nfn main() {}\n")},
		"dir/lib/lib.vx":   {Data: []byte("pub fn f() {}\n")},
		"dir/package.toml": {Data: []byte("not toml at all")},
	}
	prog, err := LoadFS(fsys, "dir/hello.vx", "dir/hello.vx", false)
	if err != nil {
		t.Fatal(err)
	}
	if prog.Name != "" || len(prog.Modules) != 3 || prog.Root.Files[0].Name != "dir/hello.vx" {
		t.Fatalf("name %q, %d modules, root file %q", prog.Name, len(prog.Modules), prog.Root.Files[0].Name)
	}
	if got := prog.Modules[2].Files[0].Name; got != "dir/lib/lib.vx" {
		t.Errorf("module file shown as %q", got)
	}
}

func TestParseProject(t *testing.T) {
	tests := []struct {
		src, want string
	}{
		{"# comment\n[package]\nname = \"app\" # trailing\nversion = \"1.0\"\nvex = \"0.1\"\n\n[dependencies]\nui = { git = \"x\" }\n", ""},
		{"[package]\r\nname = \"app\"\r\n", ""},
		{"[package]\nversion = \"1\"\n", "1:1: the project file needs a package name"},
		{"[pkg]\nname = \"a\"\n", "1:1: unknown section [pkg]"},
		{"[package\n", "1:1: unterminated section header"},
		{"[package]\nname\n", "2:1: expected a key and a value"},
		{"[package]\nname = app\n", "2:8: the value of 'name' must be a string in quotes"},
		{"[package]\nname = \"pub\"\n", "2:8: 'pub' isn't a valid package name"},
		{"[package]\nname = \"my-app\"\n", "2:8: 'my-app' isn't a valid package name"},
		{"[package]\nname = \"a\"\nname = \"b\"\n", "3:1: 'name' is already set on line 2"},
		{"[package]\n  name = \"a\"\n  colour = \"red\"\n", "3:3: unknown key 'colour' in [package]"},
	}
	for _, tt := range tests {
		f := diag.NewFile("package.toml", tt.src)
		p, errs := ParseProject(f)
		var got []string
		for _, d := range errs {
			got = append(got, d.Error())
		}
		if strings.Join(got, "\n") != tt.want {
			t.Errorf("%q:\n got %q\nwant %q", tt.src, strings.Join(got, "\n"), tt.want)
		}
		if tt.want == "" && p.Name != "app" {
			t.Errorf("%q: name %q", tt.src, p.Name)
		}
	}
}

func TestProjectC(t *testing.T) {
	src := `[package]
name = "app"

[c]
sources = [
    "native/a.c",   # first
    "lib/b.o",
]
libs = ["z", "sqlite3"]
flags = ["-DNAME=\"x, y\"", "-Inative", "-O3", "-Wall", "-Wno-unused", "-fvisibility=hidden", "-mmacosx-version-min=11.0", "-std=c11"]
`
	p, errs := ParseProject(diag.NewFile("package.toml", src))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	got := fmt.Sprintf("%q %q %q", p.CSources, p.CLibs, p.CFlags)
	if want := `["native/a.c" "lib/b.o"] ["z" "sqlite3"] ["-DNAME=\"x, y\"" "-Inative" "-O3" "-Wall" "-Wno-unused" "-fvisibility=hidden" "-mmacosx-version-min=11.0" "-std=c11"]`; got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	prog := &Program{Dir: "/proj", CSources: p.CSources, CLibs: p.CLibs, CFlags: p.CFlags}
	opts := prog.CCOptions()
	got = fmt.Sprintf("%q %q %q", opts.Sources, opts.Libs, opts.Flags)
	if want := `["/proj/native/a.c" "/proj/lib/b.o"] ["z" "sqlite3"] ["-DNAME=\"x, y\"" "-I/proj/native" "-O3" "-Wall" "-Wno-unused" "-fvisibility=hidden" "-mmacosx-version-min=11.0" "-std=c11"]`; got != want {
		t.Errorf("options\n got %s\nwant %s", got, want)
	}

	tests := []struct{ src, want string }{
		{"[c]\nsources = \"a.c\"\n", "2:11: the value of 'sources' must be a list of strings"},
		{"[c]\nsources = [\"../x.c\"]\n", "2:11: '../x.c' is outside the project"},
		{"[c]\nsources = [\"/x.c\"]\n", "2:11: '/x.c' is outside the project"},
		{"[c]\nsources = [\"x.py\"]\n", "2:11: 'x.py' isn't a C source file"},
		{"[c]\nframeworks = [\"-framework UIKit\"]\n", "2:14: '-framework UIKit' isn't a framework name"},
		{"[c]\nlibs = [\"-lz\"]\n", "2:8: '-lz' isn't a library name"},
		{"[c]\nflags = [\"-o\", \"out\"]\n", "2:9: flag '-o' isn't allowed\n2:9: flag 'out' isn't allowed"},
		{"[c]\nflags = [\"-D\"]\n", "2:9: flag '-D' isn't allowed"},
		{"[c]\nflags = [\"-fplugin=/tmp/x.dylib\"]\n", "2:9: flag '-fplugin=/tmp/x.dylib' isn't allowed"},
		{"[c]\nflags = [\"-Wl,-o,/tmp/x\"]\n", "2:9: flag '-Wl,-o,/tmp/x' isn't allowed"},
		{"[c]\nflags = [\"-Wp,-MD,/tmp/x\"]\n", "2:9: flag '-Wp,-MD,/tmp/x' isn't allowed"},
		{"[c]\nflags = [\"-I../../..\"]\n", "2:9: flag '-I../../..' isn't allowed"},
		{"[c]\nflags = [\"-L/usr/lib\"]\n", "2:9: flag '-L/usr/lib' isn't allowed"},
		{"[c]\nflags = [\"-fprofile-generate\"]\n", "2:9: flag '-fprofile-generate' isn't allowed"},
		{"[c]\ncolour = []\n", "2:1: unknown key 'colour' in [c]"},
		{"[c]\nlibs = [\"z\"]\nlibs = []\n", "3:1: 'libs' is already set on line 4"},
		{"[c]\nlibs = [\"z\"\n", "2:8: the value of 'libs' must be a list of strings"},
	}
	for _, tt := range tests {
		_, errs := ParseProject(diag.NewFile("package.toml", "[package]\nname = \"app\"\n"+tt.src))
		var got []string
		for _, d := range errs {
			d.Pos.Line -= 2
			got = append(got, d.Error())
		}
		if strings.Join(got, "\n") != tt.want {
			t.Errorf("%q:\n got %q\nwant %q", tt.src, strings.Join(got, "\n"), tt.want)
		}
	}
}

func TestLoadCSources(t *testing.T) {
	fsys := fstest.MapFS{
		"app/package.toml": {Data: []byte(project + "[c]\nsources = [\"native/missing.c\"]\n")},
		"app/main.vx":      {Data: []byte("fn main() {}\n")},
	}
	_, err := LoadFS(fsys, "app", "app", true)
	if err == nil || err.Error() != "app/package.toml: C source 'native/missing.c' not found" {
		t.Errorf("got %v", err)
	}
}
