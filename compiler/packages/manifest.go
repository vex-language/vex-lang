package packages

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vex-language/vex-lang/compiler/diag"
	"github.com/vex-language/vex-lang/compiler/lexer"
	"github.com/vex-language/vex-lang/compiler/modules"
)

// A dependency listed in a project file.
type dependency struct {
	// The name `package:` imports use for it.
	key string
	src source

	// The oldest version it accepts, as written, such as "0.3".
	want string
	min  version
}

// The project file of a project or package, with its dependencies.
type manifest struct {
	name string
	deps []*dependency
	file *diag.File
}

var depKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Reads the project file in [dir]. Problems in the file come back as a
// [diag.List].
func readManifest(dir string) (*manifest, error) {
	p := filepath.Join(dir, modules.ProjectFile)
	src, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no %s in %s", modules.ProjectFile, dir)
		}
		return nil, err
	}
	return parseManifest(diag.NewFile(p, string(src)))
}

// Parses project file [f].
func parseManifest(f *diag.File) (*manifest, error) {
	proj, errs := modules.ParseProject(f)
	m := &manifest{name: proj.Name, file: f}
	seen := map[string]int{}
	section := ""
	offset := 0
	for i, raw := range strings.Split(f.Src, "\n") {
		start := offset
		offset += len(raw) + 1
		line := strings.TrimSpace(stripComment(strings.TrimSuffix(raw, "\r")))
		if strings.HasPrefix(line, "[") {
			section = strings.TrimSpace(strings.Trim(line, "[]"))
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if section != "dependencies" || !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		fail := func(hint, format string, args ...any) {
			col := strings.Index(raw, key)
			errs = append(errs, diag.Diagnostic{File: f, Pos: f.PosOf(start + col), Len: len(key),
				Message: fmt.Sprintf(format, args...), Hint: hint})
		}
		if !depKey.MatchString(key) || lexer.IsKeyword(key) {
			fail("use letters, digits and _, not starting with a digit or being a keyword", "'%s' isn't a valid package name", key)
			continue
		}
		if prev, ok := seen[key]; ok {
			fail("", "'%s' is already a dependency on line %d", key, prev)
			continue
		}
		seen[key] = i + 1
		fields, ok := parseTable(val)
		const form = `name = { git = "https://...", version = "1.0" }`
		if !ok {
			fail(form, "the dependency '%s' must be a table in braces", key)
			continue
		}
		d := &dependency{key: key, src: source{git: fields["git"], path: fields["path"]}, want: fields["version"]}
		for k := range fields {
			if k != "git" && k != "path" && k != "version" {
				fail("a dependency has git, path and version", "unknown key '%s' in dependency '%s'", k, key)
			}
		}
		if d.src.git == "" {
			fail(form, "the dependency '%s' needs the git repository it comes from", key)
			continue
		}
		if strings.HasPrefix(d.src.git, "-") {
			fail("write the repository's URL", "'%s' isn't a git repository", d.src.git)
			continue
		}
		if d.src.path != "" && !validPath(d.src.path) {
			fail("name a folder inside the repository, with /", "the path '%s' of '%s' isn't a folder inside the repository", d.src.path, key)
			continue
		}
		if d.min, ok = parseVersion(d.want); !ok {
			fail(form, "the dependency '%s' needs a version such as \"1.0\" or \"0.3.1\"", key)
			continue
		}
		m.deps = append(m.deps, d)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return m, nil
}

// Reports whether [p] is a relative folder path without `..`.
func validPath(p string) bool {
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// Parses an inline table of strings such as `{ git = "x", version = "1" }`.
func parseTable(s string) (map[string]string, bool) {
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return nil, false
	}
	out := map[string]string{}
	rest := strings.TrimSpace(s[1 : len(s)-1])
	for rest != "" {
		key, after, ok := strings.Cut(rest, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, false
		}
		val, after, ok := cutString(strings.TrimSpace(after))
		if !ok {
			return nil, false
		}
		out[key] = val
		rest = strings.TrimSpace(after)
		if rest == "" {
			break
		}
		if rest[0] != ',' {
			return nil, false
		}
		rest = strings.TrimSpace(rest[1:])
	}
	return out, true
}

// Splits the quoted string at the start of [s] from the text after it.
func cutString(s string) (val, rest string, ok bool) {
	if !strings.HasPrefix(s, `"`) {
		return "", "", false
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
		case '"':
			return b.String(), s[i+1:], true
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", false
}

// Returns [line] without a trailing `#` comment outside quotes.
func stripComment(line string) string {
	in := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if in {
				i++
			}
		case '"':
			in = !in
		case '#':
			if !in {
				return line[:i]
			}
		}
	}
	return line
}

// Returns [s] as a quoted string.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Returns the line of the project file that lists dependency [d].
func (d *dependency) line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s = { git = %s", d.key, quote(d.src.git))
	if d.src.path != "" {
		fmt.Fprintf(&b, ", path = %s", quote(d.src.path))
	}
	fmt.Fprintf(&b, ", version = %s }", quote(d.want))
	return b.String()
}

// Returns the project file text [src] with dependency [d] added, or put in
// place of the line with its key, keeping everything else as it was.
func setDependency(src string, d *dependency) string {
	lines := strings.Split(src, "\n")
	section := ""
	insert := -1
	for i, raw := range lines {
		line := strings.TrimSpace(stripComment(strings.TrimSuffix(raw, "\r")))
		if strings.HasPrefix(line, "[") {
			section = strings.TrimSpace(strings.Trim(line, "[]"))
			if section == "dependencies" {
				insert = i + 1
			}
			continue
		}
		if section != "dependencies" || strings.TrimSpace(raw) == "" {
			continue
		}
		insert = i + 1
		if key, _, ok := strings.Cut(line, "="); ok && strings.TrimSpace(key) == d.key {
			lines[i] = d.line()
			return strings.Join(lines, "\n")
		}
	}
	if insert < 0 {
		src = strings.TrimRight(src, "\n")
		return src + "\n\n[dependencies]\n" + d.line() + "\n"
	}
	lines = append(lines[:insert], append([]string{d.line()}, lines[insert:]...)...)
	return strings.Join(lines, "\n")
}
