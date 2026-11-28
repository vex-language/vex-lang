package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vex-language/vex-lang/compiler/cc"
)

// A [system] made of canned files and program outputs.
type fakeSystem struct {
	os    string
	env   map[string]string
	files map[string]string
	paths map[string]string

	// Outputs of the programs that run, by command line.
	out map[string]string

	// Command lines that fail, with what they print.
	failing map[string]string

	cc       []string
	ccOrigin cc.Origin
	ccErr    error

	// Compiler output for the C sources that don't compile.
	badC map[string]string
}

func (f *fakeSystem) goos() string             { return f.os }
func (f *fakeSystem) getenv(key string) string { return f.env[key] }
func (f *fakeSystem) home() string             { return filepath.FromSlash("/home/ann") }
func (f *fakeSystem) executable() (string, error) {
	return filepath.FromSlash("/usr/local/bin/vex"), nil
}

func (f *fakeSystem) lookPath(name string) (string, error) {
	if p, ok := f.paths[name]; ok {
		return filepath.FromSlash(p), nil
	}
	return "", errors.New("not found")
}

func (f *fakeSystem) run(_ time.Duration, name string, args ...string) (string, error) {
	line := filepath.ToSlash(strings.Join(append([]string{name}, args...), " "))
	if out, ok := f.failing[line]; ok {
		return out, errors.New("exit status 1")
	}
	if out, ok := f.out[line]; ok {
		return out, nil
	}
	return "", errors.New("executable file not found")
}

func (f *fakeSystem) exists(path string) bool {
	path = filepath.ToSlash(path)
	for p := range f.files {
		if p == path || strings.HasPrefix(p, path+"/") {
			return true
		}
	}
	return false
}

func (f *fakeSystem) readFile(path string) ([]byte, error) {
	if text, ok := f.files[filepath.ToSlash(path)]; ok {
		return []byte(text), nil
	}
	return nil, errors.New("no such file")
}

func (f *fakeSystem) readDir(path string) ([]string, error) {
	prefix := filepath.ToSlash(path) + "/"
	seen := map[string]bool{}
	var names []string
	for p := range f.files {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			name, _, _ := strings.Cut(rest, "/")
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names, nil
}

func (f *fakeSystem) ccCommand() (cc.Compiler, error) {
	return cc.Compiler{Command: f.cc, Origin: f.ccOrigin}, f.ccErr
}

func (f *fakeSystem) compile(_ []string, src string, _ ...string) (string, error) {
	if out, ok := f.badC[src]; ok {
		return out, errors.New("exit status 1")
	}
	return "", nil
}

// Returns a Mac with every tool installed.
func healthyMac() *fakeSystem {
	return &fakeSystem{
		os:  "darwin",
		env: map[string]string{},
		files: map[string]string{
			"/Applications/Xcode.app/Contents/Info.plist":                           "",
			"/Applications/Android Studio.app/Contents/Resources/product-info.json": `{"dataDirectoryName": "AndroidStudio2024.2"}`,
			"/Applications/Android Studio.app/Contents/jbr/Contents/Home/bin/java":  "",
			"/home/ann/Library/Android/sdk/platform-tools/adb":                      "",
			"/home/ann/Library/Android/sdk/platform-tools/source.properties":        "Pkg.Revision=35.0.2\n",
			"/home/ann/Library/Android/sdk/ndk/9.1.0/source.properties":             "",
			"/home/ann/Library/Android/sdk/ndk/27.0.12077973/source.properties":     "",
			"/home/ann/Library/Android/sdk/ndk/26.1.10909125/source.properties":     "",
		},
		paths: map[string]string{"git": "/usr/bin/git"},
		out: map[string]string{
			"xcode-select -p":            "/Applications/Xcode.app/Contents/Developer\n",
			"/usr/bin/clang --version":   "Apple clang version 17.0.0 (clang-1700.0.13.3)\nTarget: arm64-apple-darwin24.4.0\n",
			"xcrun --find swiftc":        "/usr/bin/swiftc\n",
			"/usr/bin/git --version":     "git version 2.39.5 (Apple Git-154)\n",
			"xcodebuild -version":        "Xcode 16.3\nBuild version 16E140\n",
			"xcodebuild -license check":  "",
			"xcrun simctl list runtimes": "== Runtimes ==\niOS 18.4 (18.4 - 22E238) - com.apple.CoreSimulator.SimRuntime.iOS-18-4\n",
			"/Applications/Android Studio.app/Contents/jbr/Contents/Home/bin/java -version": "openjdk version \"21.0.6\" 2025-01-21\n",
		},
		cc: []string{"/usr/bin/clang"},
	}
}

// Returns a Linux machine with a C compiler and git, and nothing for
// Android.
func healthyLinux() *fakeSystem {
	return &fakeSystem{
		os:    "linux",
		env:   map[string]string{},
		files: map[string]string{},
		paths: map[string]string{"git": "/usr/bin/git"},
		out: map[string]string{
			"/usr/bin/cc --version":  "cc (Debian 12.2.0-14) 12.2.0\n",
			"/usr/bin/git --version": "git version 2.39.2\n",
		},
		cc: []string{"/usr/bin/cc"},
	}
}

// Runs the doctor on [sys] and returns its report and whether the
// required sections passed.
func diagnose(sys system, verbose bool) (string, bool) {
	d := &doctor{sys: sys, std: []string{"core", "json"}}
	var out bytes.Buffer
	ok := render(&out, d.sections(), verbose, false)
	return filepath.ToSlash(out.String()), ok
}

func TestDoctorHealthyMac(t *testing.T) {
	out, ok := diagnose(healthyMac(), false)
	want := `Doctor summary (to see all details, run vex doctor -v):
[✓] Vex (` + version + `)
[✓] C toolchain - builds Vex programs (Apple clang 17.0.0)
[✓] git - downloads packages for vex get (git 2.39.5)
[✓] Xcode - builds iOS apps (Xcode 16.3)
[✓] Android toolchain - builds Android apps (NDK 27.0.12077973)

• No issues found!
`
	if !ok || out != want {
		t.Errorf("ok %v, got\n%s\nwant\n%s", ok, out, want)
	}
}

func TestDoctorVerbose(t *testing.T) {
	out, _ := diagnose(healthyMac(), true)
	for _, want := range []string{
		"[✓] Vex (" + version + ")\n    • Vex " + version + " at /usr/local/bin/vex\n    • Standard library built in: core, json\n\n",
		"    • Apple clang 17.0.0 at /usr/bin/clang (found on PATH)\n    • A test program compiles and links\n",
		"    • swiftc at /usr/bin/swiftc, for vex:crypto\n",
		"    • git 2.39.5 (Apple Git-154) at /usr/bin/git\n",
		"    • Xcode 16.3 (build 16E140) at /Applications/Xcode.app/Contents/Developer\n    • Xcode license accepted\n    • Simulator runtimes: iOS 18.4\n",
		"    • Android Studio 2024.2 at /Applications/Android Studio.app\n",
		"    • Android SDK at /home/ann/Library/Android/sdk\n",
		"    • Platform-tools 35.0.2 at /home/ann/Library/Android/sdk/platform-tools/adb\n",
		"    • NDK 27.0.12077973 at /home/ann/Library/Android/sdk/ndk/27.0.12077973\n",
		"    • Java 21.0.6 at /Applications/Android Studio.app/Contents/jbr/Contents/Home/bin/java (from Android Studio)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "Doctor summary") {
		t.Errorf("verbose report has the summary header:\n%s", out)
	}
}

func TestDoctorMacWithoutDeveloperTools(t *testing.T) {
	sys := healthyMac()
	delete(sys.out, "xcode-select -p")
	delete(sys.files, "/Applications/Xcode.app/Contents/Info.plist")
	out, ok := diagnose(sys, false)
	for _, want := range []string{
		"[✗] C toolchain - builds Vex programs (no C compiler)\n    ✗ The Xcode Command Line Tools aren't installed; they include clang\n      Install them with: xcode-select --install\n",
		"[!] git - downloads packages for vex get (not found)\n    ! git wasn't found\n      Install the Xcode Command Line Tools, which include it, with: xcode-select --install\n",
		"[!] Xcode - builds iOS apps (not installed)\n    ! Xcode isn't installed; iOS apps need it\n      Install it from the App Store, then run: sudo xcode-select -s /Applications/Xcode.app/Contents/Developer\n",
		"! Doctor found issues in 3 categories.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if ok {
		t.Error("a Mac without a C compiler passed")
	}
}

func TestDoctorMacCompilerFromCC(t *testing.T) {
	sys := healthyMac()
	delete(sys.out, "xcode-select -p")
	sys.env["CC"] = "/opt/llvm/bin/clang"
	sys.cc, sys.ccOrigin = []string{"/opt/llvm/bin/clang"}, cc.FromEnv
	sys.out["/opt/llvm/bin/clang --version"] = "clang version 19.1.7\n"
	out, ok := diagnose(sys, true)
	for _, want := range []string{
		"[!] C toolchain - builds Vex programs (clang 19.1.7)\n    • clang 19.1.7 at /opt/llvm/bin/clang (from $CC)\n",
		"    ! The Xcode Command Line Tools aren't installed; they include swiftc, which vex:crypto needs\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if !ok {
		t.Error("a working compiler from $CC failed the check")
	}
}

func TestDoctorCompileFailure(t *testing.T) {
	sys := healthyMac()
	sys.badC = map[string]string{testC: "You have not agreed to the Xcode license agreements.\n"}
	out, ok := diagnose(sys, false)
	want := "[✗] C toolchain - builds Vex programs (Apple clang 17.0.0)\n    ✗ A test program didn't compile and link:\n      You have not agreed to the Xcode license agreements.\n      Accept the Xcode license with: sudo xcodebuild -license accept\n"
	if ok || !strings.Contains(out, want) {
		t.Errorf("ok %v, missing %q in\n%s", ok, want, out)
	}
}

func TestDoctorXcodeProblems(t *testing.T) {
	sys := healthyMac()
	sys.failing = map[string]string{"xcodebuild -license check": ""}
	sys.out["xcrun simctl list runtimes"] = "== Runtimes ==\nwatchOS 11.4 (11.4 - 22T250) - com.apple.CoreSimulator.SimRuntime.watchOS-11-4\n"
	out, ok := diagnose(sys, false)
	want := "[!] Xcode - builds iOS apps (Xcode 16.3)\n" +
		"    ! The Xcode license hasn't been accepted\n      Accept it with: sudo xcodebuild -license accept\n" +
		"    ! No iOS simulator runtime is installed\n      Install one with: xcodebuild -downloadPlatform iOS, or in Xcode > Settings > Components\n"
	if !ok || !strings.Contains(out, want) {
		t.Errorf("ok %v, missing %q in\n%s", ok, want, out)
	}

	sys = healthyMac()
	sys.out["xcode-select -p"] = "/Library/Developer/CommandLineTools\n"
	delete(sys.out, "xcodebuild -version")
	out, _ = diagnose(sys, false)
	want = "[!] Xcode - builds iOS apps (not selected)\n    ! Xcode is installed, but the active developer tools are /Library/Developer/CommandLineTools\n      Select Xcode with: sudo xcode-select -s /Applications/Xcode.app/Contents/Developer\n"
	if !strings.Contains(out, want) {
		t.Errorf("missing %q in\n%s", want, out)
	}
}

func TestDoctorLinux(t *testing.T) {
	sys := healthyLinux()
	out, ok := diagnose(sys, false)
	for _, want := range []string{
		"[✓] C toolchain - builds Vex programs (cc (Debian 12.2.0-14) 12.2.0)\n",
		"[✓] git - downloads packages for vex get (git 2.39.2)\n",
		"[!] Android toolchain - builds Android apps (no SDK)\n" +
			"    ! Android Studio wasn't found\n      Install it from https://developer.android.com/studio\n" +
			"    ! The Android SDK wasn't found\n      Install Android Studio from https://developer.android.com/studio and open it once to download the SDK, or set $ANDROID_HOME\n" +
			"    ! No Java was found; Android builds need JDK 17 or newer\n",
		"! Doctor found issues in 1 category.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "Xcode") {
		t.Errorf("Linux report checks Xcode:\n%s", out)
	}
	if !ok {
		t.Error("missing Android tools failed the check")
	}

	sys.badC = map[string]string{cryptoC: "fatal error: openssl/evp.h: No such file or directory\n"}
	out, ok = diagnose(sys, false)
	want := "[!] C toolchain - builds Vex programs (cc (Debian 12.2.0-14) 12.2.0)\n    ! OpenSSL 3's libcrypto wasn't found; vex:crypto needs it\n      Install it, such as with: sudo apt install libssl-dev (Debian, Ubuntu) or sudo dnf install openssl-devel (Fedora)\n"
	if !ok || !strings.Contains(out, want) {
		t.Errorf("ok %v, missing %q in\n%s", ok, want, out)
	}
}

func TestDoctorBundledToolchain(t *testing.T) {
	sys := healthyLinux()
	clang := "/opt/vex sdk/toolchain/bin/clang"
	sys.cc, sys.ccOrigin = []string{clang}, cc.FromSDK
	sys.out[clang+" --version"] = "clang version 23.1.6\n"
	out, ok := diagnose(sys, true)
	want := "    • clang 23.1.6 at " + clang + " (bundled with the Vex SDK)\n    • A test program compiles and links\n    • OpenSSL 3's libcrypto, for vex:crypto\n"
	if !ok || !strings.Contains(out, want) {
		t.Errorf("ok %v, missing %q in\n%s", ok, want, out)
	}

	sys.badC = map[string]string{cryptoC: "ld.lld: error: unable to find library -lcrypto\n"}
	out, _ = diagnose(sys, false)
	want = "    ! The SDK's toolchain is missing OpenSSL 3's libcrypto, which vex:crypto needs\n      " + fixSDK + "\n"
	if !strings.Contains(out, want) {
		t.Errorf("missing %q in\n%s", want, out)
	}

	sys.badC = map[string]string{testC: "ld.lld: error: cannot open crt1.o\n"}
	out, ok = diagnose(sys, false)
	want = "    ✗ A test program didn't compile and link:\n      ld.lld: error: cannot open crt1.o\n      " + fixSDK + "\n"
	if ok || !strings.Contains(out, want) {
		t.Errorf("ok %v, missing %q in\n%s", ok, want, out)
	}
}

func TestDoctorNoCompiler(t *testing.T) {
	for goos, fix := range map[string]string{
		"linux":   "Install clang, such as with: sudo apt install clang (Debian, Ubuntu) or sudo dnf install clang (Fedora), or set $CC",
		"windows": "Install LLVM-MinGW with: winget install -e --id MartinStorsjo.LLVM-MinGW.UCRT, or set $CC",
	} {
		sys := healthyLinux()
		sys.os = goos
		sys.cc, sys.ccErr = nil, errors.New("no C compiler found")
		out, ok := diagnose(sys, false)
		want := "[✗] C toolchain - builds Vex programs (no C compiler)\n    ✗ No C compiler was found\n      " + fix + "\n"
		if ok || !strings.Contains(out, want) {
			t.Errorf("%s: ok %v, missing %q in\n%s", goos, ok, want, out)
		}
	}
}

func TestDoctorGitMissing(t *testing.T) {
	sys := healthyLinux()
	sys.os = "windows"
	delete(sys.paths, "git")
	out, ok := diagnose(sys, false)
	want := "[!] git - downloads packages for vex get (not found)\n    ! git wasn't found\n      Install it with: winget install -e --id Git.Git\n"
	if !ok || !strings.Contains(out, want) {
		t.Errorf("ok %v, missing %q in\n%s", ok, want, out)
	}
}

func TestDoctorAndroid(t *testing.T) {
	sys := healthyLinux()
	sys.env["ANDROID_HOME"] = "/gone"
	sys.env["ANDROID_SDK_ROOT"] = "/sdk"
	sys.env["JAVA_HOME"] = "/jdk11"
	sys.files = map[string]string{
		"/opt/android-studio/product-info.json": `{"dataDirectoryName": "AndroidStudio2024.1"}`,
		"/opt/android-studio/jbr/bin/java":      "",
		"/sdk/ndk-bundle/source.properties":     "Pkg.Revision = 21.4.7075529\n",
		"/jdk11/bin/java":                       "",
	}
	sys.out["/jdk11/bin/java -version"] = "openjdk version \"11.0.26\" 2025-01-21\n"
	out, ok := diagnose(sys, true)
	want := "[!] Android toolchain - builds Android apps (NDK 21.4.7075529)\n" +
		"    • Android Studio 2024.1 at /opt/android-studio\n" +
		"    ! $ANDROID_HOME is /gone, which doesn't exist\n      Set $ANDROID_HOME to the Android SDK folder, or unset it\n" +
		"    • Android SDK at /sdk (from $ANDROID_SDK_ROOT)\n" +
		"    ! The Android SDK Platform-Tools (adb) aren't installed\n      Install them in " + sdkTools + "\n" +
		"    • NDK 21.4.7075529 at /sdk/ndk-bundle\n" +
		"    ! Java 11.0.26 at /jdk11/bin/java (from $JAVA_HOME) is too old; Android builds need JDK 17 or newer\n" +
		"      Set $JAVA_HOME to Android Studio's JDK: /opt/android-studio/jbr\n"
	if !ok || !strings.Contains(out, want) {
		t.Errorf("ok %v, missing %q in\n%s", ok, want, out)
	}

	sys = healthyLinux()
	sys.files = map[string]string{"/home/ann/Android/Sdk/platform-tools/adb": ""}
	sys.paths["java"] = "/usr/bin/java"
	sys.out["/usr/bin/java -version"] = "openjdk version \"17.0.14\" 2025-01-21\n"
	out, _ = diagnose(sys, true)
	for _, want := range []string{
		"    • Android SDK at /home/ann/Android/Sdk\n",
		"    • Platform-tools at /home/ann/Android/Sdk/platform-tools/adb\n",
		"    ! No Android NDK is installed; Vex code for Android is compiled with it\n      Install NDK (Side by side) in " + sdkTools + "\n",
		"    • Java 17.0.14 at /usr/bin/java (from PATH)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestDoctorStandardLibraryMissing(t *testing.T) {
	d := &doctor{sys: healthyLinux()}
	var out bytes.Buffer
	if render(&out, d.sections(), false, false) || !strings.Contains(out.String(), "[✗] Vex") {
		t.Errorf("missing standard library passed:\n%s", out.String())
	}
}

func TestDoctorVersions(t *testing.T) {
	for out, want := range map[string]string{
		"Apple clang version 17.0.0 (clang-1700.0.13.3)\nTarget: x": "Apple clang 17.0.0",
		"Ubuntu clang version 18.1.3 (1ubuntu1)":                    "Ubuntu clang 18.1.3",
		"gcc (GCC) 14.2.1 20250110":                                 "gcc (GCC) 14.2.1 20250110",
	} {
		if got := compilerVersion(out); got != want {
			t.Errorf("compilerVersion(%q) = %q, want %q", out, got, want)
		}
	}
	for out, want := range map[string]int{
		`openjdk version "17.0.11" 2024-04-16`: 17,
		`java version "1.8.0_392"`:             8,
		`openjdk version "21" 2023-09-19`:      21,
		"Unable to locate a Java Runtime.":     0,
	} {
		if _, got := javaVersion(out); got != want {
			t.Errorf("javaVersion(%q) = %d, want %d", out, got, want)
		}
	}
	for _, c := range []struct {
		a, b string
		want int
	}{{"27.0.1", "9.1", 1}, {"26.1", "26.1.0", 0}, {"25.2.9519653", "26.1.10909125", -1}} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestDoctorUsage(t *testing.T) {
	if code, _, errOut := runCLI("doctor", "extra"); code != exitUsage || !strings.Contains(errOut, "Usage: vex doctor [-v]") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
	if code, out, _ := runCLI("help", "doctor"); code != exitOK || !strings.Contains(out, "never installs anything") {
		t.Errorf("help doctor: code %d, output %q", code, out)
	}
}
