// Command vex is the compiler and toolchain for the Vex language.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/diag"
	"github.com/vex-language/vex-lang/compiler/lexer"
	"github.com/vex-language/vex-lang/compiler/parser"
)

// The toolchain version, set at build time with
// `-ldflags "-X main.version=..."`.
var version = "0.1.0-dev"

// Exit codes returned by the command.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// A subcommand such as `vex version`.
type command struct {
	name  string
	args  string
	short string
	run   func(args []string, stdout, stderr io.Writer) int

	// More about the command, for `vex help <command>`.
	help string
}

// The flags and variables that choose how a program is compiled.
const modeHelp = `  --debug           compile without optimizations and with debug information
                    (-O0 -g) instead of -O3
  --sanitize=list   build in sanitizers that stop the program at memory and
                    other errors: address, undefined and thread, e.g.
                    --sanitize=address,undefined

Environment:
  CC                the C compiler (default: clang, then cc, then gcc)
  VEX_CFLAGS        more flags for the C compiler, after the others, so they
                    can override them, e.g. VEX_CFLAGS="-march=native"
`

var commands []command

func init() {
	commands = []command{
		{"run", "[flags] [file or folder] [-- args]", "Compile and run a program", runRun,
			"Flags, before the program:\n" + modeHelp},
		{"build", "[file or folder] [-o out] [flags]", "Build a native executable", runBuild,
			"Flags:\n  -o out            where to write the program (default: its name)\n" +
				"  --traces          list the calls that led to a runtime error; on with --debug\n" + modeHelp +
				"\nWith --debug, the generated C is kept next to the program as <out>.c, so a\ndebugger can step through it.\n" +
				"\nRuntime errors show their source line. Stack traces, on for vex run and vex\ntest, cost time in programs made of many small calls, so a build leaves them\nout unless --traces or --debug is given.\n"},
		{"emit-c", "[file or folder]", "Print the generated C code", runEmitC, ""},
		{"check", "[file or folder]", "Check a program for errors", runCheck, ""},
		{"test", "[file or folder] [flags]", "Run the program's tests", runTest,
			"Flags:\n  -filter text      run only the tests whose names contain text\n" + modeHelp},
		{"get", "[flags] [repo[@version]]...", "Add dependencies and lock their versions", runGet, getHelp},
		{"doc", "[path] [name]", "Show or write the API documentation", runDoc, docHelp},
		{"ast", "<file>", "Print the syntax tree of a source file", runAST, ""},
		{"tokens", "<file>", "Print the tokens of a source file", runTokens, ""},
		{"version", "", "Print the Vex version", runVersion, ""},
		{"help", "[command]", "Show help", runHelp, ""},
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Runs the command line [args] and returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}
	name := args[0]
	switch name {
	case "-h", "--help":
		name = "help"
	case "-v", "--version":
		name = "version"
	}
	for _, c := range commands {
		if c.name == name {
			return c.run(args[1:], stdout, stderr)
		}
	}
	fmt.Fprintf(stderr, "vex: unknown command %q\nRun 'vex help' for a list of commands.\n", args[0])
	return exitUsage
}

func runVersion(_ []string, stdout, _ io.Writer) int {
	fmt.Fprintf(stdout, "vex %s\n", version)
	return exitOK
}

func runHelp(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return exitOK
	}
	for _, c := range commands {
		if c.name == args[0] {
			fmt.Fprintf(stdout, "Usage: vex %s %s\n\n%s.\n", c.name, c.args, c.short)
			if c.help != "" {
				fmt.Fprintf(stdout, "\n%s", c.help)
			}
			return exitOK
		}
	}
	fmt.Fprintf(stderr, "vex help: unknown command %q\n", args[0])
	return exitUsage
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, "Vex is a compiled language for native apps.\n\nUsage: vex <command> [arguments]\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-22s %s\n", c.name+" "+c.args, c.short)
	}
	fmt.Fprint(w, "\nRun 'vex help <command>' for details.\n")
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(stderr, "Usage: vex check [file or folder]")
		return exitUsage
	}
	if _, _, code := load(target(args), stderr); code != exitOK {
		return code
	}
	fmt.Fprintf(stdout, "%s: no errors\n", target(args))
	return exitOK
}

func runAST(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: vex ast <file>")
		return exitUsage
	}
	f, err := readSource(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "vex: %v\n", err)
		return exitError
	}
	tree, err := parser.Parse(f)
	if err != nil {
		return report(f, err, stderr)
	}
	ast.Fprint(stdout, tree)
	return exitOK
}

func runTokens(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: vex tokens <file>")
		return exitUsage
	}
	f, err := readSource(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "vex: %v\n", err)
		return exitError
	}
	toks, err := lexer.Tokenize(f)
	if err != nil {
		return report(f, err, stderr)
	}
	for _, t := range toks {
		text := t.Text
		switch {
		case t.IsAutoSemi():
			text = "newline"
		case t.Kind == lexer.EOF:
			text = ""
		case t.Kind >= lexer.String && t.Kind <= lexer.StringTail:
			text = strconv.Quote(t.Value)
		}
		fmt.Fprintf(stdout, "%-8s %-18s %s\n", t.Pos, t.Kind, text)
	}
	return exitOK
}

// Prints compile errors from [err] for source file [f] and returns the
// exit code to use.
// Prints the warnings found in [tree].
func warn(warnings diag.List, stderr io.Writer) {
	color := useColor(stderr)
	for _, d := range warnings {
		fmt.Fprintln(stderr, diag.Format(nil, d, color))
	}
}

func report(f *diag.File, err error, stderr io.Writer) int {
	var list diag.List
	if !errors.As(err, &list) {
		fmt.Fprintf(stderr, "vex: %v\n", err)
		return exitError
	}
	list.Sort()
	color := useColor(stderr)
	for i, d := range list {
		if i > 0 {
			fmt.Fprintln(stderr)
		}
		fmt.Fprint(stderr, diag.Format(f, d, color))
	}
	if len(list) > 1 {
		fmt.Fprintf(stderr, "\n%d errors\n", len(list))
	}
	return exitError
}

// Reports whether [w] is a terminal that should get coloured output.
func useColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Reads the source file at [path] into a [diag.File].
func readSource(path string) (*diag.File, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return diag.NewFile(path, string(src)), nil
}
