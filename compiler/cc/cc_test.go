package cc

import (
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
