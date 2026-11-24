package tests

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/cc"
	"github.com/irshadio/vex-lang/compiler/codegen"
	"github.com/irshadio/vex-lang/compiler/diag"
	"github.com/irshadio/vex-lang/compiler/lexer"
	"github.com/irshadio/vex-lang/compiler/parser"
	"github.com/irshadio/vex-lang/compiler/types"
)

// Runs any source text through the whole compiler. It must either report
// errors or produce C code; it must never panic or hang.
//
// Run with: go test ./tests -run '^$' -fuzz FuzzCompiler -fuzztime 5m
func FuzzCompiler(f *testing.F) {
	for _, pattern := range []string{"../examples/*.vx", "errors/*.vx", "../compiler/parser/testdata/*.vx"} {
		paths, _ := filepath.Glob(pattern)
		for _, path := range paths {
			if src, err := os.ReadFile(path); err == nil {
				f.Add(string(src))
			}
		}
	}
	f.Fuzz(func(t *testing.T, src string) {
		file := diag.NewFile("main.vx", src)
		lexer.Tokenize(file)
		tree, err := parser.Parse(file)
		if err != nil {
			checkErrors(t, file, err)
		}
		if tree == nil {
			return
		}
		ast.Sexpr(tree)
		info, err := types.Check(file, tree)
		if err != nil {
			checkErrors(t, file, err)
			return
		}
		codegen.Generate(info, "main.vx")
	})
}

// Fails unless [err] is a list of diagnostics that all format cleanly.
func checkErrors(t *testing.T, file *diag.File, err error) {
	var list diag.List
	if !errors.As(err, &list) || len(list) == 0 {
		t.Fatalf("expected diagnostics, got %v", err)
	}
	for _, d := range list {
		if d.Message == "" || !utf8.ValidString(d.Message) && utf8.ValidString(file.Src) {
			t.Fatalf("bad diagnostic %+v", d)
		}
		diag.Format(file, d, false)
	}
}

// Programs that pass the type checker must produce C that compiles.
//
// Run with: go test ./tests -run '^$' -fuzz FuzzGeneratedC -fuzztime 5m
func FuzzGeneratedC(f *testing.F) {
	command, err := cc.Command()
	if err != nil {
		f.Skip(err)
	}
	for _, pattern := range []string{"../examples/*.vx", "../compiler/parser/testdata/*.vx"} {
		paths, _ := filepath.Glob(pattern)
		for _, path := range paths {
			if src, err := os.ReadFile(path); err == nil {
				f.Add(string(src))
			}
		}
	}
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, src string) {
		file := diag.NewFile("main.vx", src)
		tree, err := parser.Parse(file)
		if err != nil {
			return
		}
		info, err := types.Check(file, tree)
		if err != nil {
			return
		}
		cfile := filepath.Join(dir, fmt.Sprintf("fuzz%d.c", os.Getpid()))
		if err := os.WriteFile(cfile, []byte(codegen.Generate(info, "main.vx")), 0o644); err != nil {
			t.Fatal(err)
		}
		args := append(command[1:len(command):len(command)], "-std=gnu11", "-fwrapv", "-w", "-fsyntax-only", cfile)
		if out, err := exec.Command(command[0], args...).CombinedOutput(); err != nil {
			t.Fatalf("generated C doesn't compile:\n%s", out)
		}
	})
}
