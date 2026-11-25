package lexer

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/irshadio/vex-lang/compiler/diag"
)

// The maximum number of errors reported before the lexer stops.
const maxErrors = 20

// An open `{` inside an interpolated string.
type interpolation struct {
	// Unclosed `{` seen inside the expression, so `}` of a nested block
	// is not taken as the end of the interpolation.
	depth int

	// The byte offset of the string's opening quote.
	start int
}

// Splits source text into tokens.
type lexer struct {
	file   *diag.File
	src    string
	pos    int
	toks   []Token
	errs   diag.List
	interp []interpolation
}

// Returns the tokens of [f], always ending with an [EOF] token.
//
// Lexing continues after an error so several problems can be reported at
// once; the returned error is a [diag.List].
func Tokenize(f *diag.File) ([]Token, error) {
	l := &lexer{file: f, src: f.Src}
	l.run()
	if len(l.errs) > 0 {
		return l.toks, l.errs
	}
	return l.toks, nil
}

func (l *lexer) run() {
	for l.pos < len(l.src) && len(l.errs) < maxErrors {
		start := l.pos
		c := l.src[l.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			l.pos++
		case c == '\n':
			if len(l.interp) > 0 {
				l.unterminatedInterpolation()
			}
			l.lineBreak(start)
			l.pos++
		case c == '/' && l.peek(1) == '/':
			l.skipLineComment()
		case c == '/' && l.peek(1) == '*':
			l.blockComment()
		case isLetter(c):
			l.identifier()
		case c == '$' && isDigit(l.peek(1)):
			// The positional fields of records: `$1`, `$2`, ...
			l.pos++
			for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
				l.pos++
			}
			l.emit(Ident, start, l.pos, "")
		case isDigit(c):
			l.number()
		case c == '"':
			l.pos++
			l.stringBody(start, start, true)
		case c == '{' && len(l.interp) > 0:
			l.interp[len(l.interp)-1].depth++
			l.operator()
		case c == '}' && len(l.interp) > 0:
			top := &l.interp[len(l.interp)-1]
			if top.depth > 0 {
				top.depth--
				l.operator()
				break
			}
			l.interp = l.interp[:len(l.interp)-1]
			l.pos++
			l.stringBody(start, top.start, false)
		case c >= utf8.RuneSelf:
			l.nonASCII()
		default:
			l.operator()
		}
	}
	if len(l.interp) > 0 {
		l.unterminatedInterpolation()
	}
	l.lineBreak(len(l.src))
	l.emit(EOF, len(l.src), len(l.src), "")
}

// Returns the byte [n] positions ahead, or 0 past the end.
func (l *lexer) peek(n int) byte {
	if l.pos+n < len(l.src) {
		return l.src[l.pos+n]
	}
	return 0
}

func (l *lexer) emit(k Kind, start, end int, value string) {
	l.toks = append(l.toks, Token{Kind: k, Text: l.src[start:end], Value: value, Pos: l.file.PosOf(start), End: end})
}

// Records an error at byte offset [start] that underlines [length] characters.
func (l *lexer) errorAt(start, length int, hint, format string, args ...any) {
	l.errs = append(l.errs, diag.Diagnostic{
		Pos:     l.file.PosOf(start),
		Len:     length,
		Message: fmt.Sprintf(format, args...),
		Hint:    hint,
	})
}

// Inserts a statement end at a line break when the previous token can end
// a statement, unless the next line continues it with `.member`, `?.` or
// `??`.
func (l *lexer) lineBreak(at int) {
	if len(l.toks) == 0 {
		return
	}
	switch l.toks[len(l.toks)-1].Kind {
	case Ident, Int, Float, String, StringTail, True, False, Nil, Question, Return, Break, Continue, RParen, RBracket, RBrace:
	case Gt:
		if !l.closesTypeArgs() {
			return
		}
	default:
		return
	}
	if l.nextLineStartsWithDot(at) {
		return
	}
	l.toks = append(l.toks, Token{Kind: Semi, Text: "\n", Pos: l.file.PosOf(at), End: at})
}

// Reports whether the last token, a `>`, closes type arguments that touch
// the name before them, as in `first<int>`.
func (l *lexer) closesTypeArgs() bool {
	depth := 0
	for i := len(l.toks) - 1; i > 0; i-- {
		switch t := l.toks[i]; t.Kind {
		case Gt:
			depth++
		case Lt:
			depth--
			if depth == 0 {
				prev := l.toks[i-1]
				return prev.Kind == Ident && prev.End == t.Pos.Offset
			}
		case Ident, LBracket, RBracket, Colon, Comma, Question, Fn, LParen, RParen, Arrow:
		default:
			return false
		}
	}
	return false
}

// Reports whether the first code after offset [from] is `.` followed by
// something other than another `.`, or `?.` or `??`, skipping blank lines
// and comments.
func (l *lexer) nextLineStartsWithDot(from int) bool {
	i := from
	for i < len(l.src) {
		switch c := l.src[i]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case strings.HasPrefix(l.src[i:], "//"):
			for i < len(l.src) && l.src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(l.src[i:], "/*"):
			end := strings.Index(l.src[i+2:], "*/")
			if end < 0 {
				return false
			}
			i += end + 4
		default:
			if strings.HasPrefix(l.src[i:], "?.") || strings.HasPrefix(l.src[i:], "??") {
				return true
			}
			return c == '.' && (i+1 >= len(l.src) || l.src[i+1] != '.')
		}
	}
	return false
}

func (l *lexer) skipLineComment() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.pos++
	}
}

// Skips a `/* */` comment, which may nest. A comment spanning lines acts
// as a line break.
func (l *lexer) blockComment() {
	start := l.pos
	l.pos += 2
	depth, lines := 1, false
	for depth > 0 {
		if l.pos >= len(l.src) {
			l.errorAt(start, 2, "close it with */", "unterminated block comment")
			return
		}
		switch {
		case strings.HasPrefix(l.src[l.pos:], "/*"):
			depth++
			l.pos += 2
		case strings.HasPrefix(l.src[l.pos:], "*/"):
			depth--
			l.pos += 2
		default:
			if l.src[l.pos] == '\n' {
				lines = true
			}
			l.pos++
		}
	}
	if lines {
		l.lineBreak(start)
	}
}

func (l *lexer) identifier() {
	start := l.pos
	for l.pos < len(l.src) && (isLetter(l.src[l.pos]) || isDigit(l.src[l.pos])) {
		l.pos++
	}
	text := l.src[start:l.pos]
	if k, ok := keywords[text]; ok {
		l.emit(k, start, l.pos, "")
		return
	}
	l.emit(Ident, start, l.pos, "")
}

// Lexes an integer or float: `42`, `1_000`, `0xFF`, `0b1010`, `0o17`,
// `3.14`, `1e9`, `2.5e-3`.
func (l *lexer) number() {
	start := l.pos
	base := 10
	if l.src[l.pos] == '0' {
		switch l.peek(1) {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
	}
	kind := Int
	if base != 10 {
		l.pos += 2
		digitsStart := l.pos
		l.digits(base)
		if l.pos == digitsStart {
			l.consumeAlnum()
			l.errorAt(start, utf8.RuneCountInString(l.src[start:l.pos]), "", "%s has no digits", l.src[start:start+2])
			l.emit(Int, start, l.pos, "0")
			return
		}
	} else {
		l.digits(10)
		leadingZero := l.src[start] == '0' && l.pos-start > 1
		if l.peek(0) == '.' && isDigit(l.peek(1)) {
			kind = Float
			l.pos++
			l.digits(10)
		}
		if e := l.peek(0); e == 'e' || e == 'E' {
			n := 1
			if s := l.peek(1); s == '+' || s == '-' {
				n = 2
			}
			if isDigit(l.peek(n)) {
				kind = Float
				l.pos += n
				l.digits(10)
			}
		}
		if leadingZero && kind == Int && !isLetter(l.peek(0)) {
			text := strings.ReplaceAll(l.src[start:l.pos], "_", "")
			l.errorAt(start, l.pos-start, "write "+strings.TrimLeft(text[:len(text)-1], "0")+text[len(text)-1:]+", or 0o"+strings.TrimLeft(text, "0")+" for octal", "numbers can't start with 0")
			l.emit(Int, start, l.pos, "0")
			return
		}
	}
	if c := l.peek(0); isLetter(c) || isDigit(c) {
		l.consumeAlnum()
		text := l.src[start:l.pos]
		l.errorAt(start, utf8.RuneCountInString(text), "", "invalid number %q", text)
		l.emit(kind, start, l.pos, "0")
		return
	}
	text := l.src[start:l.pos]
	if bad := misplacedSeparator(text, base); bad >= 0 {
		l.errorAt(start+bad, 1, "put _ only between digits", "misplaced digit separator in %s", text)
	}
	l.emit(kind, start, l.pos, strings.ReplaceAll(text, "_", ""))
}

func (l *lexer) digits(base int) {
	for l.pos < len(l.src) && (isDigitOf(l.src[l.pos], base) || l.src[l.pos] == '_') {
		l.pos++
	}
}

func (l *lexer) consumeAlnum() {
	for l.pos < len(l.src) && (isLetter(l.src[l.pos]) || isDigit(l.src[l.pos])) {
		l.pos++
	}
}

// Returns the index of the first `_` in [text] that is not between two
// digits of [base], or -1 if every separator is valid.
func misplacedSeparator(text string, base int) int {
	from := 0
	if base != 10 {
		from = 2
	}
	for i := from; i < len(text); i++ {
		if text[i] != '_' {
			continue
		}
		if i == from || i+1 >= len(text) || !isDigitOf(text[i-1], base) || !isDigitOf(text[i+1], base) {
			return i
		}
	}
	return -1
}

// Lexes string text up to the closing quote or the next `{`.
//
// [tokStart] is where this token begins (the quote or the `}`), and
// [strStart] is the opening quote of the whole string, used for errors.
func (l *lexer) stringBody(tokStart, strStart int, first bool) {
	var b strings.Builder
	for {
		if l.pos >= len(l.src) || l.src[l.pos] == '\n' {
			l.interp = l.interp[:0]
			end := l.pos
			for end > strStart && l.src[end-1] == '\r' {
				end--
			}
			l.errorAt(strStart, utf8.RuneCountInString(l.src[strStart:end]), `add a closing "`, "unterminated string")
			l.emit(tail(first), tokStart, l.pos, b.String())
			return
		}
		c := l.src[l.pos]
		switch c {
		case '"':
			l.pos++
			l.emit(tail(first), tokStart, l.pos, b.String())
			return
		case '{':
			l.pos++
			kind := StringMid
			if first {
				kind = StringHead
			}
			l.emit(kind, tokStart, l.pos, b.String())
			l.interp = append(l.interp, interpolation{start: strStart})
			return
		case '\\':
			l.escape(&b)
		default:
			b.WriteByte(c)
			l.pos++
		}
	}
}

// Returns the kind for a string piece that ends at a closing quote.
func tail(first bool) Kind {
	if first {
		return String
	}
	return StringTail
}

// Decodes one escape sequence starting at the backslash.
func (l *lexer) escape(b *strings.Builder) {
	start := l.pos
	l.pos++
	if l.pos >= len(l.src) || l.src[l.pos] == '\n' {
		return
	}
	c := l.src[l.pos]
	l.pos++
	switch c {
	case 'n':
		b.WriteByte('\n')
	case 't':
		b.WriteByte('\t')
	case 'r':
		b.WriteByte('\r')
	case '\\', '"', '{', '}':
		b.WriteByte(c)
	case 'u':
		l.unicodeEscape(b, start)
	default:
		r, size := utf8.DecodeRuneInString(l.src[l.pos-1:])
		l.pos += size - 1
		l.errorAt(start, 2, `write \\ for a backslash`, "unknown escape sequence \\%c", r)
	}
}

// Decodes `\u{1F600}` after the `u` has been read.
func (l *lexer) unicodeEscape(b *strings.Builder, start int) {
	const hint = `write it as \u{1F600}`
	if l.peek(0) != '{' {
		l.errorAt(start, 2, hint, "invalid unicode escape")
		return
	}
	l.pos++
	digitsStart := l.pos
	for l.pos < len(l.src) && isDigitOf(l.src[l.pos], 16) {
		l.pos++
	}
	hex := l.src[digitsStart:l.pos]
	if l.peek(0) != '}' || len(hex) == 0 || len(hex) > 6 {
		l.errorAt(start, l.pos-start, hint, "invalid unicode escape")
		return
	}
	l.pos++
	v, _ := strconv.ParseUint(hex, 16, 32)
	r := rune(v)
	if !utf8.ValidRune(r) {
		l.errorAt(start, l.pos-start, "", "invalid unicode code point U+%s", strings.ToUpper(hex))
		return
	}
	b.WriteRune(r)
}

// Reports an interpolation still open at the end of the input.
func (l *lexer) unterminatedInterpolation() {
	start := l.interp[0].start
	l.interp = l.interp[:0]
	l.errorAt(start, 1, `close it with } and "`, "unterminated string interpolation")
}

func (l *lexer) operator() {
	start := l.pos
	for _, op := range operators {
		if strings.HasPrefix(l.src[l.pos:], op.text) {
			l.pos += len(op.text)
			l.emit(op.kind, start, l.pos, "")
			return
		}
	}
	c := l.src[l.pos]
	if c == '\'' {
		l.singleQuoted()
		return
	}
	l.pos++
	hint := ""
	switch c {
	case '&':
		hint = "use && for logical and"
	case '|':
		hint = "use || for logical or"
	case '#':
		hint = "comments start with //"
	}
	l.errorAt(start, 1, hint, "unexpected character %q", c)
}

// Reports a single-quoted literal once and keeps it as a string so lexing
// can continue normally.
func (l *lexer) singleQuoted() {
	start := l.pos
	end := strings.IndexAny(l.src[start+1:], "'\n")
	if end < 0 || l.src[start+1+end] != '\'' {
		l.pos++
		l.errorAt(start, 1, `strings use double quotes: "text"`, "unexpected character '")
		return
	}
	l.pos = start + end + 2
	text := l.src[start:l.pos]
	l.errorAt(start, utf8.RuneCountInString(text), `strings use double quotes: "text"`, "single quotes are not used for strings")
	l.emit(String, start, l.pos, text[1:len(text)-1])
}

// Reports a non-ASCII character outside a string or comment.
func (l *lexer) nonASCII() {
	start := l.pos
	r, size := utf8.DecodeRuneInString(l.src[l.pos:])
	l.pos += size
	hint := ""
	switch r {
	case '“', '”':
		hint = `use straight quotes: "text"`
	case '‘', '’':
		hint = `strings use straight double quotes: "text"`
	default:
		hint = "names may use only ASCII letters, digits and _"
	}
	if r == utf8.RuneError && size == 1 {
		l.errorAt(start, 1, "", "invalid UTF-8 in source")
		return
	}
	l.errorAt(start, 1, hint, "unexpected character %q", r)
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isDigitOf(c byte, base int) bool {
	switch base {
	case 2:
		return c == '0' || c == '1'
	case 8:
		return c >= '0' && c <= '7'
	case 16:
		return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
	}
	return isDigit(c)
}
