package cc

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRespectsCCVariable(t *testing.T) {
	t.Setenv("CC", "/usr/bin/false --some-flag")
	command, err := Command()
	if err != nil || strings.Join(command, " ") != "/usr/bin/false --some-flag" {
		t.Fatalf("Command() = %v, %v", command, err)
	}
	err = Compile("int main(void) { return 0; }", filepath.Join(t.TempDir(), "out"))
	if err == nil || !strings.Contains(err.Error(), "the C compiler (/usr/bin/false) failed") {
		t.Errorf("got %v", err)
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
