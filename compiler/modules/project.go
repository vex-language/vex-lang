package modules

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/vex-language/vex-lang/compiler/diag"
	"github.com/vex-language/vex-lang/compiler/lexer"
)

// The contents of a project file.
type Project struct {
	Name    string
	Version string

	// The Vex version the package needs, such as "0.2".
	Vex string

	// C code built into the program: source files relative to the project
	// folder, libraries to link, and extra flags for the C compiler.
	CSources []string
	CLibs    []string
	CFlags   []string

	// Apple frameworks to link, such as Foundation or UIKit.
	CFrameworks []string

	// The C code for one target only, from sections such as [c.macos],
	// by the name of the target.
	CTargets map[string]*Project
}

var (
	cLib = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.+-]*$`)

	// The forms each allowed C compiler flag may take. None can load code
	// into the compiler, pass options on to other tools, or write files.
	cFlagForms = []*regexp.Regexp{
		regexp.MustCompile(`^-D[A-Za-z_][A-Za-z0-9_]*(=.*)?$`),
		regexp.MustCompile(`^-U[A-Za-z_][A-Za-z0-9_]*$`),
		regexp.MustCompile(`^-O([0-3sz]|fast)$`),
		regexp.MustCompile(`^-f(no-)?[a-z0-9][a-z0-9-]*$`),
		regexp.MustCompile(`^-f(visibility=(default|hidden)|sanitize=[a-z,-]+|fp-contract=(on|off|fast))$`),
		regexp.MustCompile(`^-W(no-)?[a-z0-9][a-z0-9=+-]*$`),
		regexp.MustCompile(`^-m[a-z0-9][a-z0-9.=-]*$`),
		regexp.MustCompile(`^-std=[a-z0-9+]+$`),
	}

	// Flags of an allowed form that still load code or write files.
	cFlagsDenied = regexp.MustCompile(`^-f(no-)?(plugin|pass-plugin|load|crash|save|record|profile|coverage|dump|debug|modules-cache|diagnostics-|time-trace)`)
)

// Reports whether [flag] may be passed to the C compiler.
func cFlagAllowed(flag string) bool {
	if strings.HasPrefix(flag, "-I") || strings.HasPrefix(flag, "-L") {
		dir := flag[2:]
		return dir != "" && !path.IsAbs(dir) && !strings.Contains(dir, "\\") && !strings.HasPrefix(path.Clean(dir), "..")
	}
	if cFlagsDenied.MatchString(flag) {
		return false
	}
	for _, form := range cFlagForms {
		if form.MatchString(flag) {
			return true
		}
	}
	return false
}

// Parses a project file: `[section]` headers and `key = value` lines,
// where a value is a string in quotes or a list of them, with `#`
// comments.
func ParseProject(f *diag.File) (*Project, diag.List) {
	proj := &Project{}
	var errs diag.List
	start := 0
	fail := func(col int, length int, hint, format string, args ...any) {
		errs = append(errs, diag.Diagnostic{
			File:    f,
			Pos:     f.PosOf(start + col - 1),
			Len:     length,
			Message: fmt.Sprintf(format, args...),
			Hint:    hint,
		})
	}
	section := ""
	seen := map[string]int{}
	lines := strings.Split(f.Src, "\n")
	next := 0
	for i := 0; i < len(lines); i++ {
		start = next
		next += len(lines[i]) + 1
		n := i + 1
		raw := strings.TrimSuffix(lines[i], "\r")
		line := strings.TrimSpace(stripComment(raw))
		col := strings.Index(raw, line) + 1
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "["):
			if !strings.HasSuffix(line, "]") {
				fail(col, len(line), "close it with ]", "unterminated section header")
				continue
			}
			section = strings.TrimSpace(line[1 : len(line)-1])
			switch {
			case section == "package", section == "c", section == "dependencies", section == "dev-dependencies":
			case strings.HasPrefix(section, "c."):
				if !slices.Contains(Targets, section[2:]) {
					fail(col, len(line), "use [c."+strings.Join(Targets, "], [c.")+"]", "unknown target '%s'", section[2:])
				}
			default:
				fail(col, len(line), "use [package], [c], [dependencies] or [dev-dependencies]", "unknown section [%s]", section)
			}
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			fail(col, len(line), `write key = "value"`, "expected a key and a value")
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		vcol := col + strings.Index(line, val)
		if strings.HasPrefix(val, "[") {
			// A list may continue over the following lines.
			for !strings.Contains(stripQuoted(val), "]") && i+1 < len(lines) {
				i++
				next += len(lines[i]) + 1
				val += " " + strings.TrimSpace(stripComment(strings.TrimSuffix(lines[i], "\r")))
			}
		}
		cSection := section == "c" || strings.HasPrefix(section, "c.")
		if section != "package" && !cSection {
			// Dependencies are read by vex get.
			continue
		}
		id := section + "." + key
		if prev, ok := seen[id]; ok {
			fail(col, len(key), "", "'%s' is already set on line %d", key, prev)
			continue
		}
		seen[id] = n
		if cSection {
			dst := proj
			if section != "c" {
				if proj.CTargets == nil {
					proj.CTargets = map[string]*Project{}
				}
				if proj.CTargets[section[2:]] == nil {
					proj.CTargets[section[2:]] = &Project{}
				}
				dst = proj.CTargets[section[2:]]
			}
			list, ok := parseList(val)
			if !ok {
				fail(vcol, len(strings.SplitN(val, " ", 2)[0]), fmt.Sprintf(`%s = ["..."]`, key), "the value of '%s' must be a list of strings", key)
				continue
			}
			for _, item := range list {
				switch key {
				case "sources":
					if item == "" || path.IsAbs(item) || strings.Contains(item, "\\") || strings.HasPrefix(path.Clean(item), "..") {
						fail(vcol, 1, "list files inside the project, relative to it, with /", "'%s' is outside the project", item)
					} else if ext := path.Ext(item); ext != ".c" && ext != ".m" && ext != ".o" && ext != ".a" {
						fail(vcol, 1, "list .c and Objective-C .m files, or built .o and .a files", "'%s' isn't a C source file", item)
					}
				case "frameworks":
					if !cLib.MatchString(item) {
						fail(vcol, 1, `name the framework without -framework, e.g. "Foundation"`, "'%s' isn't a framework name", item)
					}
				case "libs":
					if !cLib.MatchString(item) {
						fail(vcol, 1, `name the library without -l, e.g. "z" for libz`, "'%s' isn't a library name", item)
					}
				case "flags":
					if !cFlagAllowed(item) {
						fail(vcol, 1, "allowed are -D, -U, -O, -std=, warning -W flags, -f and -m options, and -I or -L folders inside the project", "flag '%s' isn't allowed", item)
					}
				}
			}
			switch key {
			case "sources":
				dst.CSources = list
			case "libs":
				dst.CLibs = list
			case "flags":
				dst.CFlags = list
			case "frameworks":
				dst.CFrameworks = list
			default:
				fail(col, len(key), "known keys are sources, libs, frameworks and flags", "unknown key '%s' in [%s]", key, section)
			}
			continue
		}
		if len(val) < 2 || val[0] != '"' || val[len(val)-1] != '"' {
			fail(vcol, len(val), fmt.Sprintf("%s = \"...\"", key), "the value of '%s' must be a string in quotes", key)
			continue
		}
		s := val[1 : len(val)-1]
		switch key {
		case "name":
			if !validName.MatchString(s) || lexer.IsKeyword(s) {
				fail(vcol, len(val), "use letters, digits and _, not starting with a digit or being a keyword", "'%s' isn't a valid package name", s)
				continue
			}
			proj.Name = s
		case "version":
			proj.Version = s
		case "vex":
			proj.Vex = s
		case "description", "repository", "license", "homepage":
		default:
			fail(col, len(key), "known keys are name, version, vex, description, repository, license, homepage", "unknown key '%s' in [package]", key)
		}
	}
	if proj.Name == "" && len(errs) == 0 {
		errs = append(errs, diag.Diagnostic{File: f, Pos: diag.Pos{Line: 1, Col: 1}, Len: 1,
			Message: "the project file needs a package name", Hint: "[package]\nname = \"myapp\""})
	}
	return proj, errs
}

// Parses a list of strings such as `["a", "b"]`, allowing a trailing comma.
func parseList(val string) ([]string, bool) {
	if len(val) < 2 || val[0] != '[' || val[len(val)-1] != ']' {
		return nil, false
	}
	out := []string{}
	rest := strings.TrimSpace(val[1 : len(val)-1])
	for rest != "" {
		if rest[0] != '"' {
			return nil, false
		}
		var b strings.Builder
		i := 1
		for ; i < len(rest) && rest[i] != '"'; i++ {
			if rest[i] == '\\' && i+1 < len(rest) {
				i++
			}
			b.WriteByte(rest[i])
		}
		if i >= len(rest) {
			return nil, false
		}
		out = append(out, b.String())
		rest = strings.TrimSpace(rest[i+1:])
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

// Returns [s] without the text inside quotes.
func stripQuoted(s string) string {
	var b strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		switch {
		case in && s[i] == '\\':
			i++
		case s[i] == '"':
			in = !in
		case !in:
			b.WriteByte(s[i])
		}
	}
	return b.String()
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
