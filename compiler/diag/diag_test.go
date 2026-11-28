package diag

import (
	"strings"
	"testing"
)

func TestPosOf(t *testing.T) {
	f := NewFile("a.vx", "let a = 1\nlet é = \"x\"\n")
	cases := []struct {
		offset    int
		line, col int
	}{
		{0, 1, 1},
		{4, 1, 5},
		{10, 2, 1},
		{17, 2, 7}, // after the two-byte "é"
	}
	for _, c := range cases {
		p := f.PosOf(c.offset)
		if p.Line != c.line || p.Col != c.col {
			t.Errorf("PosOf(%d) = %d:%d, want %d:%d", c.offset, p.Line, p.Col, c.line, c.col)
		}
	}
}

func TestLine(t *testing.T) {
	f := NewFile("a.vx", "one\r\ntwo\nthree")
	for n, want := range map[int]string{1: "one", 2: "two", 3: "three", 4: "", 0: ""} {
		if got := f.Line(n); got != want {
			t.Errorf("Line(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFormat(t *testing.T) {
	f := NewFile("main.vx", "fn main() {\n    let s = \"abc\n}\n")
	d := Diagnostic{Pos: f.PosOf(24), Len: 4, Message: "unterminated string", Hint: "add a closing \""}
	want := "error: unterminated string\n" +
		" --> main.vx:2:13\n" +
		"  |\n" +
		"2 |     let s = \"abc\n" +
		"  |             ^^^^ add a closing \"\n"
	if got := Format(f, d, false); got != want {
		t.Errorf("Format mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatKeepsTabs(t *testing.T) {
	f := NewFile("main.vx", "\tlet x = y\n")
	d := Diagnostic{Pos: f.PosOf(9), Len: 1, Message: "undefined: y"}
	want := "error: undefined: y\n" +
		" --> main.vx:1:10\n" +
		"  |\n" +
		"1 | \tlet x = y\n" +
		"  | \t        ^\n"
	if got := Format(f, d, false); got != want {
		t.Errorf("Format mismatch\n got:\n%q\nwant:\n%q", got, want)
	}
}

func TestListError(t *testing.T) {
	l := List{{Pos: Pos{Offset: 20, Line: 2, Col: 3}, Message: "a"}, {Pos: Pos{Offset: 0, Line: 1, Col: 1}, Message: "b"}}
	if got := l.Error(); got != "2:3: a (and 1 more errors)" {
		t.Errorf("Error() = %q", got)
	}
	l.Sort()
	if l[0].Message != "b" {
		t.Errorf("Sort did not order by position")
	}
}

func TestSortByModule(t *testing.T) {
	root := &File{Name: "app/main.vx"}
	lib := &File{Name: "app/a/lib.vx", Module: 2}
	util := &File{Name: "app/z/util.vx", Module: 1}
	l := List{
		{File: lib, Pos: Pos{Offset: 0}, Message: "lib"},
		{File: util, Pos: Pos{Offset: 5}, Message: "util 2"},
		{File: root, Pos: Pos{Offset: 9}, Message: "main"},
		{File: util, Pos: Pos{Offset: 1}, Message: "util 1"},
	}
	l.Sort()
	var got []string
	for _, d := range l {
		got = append(got, d.Message)
	}
	if g := strings.Join(got, ", "); g != "main, util 1, util 2, lib" {
		t.Errorf("got %s", g)
	}
}
