package main

import (
	"bytes"
	"strings"
	"testing"
)

func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		code, out, _ := runCLI(arg)
		if code != exitOK || out != "vex "+version+"\n" {
			t.Errorf("%s: code %d, output %q", arg, code, out)
		}
	}
}

func TestHelp(t *testing.T) {
	code, out, _ := runCLI("help")
	if code != exitOK || !strings.Contains(out, "Usage: vex <command>") || !strings.Contains(out, "version") {
		t.Errorf("help: code %d, output %q", code, out)
	}
	code, out, _ = runCLI("help", "version")
	if code != exitOK || !strings.HasPrefix(out, "Usage: vex version") {
		t.Errorf("help version: code %d, output %q", code, out)
	}
}

func TestNoArgsShowsUsage(t *testing.T) {
	code, _, errOut := runCLI()
	if code != exitUsage || !strings.Contains(errOut, "Usage:") {
		t.Errorf("no args: code %d, stderr %q", code, errOut)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errOut := runCLI("fly")
	if code != exitUsage || !strings.Contains(errOut, `unknown command "fly"`) {
		t.Errorf("unknown: code %d, stderr %q", code, errOut)
	}
}
