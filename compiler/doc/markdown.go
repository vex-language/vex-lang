package doc

import (
	"html"
	"regexp"
	"strings"
)

// Finds what a `[Name]` or `[Type.member]` reference in a doc comment
// names. It returns the link to it, or "" for a name that is known but
// has no page, such as a parameter or a built-in type; ok is false when
// nothing has that name.
type Resolver func(ref string) (href string, ok bool)

// Returns the HTML for doc comment [text], written in Markdown:
// paragraphs, lists, block quotes, headings, fenced code blocks, `code`,
// **bold**, *italic*, [links](url) and `[Name]` references, which
// [resolve] turns into links. Also returns the references nothing has the
// name of; they are shown as code.
func Markdown(text string, resolve Resolver) (string, []string) {
	r := &mdRenderer{resolve: resolve}
	r.blocks(strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n"))
	return r.out.String(), r.unresolved
}

// Returns the HTML for the first sentence of [text], for summaries.
func Summary(text string, resolve Resolver) (string, []string) {
	r := &mdRenderer{resolve: resolve}
	r.inline(firstSentence(text))
	return r.out.String(), r.unresolved
}

// Returns the first sentence of the first paragraph of [text].
func firstSentence(text string) string {
	para, _, _ := strings.Cut(strings.TrimSpace(text), "\n\n")
	para = strings.Join(strings.Fields(para), " ")
	inCode := false
	for i := 0; i < len(para); i++ {
		switch para[i] {
		case '`':
			inCode = !inCode
		case '.':
			if !inCode && (i+1 == len(para) || para[i+1] == ' ') {
				return para[:i+1]
			}
		}
	}
	return para
}

type mdRenderer struct {
	out        strings.Builder
	resolve    Resolver
	unresolved []string
}

var (
	listItem    = regexp.MustCompile(`^( {0,3})([-*+]|\d{1,9}[.)])( +|$)`)
	headingRe   = regexp.MustCompile(`^ {0,3}(#{1,6}) +(.*?)[ #]*$`)
	referenceRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
)

func blank(line string) bool { return strings.TrimSpace(line) == "" }

func isFence(line string) bool { return strings.HasPrefix(strings.TrimLeft(line, " "), "```") }

// Writes the blocks of [lines].
func (r *mdRenderer) blocks(lines []string) {
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case blank(line):
			i++
		case isFence(line):
			i = r.fence(lines, i)
		case headingRe.MatchString(line):
			m := headingRe.FindStringSubmatch(line)
			r.out.WriteString("<h4>")
			r.inline(m[2])
			r.out.WriteString("</h4>\n")
			i++
		case strings.HasPrefix(strings.TrimLeft(line, " "), ">"):
			var quoted []string
			for i < len(lines) && !blank(lines[i]) {
				l := strings.TrimLeft(lines[i], " ")
				l = strings.TrimPrefix(l, ">")
				quoted = append(quoted, strings.TrimPrefix(l, " "))
				i++
			}
			r.out.WriteString("<blockquote>\n")
			r.blocks(quoted)
			r.out.WriteString("</blockquote>\n")
		case listItem.MatchString(line):
			i = r.list(lines, i)
		default:
			start := i
			for i < len(lines) && !blank(lines[i]) && !isFence(lines[i]) && !headingRe.MatchString(lines[i]) && (i == start || !listItem.MatchString(lines[i])) {
				i++
			}
			r.out.WriteString("<p>")
			r.inline(strings.TrimSpace(strings.Join(trimAll(lines[start:i]), "\n")))
			r.out.WriteString("</p>\n")
		}
	}
}

func trimAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimSpace(l)
	}
	return out
}

// Writes the fenced code block starting at line [i] and returns the line
// after it.
func (r *mdRenderer) fence(lines []string, i int) int {
	open := strings.TrimLeft(lines[i], " ")
	indent := len(lines[i]) - len(open)
	lang := strings.TrimSpace(strings.TrimLeft(open, "`"))
	var code []string
	for i++; i < len(lines) && !isFence(lines[i]); i++ {
		l := lines[i]
		for n := 0; n < indent && strings.HasPrefix(l, " "); n++ {
			l = l[1:]
		}
		code = append(code, l)
	}
	if lang != "" {
		r.out.WriteString(`<pre><code class="language-` + html.EscapeString(lang) + `">`)
	} else {
		r.out.WriteString("<pre><code>")
	}
	r.out.WriteString(html.EscapeString(strings.Join(code, "\n")))
	r.out.WriteString("</code></pre>\n")
	return i + 1
}

// Writes the list starting at line [i] and returns the line after it.
func (r *mdRenderer) list(lines []string, i int) int {
	first := listItem.FindStringSubmatch(lines[i])
	ordered := first[2][0] >= '0' && first[2][0] <= '9'
	tag := "ul"
	if ordered {
		tag = "ol"
	}
	r.out.WriteString("<" + tag + ">\n")
	for i < len(lines) {
		m := listItem.FindStringSubmatch(lines[i])
		if m == nil || (m[2][0] >= '0' && m[2][0] <= '9') != ordered {
			break
		}
		width := len(m[0])
		item := []string{lines[i][width:]}
		i++
		// The item goes on over lines that aren't another item at the same
		// depth, and over blank lines followed by indented ones.
		for i < len(lines) {
			l := lines[i]
			if blank(l) {
				if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  ") {
					item = append(item, "")
					i++
					continue
				}
				break
			}
			if listItem.MatchString(l) && !strings.HasPrefix(l, "  ") || isFence(l) && !strings.HasPrefix(l, "  ") {
				break
			}
			item = append(item, dedent(l, width))
			i++
		}
		r.out.WriteString("<li>")
		if len(item) > 1 && hasBlocks(item) {
			r.out.WriteString("\n")
			r.blocks(item)
		} else {
			r.inline(strings.TrimSpace(strings.Join(trimAll(item), "\n")))
		}
		r.out.WriteString("</li>\n")
		// A blank line between items doesn't end the list.
		if i+1 < len(lines) && blank(lines[i]) && listItem.MatchString(lines[i+1]) {
			i++
		}
	}
	r.out.WriteString("</" + tag + ">\n")
	return i
}

// Removes up to [n] leading spaces from [line].
func dedent(line string, n int) string {
	for k := 0; k < n && strings.HasPrefix(line, " "); k++ {
		line = line[1:]
	}
	return line
}

// Reports whether the lines of a list item hold more than one paragraph
// of text: a nested list, a code block or a blank line.
func hasBlocks(lines []string) bool {
	for _, l := range lines[1:] {
		if blank(l) || isFence(l) || listItem.MatchString(l) {
			return true
		}
	}
	return false
}

// Writes the inline text [s]: code spans, emphasis, links and references.
func (r *mdRenderer) inline(s string) {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && strings.IndexByte("\\`*_[]()#>-+.!{}", s[i+1]) >= 0:
			r.out.WriteString(html.EscapeString(s[i+1 : i+2]))
			i += 2
		case c == '`':
			n := run(s[i:], '`')
			end := strings.Index(s[i+n:], s[i:i+n])
			if end < 0 {
				r.out.WriteString(s[i : i+n])
				i += n
				break
			}
			code := strings.ReplaceAll(s[i+n:i+n+end], "\n", " ")
			if len(code) > 1 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.Trim(code, " ") != "" {
				code = code[1 : len(code)-1]
			}
			r.out.WriteString("<code>" + html.EscapeString(code) + "</code>")
			i += n + end + n
		case c == '*' || c == '_':
			if next, ok := r.emphasis(s, i); ok {
				i = next
				break
			}
			r.out.WriteByte(c)
			i++
		case c == '[':
			i = r.bracket(s, i)
		case c == '\n':
			r.out.WriteByte('\n')
			i++
		default:
			j := i + 1
			for j < len(s) && strings.IndexByte("\\`*_[\n", s[j]) < 0 {
				j++
			}
			r.out.WriteString(html.EscapeString(s[i:j]))
			i = j
		}
	}
}

// Returns how many times [c] repeats at the start of [s].
func run(s string, c byte) int {
	n := 0
	for n < len(s) && s[n] == c {
		n++
	}
	return n
}

// Writes the bold or italic text starting at offset [i] of [s], and
// returns the offset after it; ok is false when the delimiter there
// doesn't open emphasis.
func (r *mdRenderer) emphasis(s string, i int) (int, bool) {
	c := s[i]
	n := min(run(s[i:], c), 2)
	delim := s[i : i+n]
	// `_` inside a word, as in snake_case, is plain text.
	if c == '_' && i > 0 && isWordByte(s[i-1]) {
		return 0, false
	}
	start := i + n
	if start >= len(s) || s[start] == ' ' || s[start] == '\n' {
		return 0, false
	}
	for j := start; j+n <= len(s); j++ {
		if s[j] == '`' {
			if k := strings.IndexByte(s[j+1:], '`'); k >= 0 {
				j += k + 1
				continue
			}
		}
		if s[j:j+n] != delim || s[j-1] == ' ' || j == start {
			continue
		}
		if n == 1 && j+1 < len(s) && s[j+1] == c {
			continue
		}
		if c == '_' && j+n < len(s) && isWordByte(s[j+n]) {
			continue
		}
		tag := "em"
		if n == 2 {
			tag = "strong"
		}
		r.out.WriteString("<" + tag + ">")
		r.inline(s[start:j])
		r.out.WriteString("</" + tag + ">")
		return j + n, true
	}
	return 0, false
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// Writes the link or reference starting with the `[` at offset [i] of
// [s], and returns the offset after it.
func (r *mdRenderer) bracket(s string, i int) int {
	end := closingBracket(s, i)
	if end < 0 {
		r.out.WriteByte('[')
		return i + 1
	}
	label := s[i+1 : end]
	if end+1 < len(s) && s[end+1] == '(' {
		if close := strings.IndexByte(s[end+2:], ')'); close >= 0 {
			url := strings.TrimSpace(s[end+2 : end+2+close])
			r.out.WriteString(`<a href="` + html.EscapeString(url) + `">`)
			r.inline(label)
			r.out.WriteString("</a>")
			return end + 2 + close + 1
		}
	}
	if !referenceRe.MatchString(label) {
		r.out.WriteByte('[')
		return i + 1
	}
	code := "<code>" + html.EscapeString(label) + "</code>"
	href, ok := "", false
	if r.resolve != nil {
		href, ok = r.resolve(label)
	}
	switch {
	case !ok:
		r.unresolved = append(r.unresolved, label)
		r.out.WriteString(code)
	case href == "":
		r.out.WriteString(code)
	default:
		r.out.WriteString(`<a href="` + html.EscapeString(href) + `">` + code + "</a>")
	}
	return end + 1
}

// Returns the offset of the `]` that closes the `[` at offset [i] of
// [s], or -1.
func closingBracket(s string, i int) int {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '`':
			if k := strings.IndexByte(s[j+1:], '`'); k >= 0 {
				j += k + 1
			}
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return j
			}
		case '\n':
			return -1
		}
	}
	return -1
}
