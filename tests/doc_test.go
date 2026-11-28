package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/vex-language/vex-lang/compiler/doc"
	"github.com/vex-language/vex-lang/compiler/modules"
)

// The declarations whose text documentation doc/geometry.txt holds, after
// the overview of each module.
var docNames = []string{"shapes.Circle", "Kind", "Kind.hasCorners", "describe", "Square.area", "pi"}

// Builds the documentation of the project in doc/geometry and compares
// its text form with doc/geometry.txt and its pages with the files in
// doc/geometry_html.
func TestDocGolden(t *testing.T) {
	prog, err := modules.Load(filepath.Join("doc", "geometry"))
	if err != nil {
		t.Fatal(err)
	}
	mods := doc.FromProgram(prog)
	var warnings bytes.Buffer
	set := doc.NewSet(mods, &warnings)

	var text bytes.Buffer
	for _, m := range mods {
		doc.WriteModuleText(&text, m)
		text.WriteString("\n")
	}
	for _, name := range docNames {
		d := set.Find(name)
		if d == nil {
			t.Fatalf("no declaration %s", name)
		}
		text.WriteString("--- " + name + "\n")
		doc.WriteText(&text, d)
	}
	golden(t, filepath.Join("doc", "geometry.txt"), text.Bytes())

	dir := t.TempDir()
	files, err := set.WriteHTML(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Errorf("wrote %d files, want index.html and a page for each of the 2 modules", len(files))
	}
	for _, f := range files {
		got, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		golden(t, filepath.Join("doc", "geometry_html", filepath.Base(f)), got)
	}
	wantWarn := filepath.Join("doc", "geometry", "shapes", "shapes.vx") +
		":72: warning: [Triangle] in the doc of describe doesn't name a declaration\n"
	if warnings.String() != wantWarn {
		t.Errorf("warnings:\n%s\nwant:\n%s", warnings.String(), wantWarn)
	}
}

// Compares [got] with the file at [path], or rewrites it with -update.
func golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing expected output; run go test ./tests -run TestDocGolden -update")
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the output:\n%s", path, got)
	}
}
