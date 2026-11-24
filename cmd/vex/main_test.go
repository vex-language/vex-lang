package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irshadio/vex-lang/compiler/cc"
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
	path := writeFile(t, "let s = \"abc\nlet t = a & b\n")
	code, _, errOut := runCLI("tokens", path)
	if code != exitError {
		t.Errorf("code %d, want %d", code, exitError)
	}
	for _, want := range []string{"error: unterminated string", "main.vx:1:9", "^^^^ add a closing \"", "use && for logical and", "2 errors"} {
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
}

func TestBuild(t *testing.T) {
	needCC(t)
	path := writeFile(t, "fn main() {\n    println(\"built\")\n}\n")
	exe := filepath.Join(t.TempDir(), "app")
	code, out, errOut := runCLI("build", path, "-o", exe)
	if code != exitOK || out != "Built "+exe+"\n" {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	got, err := exec.Command(exe).Output()
	if err != nil || string(got) != "built\n" {
		t.Errorf("running the binary: %q, %v", got, err)
	}
	if code, _, _ := runCLI("build"); code != exitUsage {
		t.Errorf("no file: code %d", code)
	}
}

func TestEmitC(t *testing.T) {
	path := writeFile(t, "fn main() {\n    println(\"hi\")\n}\n")
	code, out, _ := runCLI("emit-c", path)
	if code != exitOK || !strings.Contains(out, "static void fn_main(void)") || !strings.Contains(out, "int main(void)") {
		t.Errorf("code %d, output:\n%s", code, out)
	}
}
