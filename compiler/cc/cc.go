// Package cc runs the system C compiler on generated code.
package cc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Flags passed to every compile. `-fwrapv` makes int overflow wrap around
// instead of being undefined; `-w` hides warnings about generated code.
var flags = []string{"-std=gnu11", "-fwrapv", "-O2", "-w"}

// Returns the C compiler command: `$CC` if set (it may include arguments),
// otherwise clang, otherwise cc.
func Command() ([]string, error) {
	if env := strings.Fields(os.Getenv("CC")); len(env) > 0 {
		return env, nil
	}
	for _, name := range []string{"clang", "cc", "gcc"} {
		if path, err := exec.LookPath(name); err == nil {
			return []string{path}, nil
		}
	}
	return nil, errors.New("no C compiler found; install clang (on macOS: xcode-select --install) or set $CC")
}

// Compiles the C source [src] into the executable [out]. Flags in
// `$VEX_CFLAGS` are added after the default ones, so they can override them.
func Compile(src, out string) error {
	command, err := Command()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "vex-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cfile := filepath.Join(dir, "main.c")
	if err := os.WriteFile(cfile, []byte(src), 0o644); err != nil {
		return err
	}
	args := append(append(command[1:len(command):len(command)], flags...), strings.Fields(os.Getenv("VEX_CFLAGS"))...)
	args = append(args, "-o", out, cfile, "-lm")
	cmd := exec.Command(command[0], args...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("the C compiler (%s) failed on generated code; this is a bug in vex\n%s", command[0], strings.TrimSpace(output.String()))
		}
		return fmt.Errorf("running the C compiler (%s): %w", command[0], err)
	}
	return nil
}
