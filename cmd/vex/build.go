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

	"github.com/irshadio/vex-lang/compiler/cc"
	"github.com/irshadio/vex-lang/compiler/codegen"
	"github.com/irshadio/vex-lang/compiler/parser"
	"github.com/irshadio/vex-lang/compiler/types"
)

// Translates the source file at [path] to C, reporting any errors to
// [stderr]. Returns the exit code to use when it fails.
func translate(path string, stderr io.Writer) (string, int) {
	f, err := readSource(path)
	if err != nil {
		fmt.Fprintf(stderr, "vex: %v\n", err)
		return "", exitError
	}
	tree, err := parser.Parse(f)
	if err != nil {
		return "", report(f, err, stderr)
	}
	warn(f, tree, stderr)
	info, err := types.Check(f, tree)
	if err != nil {
		return "", report(f, err, stderr)
	}
	return codegen.Generate(info, filepath.Base(path)), exitOK
}

func runEmitC(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: vex emit-c <file>")
		return exitUsage
	}
	src, code := translate(args[0], stderr)
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
	fs.Usage = func() { fmt.Fprintln(stderr, "Usage: vex build <file> [-o output]") }
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
	if len(files) != 1 {
		fs.Usage()
		return exitUsage
	}
	path := files[0]
	if *out == "" {
		*out = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	src, code := translate(path, stderr)
	if code != exitOK {
		return code
	}
	if err := cc.Compile(src, *out); err != nil {
		fmt.Fprintf(stderr, "vex: %v\n", err)
		return exitError
	}
	fmt.Fprintf(stdout, "Built %s\n", *out)
	return exitOK
}

func runRun(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: vex run <file>")
		return exitUsage
	}
	src, code := translate(args[0], stderr)
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
	if err := cc.Compile(src, exe); err != nil {
		fmt.Fprintf(stderr, "vex: %v\n", err)
		return exitError
	}
	cmd := exec.Command(exe)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintf(stderr, "vex: %v\n", err)
		return exitError
	}
	return exitOK
}
