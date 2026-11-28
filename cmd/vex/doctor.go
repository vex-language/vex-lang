package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vex-language/vex-lang/compiler/cc"
	"github.com/vex-language/vex-lang/compiler/modules"
)

const doctorHelp = `Checks the tools Vex builds with and says how to install what is missing.
It never installs anything itself.

The C toolchain is needed to build programs, git to download packages with
vex get, and Xcode (on macOS) and the Android SDK, NDK and a JDK only to
build apps for phones, so problems with those are warnings.

Flags:
  -v, --verbose     show where each tool was found, and more details

The exit status is 1 when Vex can't build programs, otherwise 0.
`

// How long one probe of an outside tool may take before it counts as
// failed, so a tool that hangs can't hang the doctor.
const (
	probeTimeout   = 10 * time.Second
	compileTimeout = time.Minute
)

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	verbose := fs.Bool("v", false, "show more details")
	fs.BoolVar(verbose, "verbose", false, "show more details")
	fs.Usage = func() { fmt.Fprintln(stderr, "Usage: vex doctor [-v]") }
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return exitUsage
	}
	d := &doctor{sys: hostSystem{}, std: modules.StdModules()}
	if !render(stdout, d.sections(), *verbose, useColor(stdout)) {
		return exitError
	}
	return exitOK
}

// The machine the doctor examines, behind an interface so tests can
// fake its programs and files.
type system interface {
	goos() string
	getenv(key string) string
	home() string
	executable() (string, error)
	lookPath(name string) (string, error)

	// Runs a program, stopping it after [timeout], and returns what it
	// printed to stdout and stderr.
	run(timeout time.Duration, name string, args ...string) (string, error)

	exists(path string) bool
	readFile(path string) ([]byte, error)
	readDir(path string) ([]string, error)

	// Returns the C compiler the build would use.
	ccCommand() (cc.Compiler, error)

	// Compiles and links the C source [src] with [command] in a temporary
	// folder, adding [args] after the source file, and returns the
	// compiler's output.
	compile(command []string, src string, args ...string) (string, error)
}

// The [system] vex runs on.
type hostSystem struct{}

func (hostSystem) goos() string                          { return runtime.GOOS }
func (hostSystem) getenv(key string) string              { return os.Getenv(key) }
func (hostSystem) executable() (string, error)           { return os.Executable() }
func (hostSystem) lookPath(name string) (string, error)  { return exec.LookPath(name) }
func (hostSystem) readFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func (hostSystem) ccCommand() (cc.Compiler, error)       { return cc.Find() }
func (hostSystem) exists(path string) bool               { _, err := os.Stat(path); return err == nil }
func (hostSystem) home() string                          { dir, _ := os.UserHomeDir(); return dir }
func (hostSystem) readDir(path string) ([]string, error) { return readDirNames(path) }

func (hostSystem) run(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("%s didn't finish within %s", filepath.Base(name), timeout)
	}
	return string(out), err
}

func (h hostSystem) compile(command []string, src string, args ...string) (string, error) {
	dir, err := os.MkdirTemp("", "vex-doctor-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "main.c")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		return "", err
	}
	all := append(command[1:len(command):len(command)], "-o", filepath.Join(dir, "main"), file)
	return h.run(compileTimeout, command[0], append(all, args...)...)
}

// Returns the names of the entries of the folder at [path].
func readDirNames(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, err
}

// The outcome of one check, which also marks a section.
type mark int

const (
	markOK mark = iota
	markWarn
	markFail
)

// One finding of a [section], with how to fix it when it's a problem.
type note struct {
	mark mark
	text string
	fix  string
}

// A group of checks, such as the C toolchain, shown as one line.
type section struct {
	title string

	// What was found, such as a version, shown after the title.
	summary string

	// Set for sections Vex can't build programs without; a problem in
	// any other section is a warning.
	required bool

	notes []note
}

func (s *section) ok(format string, args ...any) {
	s.notes = append(s.notes, note{markOK, fmt.Sprintf(format, args...), ""})
}

func (s *section) warn(text, fix string) { s.notes = append(s.notes, note{markWarn, text, fix}) }

func (s *section) fail(text, fix string) {
	m := markFail
	if !s.required {
		m = markWarn
	}
	s.notes = append(s.notes, note{m, text, fix})
}

// Returns the worst mark of the section's notes.
func (s *section) mark() mark {
	worst := markOK
	for _, n := range s.notes {
		worst = max(worst, n.mark)
	}
	return worst
}

// Writes the report on [sections] to [w], with every finding when
// [verbose] is set and only the problems otherwise. Reports whether every
// required section passed.
func render(w io.Writer, sections []*section, verbose, color bool) bool {
	paint := func(m mark, s string) string {
		if !color {
			return s
		}
		return [...]string{"\x1b[32m", "\x1b[33m", "\x1b[31m"}[m] + s + "\x1b[0m"
	}
	bullets := [...]string{"•", "!", "✗"}
	if !verbose {
		fmt.Fprintln(w, "Doctor summary (to see all details, run vex doctor -v):")
	}
	ok, issues := true, 0
	for i, s := range sections {
		m := s.mark()
		if m != markOK {
			issues++
		}
		if m == markFail && s.required {
			ok = false
		}
		if verbose && i > 0 {
			fmt.Fprintln(w)
		}
		line := "[" + paint(m, [...]string{"✓", "!", "✗"}[m]) + "] " + s.title
		if s.summary != "" {
			line += " (" + s.summary + ")"
		}
		fmt.Fprintln(w, line)
		for _, n := range s.notes {
			if n.mark == markOK && !verbose {
				continue
			}
			fmt.Fprintln(w, "    "+paint(n.mark, bullets[n.mark])+" "+strings.ReplaceAll(n.text, "\n", "\n      "))
			if n.fix != "" {
				fmt.Fprintln(w, "      "+n.fix)
			}
		}
	}
	fmt.Fprintln(w)
	switch {
	case issues == 0:
		fmt.Fprintln(w, paint(markOK, "•")+" No issues found!")
	case issues == 1:
		fmt.Fprintln(w, paint(markWarn, "!")+" Doctor found issues in 1 category.")
	default:
		fmt.Fprintf(w, "%s Doctor found issues in %d categories.\n", paint(markWarn, "!"), issues)
	}
	return ok
}

// Examines a [system] for the tools Vex builds with.
type doctor struct {
	sys system

	// The modules of the standard library built into vex.
	std []string

	// On macOS, the active developer folder (`xcode-select -p`), or ""
	// when neither Xcode nor its Command Line Tools are installed.
	devDir string
}

// Returns the checked sections, in the order they're shown.
func (d *doctor) sections() []*section {
	if d.sys.goos() == "darwin" {
		if out, err := d.sys.run(probeTimeout, "xcode-select", "-p"); err == nil {
			d.devDir = strings.TrimSpace(out)
		}
	}
	list := []*section{d.vex(), d.toolchain(), d.git()}
	if d.sys.goos() == "darwin" {
		list = append(list, d.xcode())
	}
	return append(list, d.android())
}

// Reports whether [path] is one of the stubs macOS keeps in /usr/bin,
// which ask to install the Command Line Tools when they're missing
// instead of running.
func (d *doctor) stub(path string) bool {
	return d.sys.goos() == "darwin" && d.devDir == "" && strings.HasPrefix(filepath.ToSlash(path), "/usr/bin/")
}

func (d *doctor) vex() *section {
	s := &section{title: "Vex", summary: version, required: true}
	if exe, err := d.sys.executable(); err == nil {
		s.ok("Vex %s at %s", version, exe)
	} else {
		s.ok("Vex %s", version)
	}
	if len(d.std) == 0 {
		s.fail("The standard library is missing from this vex", "Build vex again with ./build.sh")
	} else {
		s.ok("Standard library built in: %s", strings.Join(d.std, ", "))
	}
	return s
}

const testC = `#include <math.h>
#include <pthread.h>
#include <stdio.h>

int main(void) {
    printf("%f\n", sqrt(2.0));
    return 0;
}
`

const cryptoC = `#include <openssl/evp.h>
#if OPENSSL_VERSION_MAJOR < 3
#error vex:crypto needs OpenSSL 3
#endif

int main(void) { return EVP_MD_fetch(NULL, "SHA256", NULL) == NULL; }
`

func (d *doctor) toolchain() *section {
	s := &section{title: "C toolchain - builds Vex programs", required: true}
	goos := d.sys.goos()
	compiler, err := d.sys.ccCommand()
	command, bundled := compiler.Command, compiler.Origin == cc.FromSDK
	working := false
	switch {
	case err != nil:
		s.summary = "no C compiler"
		s.fail("No C compiler was found", installCC(goos))
	case d.stub(command[0]):
		s.summary = "no C compiler"
	default:
		from := [...]string{cc.FromPath: "found on PATH", cc.FromEnv: "from $CC", cc.FromSDK: "bundled with the Vex SDK"}[compiler.Origin]
		out, err := d.sys.run(probeTimeout, command[0], append(command[1:len(command):len(command)], "--version")...)
		if err != nil {
			s.summary = "not working"
			s.fail(fmt.Sprintf("Couldn't run the C compiler %s: %v", strings.Join(command, " "), err), fixCC(goos, bundled))
			break
		}
		s.summary = compilerVersion(out)
		s.ok("%s at %s (%s)", s.summary, strings.Join(command, " "), from)
		if out, err := d.sys.compile(command, testC, "-pthread", "-lm"); err != nil {
			fix := fixCompile(goos, out)
			if bundled {
				fix = fixSDK
			}
			s.fail("A test program didn't compile and link:\n"+trimOutput(out, err), fix)
			break
		}
		s.ok("A test program compiles and links")
		working = true
	}
	switch goos {
	case "darwin":
		if d.devDir == "" && working {
			s.warn("The Xcode Command Line Tools aren't installed; they include swiftc, which vex:crypto needs", "Install them with: xcode-select --install")
			break
		}
		if d.devDir == "" {
			s.fail("The Xcode Command Line Tools aren't installed; they include clang", "Install them with: xcode-select --install")
			break
		}
		s.ok("Developer tools at %s", d.devDir)
		if out, err := d.sys.run(probeTimeout, "xcrun", "--find", "swiftc"); err != nil {
			s.warn("swiftc wasn't found; vex:crypto needs it on Apple systems", "Install the Xcode Command Line Tools with: xcode-select --install")
		} else {
			s.ok("swiftc at %s, for vex:crypto", strings.TrimSpace(out))
		}
	case "windows":
	default:
		if !working {
			break
		}
		if _, err := d.sys.compile(command, cryptoC, "-lcrypto"); err != nil && bundled {
			s.warn("The SDK's toolchain is missing OpenSSL 3's libcrypto, which vex:crypto needs", fixSDK)
		} else if err != nil {
			s.warn("OpenSSL 3's libcrypto wasn't found; vex:crypto needs it", "Install it, such as with: sudo apt install libssl-dev (Debian, Ubuntu) or sudo dnf install openssl-devel (Fedora)")
		} else {
			s.ok("OpenSSL 3's libcrypto, for vex:crypto")
		}
	}
	return s
}

// How to fix the toolchain bundled with the Vex SDK.
const fixSDK = "Download and unpack the Vex SDK again, or set $CC to another C compiler"

// Returns how to fix a C compiler on [goos] that doesn't run; [bundled]
// is set when it came with the Vex SDK.
func fixCC(goos string, bundled bool) string {
	if bundled {
		return fixSDK
	}
	return installCC(goos)
}

// Returns how to install a C compiler on [goos].
func installCC(goos string) string {
	switch goos {
	case "darwin":
		return "Install the Xcode Command Line Tools with: xcode-select --install, or set $CC"
	case "windows":
		return "Install LLVM-MinGW with: winget install -e --id MartinStorsjo.LLVM-MinGW.UCRT, or set $CC"
	}
	return "Install clang, such as with: sudo apt install clang (Debian, Ubuntu) or sudo dnf install clang (Fedora), or set $CC"
}

// Returns how to fix a C compiler on [goos] that failed to build a test
// program with [output].
func fixCompile(goos, output string) string {
	switch {
	case goos == "darwin" && strings.Contains(output, "license"):
		return "Accept the Xcode license with: sudo xcodebuild -license accept"
	case goos == "darwin":
		return "Install the Xcode Command Line Tools again with: xcode-select --install, or set $CC to another compiler"
	case goos == "windows":
		return "Install LLVM-MinGW again with: winget install -e --id MartinStorsjo.LLVM-MinGW.UCRT, or set $CC to another compiler"
	}
	return "Install the C library headers, such as with: sudo apt install build-essential (Debian, Ubuntu) or sudo dnf install glibc-devel (Fedora)"
}

var versionPattern = regexp.MustCompile(`^(.*?)\s+version\s+(\S+)`)

// Returns the compiler and version named by `cc --version` output, such
// as "Apple clang 17.0.0".
func compilerVersion(out string) string {
	line := firstLine(out)
	if m := versionPattern.FindStringSubmatch(line); m != nil {
		return m[1] + " " + m[2]
	}
	return line
}

// Returns the first lines of a failed tool's [output], or [err] when it
// printed nothing.
func trimOutput(output string, err error) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 6 {
		lines = append(lines[:6], "...")
	}
	if lines[0] == "" {
		return err.Error()
	}
	return strings.Join(lines, "\n")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

func (d *doctor) git() *section {
	s := &section{title: "git - downloads packages for vex get"}
	goos := d.sys.goos()
	fix := "Install it, such as with: sudo apt install git (Debian, Ubuntu) or sudo dnf install git (Fedora)"
	switch goos {
	case "darwin":
		fix = "Install the Xcode Command Line Tools, which include it, with: xcode-select --install"
	case "windows":
		fix = "Install it with: winget install -e --id Git.Git"
	}
	path, err := d.sys.lookPath("git")
	if err != nil || d.stub(path) {
		s.summary = "not found"
		s.warn("git wasn't found", fix)
		return s
	}
	out, err := d.sys.run(probeTimeout, path, "--version")
	if err != nil {
		s.summary = "not working"
		s.warn(fmt.Sprintf("Couldn't run %s: %v", path, err), fix)
		return s
	}
	v := strings.TrimPrefix(firstLine(out), "git version ")
	s.summary = "git " + strings.Fields(v + " ")[0]
	s.ok("git %s at %s", v, path)
	return s
}

const xcodeApp = "/Applications/Xcode.app"

func (d *doctor) xcode() *section {
	s := &section{title: "Xcode - builds iOS apps"}
	var out string
	err := errors.New("no developer tools")
	if d.devDir != "" {
		out, err = d.sys.run(probeTimeout, "xcodebuild", "-version")
	}
	if err != nil {
		if d.sys.exists(xcodeApp) {
			s.summary = "not selected"
			s.warn("Xcode is installed, but the active developer tools are "+d.devDir, "Select Xcode with: sudo xcode-select -s "+xcodeApp+"/Contents/Developer")
		} else {
			s.summary = "not installed"
			s.warn("Xcode isn't installed; iOS apps need it", "Install it from the App Store, then run: sudo xcode-select -s "+xcodeApp+"/Contents/Developer")
		}
		return s
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	s.summary = strings.TrimSpace(lines[0])
	detail := s.summary
	if len(lines) > 1 {
		detail += " (build " + strings.TrimPrefix(strings.TrimSpace(lines[1]), "Build version ") + ")"
	}
	s.ok("%s at %s", detail, d.devDir)
	if _, err := d.sys.run(probeTimeout, "xcodebuild", "-license", "check"); err != nil {
		s.warn("The Xcode license hasn't been accepted", "Accept it with: sudo xcodebuild -license accept")
	} else {
		s.ok("Xcode license accepted")
	}
	out, err = d.sys.run(3*probeTimeout, "xcrun", "simctl", "list", "runtimes")
	var runtimes []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "iOS ") && !strings.Contains(line, "unavailable") {
			name, _, _ := strings.Cut(line, " (")
			runtimes = append(runtimes, name)
		}
	}
	if err != nil || len(runtimes) == 0 {
		s.warn("No iOS simulator runtime is installed", "Install one with: xcodebuild -downloadPlatform iOS, or in Xcode > Settings > Components")
	} else {
		s.ok("Simulator runtimes: %s", strings.Join(runtimes, ", "))
	}
	return s
}

const sdkTools = "Android Studio > Settings > Languages & Frameworks > Android SDK > SDK Tools"

func (d *doctor) android() *section {
	s := &section{title: "Android toolchain - builds Android apps"}
	studio := d.androidStudio(s)
	sdk := d.androidSDK(s)
	if sdk == "" {
		s.summary = "no SDK"
		s.warn("The Android SDK wasn't found", "Install Android Studio from https://developer.android.com/studio and open it once to download the SDK, or set $ANDROID_HOME")
	} else {
		d.androidTools(s, sdk)
	}
	d.java(s, studio)
	return s
}

// Returns the folder of Android Studio, or "" when it isn't found.
func (d *doctor) androidStudio(s *section) string {
	home := d.sys.home()
	var dirs []string
	info := "product-info.json"
	switch d.sys.goos() {
	case "darwin":
		for _, apps := range []string{"/Applications", filepath.Join(home, "Applications")} {
			names, _ := d.sys.readDir(apps)
			for _, name := range names {
				if strings.HasPrefix(name, "Android Studio") && strings.HasSuffix(name, ".app") {
					dirs = append(dirs, filepath.Join(apps, name))
				}
			}
		}
		info = filepath.Join("Contents", "Resources", info)
	case "windows":
		dirs = []string{filepath.Join(d.sys.getenv("ProgramFiles"), "Android", "Android Studio")}
	default:
		dirs = []string{"/opt/android-studio", filepath.Join(home, "android-studio"), "/usr/local/android-studio", "/snap/android-studio/current"}
	}
	for _, dir := range dirs {
		if !d.sys.exists(filepath.Join(dir, info)) {
			continue
		}
		var product struct{ DataDirectoryName string }
		text, _ := d.sys.readFile(filepath.Join(dir, info))
		json.Unmarshal(text, &product)
		v := strings.TrimPrefix(product.DataDirectoryName, "AndroidStudio")
		if v != "" {
			v += " "
		}
		s.ok("Android Studio %sat %s", v, dir)
		return dir
	}
	s.warn("Android Studio wasn't found", "Install it from https://developer.android.com/studio")
	return ""
}

// Returns the folder of the Android SDK, or "" when it isn't found.
func (d *doctor) androidSDK(s *section) string {
	for _, key := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		dir := d.sys.getenv(key)
		if dir == "" {
			continue
		}
		if d.sys.exists(dir) {
			s.ok("Android SDK at %s (from $%s)", dir, key)
			return dir
		}
		s.warn(fmt.Sprintf("$%s is %s, which doesn't exist", key, dir), "Set $"+key+" to the Android SDK folder, or unset it")
	}
	dir := filepath.Join(d.sys.home(), "Android", "Sdk")
	switch d.sys.goos() {
	case "darwin":
		dir = filepath.Join(d.sys.home(), "Library", "Android", "sdk")
	case "windows":
		dir = filepath.Join(d.sys.getenv("LOCALAPPDATA"), "Android", "Sdk")
	}
	if d.sys.exists(dir) {
		s.ok("Android SDK at %s", dir)
		return dir
	}
	return ""
}

// Checks the platform-tools and NDK of the Android SDK in [sdk].
func (d *doctor) androidTools(s *section, sdk string) {
	adb := filepath.Join(sdk, "platform-tools", "adb")
	if d.sys.goos() == "windows" {
		adb += ".exe"
	}
	if d.sys.exists(adb) {
		rev := d.revision(filepath.Join(sdk, "platform-tools"))
		if rev != "" {
			rev += " "
		}
		s.ok("Platform-tools %sat %s", rev, adb)
	} else {
		s.warn("The Android SDK Platform-Tools (adb) aren't installed", "Install them in "+sdkTools)
	}
	newest := ""
	names, _ := d.sys.readDir(filepath.Join(sdk, "ndk"))
	for _, name := range names {
		if name != "" && name[0] >= '0' && name[0] <= '9' && (newest == "" || compareVersions(name, newest) > 0) {
			newest = name
		}
	}
	ndk := filepath.Join(sdk, "ndk", newest)
	if newest == "" && d.sys.exists(filepath.Join(sdk, "ndk-bundle")) {
		ndk = filepath.Join(sdk, "ndk-bundle")
		newest = d.revision(ndk)
	}
	if newest == "" {
		s.warn("No Android NDK is installed; Vex code for Android is compiled with it", "Install NDK (Side by side) in "+sdkTools)
		return
	}
	s.summary = "NDK " + newest
	s.ok("NDK %s at %s", newest, ndk)
}

// Returns the Pkg.Revision of the SDK package in [dir], or "".
func (d *doctor) revision(dir string) string {
	text, _ := d.sys.readFile(filepath.Join(dir, "source.properties"))
	for _, line := range strings.Split(string(text), "\n") {
		if key, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(key) == "Pkg.Revision" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Checks for a JDK of version 17 or newer, preferring $JAVA_HOME, which
// Gradle uses, then the one inside Android Studio at [studio].
func (d *doctor) java(s *section, studio string) {
	goos := d.sys.goos()
	bin := func(home string) string {
		java := filepath.Join(home, "bin", "java")
		if goos == "windows" {
			java += ".exe"
		}
		return java
	}
	bundled := ""
	if studio != "" {
		bundled = filepath.Join(studio, "jbr")
		if goos == "darwin" {
			bundled = filepath.Join(studio, "Contents", "jbr", "Contents", "Home")
		}
		if !d.sys.exists(bin(bundled)) {
			bundled = ""
		}
	}
	java, from := "", ""
	if home := d.sys.getenv("JAVA_HOME"); home != "" {
		if d.sys.exists(bin(home)) {
			java, from = bin(home), "$JAVA_HOME"
		} else {
			s.warn("$JAVA_HOME is "+home+", which has no bin/java", "Set $JAVA_HOME to a JDK folder, or unset it")
		}
	}
	if java == "" && bundled != "" {
		java, from = bin(bundled), "Android Studio"
	}
	if java == "" && goos == "darwin" {
		if out, err := d.sys.run(probeTimeout, "/usr/libexec/java_home"); err == nil && d.sys.exists(bin(strings.TrimSpace(out))) {
			java, from = bin(strings.TrimSpace(out)), "java_home"
		}
	}
	if java == "" && goos != "darwin" {
		if path, err := d.sys.lookPath("java"); err == nil {
			java, from = path, "PATH"
		}
	}
	fix := "Install JDK 17 or newer, such as from https://adoptium.net, and set $JAVA_HOME to it"
	if bundled != "" && java != bin(bundled) {
		fix = "Set $JAVA_HOME to Android Studio's JDK: " + bundled
	}
	if java == "" {
		s.warn("No Java was found; Android builds need JDK 17 or newer", "Install Android Studio, which includes one, or a JDK from https://adoptium.net")
		return
	}
	out, err := d.sys.run(probeTimeout, java, "-version")
	if err != nil {
		s.warn(fmt.Sprintf("Couldn't run %s: %v", java, err), fix)
		return
	}
	v, major := javaVersion(out)
	if major < 17 {
		s.warn(fmt.Sprintf("Java %s at %s (from %s) is too old; Android builds need JDK 17 or newer", v, java, from), fix)
		return
	}
	s.ok("Java %s at %s (from %s)", v, java, from)
}

var javaPattern = regexp.MustCompile(`version "([^"]+)"`)

// Returns the version named by `java -version` output and its major
// number, which is 8 for "1.8.0_392"; the number is 0 when it isn't found.
func javaVersion(out string) (string, int) {
	m := javaPattern.FindStringSubmatch(out)
	if m == nil {
		return firstLine(out), 0
	}
	parts := strings.FieldsFunc(m[1], func(r rune) bool { return r < '0' || r > '9' })
	if len(parts) > 1 && parts[0] == "1" {
		parts = parts[1:]
	}
	major := 0
	if len(parts) > 0 {
		major, _ = strconv.Atoi(parts[0])
	}
	return m[1], major
}

// Compares dotted version numbers such as "26.1.10909125", returning -1,
// 0 or 1.
func compareVersions(a, b string) int {
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(x), len(y)); i++ {
		var p, q int
		if i < len(x) {
			p, _ = strconv.Atoi(x[i])
		}
		if i < len(y) {
			q, _ = strconv.Atoi(y[i])
		}
		if p != q {
			if p < q {
				return -1
			}
			return 1
		}
	}
	return 0
}
