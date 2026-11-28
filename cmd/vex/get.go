package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/vex-language/vex-lang/compiler/packages"
)

const getHelp = `Adds each repository to package.toml, under the name the package gives
itself, and records the exact version and content hash of every
dependency, including indirect ones, in package.lock. Without a version
the newest tag is used; 1.2 means the newest 1.2.x. With no repositories
it downloads what package.toml lists and writes package.lock again.

  vex get https://github.com/ann/charts
  vex get https://github.com/ann/charts@0.3.1
  vex get --path camera https://github.com/vex-lang/packages

Flags:
  --path folder     the folder of the package, for repositories that hold
                    several packages (tagged camera/v0.2.0)

Environment:
  VEX_CACHE         where downloaded packages are kept (default: ~/.vex/pkg)
`

func runGet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("path", "", "the folder of the package inside the repository")
	fs.Usage = func() { fmt.Fprintln(stderr, "Usage: vex get [--path folder] [repository[@version]]...") }
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *path != "" && fs.NArg() == 0 {
		fs.Usage()
		return exitUsage
	}
	if err := packages.Get(".", fs.Args(), *path, stdout); err != nil {
		return report(nil, err, stderr)
	}
	return exitOK
}
