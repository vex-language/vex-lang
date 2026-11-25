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
	"runtime"
	"strings"
	"testing"

	"github.com/irshadio/vex-lang/compiler/cc"
	"github.com/irshadio/vex-lang/compiler/codegen"
	"github.com/irshadio/vex-lang/compiler/diag"
	"github.com/irshadio/vex-lang/compiler/modules"
	"github.com/irshadio/vex-lang/compiler/types"
)

var update = flag.Bool("update", false, "rewrite the .out files of the examples")

// Loads and checks the program at [path], a file or a project folder,
// returning its C code.
func compile(path string) (string, error) {
	src, _, err := compileProgram(path)
	return src, err
}

// Like [compile], also returning the C code the project brings along.
func compileProgram(path string) (string, cc.Options, error) {
	prog, err := modules.Load(path)
	if err != nil {
		return "", cc.Options{}, err
	}
	info, err := types.CheckProgram(prog)
	if err != nil {
		return "", cc.Options{}, err
	}
	return codegen.Generate(info, filepath.Base(path)), prog.CCOptions(), nil
}

// Returns the project folders, those with a project file, matching [glob].
func projects(glob string) []string {
	files, _ := filepath.Glob(filepath.Join(glob, modules.ProjectFile))
	var dirs []string
	for _, f := range files {
		dirs = append(dirs, filepath.Dir(f))
	}
	return dirs
}

var stressComment = regexp.MustCompile(`(?m)^// stress: (\d+)$`)

// Returns how often the program at [path] collects under GC stress: on
// every allocation unless a `// stress: N` comment says otherwise.
func stressInterval(path string) string {
	src, _ := os.ReadFile(path)
	if m := stressComment.FindSubmatch(src); m != nil {
		return string(m[1])
	}
	return "1"
}

// Runs each examples/NAME.vx and run/NAME.vx with NAME.in as input, if
// present, and compares what it prints with NAME.out, also while the
// collector runs far more often than usual.
func TestExamples(t *testing.T) {
	if _, err := cc.Command(); err != nil {
		t.Skip(err)
	}
	paths, _ := filepath.Glob("../examples/*.vx")
	if len(paths) == 0 {
		t.Fatal("no examples found")
	}
	more, _ := filepath.Glob("run/*.vx")
	paths = append(paths, more...)
	paths = append(paths, projects("../examples/*")...)
	paths = append(paths, projects("run/*")...)
	if runtime.GOOS == "darwin" {
		// Projects that use Objective-C and Apple frameworks.
		paths = append(paths, projects("run_darwin/*")...)
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".vx")
		base := strings.TrimSuffix(path, ".vx")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src, opts, err := compileProgram(path)
			if err != nil {
				t.Fatalf("compile errors:\n%v", err)
			}
			exe := filepath.Join(t.TempDir(), name)
			if err := cc.CompileWith(src, exe, opts); err != nil {
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
			stress.Env = append(os.Environ(), "VEX_GC_STRESS="+stressInterval(path))
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

// Checks that each tests/errors/*.vx, and each project in tests/errors,
// fails with exactly the errors listed in its comments, in order. In a
// project, each error is listed in the file it is in.
func TestErrors(t *testing.T) {
	paths, _ := filepath.Glob("errors/*.vx")
	paths = append(paths, projects("errors/*")...)
	if len(paths) == 0 {
		t.Fatal("no error tests found")
	}
	for _, path := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".vx"), func(t *testing.T) {
			sources := []string{path}
			project := !strings.HasSuffix(path, ".vx")
			if project {
				sources = nil
				filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
					if err == nil && strings.HasSuffix(p, ".vx") {
						sources = append(sources, p)
					}
					return nil
				})
			}
			var want []string
			for _, p := range sources {
				src, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				prefix := ""
				if project {
					prefix = filepath.ToSlash(strings.TrimPrefix(p, path+string(filepath.Separator))) + ":"
				}
				for _, m := range expectRE.FindAllStringSubmatch(string(src), -1) {
					want = append(want, prefix+m[1]+": "+m[2])
				}
			}
			if len(want) == 0 {
				t.Fatal("no // error: comments in the file")
			}
			_, err := compile(path)
			var list diag.List
			if !errors.As(err, &list) {
				t.Fatalf("expected errors, got %v", err)
			}
			list.Sort()
			var got []string
			for _, d := range list {
				prefix := ""
				if project && d.File != nil {
					prefix = filepath.ToSlash(strings.TrimPrefix(d.File.Name, path+string(filepath.Separator))) + ":"
				}
				got = append(got, fmt.Sprintf("%s%s: %s", prefix, d.Pos, d.Message))
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("errors differ\n--- got\n%s\n--- want\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}
