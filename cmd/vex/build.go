package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/irshadio/vex-lang/compiler/cc"
	"github.com/irshadio/vex-lang/compiler/codegen"
	"github.com/irshadio/vex-lang/compiler/modules"
	"github.com/irshadio/vex-lang/compiler/types"
)

// Loads and checks the program at [target], a source file or a project
// folder, reporting any problems to [stderr]. Returns the exit code to
// use when it fails.
func load(target string, stderr io.Writer) (*modules.Program, *types.Info, int) {
	prog, err := modules.Load(target)
	if prog != nil {
		warn(prog.Warnings(), stderr)
	}
	if err != nil {
		return nil, nil, report(nil, err, stderr)
	}
	info, err := types.CheckProgram(prog)
	if err != nil {
		return nil, nil, report(nil, err, stderr)
	}
	return prog, info, exitOK
}

// Translates the program at [target] to C. Returns the exit code to use
// when it fails.
func translate(target string, stderr io.Writer) (string, *modules.Program, int) {
	prog, info, code := load(target, stderr)
	if code != exitOK {
		return "", nil, code
	}
	name := prog.Root.Files[0].Name
	if prog.Name == "" {
		name = filepath.Base(name)
	}
	return codegen.Generate(info, name), prog, exitOK
}

// Returns the program named on the command line, or the current folder.
func target(args []string) string {
	if len(args) == 0 {
		return "."
	}
	return args[0]
}

func runEmitC(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(stderr, "Usage: vex emit-c [file or folder]")
		return exitUsage
	}
	src, _, code := translate(target(args), stderr)
	if code != exitOK {
		return code
	}
	io.WriteString(stdout, src)
	return exitOK
}

func runBuild(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "output file")
	fs.Usage = func() { fmt.Fprintln(stderr, "Usage: vex build [file or folder] [-o output]") }
	// Accept the flag after the file name too: vex build app.vx -o app.
	var files []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return exitUsage
		}
		if fs.NArg() == 0 {
			break
		}
		files = append(files, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(files) > 1 {
		fs.Usage()
		return exitUsage
	}
	path := target(files)
	src, prog, code := translate(path, stderr)
	if code != exitOK {
		return code
	}
	name := prog.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if *out == "" {
		*out = name
	}
	// An output naming a folder, such as the project's own folder, puts
	// the program inside it.
	if info, err := os.Stat(*out); err == nil && info.IsDir() {
		*out = filepath.Join(*out, name)
	}
	if same(*out, path) {
		fmt.Fprintf(stderr, "vex: building would replace the source file %s; choose another name with -o\n", path)
		return exitUsage
	}
	if err := cc.CompileWith(src, *out, prog.CCOptions()); err != nil {
		fmt.Fprintf(stderr, "vex: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stdout, "Built %s\n", *out)
	return exitOK
}

func runRun(args []string, stdout, stderr io.Writer) int {
	// The program is the first argument when it names a .vx file or a
	// folder; the rest, after an optional --, go to the program.
	var program []string
	if len(args) > 0 && args[0] != "--" {
		if info, err := os.Stat(args[0]); strings.HasSuffix(args[0], ".vx") || err == nil && info.IsDir() {
			program, args = args[:1], args[1:]
		}
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	src, prog, code := translate(target(program), stderr)
	if code != exitOK {
		return code
	}
	dir, err := os.MkdirTemp("", "vex-run-")
	if err != nil {
		fmt.Fprintf(stderr, "vex: %v\n", err)
		return exitError
	}
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, "main")
	if err := cc.CompileWith(src, exe, prog.CCOptions()); err != nil {
		fmt.Fprintf(stderr, "vex: %v\n", err)
		return exitError
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				switch sig := status.Signal(); sig {
				// A closed pipe, an interrupt or a request to stop ends the
				// program as it would end any other.
				case syscall.SIGPIPE, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP:
				case syscall.SIGKILL:
					fmt.Fprintf(stderr, "vex: the program was killed\n")
				default:
					fmt.Fprintf(stderr, "vex: the program crashed (%v); this is a bug in vex\n", sig)
				}
				return 128 + int(status.Signal())
			}
			return exit.ExitCode()
		}
		fmt.Fprintf(stderr, "vex: %v\n", err)
		return exitError
	}
	return exitOK
}

// Reports whether [a] and [b] name the same existing file.
func same(a, b string) bool {
	x, err1 := os.Stat(a)
	y, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(x, y)
}
