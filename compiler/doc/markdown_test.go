package doc

import (
	"reflect"
	"testing"
)

func TestMarkdown(t *testing.T) {
	resolve := func(ref string) (string, bool) {
		switch ref {
		case "Point", "Point.x":
			return "#" + ref, true
		case "count":
			return "", true
		}
		return "", false
	}
	cases := []struct{ src, want string }{
		{"Hello.", "<p>Hello.</p>\n"},
		{"One\ntwo.\n\nThree.", "<p>One\ntwo.</p>\n<p>Three.</p>\n"},
		{"Uses `a < b` here.", "<p>Uses <code>a &lt; b</code> here.</p>\n"},
		{"Some ``code with ` inside``.", "<p>Some <code>code with ` inside</code>.</p>\n"},
		{"**bold** and *it* and _it_", "<p><strong>bold</strong> and <em>it</em> and <em>it</em></p>\n"},
		{"snake_case_name and 2 * 3 * 4", "<p>snake_case_name and 2 * 3 * 4</p>\n"},
		{"See [the site](https://example.com).", "<p>See <a href=\"https://example.com\">the site</a>.</p>\n"},
		{"A [Point] at [Point.x].", "<p>A <a href=\"#Point\"><code>Point</code></a> at <a href=\"#Point.x\"><code>Point.x</code></a>.</p>\n"},
		{"Adds [count].", "<p>Adds <code>count</code>.</p>\n"},
		{"Not refs: [], [a b], `[Point]`.", "<p>Not refs: [], [a b], <code>[Point]</code>.</p>\n"},
		{"Escaped \\*star\\* and <tag> & co.", "<p>Escaped *star* and &lt;tag&gt; &amp; co.</p>\n"},
		{"- one\n- two\n  more\n- three", "<ul>\n<li>one</li>\n<li>two\nmore</li>\n<li>three</li>\n</ul>\n"},
		{"Steps:\n1. first\n2. second", "<p>Steps:</p>\n<ol>\n<li>first</li>\n<li>second</li>\n</ol>\n"},
		{"- outer\n  - inner", "<ul>\n<li>\n<p>outer</p>\n<ul>\n<li>inner</li>\n</ul>\n</li>\n</ul>\n"},
		{"```swift\nlet x = 1 < 2\n\n  [Point]\n```", "<pre><code class=\"language-swift\">let x = 1 &lt; 2\n\n  [Point]</code></pre>\n"},
		{"# Usage\n\nText", "<h4>Usage</h4>\n<p>Text</p>\n"},
		{"> quoted *text*", "<blockquote>\n<p>quoted <em>text</em></p>\n</blockquote>\n"},
	}
	for _, c := range cases {
		got, bad := Markdown(c.src, resolve)
		if got != c.want {
			t.Errorf("Markdown(%q) =\n%q\nwant\n%q", c.src, got, c.want)
		}
		if len(bad) != 0 {
			t.Errorf("Markdown(%q) unresolved %v", c.src, bad)
		}
	}
}

func TestMarkdownUnresolved(t *testing.T) {
	got, bad := Markdown("Uses [Missing] and [a.b].", func(string) (string, bool) { return "", false })
	if want := "<p>Uses <code>Missing</code> and <code>a.b</code>.</p>\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !reflect.DeepEqual(bad, []string{"Missing", "a.b"}) {
		t.Errorf("unresolved %v", bad)
	}
}

func TestSummary(t *testing.T) {
	cases := []struct{ src, want string }{
		{"Returns the sum. More text.", "Returns the sum."},
		{"Reads `a.b` first. Then more.", "Reads <code>a.b</code> first."},
		{"Spans\ntwo lines.\n\nSecond paragraph.", "Spans two lines."},
		{"No period", "No period"},
	}
	for _, c := range cases {
		if got, _ := Summary(c.src, nil); got != c.want {
			t.Errorf("Summary(%q) = %q, want %q", c.src, got, c.want)
		}
	}
}
