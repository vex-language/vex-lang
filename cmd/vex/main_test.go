package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/vex-language/vex-lang/compiler/cc"
)

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		code, out, _ := runCLI(arg)
		if code != exitOK || out != "vex "+version+"\n" {
			t.Errorf("%s: code %d, output %q", arg, code, out)
		}
	}
}

func TestHelp(t *testing.T) {
	code, out, _ := runCLI("help")
	if code != exitOK || !strings.Contains(out, "Usage: vex <command>") || !strings.Contains(out, "version") {
		t.Errorf("help: code %d, output %q", code, out)
	}
	code, out, _ = runCLI("help", "version")
	if code != exitOK || !strings.HasPrefix(out, "Usage: vex version") {
		t.Errorf("help version: code %d, output %q", code, out)
	}
}

func TestNoArgsShowsUsage(t *testing.T) {
	code, _, errOut := runCLI()
	if code != exitUsage || !strings.Contains(errOut, "Usage:") {
		t.Errorf("no args: code %d, stderr %q", code, errOut)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errOut := runCLI("fly")
	if code != exitUsage || !strings.Contains(errOut, `unknown command "fly"`) {
		t.Errorf("unknown: code %d, stderr %q", code, errOut)
	}
}

func writeFile(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.vx")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTokens(t *testing.T) {
	path := writeFile(t, "let name = \"Vex\"\n")
	code, out, errOut := runCLI("tokens", path)
	if code != exitOK {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"1:1      'let'", "identifier         name", `string             "Vex"`, "end of statement   newline", "end of file"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestTokensReportsErrors(t *testing.T) {
	path := writeFile(t, "let s = \"abc\nlet t = a # b\n")
	code, _, errOut := runCLI("tokens", path)
	if code != exitError {
		t.Errorf("code %d, want %d", code, exitError)
	}
	for _, want := range []string{"error: unterminated string", "main.vx:1:9", "^^^^ add a closing \"", "comments start with //", "2 errors"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOut)
		}
	}
}

func TestTokensUsage(t *testing.T) {
	if code, _, _ := runCLI("tokens"); code != exitUsage {
		t.Errorf("code %d, want %d", code, exitUsage)
	}
	if code, _, errOut := runCLI("tokens", "missing.vx"); code != exitError || !strings.Contains(errOut, "missing.vx") {
		t.Errorf("missing file: code %d, stderr %q", code, errOut)
	}
}

func TestAST(t *testing.T) {
	path := writeFile(t, "fn main() {\n    let x = 1 + 2\n}\n")
	code, out, errOut := runCLI("ast", path)
	if code != exitOK {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	want := "file\n  fn main\n    block\n      let x\n        +\n          1\n          2\n"
	if out != want {
		t.Errorf("output\n got:\n%s\nwant:\n%s", out, want)
	}
}

func TestASTReportsSyntaxErrors(t *testing.T) {
	path := writeFile(t, "fn main() {\n    if a = 1 {}\n}\n")
	code, _, errOut := runCLI("ast", path)
	if code != exitError || !strings.Contains(errOut, "use == to compare in a condition") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
	if code, _, _ := runCLI("ast"); code != exitUsage {
		t.Errorf("no file: code %d, want %d", code, exitUsage)
	}
}

func TestCheck(t *testing.T) {
	path := writeFile(t, "fn main() {\n    let n = 3\n    println(n + \" items\")\n}\n")
	code, _, errOut := runCLI("check", path)
	if code != exitError || !strings.Contains(errOut, `"{n} items"`) {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
	path = writeFile(t, "fn main() {\n    println(\"hi\")\n}\n")
	if code, out, errOut := runCLI("check", path); code != exitOK || !strings.HasSuffix(out, ": no errors\n") {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func needCC(t *testing.T) {
	t.Helper()
	if _, err := cc.Command(); err != nil {
		t.Skip(err)
	}
}

func TestRun(t *testing.T) {
	needCC(t)
	path := writeFile(t, "fn main() {\n    println(\"sum: {1 + 2}\")\n}\n")
	code, out, errOut := runCLI("run", path)
	if code != exitOK || out != "sum: 3\n" {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	path = writeFile(t, "fn main() {\n    let xs = [1]\n    println(xs[3])\n}\n")
	code, _, errOut = runCLI("run", path)
	if code != exitError || !strings.Contains(errOut, "index 3 is out of range") || !strings.Contains(errOut, "main.vx:3") {
		t.Errorf("runtime error: code %d, stderr %q", code, errOut)
	}
	path = writeFile(t, "fn main() {\n    let n = 2;\n    println(n)\n}\n")
	code, out, errOut = runCLI("run", path)
	if code != exitOK || out != "2\n" || !strings.Contains(errOut, "warning: a ; isn't needed at the end of a line") {
		t.Errorf("warning: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	path = writeFile(t, "fn main() {\n    let n: int = \"x\"\n}\n")
	if code, _, errOut := runCLI("run", path); code != exitError || !strings.Contains(errOut, "expected int, found string") {
		t.Errorf("type error: code %d, stderr %q", code, errOut)
	}
	path = writeFile(t, "import \"vex:os\"\nfn main() {\n    println(os.args())\n    os.exit(3)\n}\n")
	for _, args := range [][]string{{"run", path, "a", "b c"}, {"run", path, "--", "a", "b c"}} {
		if code, out, errOut := runCLI(args...); code != 3 || out != "[\"a\", \"b c\"]\n" {
			t.Errorf("%q: code %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

func TestRunIntoClosedPipe(t *testing.T) {
	needCC(t)
	path := writeFile(t, "fn main() {\n    for i in 0..1000000 { println(i) }\n}\n")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	defer w.Close()
	var errOut bytes.Buffer
	if code := run([]string{"run", path}, w, &errOut); code != 128+int(syscall.SIGPIPE) || errOut.Len() > 0 {
		t.Errorf("code %d, stderr %q", code, errOut.String())
	}
}

func TestRunMissingCFunction(t *testing.T) {
	needCC(t)
	path := writeFile(t, "extern fn vx_mylib_init() -> int\nfn main() {\n    println(vx_mylib_init())\n}\n")
	code, _, errOut := runCLI("run", path)
	if code != exitError || !strings.Contains(errOut, "linking failed") || strings.Contains(errOut, "bug in vex") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
}

func TestBuild(t *testing.T) {
	needCC(t)
	path := writeFile(t, "fn main() {\n    println(\"built\")\n}\n")
	exe := cc.Executable(filepath.Join(t.TempDir(), "app"))
	code, out, errOut := runCLI("build", path, "-o", strings.TrimSuffix(exe, ".exe"))
	if code != exitOK || out != "Built "+exe+"\n" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	got, err := exec.Command(exe).Output()
	if err != nil || string(got) != "built\n" {
		t.Errorf("running the binary: %q, %v", got, err)
	}
	if code, _, _ := runCLI("build", path, path); code != exitUsage {
		t.Errorf("two files: code %d", code)
	}
	if code, _, errOut := runCLI("build", t.TempDir()); code != exitError || !strings.Contains(errOut, "no package.toml") {
		t.Errorf("folder without a project file: code %d, stderr %q", code, errOut)
	}
}

func TestEmitC(t *testing.T) {
	path := writeFile(t, "fn main() {\n    println(\"hi\")\n}\n")
	code, out, _ := runCLI("emit-c", path)
	if code != exitOK || !strings.Contains(out, "static void fn_main(void)") || !strings.Contains(out, "int main(int argc, char **argv)") {
		t.Errorf("code %d, output:\n%s", code, out)
	}
}

func TestBuildOutputPath(t *testing.T) {
	needCC(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "script")
	src := "fn main() {\n    println(\"hi\")\n}\n"
	if err := os.WriteFile(script, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// On Windows the program is script.exe, which leaves the source alone.
	if code, _, errOut := runCLI("build", script, "-o", script); cc.Executable(script) == script && (code != exitUsage || !strings.Contains(errOut, "would replace the source file")) {
		t.Errorf("same output as source: code %d, stderr %q", code, errOut)
	}
	if got, _ := os.ReadFile(script); string(got) != src {
		t.Errorf("source was changed: %q", got)
	}

	store := filepath.Join(dir, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(store, "package.toml"), []byte("[package]\nname = \"store\"\n"), 0o644)
	os.WriteFile(filepath.Join(store, "main.vx"), []byte(src), 0o644)
	exe := cc.Executable(filepath.Join(store, "store"))
	if code, out, errOut := runCLI("build", store, "-o", store); code != exitOK || out != "Built "+exe+"\n" {
		t.Fatalf("folder output: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	if got, err := exec.Command(exe).Output(); err != nil || string(got) != "hi\n" {
		t.Errorf("running the binary: %q, %v", got, err)
	}
}

func TestTest(t *testing.T) {
	needCC(t)
	dir := t.TempDir()
	files := map[string]string{
		"package.toml": "[package]\nname = \"calc\"\n",
		"main.vx": `fn add(a: int, b: int) -> int => a + b

fn parse(s: string) throws -> int {
    if s == "" {
        throw Error("empty input")
    }
    return 1
}

fn main() {
    println(add(1, 2))
}

test "adds in the same file" {
    expect(add(2, 2) == 4)
}
`,
		"main_test.vx": `test "adds two numbers" {
    expect(add(2, 3) == 5)
}

test "shows both sides" {
    expect(add(2, 3) == 6)
    let name: string? = "vex"
    expect(name == "vax", "names differ")
    expect(add(1, 1) > 2 && true)
}

test "passes an error up" throws {
    let n = try parse("")
    expect(n == 1)
}

test "crashes" {
    let xs: [int] = []
    println(xs[3])
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errOut := runCLI("test", dir)
	want := `✓ adds in the same file
✓ adds two numbers
✗ shows both sides
  ` + filepath.Join(dir, "main_test.vx") + `:6: expect(add(2, 3) == 6)
    left:  5
    right: 6
  ` + filepath.Join(dir, "main_test.vx") + `:8: expect(name == "vax")
    names differ
    left:  "vex"
    right: "vax"
  ` + filepath.Join(dir, "main_test.vx") + `:9: expect(add(1, 1) > 2 && true)
✗ passes an error up
  error: empty input
✗ crashes

5 tests: 2 passed, 3 failed
`
	if code != exitError || out != want || !strings.Contains(errOut, "index 3 is out of range") {
		t.Errorf("code %d\nstdout:\n%s\nwant:\n%s\nstderr %q", code, out, want, errOut)
	}
	code, out, _ = runCLI("test", dir, "-filter", "adds")
	if code != exitOK || out != "✓ adds in the same file\n✓ adds two numbers\n\n2 tests: 2 passed, 0 failed\n" {
		t.Errorf("filter: code %d, stdout %q", code, out)
	}
	if code, out, _ := runCLI("test", dir, "-filter", "nothing"); code != exitError || out != "no tests match 'nothing'\n" {
		t.Errorf("no match: code %d, stdout %q", code, out)
	}
	// Other builds leave the tests out.
	if code, out, errOut := runCLI("run", dir); code != exitOK || out != "3\n" {
		t.Errorf("run: code %d, stdout %q, stderr %q", code, out, errOut)
	}

	path := writeFile(t, "test \"same\" {\n}\n\ntest \"same\" {\n}\n")
	if code, _, errOut := runCLI("test", path); code != exitError || !strings.Contains(errOut, "a test named 'same' is already on line 1") {
		t.Errorf("duplicate: code %d, stderr %q", code, errOut)
	}
	path = writeFile(t, "fn main() {\n    expect(1 == 1)\n}\n")
	if code, _, errOut := runCLI("run", path); code != exitError || !strings.Contains(errOut, "expect only works in tests") {
		t.Errorf("expect outside tests: code %d, stderr %q", code, errOut)
	}
	path = writeFile(t, "fn main() {}\n")
	if code, out, _ := runCLI("test", path); code != exitOK || out != "no tests\n" {
		t.Errorf("no tests: code %d, stdout %q", code, out)
	}
}

// A build leaves stack traces out unless asked; vex run keeps them.
func TestBuildTraces(t *testing.T) {
	needCC(t)
	path := writeFile(t, "fn half(n: int) -> int {\n    return 10 / n\n}\n\nfn main() {\n    println(half(0))\n}\n")
	at := "--> " + path + ":2\n"
	trace := "  at half (" + path + ":2)\n  at main (" + path + ":6)\n"
	out := filepath.Join(t.TempDir(), "app")
	for _, tt := range []struct {
		flags []string
		trace bool
	}{{nil, false}, {[]string{"--traces"}, true}, {[]string{"--debug"}, true}} {
		args := append([]string{"build", path, "-o", out}, tt.flags...)
		if code, _, errOut := runCLI(args...); code != exitOK {
			t.Fatalf("build %v: code %d, stderr %q", tt.flags, code, errOut)
		}
		var errOut bytes.Buffer
		cmd := exec.Command(out)
		cmd.Stderr = &errOut
		cmd.Run()
		want := "error: division by zero\n " + at
		if tt.trace {
			want += trace
		}
		if errOut.String() != want {
			t.Errorf("build %v: stderr %q, want %q", tt.flags, errOut.String(), want)
		}
	}
	if code, _, errOut := runCLI("run", path); code != 1 || !strings.HasSuffix(errOut, trace) {
		t.Errorf("run: code %d, stderr %q", code, errOut)
	}
}

func TestBuildModes(t *testing.T) {
	needCC(t)
	path := writeFile(t, "import \"vex:os\"\nfn main() {\n    println(\"hi\", os.args())\n}\n")
	out := filepath.Join(filepath.Dir(path), "app")
	code, stdout, errOut := runCLI("build", path, "-o", out, "--debug")
	if code != exitOK || !strings.Contains(stdout, "Kept the C code in "+out+".c") {
		t.Fatalf("build --debug: code %d, stdout %q, stderr %q", code, stdout, errOut)
	}
	if c, err := os.ReadFile(out + ".c"); err != nil || !strings.Contains(string(c), "int main(") {
		t.Errorf("kept C: %v", err)
	}
	if got, err := exec.Command(out).Output(); err != nil || string(got) != "hi []\n" {
		t.Errorf("debug program: %q, %v", got, err)
	}
	code, stdout, errOut = runCLI("run", "--debug", "--sanitize=address,undefined", path, "a")
	if code != exitOK || stdout != "hi [\"a\"]\n" {
		t.Errorf("run with flags: code %d, stdout %q, stderr %q", code, stdout, errOut)
	}
	for _, tt := range []struct{ arg, want string }{
		{"--sanitize=memory", `unknown sanitizer "memory"`},
		{"--sanitize=thread,address", "can't be combined with address"},
	} {
		if code, _, errOut := runCLI("build", path, "-o", out, tt.arg); code != exitUsage || !strings.Contains(errOut, tt.want) {
			t.Errorf("%s: code %d, stderr %q", tt.arg, code, errOut)
		}
	}
	code, stdout, _ = runCLI("help", "build")
	for _, want := range []string{"--debug", "--sanitize=list", "--traces", "VEX_CFLAGS", "<out>.c"} {
		if code != exitOK || !strings.Contains(stdout, want) {
			t.Errorf("help build lacks %q:\n%s", want, stdout)
		}
	}
}

func TestDoc(t *testing.T) {
	path := writeFile(t, "/// Adds [a] and [b].\npub fn add(a: int, b: int) -> int => a + b\n\nfn main() {}\n")
	code, out, errOut := runCLI("doc", path, "add")
	if want := "fn add(a: int, b: int) -> int\n\n    Adds [a] and [b].\n"; code != exitOK || out != want {
		t.Errorf("doc add: code %d, output %q, stderr %q", code, out, errOut)
	}
	code, out, _ = runCLI("doc", "--text", path)
	if code != exitOK || !strings.HasPrefix(out, "module main\n\nFunctions:\n    fn add(a: int, b: int) -> int\n") {
		t.Errorf("doc --text: code %d, output %q", code, out)
	}
	code, _, errOut = runCLI("doc", path, "sub")
	if code != exitError || !strings.Contains(errOut, "no public declaration called 'sub'") {
		t.Errorf("doc sub: code %d, stderr %q", code, errOut)
	}
	dir := filepath.Join(t.TempDir(), "site")
	code, out, errOut = runCLI("doc", path, "-o", dir)
	if code != exitOK || !strings.HasPrefix(out, "Wrote the documentation of 1 module") {
		t.Fatalf("doc -o: code %d, output %q, stderr %q", code, out, errOut)
	}
	page, err := os.ReadFile(filepath.Join(dir, "main.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "<pre class=\"sig\"><code>fn add(a: int, b: int) -&gt; int</code></pre>") {
		t.Errorf("page:\n%s", page)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Error(err)
	}
}

func TestDocStd(t *testing.T) {
	code, out, errOut := runCLI("doc", "vex:math", "abs")
	if code != exitOK || !strings.HasPrefix(out, "fn abs(x: int) -> int\n") {
		t.Errorf("doc vex:math abs: code %d, output %q, stderr %q", code, out, errOut)
	}
	code, _, errOut = runCLI("doc", "vex:nothing")
	if code != exitError || !strings.Contains(errOut, "unknown module 'vex:nothing'") {
		t.Errorf("doc vex:nothing: code %d, stderr %q", code, errOut)
	}
	code, _, _ = runCLI("doc", "a", "b", "c")
	if code != exitUsage {
		t.Errorf("doc a b c: code %d", code)
	}
}

func TestStackOverflowOffTheMainStack(t *testing.T) {
	needCC(t)
	if strings.Contains(os.Getenv("VEX_CFLAGS"), "sanitize") {
		t.Skip("sanitizers report stack overflows themselves")
	}
	depth := `
fn depth(n: int) -> int {
    if n < 0 { return 0 }
    return depth(n + 1) + depth(n - 1)
}
`
	tests := map[string]string{
		"task": `import "vex:time"
` + depth + `
async fn deep() -> int {
    await time.sleep(1)
    return depth(0)
}

async fn main() {
    println("start")
    println(await deep())
}
`,
		"isolate": `import "vex:isolate"
` + depth + `
async fn main() {
    println("start")
    println(await isolate.run(fn() -> int => depth(0)))
}
`,
	}
	want := "error: stack overflow: too many nested function calls, probably a function that calls itself without stopping\n"
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := runCLI("run", writeFile(t, src))
			if code != exitError || out != "start\n" || errOut != want {
				t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
			}
		})
	}
}
