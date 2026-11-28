package main

import (
	"bytes"
	"os"
	"testing"
	"unicode"
)

// Checks that the runtime's tables match what this command writes.
func TestTablesAreCurrent(t *testing.T) {
	if unicode.Version != version {
		t.Skipf("Go's unicode package is at %s, not %s", unicode.Version, version)
	}
	got, err := build("ucd")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../compiler/codegen/runtime/vexuni.h")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("compiler/codegen/runtime/vexuni.h is out of date; run go generate ./compiler/codegen")
	}
}
