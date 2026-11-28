package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vex-language/vex-lang/compiler/doc"
	"github.com/vex-language/vex-lang/compiler/modules"
)

const docHelp = `Flags:
  -o dir            where to write the HTML pages (default: doc)
  --text            print to the terminal instead of writing HTML

With a name, such as Point, Point.x or models.User, prints that
declaration's signature and doc comment, like --text does.

The program is a .vx file, a project folder, or a standard library module
such as vex:json. Only pub declarations are documented, except in a root
module that has none, such as a program of one file, where all are.
`

func runDoc(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "doc", "folder for the HTML pages")
	text := fs.Bool("text", false, "print to the terminal")
	fs.Usage = func() { fmt.Fprintln(stderr, "Usage: vex doc [file, folder or vex:module] [name] [-o dir] [--text]") }
	var rest []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return exitUsage
		}
		if fs.NArg() == 0 {
			break
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
	path, name := ".", ""
	switch {
	case len(rest) == 1 && isProgram(rest[0]):
		path = rest[0]
	case len(rest) == 1:
		name = rest[0]
	case len(rest) == 2:
		path, name = rest[0], rest[1]
	case len(rest) > 2:
		fs.Usage()
		return exitUsage
	}
	mods, code := loadDocs(path, stderr)
	if code != exitOK {
		return code
	}
	set := doc.NewSet(mods, stderr)
	switch {
	case name != "":
		d := set.Find(name)
		if d == nil {
			fmt.Fprintf(stderr, "vex doc: no public declaration called '%s' in %s\n", name, path)
			return exitError
		}
		doc.WriteText(stdout, d)
	case *text:
		for i, m := range mods {
			if i > 0 {
				fmt.Fprintln(stdout)
			}
			doc.WriteModuleText(stdout, m)
		}
	default:
		if _, err := set.WriteHTML(*out); err != nil {
			fmt.Fprintf(stderr, "vex: %v\n", err)
			return exitError
		}
		noun := "modules"
		if len(mods) == 1 {
			noun = "module"
		}
		fmt.Fprintf(stdout, "Wrote the documentation of %d %s to %s\n", len(mods), noun, filepath.Join(*out, "index.html"))
	}
	return exitOK
}

// Reports whether [arg] names a program to document rather than a
// declaration: a standard library module, a .vx file or a folder.
func isProgram(arg string) bool {
	if strings.HasPrefix(arg, "vex:") || strings.HasSuffix(arg, ".vx") {
		return true
	}
	_, err := os.Stat(arg)
	return err == nil
}

// Loads the documentation of the modules at [path], reporting problems
// to [stderr]. Returns the exit code to use when it fails.
func loadDocs(path string, stderr io.Writer) ([]*doc.Module, int) {
	if name, ok := strings.CutPrefix(path, "vex:"); ok {
		m, err := modules.LoadStd(name)
		if err != nil {
			return nil, report(nil, err, stderr)
		}
		return []*doc.Module{doc.FromModule(m, false)}, exitOK
	}
	prog, err := modules.Load(path)
	if err != nil {
		return nil, report(nil, err, stderr)
	}
	return doc.FromProgram(prog), exitOK
}
