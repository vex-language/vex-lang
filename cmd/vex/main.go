// Command vex is the compiler and toolchain for the Vex language.
package main

import (
	"fmt"
	"io"
	"os"
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
}

var commands []command

func init() {
	commands = []command{
		{"version", "", "Print the Vex version", runVersion},
		{"help", "[command]", "Show help", runHelp},
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
