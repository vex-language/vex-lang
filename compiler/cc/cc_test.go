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
