package codegen

import (
	_ "embed"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

//go:embed testdata/GraphemeBreakTest.txt
var graphemeBreakTest string

// Checks the runtime's grapheme clusters against every case of Unicode's
// GraphemeBreakTest.txt.
func TestGraphemeBreakTest(t *testing.T) {
	var lits, want []string
	for _, line := range strings.Split(graphemeBreakTest, "\n") {
		if !strings.HasPrefix(line, "÷") {
			continue
		}
		var lit strings.Builder
		var sizes []string
		n := 0
		for _, f := range strings.Fields(line) {
			switch f {
			case "÷":
				if n > 0 {
					sizes = append(sizes, strconv.Itoa(n))
				}
				n = 0
			case "×":
			default:
				fmt.Fprintf(&lit, `\u{%s}`, f)
				n++
			}
		}
		lits = append(lits, `"`+lit.String()+`"`)
		want = append(want, strings.Join(sizes, " "))
	}
	if len(lits) < 600 {
		t.Fatalf("found only %d test cases", len(lits))
	}
	src := "fn main() {\n    let cases = [\n        " + strings.Join(lits, ",\n        ") + `,
    ]
    for s in cases {
        var sizes: [string] = []
        for g in s.graphemes() {
            sizes.push(str(g.len))
        }
        println(sizes.join(" "), s.graphemeCount == sizes.len)
    }
}
`
	stdout, stderr, code := run(t, src, "")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	got := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i]+" true" {
			t.Errorf("%s: clusters of %s code points, want %s", lits[i], got[i], want[i])
		}
	}
}

// Checks upper, lower and capitalize on every code point with a case
// mapping, and the one after it, against Go's unicode package and the
// unconditional mappings of SpecialCasing.txt.
func TestCaseMappings(t *testing.T) {
	if unicode.Version != "15.0.0" {
		t.Skipf("the runtime's tables are Unicode 15.0.0; Go's are %s", unicode.Version)
	}
	data, err := os.ReadFile("../../tools/unigen/ucd/SpecialCasing.txt")
	if err != nil {
		t.Fatal(err)
	}
	special := map[rune][3]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Split(line, ";")
		var m [3]string
		for i := range m {
			for _, h := range strings.Fields(f[i+1]) {
				v, _ := strconv.ParseUint(h, 16, 32)
				m[i] += string(rune(v))
			}
		}
		v, _ := strconv.ParseUint(strings.TrimSpace(f[0]), 16, 32)
		special[rune(v)] = m
	}
	mapped := func(r rune, i int, simple func(rune) rune) string {
		if m, ok := special[r]; ok {
			return m[i]
		}
		return string(simple(r))
	}
	var runes []rune
	for r := rune(0x80); r <= unicode.MaxRune; r++ {
		_, ok := special[r]
		if ok || unicode.ToUpper(r) != r || unicode.ToLower(r) != r || unicode.ToTitle(r) != r {
			if len(runes) == 0 || runes[len(runes)-1] != r {
				runes = append(runes, r)
			}
			runes = append(runes, r+1)
		}
	}
	var lits, want []string
	for _, r := range runes {
		if unicode.IsControl(r) || (r >= 0xD800 && r < 0xE000) {
			continue
		}
		lits = append(lits, fmt.Sprintf(`"\u{%X}"`, r))
		want = append(want, mapped(r, 2, unicode.ToUpper)+" "+mapped(r, 0, unicode.ToLower)+" "+mapped(r, 1, unicode.ToTitle))
	}
	src := "fn main() {\n    let cases = [" + strings.Join(lits, ", ") + `]
    for c in cases {
        println(c.upper() + " " + c.lower() + " " + c.capitalize())
    }
    println("İSTANBUL ǈ".lower(), "ﬃ ŉ ᾳ".upper(), "ǆemal".capitalize())
}
`
	stdout, stderr, code := run(t, src, "")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	got := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	want = append(want, "i̇stanbul ǉ FFI ʼN ΑΙ ǅemal")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i+1, got[i], want[i])
		}
	}
}
