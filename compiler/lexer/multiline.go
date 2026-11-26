package lexer

import (
	"strings"
	"unicode/utf8"
)

// A multi-line string: `"""` on its own line, the text, and `"""` again
// on its own line, whose indentation is removed from every line.
type multiline struct {
	// The byte offset of the opening `"""`, or of the `r` before it.
	start int

	// The indentation of the closing `"""`, and its byte offset; -1 when
	// the string is never closed.
	indent string
	close  int

	raw bool
}

// Lexes `r"..."`, where `\` and `{` are plain text, after the `r`.
func (l *lexer) rawString(start int) {
	l.pos++
	from := l.pos
	for l.pos < len(l.src) && l.src[l.pos] != '"' && l.src[l.pos] != '\n' {
		l.pos++
	}
	if l.pos >= len(l.src) || l.src[l.pos] == '\n' {
		text := strings.TrimRight(l.src[from:l.pos], "\r")
		l.errorAt(start, utf8.RuneCountInString(l.src[start:from+len(text)]), `add a closing "`, "unterminated string")
		l.emit(String, start, l.pos, text)
		return
	}
	text := l.src[from:l.pos]
	l.pos++
	l.emit(String, start, l.pos, text)
}

// Lexes a multi-line string from its opening `"""`.
func (l *lexer) multilineString(start int, raw bool) {
	l.pos += 3
	m := &multiline{start: start, raw: raw, close: l.findClose(l.pos, raw)}
	// The text starts on the line after the opening quotes.
	rest := l.pos
	for rest < len(l.src) && (l.src[rest] == ' ' || l.src[rest] == '\t' || l.src[rest] == '\r') {
		rest++
	}
	closed := m.close >= 0
	early := rest < len(l.src) && l.src[rest] != '\n' && rest != m.close
	if early && closed {
		l.errorAt(rest, 1, `put the text on the line after """`, "a multi-line string starts on the line after its opening \"\"\"")
	}
	if m.close < 0 {
		l.errorAt(start, l.pos-start, `add a closing """ on its own line`, "unterminated multi-line string")
		m.close = len(l.src)
	} else {
		lineStart := strings.LastIndexByte(l.src[:m.close], '\n') + 1
		before := l.src[lineStart:m.close]
		if strings.Trim(before, " \t") != "" || lineStart <= l.pos {
			l.errorAt(m.close, 3, `put the closing """ on its own line`, "the closing \"\"\" of a multi-line string goes on its own line")
		} else {
			m.indent = before
		}
	}
	if closed && rest == m.close {
		// Both quotes on one line: an empty string, already reported.
		l.pos = m.close + 3
		l.emit(String, start, l.pos, "")
		return
	}
	l.pos = rest
	if l.pos < len(l.src) && l.src[l.pos] == '\n' {
		l.pos++
	}
	l.multilineBody(start, true, m, !early)
}

// Returns the offset of the `"""` that closes a string whose text starts
// at [from], or -1. In a string that isn't raw, `\"""` doesn't close it.
func (l *lexer) findClose(from int, raw bool) int {
	for i := from; i+3 <= len(l.src); i++ {
		if !raw && l.src[i] == '\\' {
			i++
			continue
		}
		if strings.HasPrefix(l.src[i:], `"""`) {
			return i
		}
	}
	return -1
}

// Lexes the text of [m] up to its closing quotes or the next `{`.
// [lineStart] is set when the text continues at the start of a line.
func (l *lexer) multilineBody(tokStart int, first bool, m *multiline, lineStart bool) {
	var b strings.Builder
	if l.pos > m.close {
		// An interpolation ran past the closing quotes.
		l.errorAt(m.close, 3, `close the interpolation with } before the closing """`, "the closing \"\"\" of this string is inside an interpolation")
		l.emit(tail(first), tokStart, l.pos, "")
		return
	}
	for {
		if lineStart {
			l.stripIndent(m)
			lineStart = false
		}
		if l.pos >= m.close {
			l.pos = min(m.close+3, len(l.src))
			l.emit(tail(first), tokStart, l.pos, b.String())
			return
		}
		c := l.src[l.pos]
		switch {
		case c == '\n' || c == '\r' && l.peek(1) == '\n':
			if c == '\r' {
				l.pos++
			}
			l.pos++
			if l.closingLine(m) {
				l.pos = m.close
				break
			}
			b.WriteByte('\n')
			lineStart = true
		case c == '{' && !m.raw:
			l.pos++
			kind := StringMid
			if first {
				kind = StringHead
			}
			l.emit(kind, tokStart, l.pos, b.String())
			l.interp = append(l.interp, interpolation{start: m.start, multi: m})
			return
		case c == '\\' && !m.raw:
			// A backslash at the end of a line joins it to the next one.
			if next := l.src[l.pos+1:]; strings.HasPrefix(next, "\n") || strings.HasPrefix(next, "\r\n") {
				l.pos += 1 + strings.IndexByte(next, '\n') + 1
				if l.closingLine(m) {
					l.pos = m.close
					break
				}
				lineStart = true
				break
			}
			l.escape(&b)
		default:
			b.WriteByte(c)
			l.pos++
		}
	}
}

// Reports whether the line at the current position holds only the
// closing quotes of [m].
func (l *lexer) closingLine(m *multiline) bool {
	return m.close >= l.pos && strings.Trim(l.src[l.pos:m.close], " \t") == "" && !strings.Contains(l.src[l.pos:m.close], "\n")
}

// Skips the indentation of the closing quotes at the start of a line,
// reporting a line that is indented less. Blank lines may be shorter.
func (l *lexer) stripIndent(m *multiline) {
	if strings.HasPrefix(l.src[l.pos:], m.indent) {
		l.pos += len(m.indent)
		return
	}
	end := l.pos
	for end < len(l.src) && (l.src[end] == ' ' || l.src[end] == '\t') {
		end++
	}
	if end >= len(l.src) || l.src[end] == '\n' || l.src[end] == '\r' {
		l.pos = end
		return
	}
	l.errorAt(l.pos, max(end-l.pos, 1), `indent it at least as much as the closing """`, "this line is indented less than the closing \"\"\" of its string")
	l.pos = end
}
