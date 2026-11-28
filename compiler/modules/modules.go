// Package modules finds and parses the files of a Vex program: the root
// module, the project folders it imports, and the standard library.
package modules

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/vex-language/vex-lang/compiler/ast"
	"github.com/vex-language/vex-lang/compiler/cc"
	"github.com/vex-language/vex-lang/compiler/diag"
	"github.com/vex-language/vex-lang/compiler/lexer"
	"github.com/vex-language/vex-lang/compiler/parser"
)

//go:embed std
var stdFS embed.FS

// The name of the project file at the root of a project folder.
const ProjectFile = "package.toml"

// A module: the `.vx` files of one folder, which share their names.
type Module struct {
	// How the module is imported, such as `vex:math` or `models`; empty
	// for the root module.
	Path string

	// The name other modules use for it by default, such as `math`.
	Name string

	Files []*diag.File
	Trees []*ast.File

	// The modules each file imports, by the name the file uses for them.
	Imports map[*diag.File]map[string]*Import

	// Set for the module holding `main`.
	Root bool

	dir   string
	std   bool
	scope *scope
}

// An import of a module by one file.
type Import struct {
	Module *Module
	Decl   *ast.Import
	File   *diag.File
}

// A whole program: its modules, the root first.
type Program struct {
	Modules []*Module
	Root    *Module

	// The package name from the project file, or "" for a single file.
	Name string

	// The project folder on disk, set by [Load] for a project.
	Dir string

	// C code to build into the program, from the project file's [c]
	// section; source paths are relative to [Dir].
	CSources []string
	CLibs    []string
	CFlags   []string

	// Apple frameworks to link, such as Foundation or UIKit.
	CFrameworks []string

	// The system the program is built for.
	Target Target

	// Set for a program built to run its tests: the root module's
	// `_test.vx` files are loaded, and its test blocks become [Tests],
	// functions named `test#1`, `test#2` and so on. Otherwise test blocks
	// are left out.
	Testing bool
	Tests   []*ast.FuncDecl
}

// Returns the C code of the project file, with paths made absolute, for
// the C compiler.
func (p *Program) CCOptions() cc.Options {
	opts := cc.Options{Libs: p.CLibs, Frameworks: p.CFrameworks}
	for _, s := range p.CSources {
		opts.Sources = append(opts.Sources, filepath.Join(p.Dir, filepath.FromSlash(s)))
	}
	for _, f := range p.CFlags {
		// Include and library folders are relative to the project.
		for _, prefix := range []string{"-I", "-L"} {
			if dir, ok := strings.CutPrefix(f, prefix); ok && !filepath.IsAbs(dir) {
				f = prefix + filepath.Join(p.Dir, filepath.FromSlash(dir))
			}
		}
		opts.Flags = append(opts.Flags, f)
	}
	for _, m := range p.Modules {
		if m.std && m.Name == "crypto" {
			p.addCrypto(&opts)
		}
	}
	return opts
}

// Adds what `vex:crypto` builds on to [opts]: Monocypher's sources, the
// code that reaches the system's own cryptography library (CryptoKit
// through Swift on Apple systems, CNG on Windows), and that library.
func (p *Program) addCrypto(opts *cc.Options) {
	for _, dir := range []string{"std/crypto", "std/crypto/monocypher"} {
		entries, _ := fs.ReadDir(stdFS, dir)
		for _, e := range entries {
			ext := path.Ext(e.Name())
			if ext == ".c" || ext == ".h" || ext == ".swift" && p.Target.OS == "macos" {
				text, _ := fs.ReadFile(stdFS, dir+"/"+e.Name())
				opts.Files = append(opts.Files, cc.File{Name: e.Name(), Text: text})
			}
		}
	}
	switch p.Target.OS {
	case "macos":
		opts.LinkArgs = append(opts.LinkArgs, "-framework", "Security", "-framework", "CoreFoundation")
	case "linux":
		opts.LinkArgs = append(opts.LinkArgs, "-lcrypto")
	case "windows":
		opts.LinkArgs = append(opts.LinkArgs, "-lbcrypt")
	}
}

// Returns the warnings of every file, each marked with its file.
func (p *Program) Warnings() diag.List {
	var out diag.List
	for _, m := range p.Modules {
		for i, t := range m.Trees {
			for _, w := range t.Warnings {
				w.File = m.Files[i]
				out = append(out, w)
			}
		}
	}
	return out
}

// Loads the program at [target]: a project folder with a project file, or
// a single `.vx` file. On errors the result is a [diag.List] when the
// problems are in source files.
func Load(target string) (*Program, error) { return LoadWith(target, Options{}) }

// Loads the program at [target] like [Load], with its tests.
func LoadTests(target string) (*Program, error) { return LoadWith(target, Options{Tests: true}) }

// Loads the program at [target] like [Load], with the choices in [opts].
func LoadWith(target string, opts Options) (*Program, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	fsys := os.DirFS(filepath.VolumeName(abs) + string(filepath.Separator))
	rel := filepath.ToSlash(strings.TrimPrefix(abs, filepath.VolumeName(abs)+string(filepath.Separator)))
	opts.volume = filepath.ToSlash(filepath.VolumeName(abs) + string(filepath.Separator))
	prog, err := loadFS(fsys, rel, target, info.IsDir(), opts)
	if prog != nil && info.IsDir() {
		prog.Dir = abs
	}
	return prog, err
}

// Loads a program from [fsys]. [root] is the folder or file within [fsys],
// and [shown] is how file names start in messages.
func LoadFS(fsys fs.FS, root, shown string, isDir bool) (*Program, error) {
	return loadFS(fsys, root, shown, isDir, Options{})
}

// Loads a program from [fsys] like [LoadFS], with its tests when [tests]
// is set.
func LoadTestsFS(fsys fs.FS, root, shown string, isDir bool) (*Program, error) {
	return loadFS(fsys, root, shown, isDir, Options{Tests: true})
}

func loadFS(fsys fs.FS, root, shown string, isDir bool, opts Options) (*Program, error) {
	tests := opts.Tests
	l := &loader{fsys: fsys, shown: shown, modules: map[string]*Module{}, tests: tests, opts: opts}
	if isDir {
		l.base = root
	} else {
		l.base = path.Dir(root)
		l.shown = filepath.Dir(shown)
	}
	l.main = &scope{base: l.base, shown: l.shown, deps: opts.Packages}
	l.prog = &Program{Target: Host(), Testing: tests}
	var m *Module
	if isDir {
		proj, err := l.project(root)
		if err != nil {
			return nil, err
		}
		l.prog.Name = proj.Name
		l.prog.CSources, l.prog.CLibs, l.prog.CFlags, l.prog.CFrameworks = proj.CSources, proj.CLibs, proj.CFlags, proj.CFrameworks
		if t := proj.CTargets[l.prog.Target.OS]; t != nil {
			l.prog.CSources = append(slices.Clip(l.prog.CSources), t.CSources...)
			l.prog.CLibs = append(slices.Clip(l.prog.CLibs), t.CLibs...)
			l.prog.CFlags = append(slices.Clip(l.prog.CFlags), t.CFlags...)
			l.prog.CFrameworks = append(slices.Clip(l.prog.CFrameworks), t.CFrameworks...)
		}
		for _, src := range l.prog.CSources {
			if _, err := fs.Stat(l.fsys, path.Join(root, src)); err != nil {
				return nil, fmt.Errorf("%s: C source '%s' not found", l.display(path.Join(root, ProjectFile)), src)
			}
		}
		m = l.folder(root, nil, l.main)
		if m != nil && len(m.Files) == 0 {
			return nil, fmt.Errorf("no .vx files in %s", shown)
		}
	} else {
		m = &Module{Imports: map[*diag.File]map[string]*Import{}, dir: l.base, scope: l.main}
		l.modules[l.base] = m
		l.prog.Modules = append(l.prog.Modules, m)
		if err := l.addFile(m, root, shown); err != nil {
			return nil, err
		}
	}
	if m == nil {
		return nil, l.errs
	}
	m.Root = true
	m.Name = "main"
	l.prog.Root = m
	for i := 0; i < len(l.prog.Modules); i++ {
		l.resolveImports(l.prog.Modules[i])
	}
	if l.modules["vex:"+CoreName] == nil {
		l.resolveImports(l.stdModule(CoreName, nil))
	}
	l.takeTests()
	for i, m := range l.prog.Modules {
		for _, f := range m.Files {
			f.Module = i
		}
	}
	if len(l.errs) > 0 {
		return l.prog, l.errs
	}
	return l.prog, nil
}

type loader struct {
	fsys    fs.FS
	base    string
	shown   string
	modules map[string]*Module
	prog    *Program
	errs    diag.List
	tests   bool
	opts    Options

	// The project's own files, and the scope of each package loaded.
	main     *scope
	packages map[*Package]*scope
}

// Replaces the test blocks of the root module with their functions when
// loading tests, and removes all others.
func (l *loader) takeTests() {
	for _, m := range l.prog.Modules {
		for _, tree := range m.Trees {
			decls := tree.Decls[:0]
			for _, d := range tree.Decls {
				t, ok := d.(*ast.TestDecl)
				switch {
				case !ok:
					decls = append(decls, d)
				case l.tests && m.Root:
					t.Fn.Name.Name = fmt.Sprintf("test#%d", len(l.prog.Tests)+1)
					l.prog.Tests = append(l.prog.Tests, t.Fn)
					decls = append(decls, t.Fn)
				}
			}
			tree.Decls = decls
		}
	}
}

// Returns the name of a file at [p] in [fsys], as shown in messages.
func (l *loader) display(p string) string { return l.main.display(p) }

func (l *loader) addFile(m *Module, p, shown string) error {
	src, err := fs.ReadFile(l.fsys, p)
	if err != nil {
		return err
	}
	f := diag.NewFile(shown, string(src))
	tree, err := parser.Parse(f)
	var list diag.List
	if errors.As(err, &list) {
		for _, d := range list {
			d.File = f
			l.errs = append(l.errs, d)
		}
	} else if err != nil {
		return err
	}
	if tree == nil {
		tree = &ast.File{}
	}
	m.Files = append(m.Files, f)
	m.Trees = append(m.Trees, tree)
	return nil
}

// Loads the module in folder [dir] of scope [s], once. [imp] is the
// import that asked for it, for errors; nil for the root.
func (l *loader) folder(dir string, imp *Import, s *scope) *Module {
	if m := l.modules[dir]; m != nil {
		return m
	}
	entries, err := fs.ReadDir(l.fsys, dir)
	if err != nil {
		if imp != nil {
			hint := "check the folder name; paths are relative to the importing file"
			if strings.HasPrefix(imp.Decl.Path, "package:") {
				hint = "check the folder name inside the package"
			}
			l.errorAt(imp, hint, "no folder '%s'", imp.Decl.Path)
		}
		return nil
	}
	m := &Module{Imports: map[*diag.File]map[string]*Import{}, dir: dir, scope: s}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".vx") && (!strings.HasSuffix(e.Name(), "_test.vx") || l.tests && imp == nil) {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 && imp != nil {
		l.errorAt(imp, "", "folder '%s' has no .vx files", imp.Decl.Path)
		return nil
	}
	sort.Strings(names)
	l.modules[dir] = m
	l.prog.Modules = append(l.prog.Modules, m)
	for _, n := range names {
		p := path.Join(dir, n)
		if err := l.addFile(m, p, s.display(p)); err != nil {
			l.errs = append(l.errs, diag.Diagnostic{Message: err.Error()})
		}
	}
	return m
}

// Loads standard library module [name], once.
func (l *loader) stdModule(name string, imp *Import) *Module {
	key := "vex:" + name
	if m := l.modules[key]; m != nil {
		return m
	}
	dir := "std/" + name
	entries, err := fs.ReadDir(stdFS, dir)
	if err != nil || !validName.MatchString(name) {
		var names []string
		all, _ := fs.ReadDir(stdFS, "std")
		for _, e := range all {
			if e.Name() != CoreName {
				names = append(names, "vex:"+e.Name())
			}
		}
		l.errorAt(imp, "the standard library has "+strings.Join(names, ", "), "unknown module '%s'", imp.Decl.Path)
		return nil
	}
	m := &Module{Path: key, Name: name, Imports: map[*diag.File]map[string]*Import{}, std: true}
	l.modules[key] = m
	l.prog.Modules = append(l.prog.Modules, m)
	type source struct{ name, text string }
	var sources []source
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".vx") {
			text, _ := fs.ReadFile(stdFS, dir+"/"+e.Name())
			sources = append(sources, source{e.Name(), string(text)})
		}
	}
	if name == "os" {
		sources = append(sources, source{"target.vx", l.prog.Target.source()})
	}
	for _, s := range sources {
		f := diag.NewFile(key+"/"+s.name, s.text)
		tree, err := parser.Parse(f)
		if err != nil {
			l.errs = append(l.errs, diag.Diagnostic{File: f, Message: "standard library: " + err.Error()})
			continue
		}
		m.Files = append(m.Files, f)
		m.Trees = append(m.Trees, tree)
	}
	return m
}

// The name of the standard module whose public declarations every file
// can use without importing it, such as `Iterable`.
const CoreName = "core"

// Returns a fresh copy of the core module, for programs built without a
// loader.
func Core() (*Module, error) {
	l := &loader{modules: map[string]*Module{}, prog: &Program{Target: Host()}}
	m := l.stdModule(CoreName, nil)
	if len(l.errs) > 0 {
		return nil, l.errs
	}
	m.Imports[m.Files[0]] = map[string]*Import{}
	return m, nil
}

// Reports whether [m] is the core module.
func (m *Module) Core() bool { return m.std && m.Name == CoreName }

// Reports whether [m] belongs to the standard library.
func (m *Module) Std() bool { return m.std }

// Loads standard library module [name], such as `json`, on its own,
// without the modules it imports.
func LoadStd(name string) (*Module, error) {
	if !IsStd(name) {
		var names []string
		all, _ := fs.ReadDir(stdFS, "std")
		for _, e := range all {
			names = append(names, "vex:"+e.Name())
		}
		return nil, fmt.Errorf("unknown module 'vex:%s'; the standard library has %s", name, strings.Join(names, ", "))
	}
	l := &loader{modules: map[string]*Module{}, prog: &Program{Target: Host()}}
	m := l.stdModule(name, nil)
	if len(l.errs) > 0 {
		return nil, l.errs
	}
	return m, nil
}

var validName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Finds the modules imported by each file of [m].
func (l *loader) resolveImports(m *Module) {
	for i, tree := range m.Trees {
		f := m.Files[i]
		names := map[string]*Import{}
		m.Imports[f] = names
		for _, d := range tree.Imports {
			imp := &Import{Decl: d, File: f}
			var target *Module
			var name string
			switch {
			case d.Path == "vex:"+CoreName:
				l.errorAt(imp, "leave out the import", "%s is always available", d.Path)
				continue
			case strings.HasPrefix(d.Path, "vex:"):
				name = strings.TrimPrefix(d.Path, "vex:")
				target = l.stdModule(name, imp)
			case strings.HasPrefix(d.Path, "package:"):
				if m.std {
					l.errorAt(imp, "", "the standard library can only import vex: modules")
					continue
				}
				target, name = l.packageModule(m, imp)
			case strings.Contains(d.Path, ":"):
				l.errorAt(imp, `use "vex:name" for the standard library, or a folder of this project`, "unknown import scheme in '%s'", d.Path)
				continue
			case path.IsAbs(d.Path) || strings.Contains(d.Path, "\\"):
				l.errorAt(imp, "write the folder relative to this file, with /", "import paths must be relative")
				continue
			default:
				dir := path.Join(path.Dir(path.Join(m.dir, "x")), d.Path)
				if m.std {
					l.errorAt(imp, "", "the standard library can only import vex: modules")
					continue
				}
				if base := m.scope.base; !strings.HasPrefix(dir+"/", base+"/") && dir != base {
					l.errorAt(imp, "", "'%s' is outside the %s", d.Path, m.scope.kind())
					continue
				}
				if dir == m.dir {
					l.errorAt(imp, "files in the same folder share their names without importing", "a module can't import its own folder")
					continue
				}
				name = path.Base(d.Path)
				target = l.folder(dir, imp, m.scope)
				if target != nil && target.Path == "" && !target.Root {
					target.Path = d.Path
					target.Name = name
				}
			}
			if target == nil {
				continue
			}
			imp.Module = target
			if d.As != nil {
				name = d.As.Name
			} else if !validName.MatchString(name) || lexer.IsKeyword(name) {
				l.errorAt(imp, fmt.Sprintf("give it a name: import %q as name", d.Path), "'%s' can't be used as a name in code", name)
				continue
			}
			if prev := names[name]; prev != nil {
				l.errorAt(imp, fmt.Sprintf("rename one: import %q as other", d.Path), "'%s' is already imported on line %d", name, prev.Decl.Pos().Line)
				continue
			}
			names[name] = imp
		}
	}
}

func (l *loader) errorAt(imp *Import, hint, format string, args ...any) {
	l.errs = append(l.errs, diag.Diagnostic{
		File:    imp.File,
		Pos:     imp.Decl.PathSpan.Start,
		Len:     imp.Decl.PathSpan.End - imp.Decl.PathSpan.Start.Offset,
		Message: fmt.Sprintf(format, args...),
		Hint:    hint,
	})
}

// Reads the project file in [dir].
func (l *loader) project(dir string) (*Project, error) {
	p := path.Join(dir, ProjectFile)
	src, err := fs.ReadFile(l.fsys, p)
	if err != nil {
		return nil, fmt.Errorf("no %s in %s; create one with:\n\n[package]\nname = \"%s\"", ProjectFile, l.shown, safeName(path.Base(dir)))
	}
	f := diag.NewFile(l.display(p), string(src))
	proj, errs := ParseProject(f)
	if len(errs) > 0 {
		return nil, errs
	}
	return proj, nil
}

// Returns [s] made into a valid package name.
func safeName(s string) string {
	s = regexp.MustCompile(`[^A-Za-z0-9_]`).ReplaceAllString(s, "_")
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		s = "app_" + s
	}
	return s
}

// Returns the names of the standard library's modules, such as `json`.
func StdModules() []string {
	var names []string
	all, _ := fs.ReadDir(stdFS, "std")
	for _, e := range all {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// Reports whether the standard library has a module called [name].
func IsStd(name string) bool {
	if !validName.MatchString(name) {
		return false
	}
	_, err := fs.Stat(stdFS, "std/"+name)
	return err == nil
}
