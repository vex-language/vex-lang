package lexer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/irshadio/vex-lang/compiler/diag"
)

// Returns a compact description of a token for comparisons.
func describe(t Token) string {
	switch t.Kind {
	case EOF:
		return "EOF"
	case Semi:
		if t.IsAutoSemi() {
			return "NL"
		}
		return ";"
	case Ident:
		return "id:" + t.Text
	case Int:
		return "int:" + t.Value
	case Float:
		return "float:" + t.Value
	case String:
		return "str:" + t.Value
	case StringHead:
		return "head:" + t.Value
	case StringMid:
		return "mid:" + t.Value
	case StringTail:
		return "tail:" + t.Value
	}
	return t.Text
}

func lex(t *testing.T, src string) ([]string, diag.List) {
	t.Helper()
	toks, err := Tokenize(diag.NewFile("test.vx", src))
	var errs diag.List
	if err != nil {
		errs = err.(diag.List)
	}
	out := make([]string, len(toks))
	for i, tok := range toks {
		out[i] = describe(tok)
	}
	return out, errs
}

func TestTokens(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"empty", "", "EOF"},
		{"keywords", "fn let var if else while for in return break continue struct enum match static guard true false nil",
			"fn let var if else while for in return break continue struct enum match static guard true false nil NL EOF"},
		{"identifiers", "name _tmp user2 isAdult", "id:name id:_tmp id:user2 id:isAdult NL EOF"},
		{"operators", "+ - * / % ! && || = += -= *= /= %= == != < <= > >= -> => . .. ..= ? ?. ??",
			"+ - * / % ! && || = += -= *= /= %= == != < <= > >= -> => . .. ..= ? ?. ?? EOF"},
		{"wrapping operators", "&+ &- &* &+= &-= &*= a&&-b", "&+ &- &* &+= &-= &*= id:a && - id:b NL EOF"},
		{"ampersand", "T: A & B", "id:T : id:A & id:B NL EOF"},
		{"punctuation", "( ) { } [ ] , : ;", "( ) { } [ ] , : ; EOF"},
		{"integers", "0 42 1_000_000 0xFF 0b1010 0o17", "int:0 int:42 int:1000000 int:0xFF int:0b1010 int:0o17 NL EOF"},
		{"floats", "3.14 1e9 2.5e-3 1E+2 0.5", "float:3.14 float:1e9 float:2.5e-3 float:1E+2 float:0.5 NL EOF"},
		{"range is not a float", "0..5 1..=10", "int:0 .. int:5 int:1 ..= int:10 NL EOF"},
		{"member on int", "1.len", "int:1 . id:len NL EOF"},
		{"string", `"hello"`, "str:hello NL EOF"},
		{"escapes", `"a\nb\t\"q\" \\ \{x\} \u{1F600}"`, "str:a\nb\t\"q\" \\ {x} 😀 NL EOF"},
		{"utf8 in string", `"héllo 👋"`, "str:héllo 👋 NL EOF"},
		{"line comment", "let x = 1 // note\nlet y = 2", "let id:x = int:1 NL let id:y = int:2 NL EOF"},
		{"doc comment", "/// Greets.\nfn greet() {}", "fn id:greet ( ) { } NL EOF"},
		{"block comment", "a /* b */ c", "id:a id:c NL EOF"},
		{"nested block comment", "a /* b /* c */ d */ e", "id:a id:e NL EOF"},
		{"multi-line block comment ends statement", "a /* x\ny */ b", "id:a NL id:b NL EOF"},
	}
	for _, c := range cases {
		got, errs := lex(t, c.src)
		if len(errs) > 0 {
			t.Errorf("%s: unexpected errors: %v", c.name, errs)
			continue
		}
		if g := strings.Join(got, " "); g != c.want {
			t.Errorf("%s:\n got: %s\nwant: %s", c.name, g, c.want)
		}
	}
}

func TestInterpolation(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"single", `"Hi {name}!"`, "head:Hi  id:name tail:! NL EOF"},
		{"expression", `"{a + 1}"`, "head: id:a + int:1 tail: NL EOF"},
		{"several", `"{i}: {fruit}"`, "head: id:i mid::  id:fruit tail: NL EOF"},
		{"nested string", `"{greet("Ann")} ok"`, "head: id:greet ( str:Ann ) tail: ok NL EOF"},
		{"nested interpolation", `"a {"b {c}"} d"`, "head:a  head:b  id:c tail: tail: d NL EOF"},
		{"braces inside", `"{f({})}"`, "head: id:f ( { } ) tail: NL EOF"},
		{"escaped brace", `"\{not}"`, "str:{not} NL EOF"},
		{"call after", `println("n = {n}")`, "id:println ( head:n =  id:n tail: ) NL EOF"},
	}
	for _, c := range cases {
		got, errs := lex(t, c.src)
		if len(errs) > 0 {
			t.Errorf("%s: unexpected errors: %v", c.name, errs)
			continue
		}
		if g := strings.Join(got, " "); g != c.want {
			t.Errorf("%s:\n got: %s\nwant: %s", c.name, g, c.want)
		}
	}
}

func TestMultilineAndRawStrings(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"indentation removed", "\"\"\"\n    a\n      b\n    \"\"\"", "str:a\n  b NL EOF"},
		{"empty", "\"\"\"\n  \"\"\"", "str: NL EOF"},
		{"blank lines kept", "\"\"\"\n  a\n\n  b\n  \"\"\"", "str:a\n\nb NL EOF"},
		{"shorter blank line", "\"\"\"\n    a\n  \n    \"\"\"", "str:a\n NL EOF"},
		{"quotes inside", "\"\"\"\n  say \"hi\" \"\"\n  \"\"\"", "str:say \"hi\" \"\" NL EOF"},
		{"escaped closing quotes", "\"\"\"\n  \\\"\"\"\n  \"\"\"", "str:\"\"\" NL EOF"},
		{"line continuation", "\"\"\"\n  a \\\n  b\n  \"\"\"", "str:a b NL EOF"},
		{"interpolation", "\"\"\"\n  a {x}\n  b\n  \"\"\"", "head:a  id:x tail:\nb NL EOF"},
		{"crlf", "\"\"\"\r\n  a\r\n  b\r\n  \"\"\"", "str:a\nb NL EOF"},
		{"raw", `r"\d+ {x}"`, `str:\d+ {x} NL EOF`},
		{"raw empty", `r""`, "str: NL EOF"},
		{"raw multi-line", "r\"\"\"\n  \\n {x}\n  \"\"\"", `str:\n {x} NL EOF`},
		{"r as a name", "r + 1", "id:r + int:1 NL EOF"},
	}
	for _, c := range cases {
		got, errs := lex(t, c.src)
		if len(errs) > 0 {
			t.Errorf("%s: unexpected errors: %v", c.name, errs)
			continue
		}
		if g := strings.Join(got, " "); g != c.want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, g, c.want)
		}
	}
}

func TestMultilineStringErrors(t *testing.T) {
	cases := []struct {
		name, src, pos, msg string
	}{
		{"text after opening", "\"\"\"a\n  \"\"\"", "1:4", "a multi-line string starts on the line after its opening \"\"\""},
		{"closing after text", "\"\"\"\n  a\"\"\"", "2:4", "the closing \"\"\" of a multi-line string goes on its own line"},
		{"one line", `""""""`, "1:4", "the closing \"\"\" of a multi-line string goes on its own line"},
		{"less indented", "\"\"\"\n a\n  \"\"\"", "2:1", "this line is indented less than the closing \"\"\" of its string"},
		{"unterminated", "\"\"\"\n  a", "1:1", "unterminated multi-line string"},
		{"unterminated raw", `r"abc`, "1:1", "unterminated string"},
		{"only opening quotes", `"""`, "1:1", "unterminated multi-line string"},
		{"opening quotes then text", `"""a`, "1:1", "unterminated multi-line string"},
	}
	for _, c := range cases {
		_, errs := lex(t, c.src)
		if len(errs) != 1 {
			t.Errorf("%s: got %d errors, want 1: %v", c.name, len(errs), errs)
			continue
		}
		if pos := fmt.Sprintf("%d:%d", errs[0].Pos.Line, errs[0].Pos.Col); pos != c.pos || errs[0].Message != c.msg {
			t.Errorf("%s: got %s %q, want %s %q", c.name, pos, errs[0].Message, c.pos, c.msg)
		}
	}
}

func TestBrokenMultilineStringsAreReported(t *testing.T) {
	for _, src := range []string{
		"}\r\n\"\"\"r{\\\"\"\"\"r\"\"\"x}{rr\"\"\"r",
		"\"\"\"\n  {\"\"\"\n  x\n  \"\"\"}\n  \"\"\"",
		"\"\"\"\n  {\n  \"\"\"",
	} {
		if _, errs := lex(t, src); len(errs) == 0 {
			t.Errorf("%q: no errors", src)
		}
	}
}

func TestAutomaticStatementEnds(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"after identifier", "a\nb", "id:a NL id:b NL EOF"},
		{"after literals", "1\n2.0\n\"s\"\ntrue\nfalse", "int:1 NL float:2.0 NL str:s NL true NL false NL EOF"},
		{"after closing brackets", "f()\nxs[0]\n{}", "id:f ( ) NL id:xs [ int:0 ] NL { } NL EOF"},
		{"after return break continue", "return\nbreak\ncontinue", "return NL break NL continue NL EOF"},
		{"not after operator", "a +\nb", "id:a + id:b NL EOF"},
		{"not after comma", "f(a,\nb)", "id:f ( id:a , id:b ) NL EOF"},
		{"not after opening brace", "fn f() {\nx\n}", "fn id:f ( ) { id:x NL } NL EOF"},
		{"blank lines collapse", "a\n\n\nb", "id:a NL id:b NL EOF"},
		{"optional chain continues", "user\n    ?.name\n    ?? x", "id:user ?. id:name ?? id:x NL EOF"},
		{"optional type ends line", "var u: User?\nu = nil", "var id:u : id:User ? NL id:u = nil NL EOF"},
		{"leading dot continues", "Text(\"x\")\n    .size(24)\n    .bold()", "id:Text ( str:x ) . id:size ( int:24 ) . id:bold ( ) NL EOF"},
		{"leading dot after comment", "a\n// c\n.b", "id:a . id:b NL EOF"},
		{"leading range does not continue", "a\n..b", "id:a NL .. id:b NL EOF"},
		{"crlf", "a\r\nb\r\n", "id:a NL id:b NL EOF"},
		{"after interpolated string", "\"{x}\"\ny", "head: id:x tail: NL id:y NL EOF"},
	}
	for _, c := range cases {
		got, errs := lex(t, c.src)
		if len(errs) > 0 {
			t.Errorf("%s: unexpected errors: %v", c.name, errs)
			continue
		}
		if g := strings.Join(got, " "); g != c.want {
			t.Errorf("%s:\n got: %s\nwant: %s", c.name, g, c.want)
		}
	}
}

func TestPositions(t *testing.T) {
	toks, err := Tokenize(diag.NewFile("t.vx", "fn main() {\n\tlet s = \"é\" + x\n}"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"fn": "1:1", "main": "1:4", "let": "2:2", "=": "2:8", `"é"`: "2:10", "+": "2:14", "x": "2:16", "}": "3:1"}
	seen := 0
	for _, tok := range toks {
		if pos, ok := want[tok.Text]; ok {
			seen++
			if tok.Pos.String() != pos {
				t.Errorf("%q at %s, want %s", tok.Text, tok.Pos, pos)
			}
		}
	}
	if seen != len(want) {
		t.Errorf("found %d of %d expected tokens", seen, len(want))
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name, src string
		pos, msg  string
		hint      string
	}{
		{"unterminated string", "let s = \"abc\nx", "1:9", "unterminated string", `add a closing "`},
		{"unterminated at end of file", `"abc`, "1:1", "unterminated string", `add a closing "`},
		{"unknown escape", `"a\qb"`, "1:3", `unknown escape sequence \q`, `write \\ for a backslash`},
		{"bad unicode escape", `"\u1234"`, "1:2", "invalid unicode escape", `write it as \u{1F600}`},
		{"code point too large", `"\u{110000}"`, "1:2", "invalid unicode code point U+110000", ""},
		{"interpolation across lines", "\"a {x\n}\"", "1:1", "unterminated string interpolation", `close it with } and "`},
		{"interpolation at end of file", `"a {x`, "1:1", "unterminated string interpolation", `close it with } and "`},
		{"dollar", "a $ b", "1:3", `unexpected character '$'`, ""},
		{"single quotes", "'hi'", "1:1", "single quotes are not used for strings", `strings use double quotes: "text"`},
		{"lone single quote", "it's", "1:3", "unexpected character '", `strings use double quotes: "text"`},
		{"hash comment", "# hi", "1:1", `unexpected character '#'`, "comments start with //"},
		{"curly quotes", "“hi”", "1:1", `unexpected character '“'`, `use straight quotes: "text"`},
		{"unicode name", "let ñ = 1", "1:5", `unexpected character 'ñ'`, "names may use only ASCII letters, digits and _"},
		{"letters in number", "12abc", "1:1", `invalid number "12abc"`, ""},
		{"empty hex", "0x", "1:1", "0x has no digits", ""},
		{"bad binary digit", "0b102", "1:1", `invalid number "0b102"`, ""},
		{"leading zero", "010", "1:1", "numbers can't start with 0", "write 10, or 0o10 for octal"},
		{"leading zeros with separator", "00_7", "1:1", "numbers can't start with 0", "write 7, or 0o7 for octal"},
		{"leading zero before 9", "09", "1:1", "numbers can't start with 0", "write 9"},
		{"zeros", "0_0", "1:1", "numbers can't start with 0", "write 0"},
		{"trailing separator", "1_000_", "1:6", "misplaced digit separator in 1_000_", "put _ only between digits"},
		{"double separator", "1__0", "1:2", "misplaced digit separator in 1__0", "put _ only between digits"},
		{"separator before dot", "1_.5", "1:2", "misplaced digit separator in 1_.5", "put _ only between digits"},
		{"unterminated block comment", "a /* b", "1:3", "unterminated block comment", "close it with */"},
	}
	for _, c := range cases {
		_, errs := lex(t, c.src)
		if len(errs) == 0 {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		e := errs[0]
		if e.Pos.String() != c.pos || e.Message != c.msg || e.Hint != c.hint {
			t.Errorf("%s:\n got: %s %q hint %q\nwant: %s %q hint %q", c.name, e.Pos, e.Message, e.Hint, c.pos, c.msg, c.hint)
		}
	}
}

func TestReportsSeveralErrors(t *testing.T) {
	_, errs := lex(t, "a $ b\nc ` d\n# e")
	if len(errs) != 3 {
		t.Fatalf("got %d errors, want 3: %v", len(errs), errs)
	}
}

func TestBackslashAtCRLFLineEnd(t *testing.T) {
	for _, src := range []string{"\"abc\\\nx", "\"abc\\\r\nx"} {
		_, errs := lex(t, src)
		if len(errs) != 1 || errs[0].Message != "unterminated string" {
			t.Errorf("%q: got %v, want one unterminated string", src, errs)
		}
	}
}

func TestSingleQuotedIsOneError(t *testing.T) {
	toks, errs := lex(t, "println('hi')")
	if len(errs) != 1 || errs[0].Len != 4 {
		t.Errorf("got %v, want one error underlining 'hi'", errs)
	}
	if got := strings.Join(toks, " "); got != "id:println ( str:hi ) NL EOF" {
		t.Errorf("tokens after recovery: %s", got)
	}
}

func TestStopsAfterTooManyErrors(t *testing.T) {
	toks, errs := lex(t, strings.Repeat("#", 100))
	if len(errs) != maxErrors {
		t.Errorf("got %d errors, want %d", len(errs), maxErrors)
	}
	if toks[len(toks)-1] != "EOF" {
		t.Errorf("token list does not end with EOF")
	}
}

func TestUnterminatedStringUnderlinesRestOfLine(t *testing.T) {
	_, errs := lex(t, "let s = \"abc\r\n")
	if len(errs) != 1 || errs[0].Len != 4 {
		t.Errorf("got %v, want one error underlining 4 characters", errs)
	}
}

func TestKindNames(t *testing.T) {
	for k, want := range map[Kind]string{Ident: "identifier", Fn: "'fn'", LParen: "'('", DotDotEq: "'..='", EOF: "end of file"} {
		if k.String() != want {
			t.Errorf("%d: got %q, want %q", k, k.String(), want)
		}
	}
	if !Return.IsKeyword() || Ident.IsKeyword() {
		t.Errorf("IsKeyword is wrong")
	}
}
