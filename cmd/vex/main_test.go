package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
