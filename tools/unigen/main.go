// Command unigen writes the Unicode tables of the C runtime: case mappings
// and the grapheme cluster break property, from Unicode 15.0.0.
//
// Simple case mappings come from Go's unicode package, which must be at the
// pinned version; the rest come from the extracts of the Unicode Character
// Database in ucd/. Run it through `go generate ./compiler/codegen`.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The Unicode version of every table.
const version = "15.0.0"

func main() {
	ucd := flag.String("ucd", "ucd", "the folder holding the Unicode data extracts")
	out := flag.String("o", "vexuni.h", "the C header to write")
	flag.Parse()
	src, err := build(*ucd)
	if err == nil {
		err = os.WriteFile(*out, src, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "unigen:", err)
		os.Exit(1)
	}
}

// Returns the C header built from the data files in [ucd].
func build(ucd string) ([]byte, error) {
	if unicode.Version != version {
		return nil, fmt.Errorf("Go's unicode package is at %s, but the tables are pinned to %s; build with a Go release that has %s", unicode.Version, version, version)
	}
	special, err := readSpecialCasing(filepath.Join(ucd, "SpecialCasing.txt"))
	if err != nil {
		return nil, err
	}
	gcb, err := readGraphemeBreaks(ucd)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "// Unicode %s tables for the runtime, written by tools/unigen from Go's\n", version)
	b.WriteString("// unicode package and the data in tools/unigen/ucd. Run `go generate\n")
	b.WriteString("// ./compiler/codegen` to rebuild them instead of editing this file.\n\n")
	b.WriteString("// A code point that maps to text of more than one code point.\n")
	b.WriteString("typedef struct {\n    uint32_t cp;\n    const char *s;\n} vx_uni_special;\n\n")

	writeCase(&b, "upper", unicode.ToUpper)
	writeCase(&b, "lower", unicode.ToLower)
	writeTitle(&b)
	for i, name := range []string{"lower", "title", "upper"} {
		writeSpecial(&b, name, special, i)
	}
	writeBreaks(&b, gcb)
	return b.Bytes(), nil
}

// A run of code points mapped by adding [delta]: every [stride]th one
// from [lo] to [hi].
type caseRun struct {
	lo, hi, stride, delta int
}

// Writes the simple case mapping [f] as runs of code points sharing a delta.
func writeCase(b *bytes.Buffer, name string, f func(rune) rune) {
	type pair struct{ cp, delta int }
	var ps []pair
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if m := f(r); m != r {
			ps = append(ps, pair{int(r), int(m - r)})
		}
	}
	var runs []caseRun
	for i := 0; i < len(ps); {
		run := caseRun{ps[i].cp, ps[i].cp, 1, ps[i].delta}
		j := i + 1
		for j < len(ps) && ps[j].cp == run.hi+1 && ps[j].delta == run.delta {
			run.hi = ps[j].cp
			j++
		}
		if j == i+1 {
			// Upper and lower case letters often alternate, as in Latin
			// Extended-A; the code points between are left as they are.
			for j < len(ps) && ps[j].cp == run.hi+2 && ps[j].delta == run.delta {
				run.hi, run.stride = ps[j].cp, 2
				j++
			}
		}
		runs = append(runs, run)
		i = j
	}
	fmt.Fprintf(b, "// The simple %s case mapping: {first, last, stride, delta}.\n", name)
	fmt.Fprintf(b, "static const int32_t vx_uni_%s[][4] = {\n", name)
	var items []string
	for _, r := range runs {
		items = append(items, fmt.Sprintf("{0x%X,0x%X,%d,%d}", r.lo, r.hi, r.stride, r.delta))
	}
	writeItems(b, items)
	b.WriteString("};\n\n")
}

// Writes the code points whose simple title case differs from their upper
// case.
func writeTitle(b *bytes.Buffer) {
	var items []string
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if t := unicode.ToTitle(r); t != unicode.ToUpper(r) {
			items = append(items, fmt.Sprintf("{0x%X,0x%X}", r, t))
		}
	}
	b.WriteString("// The simple title case mappings that differ from upper case: {code, title}.\n")
	b.WriteString("static const uint32_t vx_uni_title[][2] = {\n")
	writeItems(b, items)
	b.WriteString("};\n\n")
}

// An unconditional mapping of one code point to several.
type specialCase struct {
	cp   int
	maps [3][]rune // lower, title and upper
}

// Writes the special mappings in field [field] of [special] that differ from
// the simple ones, as UTF-8 strings.
func writeSpecial(b *bytes.Buffer, name string, special []specialCase, field int) {
	simple := []func(rune) rune{unicode.ToLower, unicode.ToTitle, unicode.ToUpper}[field]
	var items []string
	for _, s := range special {
		m := s.maps[field]
		if len(m) == 1 && m[0] == simple(rune(s.cp)) {
			continue
		}
		items = append(items, fmt.Sprintf("{0x%X,%s}", s.cp, cString(string(m))))
	}
	fmt.Fprintf(b, "// The unconditional %s case mappings of SpecialCasing.txt: {code, text}.\n", name)
	fmt.Fprintf(b, "static const vx_uni_special vx_uni_special_%s[] = {\n", name)
	writeItems(b, items)
	b.WriteString("};\n\n")
}

// The values of the Grapheme_Cluster_Break property, in the order of the C
// enum, with Extended_Pictographic as a value of its own.
var breakNames = []string{"Other", "CR", "LF", "Control", "Extend", "ZWJ", "Regional_Indicator", "Prepend",
	"SpacingMark", "L", "V", "T", "LV", "LVT", "Extended_Pictographic"}

// The first Hangul syllable, the number of them and how many share a
// leading and a vowel jamo.
const (
	hangulBase  = 0xAC00
	hangulCount = 11172
	hangulT     = 28
)

// Writes the grapheme cluster break property as ranges. Hangul syllables
// are left out: the runtime works out LV and LVT from the code point.
func writeBreaks(b *bytes.Buffer, gcb map[int]int) {
	cps := make([]int, 0, len(gcb))
	for cp := range gcb {
		cps = append(cps, cp)
	}
	sort.Ints(cps)
	var items []string
	for i := 0; i < len(cps); {
		j := i + 1
		for j < len(cps) && cps[j] == cps[j-1]+1 && gcb[cps[j]] == gcb[cps[i]] {
			j++
		}
		items = append(items, fmt.Sprintf("{0x%X,0x%X,%d}", cps[i], cps[j-1], gcb[cps[i]]))
		i = j
	}
	b.WriteString("enum {\n")
	for i, n := range breakNames {
		fmt.Fprintf(b, "    VX_GCB_%s = %d,\n", strings.ToUpper(n), i)
	}
	b.WriteString("};\n\n")
	b.WriteString("// The grapheme cluster break property, Hangul syllables aside: {first, last, value}.\n")
	b.WriteString("static const uint32_t vx_uni_gcb[][3] = {\n")
	writeItems(b, items)
	b.WriteString("};\n")
}

// Writes [items] separated by commas, several to a line.
func writeItems(b *bytes.Buffer, items []string) {
	line := ""
	for _, it := range items {
		if line != "" && len(line)+len(it) > 76 {
			b.WriteString("   " + line + "\n")
			line = ""
		}
		line += " " + it + ","
	}
	if line != "" {
		b.WriteString("   " + line + "\n")
	}
}

// Returns [s] as a C string literal, escaping every byte outside ASCII.
func cString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x80 && c != '"' && c != '\\' {
			sb.WriteByte(c)
		} else {
			fmt.Fprintf(&sb, "\\%03o", c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// Reads the unconditional mappings of SpecialCasing.txt at [path].
func readSpecialCasing(path string) ([]specialCase, error) {
	var out []specialCase
	err := eachLine(path, func(fields []string) error {
		if len(fields) != 5 || fields[4] != "" {
			return fmt.Errorf("want code; lower; title; upper; with no condition")
		}
		cp, err := parseCodes(fields[0])
		if err != nil || len(cp) != 1 {
			return fmt.Errorf("bad code point %q", fields[0])
		}
		s := specialCase{cp: int(cp[0])}
		for i := range s.maps {
			if s.maps[i], err = parseCodes(fields[i+1]); err != nil {
				return err
			}
		}
		out = append(out, s)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].cp < out[j].cp })
	return out, err
}

// Reads the grapheme cluster break property and the Extended_Pictographic
// code points from [ucd], giving each code point's index in [breakNames].
// Code points left out are Other.
func readGraphemeBreaks(ucd string) (map[int]int, error) {
	value := map[string]int{}
	for i, n := range breakNames {
		value[n] = i
	}
	gcb := map[int]int{}
	hangul := 0
	add := func(fields []string) error {
		if len(fields) != 2 {
			return fmt.Errorf("want code points; property")
		}
		v, ok := value[fields[1]]
		if !ok || v == 0 {
			return fmt.Errorf("unknown property %q", fields[1])
		}
		lo, hi, err := parseRange(fields[0])
		if err != nil {
			return err
		}
		for cp := lo; cp <= hi; cp++ {
			if old, ok := gcb[cp]; ok {
				return fmt.Errorf("U+%04X is both %s and %s", cp, breakNames[old], fields[1])
			}
			if fields[1] == "LV" || fields[1] == "LVT" {
				off := cp - hangulBase
				if off < 0 || off >= hangulCount || (off%hangulT == 0) != (fields[1] == "LV") {
					return fmt.Errorf("U+%04X is %s, which the runtime doesn't expect", cp, fields[1])
				}
				hangul++
				continue
			}
			gcb[cp] = v
		}
		return nil
	}
	if err := eachLine(filepath.Join(ucd, "GraphemeBreakProperty.txt"), add); err != nil {
		return nil, err
	}
	if hangul != hangulCount {
		return nil, fmt.Errorf("found %d Hangul syllables, want %d", hangul, hangulCount)
	}
	if err := eachLine(filepath.Join(ucd, "emoji-data.txt"), add); err != nil {
		return nil, err
	}
	return gcb, nil
}

// Calls [f] with the fields of each data line of the file at [path],
// split at semicolons and trimmed, without comments.
func eachLine(path string, f func([]string) error) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	sc := bufio.NewScanner(file)
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, ";")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		if err := f(fields); err != nil {
			return fmt.Errorf("%s:%d: %w", path, n, err)
		}
	}
	return sc.Err()
}

// Parses a code point or a range such as 0600..0605.
func parseRange(s string) (lo, hi int, err error) {
	a, b, ok := strings.Cut(s, "..")
	if !ok {
		b = a
	}
	l, err1 := strconv.ParseUint(a, 16, 32)
	h, err2 := strconv.ParseUint(b, 16, 32)
	if err1 != nil || err2 != nil || l > h || h > unicode.MaxRune {
		return 0, 0, fmt.Errorf("bad code point range %q", s)
	}
	return int(l), int(h), nil
}

// Parses code points in hex separated by spaces.
func parseCodes(s string) ([]rune, error) {
	var out []rune
	for _, f := range strings.Fields(s) {
		v, err := strconv.ParseUint(f, 16, 32)
		if err != nil || !utf8.ValidRune(rune(v)) {
			return nil, fmt.Errorf("bad code point %q", f)
		}
		out = append(out, rune(v))
	}
	return out, nil
}
