package doc

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

// The style of every page: plain CSS that follows the system's light or
// dark appearance.
const style = `:root {
  --bg: #ffffff; --fg: #1f2328; --muted: #59636e; --line: #d1d9e0;
  --code-bg: #f6f8fa; --link: #0969da; --accent: #8250df;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0d1117; --fg: #e6edf3; --muted: #9198a1; --line: #3d444d;
    --code-bg: #151b23; --link: #4493f8; --accent: #ab7df8;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0; background: var(--bg); color: var(--fg);
  font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
}
main { max-width: 52rem; margin: 0 auto; padding: 1.5rem 1rem 4rem; }
a { color: var(--link); text-decoration: none; }
a:hover { text-decoration: underline; }
nav { font-size: .9rem; color: var(--muted); margin-bottom: 1rem; }
h1 { font-size: 2rem; margin: .5rem 0; }
h2 { font-size: 1.4rem; margin-top: 2.5rem; padding-bottom: .3rem; border-bottom: 1px solid var(--line); }
h3 { font-size: 1.15rem; margin: 2rem 0 .4rem; }
h4 { font-size: 1rem; }
code, pre { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: .875rem; }
code { background: var(--code-bg); padding: .1em .3em; border-radius: 4px; }
pre { background: var(--code-bg); padding: .75rem 1rem; border-radius: 6px; overflow-x: auto; line-height: 1.45; }
pre code { background: none; padding: 0; }
pre.sig { border-left: 3px solid var(--accent); margin: .4rem 0; white-space: pre-wrap; }
.import { color: var(--muted); }
.anchor { color: var(--muted); margin-left: .4rem; font-weight: normal; visibility: hidden; }
h3:hover .anchor, .member:hover .anchor { visibility: visible; }
.members { margin-left: 1.25rem; }
.member { margin-top: 1rem; }
.member > .doc { margin-left: .25rem; }
.doc p { margin: .5rem 0; }
blockquote { margin: .5rem 0; padding-left: 1rem; border-left: 3px solid var(--line); color: var(--muted); }
ul.toc, ul.modules { padding-left: 1.25rem; }
ul.toc li, ul.modules li { margin: .2rem 0; }
.summary { color: var(--muted); }
`

// A group of declarations shown under one heading.
type group struct {
	title string
	kinds []Kind
}

var groups = []group{
	{"Types", []Kind{Struct, Enum, Interface, TypeAlias, ExternType}},
	{"Extensions", []Kind{Extension}},
	{"Functions", []Kind{Func}},
	{"Constants and variables", []Kind{Const, Variable}},
}

// Returns the declarations of [m] in group [g], in source order.
func (g group) of(m *Module) []*Decl {
	var out []*Decl
	for _, d := range m.Decls {
		for _, k := range g.kinds {
			if d.Kind == k {
				out = append(out, d)
			}
		}
	}
	return out
}

// Writes the HTML documentation of the set to folder [dir]: an index and
// a page for each module. Returns the files written.
func (s *Set) WriteHTML(dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	pages := map[string]string{"index.html": s.indexHTML()}
	names := []string{"index.html"}
	for _, m := range s.Modules {
		pages[m.Page()] = s.ModuleHTML(m)
		names = append(names, m.Page())
	}
	var written []string
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(pages[name]), 0o644); err != nil {
			return written, err
		}
		written = append(written, path)
	}
	return written, nil
}

// Returns the start of a page titled [title].
func pageStart(title string) string {
	return "<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n" +
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n" +
		"<title>" + html.EscapeString(title) + "</title>\n<style>\n" + style + "</style>\n</head>\n<body>\n<main>\n"
}

const pageEnd = "</main>\n</body>\n</html>\n"

// Returns the index page, which lists the modules.
func (s *Set) indexHTML() string {
	var b strings.Builder
	title := "API documentation"
	if len(s.Modules) > 0 {
		title = s.Modules[0].Name
	}
	b.WriteString(pageStart(title))
	fmt.Fprintf(&b, "<h1>%s</h1>\n<ul class=\"modules\">\n", html.EscapeString(title))
	for _, m := range s.Modules {
		fmt.Fprintf(&b, "<li><a href=\"%s\">%s</a>", m.Page(), html.EscapeString(moduleTitle(m)))
		if sum := s.summaryHTML(m, nil, nil); sum != "" {
			b.WriteString(" <span class=\"summary\">— " + sum + "</span>")
		}
		b.WriteString("</li>\n")
	}
	b.WriteString("</ul>\n" + pageEnd)
	return b.String()
}

// Returns how a module is named in titles: its import path, or its name
// for the root module.
func moduleTitle(m *Module) string {
	if m.Path != "" {
		return m.Path
	}
	return m.Name
}

// Returns the page of module [m].
func (s *Set) ModuleHTML(m *Module) string {
	var b strings.Builder
	b.WriteString(pageStart(moduleTitle(m)))
	b.WriteString("<nav><a href=\"index.html\">Index</a></nav>\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(moduleTitle(m)))
	if m.Path != "" {
		fmt.Fprintf(&b, "<pre class=\"import\"><code>import %s</code></pre>\n", html.EscapeString(quote(m.Path)))
	}
	if m.Doc != "" {
		b.WriteString("<div class=\"doc\">\n" + s.docHTML(m, nil) + "</div>\n")
	}
	if len(m.Decls) == 0 {
		b.WriteString("<p class=\"summary\">This module has no public declarations.</p>\n")
		b.WriteString(pageEnd)
		return b.String()
	}
	b.WriteString("<h2>Contents</h2>\n<ul class=\"toc\">\n")
	for _, g := range groups {
		for _, d := range g.of(m) {
			fmt.Fprintf(&b, "<li><a href=\"#%s\">%s</a>", html.EscapeString(d.Anchor()), html.EscapeString(tocName(d)))
			if sum := s.summaryHTML(m, d, m); sum != "" {
				b.WriteString(" <span class=\"summary\">— " + sum + "</span>")
			}
			b.WriteString("</li>\n")
		}
	}
	b.WriteString("</ul>\n")
	for _, g := range groups {
		decls := g.of(m)
		if len(decls) == 0 {
			continue
		}
		fmt.Fprintf(&b, "<h2>%s</h2>\n", g.title)
		for _, d := range decls {
			s.declHTML(&b, m, d)
		}
	}
	b.WriteString(pageEnd)
	return b.String()
}

// Returns the name of [d] in the table of contents.
func tocName(d *Decl) string {
	if d.Kind == Extension {
		return "extension " + d.Name
	}
	return d.Name
}

// Writes the section of top-level declaration [d] and its members.
func (s *Set) declHTML(b *strings.Builder, m *Module, d *Decl) {
	id := html.EscapeString(d.Anchor())
	fmt.Fprintf(b, "<section id=\"%s\">\n<h3>%s<a class=\"anchor\" href=\"#%s\">#</a></h3>\n", id, html.EscapeString(tocName(d)), id)
	fmt.Fprintf(b, "<pre class=\"sig\"><code>%s</code></pre>\n", html.EscapeString(d.Signature))
	if d.Doc != "" {
		b.WriteString("<div class=\"doc\">\n" + s.docHTML(m, d) + "</div>\n")
	}
	if len(d.Members) > 0 {
		b.WriteString("<div class=\"members\">\n")
		for _, mem := range d.Members {
			mid := html.EscapeString(mem.Anchor())
			fmt.Fprintf(b, "<div class=\"member\" id=\"%s\">\n", mid)
			fmt.Fprintf(b, "<pre class=\"sig\"><code>%s</code><a class=\"anchor\" href=\"#%s\">#</a></pre>\n", html.EscapeString(mem.Signature), mid)
			if mem.Doc != "" {
				b.WriteString("<div class=\"doc\">\n" + s.docHTML(m, mem) + "</div>\n")
			}
			b.WriteString("</div>\n")
		}
		b.WriteString("</div>\n")
	}
	b.WriteString("</section>\n")
}
