package parser

import (
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/vex-language/vex-lang/compiler/ast"
)

// The largest width and precision a format spec accepts.
const (
	maxSpecWidth     = 1000
	maxSpecPrecision = 20
)

// A problem in a format spec, at byte [Offset] of its text and [Len]
// characters long.
type SpecError struct {
	Offset, Len   int
	Message, Hint string
}

// Returns the parts of the format spec [text], the part after the colon
// in `{x:.2}`, or the first problem in it.
func ParseFormatSpec(text string) (*ast.FormatSpec, *SpecError) {
	s := &ast.FormatSpec{Text: text, Precision: -1}
	if text == "" {
		return nil, &SpecError{Len: 1, Message: "empty format spec", Hint: "write a spec such as .2 or >8 after the colon, or remove it"}
	}
	i := 0
	if r, size := utf8.DecodeRuneInString(text); size < len(text) && isAlign(text[size]) && r != utf8.RuneError {
		s.Fill, s.Align = text[:size], text[size]
		i = size + 1
	} else if isAlign(text[0]) {
		s.Align = text[0]
		i = 1
	}
	if i < len(text) && text[i] == '+' {
		s.Plus = true
		i++
	}
	if i < len(text) && text[i] == '#' {
		s.Alt = true
		i++
	}
	zeroAt := i
	if i < len(text) && text[i] == '0' {
		s.Zero = true
		i++
	}
	widthAt := i
	for i < len(text) && isDigit(text[i]) {
		i++
	}
	if i > widthAt {
		w, err := strconv.Atoi(text[widthAt:i])
		if err != nil || w > maxSpecWidth {
			return nil, &SpecError{Offset: widthAt, Len: i - widthAt, Message: fmt.Sprintf("format width %s is too large", text[widthAt:i]), Hint: fmt.Sprintf("use a width of at most %d", maxSpecWidth)}
		}
		s.Width = w
	}
	if s.Zero && s.Width == 0 {
		return nil, &SpecError{Offset: zeroAt, Len: 1, Message: "zero padding needs a width", Hint: "write the width after the 0, as in :04"}
	}
	if s.Zero && s.Align != 0 {
		return nil, &SpecError{Offset: zeroAt, Len: 1, Message: "a format spec can't have both an alignment and zero padding", Hint: "use a fill character instead, as in :0>4"}
	}
	commaAt := i
	if i < len(text) && text[i] == ',' {
		s.Comma = true
		i++
	}
	precAt := i
	if i < len(text) && text[i] == '.' {
		i++
		from := i
		for i < len(text) && isDigit(text[i]) {
			i++
		}
		if i == from {
			return nil, &SpecError{Offset: precAt, Len: 1, Message: "missing precision after '.'", Hint: "write the number of digits after the point, as in :.2"}
		}
		p, err := strconv.Atoi(text[from:i])
		if err != nil || p > maxSpecPrecision {
			return nil, &SpecError{Offset: from, Len: i - from, Message: fmt.Sprintf("format precision %s is too large", text[from:i]), Hint: fmt.Sprintf("use at most %d digits after the point", maxSpecPrecision)}
		}
		s.Precision = p
	}
	typeAt := i
	if i < len(text) && (text[i] == 'x' || text[i] == 'X' || text[i] == 'b' || text[i] == 'o') {
		s.Type = text[i]
		i++
	}
	if i < len(text) {
		r, _ := utf8.DecodeRuneInString(text[i:])
		hint := "a spec is written fill, alignment, +, #, 0, width, comma, .precision, type, as in :>8 or :+08.2"
		if i == 0 && r == ' ' {
			hint = "remove the space: the spec starts right after the colon, as in {x:.2}"
		}
		return nil, &SpecError{Offset: i, Len: 1, Message: fmt.Sprintf("unexpected %q in format spec", r), Hint: hint}
	}
	switch {
	case s.Type != 0 && s.Precision >= 0:
		return nil, &SpecError{Offset: precAt, Len: typeAt - precAt, Message: fmt.Sprintf("a precision can't be used with '%c'", s.Type), Hint: "precision counts decimals, which an int in another base doesn't have"}
	case s.Type != 0 && s.Comma:
		return nil, &SpecError{Offset: commaAt, Len: 1, Message: fmt.Sprintf("thousands separators can't be used with '%c'", s.Type), Hint: "separators apply to decimal numbers only"}
	case s.Alt && s.Type == 0:
		return nil, &SpecError{Offset: zeroAt - 1, Len: 1, Message: "'#' needs a base: x, X, b or o", Hint: "write :#x for a 0x prefix"}
	}
	return s, nil
}

func isAlign(c byte) bool { return c == '<' || c == '>' || c == '^' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
