// Package tests runs whole programs through the compiler: the examples
// must produce their expected output, and the programs in errors/ must
// fail with their expected messages.
package tests

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/irshadio/vex-lang/compiler/cc"
	"github.com/irshadio/vex-lang/compiler/codegen"
	"github.com/irshadio/vex-lang/compiler/diag"
	"github.com/irshadio/vex-lang/compiler/parser"
	"github.com/irshadio/vex-lang/compiler/types"
)

var update = flag.Bool("update", false, "rewrite the .out files of the examples")

// Parses and checks the file at [path], returning its C code.
func compile(path string) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	f := diag.NewFile(filepath.Base(path), string(src))
	tree, err := parser.Parse(f)
	if err != nil {
		return "", err
	}
	info, err := types.Check(f, tree)
	if err != nil {
		return "", err
	}
	return codegen.Generate(info, filepath.Base(path)), nil
}

// Runs each examples/NAME.vx with NAME.in as input, if present, and
// compares what it prints with NAME.out.
func TestExamples(t *testing.T) {
	if _, err := cc.Command(); err != nil {
		t.Skip(err)
	}
	paths, _ := filepath.Glob("../examples/*.vx")
	if len(paths) == 0 {
		t.Fatal("no examples found")
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".vx")
		base := strings.TrimSuffix(path, ".vx")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src, err := compile(path)
			if err != nil {
				t.Fatalf("compile errors:\n%v", err)
			}
			exe := filepath.Join(t.TempDir(), name)
			if err := cc.Compile(src, exe); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe)
			if in, err := os.ReadFile(base + ".in"); err == nil {
				cmd.Stdin = bytes.NewReader(in)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			got, err := cmd.Output()
			if err != nil {
				t.Fatalf("%v\n%s", err, stderr.String())
			}
			if *update {
				if err := os.WriteFile(base+".out", got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(base + ".out")
			if err != nil {
				t.Fatalf("missing expected output; run go test ./tests -update")
			}
			if !bytes.Equal(got, want) {
				t.Errorf("output differs from %s.out\n--- got\n%s--- want\n%s", name, got, want)
			}

			stress := exec.Command(exe)
			stress.Env = append(os.Environ(), "VEX_GC_STRESS=1")
			if in, err := os.ReadFile(base + ".in"); err == nil {
				stress.Stdin = bytes.NewReader(in)
			}
			if got, err := stress.Output(); err != nil || !bytes.Equal(got, want) {
				t.Errorf("under VEX_GC_STRESS: %v\n--- got\n%s", err, got)
			}
		})
	}
}

// An expected error, written in the source as `// error: LINE:COL: message`.
var expectRE = regexp.MustCompile(`(?m)^\s*// error: (\d+:\d+): (.*)$`)

// Checks that each tests/errors/*.vx fails with exactly the errors listed in
// its comments, in order.
func TestErrors(t *testing.T) {
	paths, _ := filepath.Glob("errors/*.vx")
	if len(paths) == 0 {
		t.Fatal("no error tests found")
	}
	for _, path := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".vx"), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, m := range expectRE.FindAllStringSubmatch(string(src), -1) {
				want = append(want, m[1]+": "+m[2])
			}
			if len(want) == 0 {
				t.Fatal("no // error: comments in the file")
			}
			_, err = compile(path)
			var list diag.List
			if !errors.As(err, &list) {
				t.Fatalf("expected errors, got %v", err)
			}
			var got []string
			for _, d := range list {
				got = append(got, fmt.Sprintf("%s: %s", d.Pos, d.Message))
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("errors differ\n--- got\n%s\n--- want\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}
