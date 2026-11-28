// Package cc runs the system C compiler on generated code.
package cc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Flags passed to every compile. `-fwrapv` makes int overflow wrap around
// instead of being undefined; `-w` hides warnings about generated code.
var flags = []string{"-std=gnu11", "-fwrapv", "-w", "-pthread"}

// The sanitizers `--sanitize` accepts.
var sanitizers = []string{"address", "undefined", "thread"}

// Returns the sanitizers named in [list], such as "address,undefined", or
// an error naming one that isn't known or a pair that can't be combined.
func ParseSanitizers(list string) ([]string, error) {
	var out []string
	for _, s := range strings.Split(list, ",") {
		s = strings.TrimSpace(s)
		if !slices.Contains(sanitizers, s) {
			return nil, fmt.Errorf("unknown sanitizer %q; use %s", s, strings.Join(sanitizers, ", "))
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if slices.Contains(out, "thread") && slices.Contains(out, "address") {
		return nil, errors.New("the thread sanitizer can't be combined with address")
	}
	return out, nil
}

// Returns the C compiler command: `$CC` if set (it may include arguments),
// otherwise clang, otherwise cc.
func Command() ([]string, error) {
	env := os.Getenv("CC")
	// A path with spaces, such as C:\Program Files\LLVM\bin\clang.exe,
	// names the compiler alone.
	if info, err := os.Stat(env); env != "" && err == nil && !info.IsDir() {
		return []string{env}, nil
	}
	if fields := strings.Fields(env); len(fields) > 0 {
		return fields, nil
	}
	for _, name := range []string{"clang", "cc", "gcc"} {
		if path, err := exec.LookPath(name); err == nil {
			return []string{path}, nil
		}
	}
	if runtime.GOOS == "windows" {
		return nil, errors.New("no C compiler found; install LLVM-MinGW (winget install MartinStorsjo.LLVM-MinGW.UCRT) or set $CC")
	}
	return nil, errors.New("no C compiler found; install clang (on macOS: xcode-select --install) or set $CC")
}

// Returns the file name of the program [name]: on Windows it ends in
// .exe, which is added when missing.
func Executable(name string) string {
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(name), ".exe") {
		return name + ".exe"
	}
	return name
}

// C code a program brings along: source files and built objects to
// compile and link with it, libraries to link, and extra flags.
type Options struct {
	Sources    []string
	Libs       []string
	Flags      []string
	Frameworks []string

	// C files the standard library brings along, written next to the
	// generated C: `.c` files are compiled with it, `.swift` files are
	// compiled by swiftc and linked with the Swift runtime, and headers can
	// be included by name.
	Files []File

	// Linker arguments the standard library needs, such as -lcrypto.
	LinkArgs []string

	// Set for a debug build: without optimizations and with debug
	// information. Otherwise the program is optimized with -O3.
	Debug bool

	// The sanitizers to build in, such as address and undefined.
	Sanitize []string

	// Where to keep the generated C, so a debugger can show it; empty to
	// write it to a temporary file.
	KeepC string
}

// A C file kept in memory, such as a source file of the standard library.
type File struct {
	Name string
	Text []byte
}

// Returns the flags that set how the program is optimized and checked.
func (o Options) modeFlags() []string {
	out := []string{"-O3"}
	if o.Debug {
		out = []string{"-O0", "-g"}
	}
	if len(o.Sanitize) > 0 {
		out = append(out, "-fsanitize="+strings.Join(o.Sanitize, ","), "-fno-sanitize-recover=all", "-fno-omit-frame-pointer")
		if !o.Debug {
			out = append(out, "-g")
		}
	}
	return out
}

// Compiles the C source [src] into the executable [out]. Flags in
// `$VEX_CFLAGS` are added after the default ones, so they can override them.
func Compile(src, out string) error { return CompileWith(src, out, Options{}) }

// Like [Compile], also building in the C code of [opts].
func CompileWith(src, out string, opts Options) error {
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
	if opts.KeepC != "" {
		cfile = opts.KeepC
	}
	if err := os.WriteFile(cfile, []byte(src), 0o644); err != nil {
		return err
	}
	args := append(command[1:len(command):len(command)], flags...)
	if runtime.GOOS == "windows" {
		// The main thread gets 8 MB of stack, as on macOS and Linux.
		args = append(args, "-Wl,--stack,8388608")
	}
	args = append(args, opts.modeFlags()...)
	args = append(args, strings.Fields(os.Getenv("VEX_CFLAGS"))...)
	args = append(args, opts.Flags...)
	args = append(args, "-o", out, cfile)
	if len(opts.Files) > 0 {
		args = append(args, "-I", dir)
	}
	for _, f := range opts.Files {
		path := filepath.Join(dir, f.Name)
		if err := os.WriteFile(path, f.Text, 0o644); err != nil {
			return err
		}
		switch filepath.Ext(f.Name) {
		case ".c":
			args = append(args, path)
		case ".swift":
			obj, err := compileSwift(path)
			if err != nil {
				return err
			}
			args = append(args, obj)
			if sdk, err := exec.Command("xcrun", "--show-sdk-path").Output(); err == nil {
				args = append(args, "-L", filepath.Join(strings.TrimSpace(string(sdk)), "usr", "lib", "swift"))
			}
		}
	}
	for _, s := range opts.Sources {
		if filepath.Ext(s) == ".m" {
			// Objective-C, with automatic reference counting.
			args = append(args, "-x", "objective-c", "-fobjc-arc", s, "-x", "none")
			continue
		}
		args = append(args, s)
	}
	for _, l := range opts.Libs {
		args = append(args, "-l"+l)
	}
	if len(opts.Frameworks) > 0 && runtime.GOOS != "darwin" {
		return fmt.Errorf("frameworks in package.toml (%s) can only be linked on macOS", strings.Join(opts.Frameworks, ", "))
	}
	for _, f := range opts.Frameworks {
		args = append(args, "-framework", f)
	}
	args = append(args, opts.LinkArgs...)
	args = append(args, "-lm")
	cmd := exec.Command(command[0], args...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && slices.Contains(opts.LinkArgs, "-lcrypto") && missingLibcrypto(output.String()) {
			return fmt.Errorf("vex:crypto needs the libcrypto library of OpenSSL 3; install it, such as with apt install libssl-dev or dnf install openssl-devel\n%s", strings.TrimSpace(output.String()))
		}
		if errors.As(err, &exit) && len(opts.Sources)+len(opts.Libs)+len(opts.Flags)+len(opts.Frameworks) > 0 {
			return fmt.Errorf("the C compiler (%s) failed; check the C code and the [c] section of package.toml\n%s", command[0], strings.TrimSpace(output.String()))
		}
		if errors.As(err, &exit) && (strings.Contains(output.String(), "Undefined symbols") || strings.Contains(output.String(), "undefined reference") || strings.Contains(output.String(), "undefined symbol")) {
			return fmt.Errorf("linking failed: a C function named by an extern isn't defined; add its source or library to the [c] section of package.toml\n%s", strings.TrimSpace(output.String()))
		}
		if errors.As(err, &exit) {
			return fmt.Errorf("the C compiler (%s) failed on generated code; this is a bug in vex\n%s", command[0], strings.TrimSpace(output.String()))
		}
		return fmt.Errorf("running the C compiler (%s): %w", command[0], err)
	}
	return nil
}

// Reports whether the C compiler's [output] says OpenSSL's headers or
// library are missing.
func missingLibcrypto(output string) bool {
	return strings.Contains(output, "openssl/") && strings.Contains(output, "No such file") ||
		strings.Contains(output, "-lcrypto") || strings.Contains(output, "library 'crypto' not found")
}

// Compiles the Swift file at [path] into an object file next to it, whose
// path it returns. The object names the Swift libraries it needs, so the
// linker adds them.
func compileSwift(path string) (string, error) {
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		return "", errors.New("vex:crypto uses CryptoKit, which needs swiftc from the Xcode Command Line Tools; install them with xcode-select --install")
	}
	obj := strings.TrimSuffix(path, ".swift") + ".o"
	name := strings.TrimSuffix(filepath.Base(path), ".swift")
	cmd := exec.Command(swiftc, "-parse-as-library", "-emit-object", "-O", "-module-name", "vx_"+name, "-o", obj, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("swiftc failed on %s; this is a bug in vex\n%s", filepath.Base(path), strings.TrimSpace(string(out)))
	}
	return obj, nil
}
