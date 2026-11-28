// Package bench holds programs written in both Vex and Go, for comparing
// their speed with run.sh. Its test keeps them building and agreeing.
package bench

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vex-language/vex-lang/compiler/cc"
	"github.com/vex-language/vex-lang/compiler/codegen"
	"github.com/vex-language/vex-lang/compiler/modules"
	"github.com/vex-language/vex-lang/compiler/types"
)

// The problem size each benchmark runs with in the test, kept small so
// the whole test takes seconds, and the last line it must print: the line
// the Go version prints at that size.
var small = map[string]struct{ arg, want string }{
	"binary-trees":  {"6", "binary-trees(6) = 4016"},
	"fannkuch":      {"7", "fannkuch(7) = 16"},
	"fib":           {"20", "fib(20) = 6765"},
	"generators":    {"10000", "generators(10000) = 1000 6514271631881033920"},
	"json":          {"2", "json(2) = 175194 1190418"},
	"mandelbrot":    {"200", "mandelbrot(200) = 15909 4458892548255940331"},
	"maps":          {"10000", "maps(10000) = 10000 49995000 5000 5000 2"},
	"nbody":         {"1000", "nbody(1000) = -0.169075164 -0.169087605"},
	"sort":          {"10000", "sort(10000) = -1073733767 1073736272 k-10039 k99911 3718922396484934555"},
	"spectral-norm": {"100", "spectral-norm(100) = 1.274219991"},
	"strings":       {"10", "strings(10) = 200790"},
	"tasks":         {"10", "tasks(10) = 2047"},
}

// Builds each Vex benchmark, runs it at a small size and checks the last
// line it prints. The Go versions are compared by run.sh.
func TestBenchmarks(t *testing.T) {
	if _, err := cc.Command(); err != nil {
		t.Skip(err)
	}
	dirs, _ := filepath.Glob("*/main.vx")
	if len(dirs) == 0 {
		t.Fatal("no benchmarks found")
	}
	for _, path := range dirs {
		name := filepath.Dir(path)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			size, ok := small[name]
			if !ok {
				t.Fatalf("add %s to the small table in bench_test.go", name)
			}
			out := runVex(t, path, filepath.Join(t.TempDir(), name), size.arg)
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if got := lines[len(lines)-1]; got != size.want {
				t.Errorf("got %q, want %q", got, size.want)
			}
		})
	}
}

// Compiles the Vex program at [path] to [exe] and returns what it prints
// when run with [arg].
func runVex(t *testing.T, path, exe, arg string) []byte {
	prog, err := modules.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := types.CheckProgram(prog)
	if err != nil {
		t.Fatalf("compile errors:\n%v", err)
	}
	if err := cc.CompileWith(codegen.Generate(info, "main.vx"), exe, prog.CCOptions()); err != nil {
		t.Fatal(err)
	}
	return output(t, exec.Command(exe, arg))
}

// Runs [cmd] and returns its standard output, failing the test if it
// fails.
func output(t *testing.T, cmd *exec.Cmd) []byte {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v\n%s", cmd.Path, err, stderr.String())
	}
	return out
}
