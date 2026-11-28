package cc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRespectsCCVariable(t *testing.T) {
	fail := "/usr/bin/false"
	if runtime.GOOS == "windows" {
		fail = filepath.Join(t.TempDir(), "false.bat")
		if err := os.WriteFile(fail, []byte("@exit /b 1\r\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CC", fail+" --some-flag")
	command, err := Command()
	if err != nil || strings.Join(command, " ") != fail+" --some-flag" {
		t.Fatalf("Command() = %v, %v", command, err)
	}
	err = Compile("int main(void) { return 0; }", filepath.Join(t.TempDir(), "out"))
	if err == nil || !strings.Contains(err.Error(), "the C compiler ("+fail+") failed") {
		t.Errorf("got %v", err)
	}
}

func TestCCPathWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "LLVM tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "clang")
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CC", path)
	if command, err := Command(); err != nil || len(command) != 1 || command[0] != path {
		t.Errorf("Command() = %q, %v", command, err)
	}
}

// Makes an SDK folder in a path with spaces, with vex and its clang, and
// returns the paths of both.
func fakeSDK(t *testing.T) (vex, clang string) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "My Tools", "vex-sdk")
	vex = filepath.Join(root, "bin", Executable("vex"))
	clang = filepath.Join(root, "toolchain", "bin", Executable("clang"))
	for _, path := range []string{vex, clang} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return vex, clang
}

// Makes [executable] return [path] until the test ends.
func runningAs(t *testing.T, path string) {
	saved := executable
	executable = func() (string, error) { return path, nil }
	t.Cleanup(func() { executable = saved })
}

func TestFindsBundledToolchain(t *testing.T) {
	vex, clang := fakeSDK(t)
	runningAs(t, vex)
	t.Setenv("CC", "")
	c, err := Find()
	if err != nil || len(c.Command) != 1 || c.Command[0] != clang || c.Origin != FromSDK {
		t.Errorf("Find() = %+v, %v; want %s from the SDK", c, err, clang)
	}
}

func TestCCOverridesBundledToolchain(t *testing.T) {
	vex, _ := fakeSDK(t)
	runningAs(t, vex)
	t.Setenv("CC", "gcc -m64")
	c, err := Find()
	if err != nil || strings.Join(c.Command, " ") != "gcc -m64" || c.Origin != FromEnv {
		t.Errorf("Find() = %+v, %v", c, err)
	}
}

func TestBundledFollowsSymlinks(t *testing.T) {
	vex, clang := fakeSDK(t)
	link := filepath.Join(t.TempDir(), "bin", Executable("vex"))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(vex, link); err != nil {
		t.Skipf("can't make symbolic links here: %v", err)
	}
	if got := Bundled(link); got != clang {
		t.Errorf("Bundled(%s) = %q, want %q", link, got, clang)
	}
}

func TestNoBundledToolchain(t *testing.T) {
	vex, clang := fakeSDK(t)
	if err := os.Remove(clang); err != nil {
		t.Fatal(err)
	}
	if got := Bundled(vex); got != "" {
		t.Errorf("Bundled(%s) = %q, want none", vex, got)
	}
	runningAs(t, vex)
	t.Setenv("CC", "")
	if c, err := Find(); err == nil && c.Origin == FromSDK {
		t.Errorf("Find() = %+v, want a compiler outside the SDK", c)
	}
}

func TestSanitizersNeedSystemClang(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the SDK's toolchain builds static programs only on Linux")
	}
	vex, _ := fakeSDK(t)
	runningAs(t, vex)
	t.Setenv("CC", "")
	err := CompileWith("int main(void) { return 0; }", filepath.Join(t.TempDir(), "out"), Options{Sanitize: []string{"address"}})
	if err == nil || !strings.Contains(err.Error(), "set CC=clang") {
		t.Errorf("got %v", err)
	}
}

func TestExecutable(t *testing.T) {
	want := map[string]string{"app": "app", "app.exe": "app.exe", "v1.2": "v1.2"}
	if runtime.GOOS == "windows" {
		want = map[string]string{"app": "app.exe", "app.exe": "app.exe", "APP.EXE": "APP.EXE", "v1.2": "v1.2.exe"}
	}
	for in, out := range want {
		if got := Executable(in); got != out {
			t.Errorf("Executable(%q) = %q, want %q", in, got, out)
		}
	}
}

func TestParseSanitizers(t *testing.T) {
	tests := []struct{ in, want, err string }{
		{"address", "[address]", ""},
		{"address, undefined,address", "[address undefined]", ""},
		{"thread", "[thread]", ""},
		{"memory", "", `unknown sanitizer "memory"; use address, undefined, thread`},
		{"thread,address", "", "the thread sanitizer can't be combined with address"},
		{"", "", `unknown sanitizer ""; use address, undefined, thread`},
	}
	for _, tt := range tests {
		got, err := ParseSanitizers(tt.in)
		switch {
		case tt.err != "" && (err == nil || err.Error() != tt.err):
			t.Errorf("%q: error %v, want %q", tt.in, err, tt.err)
		case tt.err == "" && (err != nil || fmt.Sprint(got) != tt.want):
			t.Errorf("%q: got %v, %v, want %s", tt.in, got, err, tt.want)
		}
	}
}

func TestModeFlags(t *testing.T) {
	tests := []struct {
		opts Options
		want string
	}{
		{Options{}, "[-O3]"},
		{Options{Debug: true}, "[-O0 -g]"},
		{Options{Sanitize: []string{"address", "undefined"}}, "[-O3 -fsanitize=address,undefined -fno-sanitize-recover=all -fno-omit-frame-pointer -g]"},
		{Options{Debug: true, Sanitize: []string{"thread"}}, "[-O0 -g -fsanitize=thread -fno-sanitize-recover=all -fno-omit-frame-pointer]"},
	}
	for _, tt := range tests {
		if got := fmt.Sprint(tt.opts.modeFlags()); got != tt.want {
			t.Errorf("%+v: got %s, want %s", tt.opts, got, tt.want)
		}
	}
}

func TestFiles(t *testing.T) {
	if _, err := Command(); err != nil {
		t.Skip(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	opts := Options{Files: []File{
		{Name: "seven.h", Text: []byte("int seven(void);\n")},
		{Name: "seven.c", Text: []byte("#include \"seven.h\"\nint seven(void) { return 7; }\n")},
	}}
	if err := CompileWith("#include \"seven.h\"\nint main(void) { return seven() - 7; }\n", out, opts); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(out).Run(); err != nil {
		t.Errorf("running the program: %v", err)
	}
}

func TestSwiftFiles(t *testing.T) {
	if _, err := exec.LookPath("swiftc"); err != nil || runtime.GOOS != "darwin" {
		t.Skip("needs swiftc on macOS")
	}
	out := filepath.Join(t.TempDir(), "out")
	opts := Options{Files: []File{
		{Name: "seven.swift", Text: []byte("@_cdecl(\"seven\")\npublic func seven() -> Int32 { 7 }\n")},
	}}
	if err := CompileWith("int seven(void);\nint main(void) { return seven() - 7; }\n", out, opts); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(out).Run(); err != nil {
		t.Errorf("running the program: %v", err)
	}
}
