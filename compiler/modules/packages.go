package modules

import (
	"path"
	"path/filepath"
	"strings"
)

// Choices for [LoadWith].
type Options struct {
	// Loads the root module's tests, as [LoadTests] does.
	Tests bool

	// The packages the project's `package:` imports name, by their key in
	// the project file.
	Packages map[string]*Package

	// The start of absolute paths, which [Package.Dir] is made relative
	// to when loading from disk.
	volume string
}

// A package a project depends on, downloaded and checked by `vex get`.
type Package struct {
	// The name the package gives itself in its project file.
	Name string

	// The folder holding the package's files.
	Dir string

	// The packages its own `package:` imports name, by their key in its
	// project file.
	Deps map[string]*Package
}

// The files of the project or of one package: the folder their imports
// stay within, how their paths start in messages, and the packages they
// can import.
type scope struct {
	base  string
	shown string
	deps  map[string]*Package

	// The package, or nil for the project.
	pkg *Package
}

// Returns the name of a file at [p], as shown in messages.
func (s *scope) display(p string) string {
	rel := strings.TrimPrefix(strings.TrimPrefix(p, s.base), "/")
	return filepath.Join(s.shown, filepath.FromSlash(rel))
}

// Returns what the scope is, for messages.
func (s *scope) kind() string {
	if s.pkg != nil {
		return "package"
	}
	return "project"
}

// Loads the module of the `package:` import [imp] in module [m], and
// returns it with the name code uses for it by default.
func (l *loader) packageModule(m *Module, imp *Import) (*Module, string) {
	spec := strings.TrimPrefix(imp.Decl.Path, "package:")
	key, sub, _ := strings.Cut(spec, "/")
	if !validName.MatchString(key) {
		l.errorAt(imp, `write "package:name" or "package:name/folder"`, "'%s' isn't a package name", key)
		return nil, ""
	}
	if sub != "" {
		if clean := path.Clean(sub); clean != sub || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(sub, "\\") {
			l.errorAt(imp, `write "package:name/folder", with / between folders`, "'%s' isn't a folder inside the package", sub)
			return nil, ""
		}
	}
	p := m.scope.deps[key]
	if p == nil {
		if m.scope.pkg != nil {
			l.errorAt(imp, "the package's own package.toml must list it", "package '%s' doesn't depend on '%s'", m.scope.pkg.Name, key)
		} else {
			l.errorAt(imp, "add it with: vex get <repository URL>", "no package '%s' in %s", key, ProjectFile)
		}
		return nil, ""
	}
	s := l.packages[p]
	if s == nil {
		s = &scope{base: strings.TrimPrefix(filepath.ToSlash(p.Dir), l.opts.volume), shown: p.Dir, deps: p.Deps, pkg: p}
		if l.packages == nil {
			l.packages = map[*Package]*scope{}
		}
		l.packages[p] = s
	}
	target := l.folder(path.Join(s.base, sub), imp, s)
	name := path.Base(spec)
	if target != nil && target.Path == "" {
		target.Path = imp.Decl.Path
		target.Name = name
	}
	return target, name
}
