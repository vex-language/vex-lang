// Package parser builds the syntax tree of a Vex source file.
package parser

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/irshadio/vex-lang/compiler/ast"
	"github.com/irshadio/vex-lang/compiler/diag"
	"github.com/irshadio/vex-lang/compiler/lexer"
)

// The maximum number of errors reported before parsing stops.
const maxErrors = 20

// Unwinds the current statement or declaration after a syntax error.
type bailout struct{}

// Stops parsing once [maxErrors] is reached.
type tooMany struct{}

type parser struct {
	file    *diag.File
	toks    []lexer.Token
	p       int
	lastEnd int
	errs    diag.List
	warns   diag.List

	// Set while parsing an `if`, `while` or `for` header, where `{` starts
	// the body instead of a trailing block.
	noTrailing bool

	// Set while parsing the header of an `if` or `for` element in a list
	// or map literal, where a `(`, `[` or `-` after a space starts the
	// element: in `if wide [1, 2]`, the list is the element.
	elemHead bool

	// Set inside an interface, where methods may leave out their body.
	inInterface bool
	inExtern    bool
}

// Parses [f] into a syntax tree.
//
// On syntax errors the returned error is a [diag.List] and the tree may be
// incomplete. Lexical errors are returned without parsing.
func Parse(f *diag.File) (*ast.File, error) {
	toks, err := lexer.Tokenize(f)
	if err != nil {
		return nil, err
	}
	p := &parser{file: f, toks: toks}
	file := p.parseFile()
	file.Warnings = p.warns
	if len(p.errs) > 0 {
		return file, p.errs
	}
	return file, nil
}

// --- Token helpers ---

func (p *parser) tok() lexer.Token { return p.toks[p.p] }

func (p *parser) peek(n int) lexer.Token {
	if p.p+n < len(p.toks) {
		return p.toks[p.p+n]
	}
	return p.toks[len(p.toks)-1]
}

func (p *parser) at(k lexer.Kind) bool { return p.toks[p.p].Kind == k }

func (p *parser) next() lexer.Token {
	t := p.toks[p.p]
	p.lastEnd = t.End
	if t.Kind != lexer.EOF {
		p.p++
	}
	return t
}

// Consumes a token of kind [k] or reports what was found instead.
func (p *parser) expect(k lexer.Kind) lexer.Token {
	if !p.at(k) {
		p.fail(p.tok(), "", "expected %s, found %s", k, describe(p.tok()))
	}
	return p.next()
}

// Skips line breaks inside brackets, where they don't end a statement.
func (p *parser) skipNewlines() {
	for p.tok().IsAutoSemi() {
		p.next()
	}
}

func (p *parser) span(start diag.Pos) ast.Span { return ast.Span{Start: start, End: p.lastEnd} }

// Returns a readable description of a token for error messages.
func describe(t lexer.Token) string {
	switch t.Kind {
	case lexer.EOF:
		return "end of file"
	case lexer.Semi:
		if t.IsAutoSemi() {
			return "end of line"
		}
		return "';'"
	case lexer.Ident:
		return "'" + t.Text + "'"
	case lexer.Int, lexer.Float:
		return "number " + t.Text
	case lexer.String, lexer.StringHead, lexer.StringMid, lexer.StringTail:
		return "a string"
	}
	return t.Kind.String()
}

// --- Errors ---

// Records an error at token [t].
func (p *parser) errorAt(t lexer.Token, hint, format string, args ...any) {
	if n := len(p.errs); n > 0 && p.errs[n-1].Pos.Offset == t.Pos.Offset {
		return
	}
	p.errs = append(p.errs, diag.Diagnostic{
		Pos:     t.Pos,
		Len:     max(1, utf8.RuneCountInString(t.Text)),
		Message: fmt.Sprintf(format, args...),
		Hint:    hint,
	})
	if len(p.errs) >= maxErrors {
		panic(tooMany{})
	}
}

// Records an error at token [t] and abandons the current statement.
func (p *parser) fail(t lexer.Token, hint, format string, args ...any) {
	p.errorAt(t, hint, format, args...)
	panic(bailout{})
}

// Runs [f], recovering from a [bailout] by calling [sync].
func (p *parser) guard(f func(), sync func()) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			sync()
		}
	}()
	f()
}

// Skips to the end of the current statement, stepping over nested blocks.
func (p *parser) syncStmt() {
	depth := 0
	for {
		switch p.tok().Kind {
		case lexer.EOF:
			return
		case lexer.LBrace:
			depth++
		case lexer.RBrace:
			if depth == 0 {
				return
			}
			depth--
		case lexer.Semi:
			if depth == 0 {
				p.next()
				return
			}
		}
		p.next()
	}
}

// Skips to the next top-level `fn` or `struct`.
func (p *parser) syncDecl() {
	depth := 0
	for !p.at(lexer.EOF) {
		switch p.tok().Kind {
		case lexer.LBrace:
			depth++
		case lexer.RBrace:
			depth--
		case lexer.Fn, lexer.Struct, lexer.Enum:
			if depth <= 0 {
				return
			}
		}
		p.next()
	}
}

// --- Declarations ---

func (p *parser) parseFile() (file *ast.File) {
	file = &ast.File{Span: ast.Span{Start: p.tok().Pos}}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(tooMany); !ok {
				panic(r)
			}
		}
		file.End = p.lastEnd
	}()
	for p.at(lexer.Semi) || p.at(lexer.Import) {
		if p.at(lexer.Semi) {
			p.next()
			continue
		}
		p.guard(func() { file.Imports = append(file.Imports, p.importDecl()) }, p.syncDecl)
	}
	for !p.at(lexer.EOF) {
		if p.at(lexer.Semi) {
			p.next()
			continue
		}
		p.guard(func() {
			if d := p.decl(); d != nil {
				file.Decls = append(file.Decls, d)
			}
		}, p.syncDecl)
	}
	return file
}

// Parses `import "path"` or `import "path" as name`.
func (p *parser) importDecl() *ast.Import {
	start := p.next().Pos
	t := p.tok()
	if t.Kind != lexer.String {
		p.fail(t, `e.g. import "vex:math" or import "models"`, "expected the path of a module in quotes, found %s", describe(t))
	}
	p.next()
	im := &ast.Import{Path: t.Value, PathSpan: ast.Span{Start: t.Pos, End: t.End}}
	if im.Path == "" {
		p.fail(t, `e.g. import "vex:math" or import "models"`, "empty import path")
	}
	if p.at(lexer.As) {
		p.next()
		im.As = p.ident("a name for the module after 'as'")
	}
	im.Span = p.span(start)
	p.endStmt()
	return im
}

func (p *parser) decl() ast.Decl {
	t := p.tok()
	if t.Kind == lexer.Pub {
		p.next()
		if p.at(lexer.Pub) {
			p.fail(p.tok(), "one pub is enough", "unexpected 'pub'")
		}
		switch d := p.decl().(type) {
		case *ast.FuncDecl:
			d.Pub = true
			return d
		case *ast.StructDecl:
			d.Pub = true
			return d
		case *ast.EnumDecl:
			d.Pub = true
			return d
		case *ast.InterfaceDecl:
			d.Pub = true
			return d
		case *ast.ExternTypeDecl:
			d.Pub = true
			return d
		}
		return nil
	}
	switch t.Kind {
	case lexer.Import:
		p.fail(t, "move it above the first declaration", "imports go at the top of the file")
	case lexer.Fn, lexer.Async:
		return p.funcDecl(false)
	case lexer.Extern:
		if p.peek(1).Kind == lexer.Ident && p.peek(1).Text == "type" {
			return p.externType()
		}
		return p.externDecl()
	case lexer.Ident:
		if t.Text == "export" && (p.peek(1).Kind == lexer.Fn || p.peek(1).Kind == lexer.Async) {
			return p.exportDecl()
		}
	case lexer.Struct:
		return p.structDecl()
	case lexer.Enum:
		return p.enumDecl()
	case lexer.Interface:
		return p.interfaceDecl()
	case lexer.Static:
		p.fail(t, "use static fn inside a struct", "'static' is only allowed inside a struct")
	case lexer.Let, lexer.Var:
		p.fail(t, "move it into a function such as fn main()", "variables must be declared inside a function")
	}
	p.fail(t, "code goes inside a function, e.g. fn main() { ... }", "expected 'fn', 'struct', 'enum' or 'interface', found %s", describe(t))
	return nil
}

// Parses a name that may come from another module: `Shape` or
// `shapes.Shape`.
func (p *parser) qualName(what string) *ast.NamedType {
	id := p.ident(what)
	nt := &ast.NamedType{Name: id.Name}
	if p.at(lexer.Dot) {
		p.next()
		nt.Module, nt.Name = id.Name, p.ident(what).Name
	}
	nt.Span = p.span(id.Start)
	return nt
}

// Parses an interface name with any type arguments: `Store<int>`.
func (p *parser) ifaceRef(what string) *ast.NamedType {
	nt := p.qualName(what)
	if p.at(lexer.Lt) {
		nt.Args = p.typeArgs()
		nt.Span = p.span(nt.Start)
	}
	return nt
}

// Parses the interfaces after `:` in a declaration's header, if any.
func (p *parser) ifaceList() []*ast.NamedType {
	if !p.at(lexer.Colon) {
		return nil
	}
	p.next()
	list := []*ast.NamedType{p.ifaceRef("an interface name")}
	for p.at(lexer.Comma) {
		p.next()
		list = append(list, p.ifaceRef("an interface name"))
	}
	return list
}

// Parses an identifier, naming [what] in the error if it is missing.
func (p *parser) ident(what string) *ast.Ident {
	t := p.tok()
	if t.Kind != lexer.Ident {
		hint := ""
		if t.Kind.IsKeyword() {
			hint = t.Text + " is a keyword; choose another name"
		}
		p.fail(t, hint, "expected %s, found %s", what, describe(t))
	}
	p.next()
	return &ast.Ident{Span: ast.Span{Start: t.Pos, End: t.End}, Name: t.Text}
}

func (p *parser) funcDecl(static bool) *ast.FuncDecl {
	start := p.tok().Pos
	async := false
	if p.at(lexer.Async) {
		p.next()
		async = true
		if !p.at(lexer.Fn) {
			p.fail(p.tok(), "write async fn name(...)", "expected 'fn' after 'async', found %s", describe(p.tok()))
		}
	}
	p.expect(lexer.Fn)
	fn := &ast.FuncDecl{Name: p.ident("a function name"), Static: static, Async: async}
	fn.TypeParams = p.typeParams()
	open := p.expect(lexer.LParen)
	p.list(open, lexer.RParen, func() { fn.Params = append(fn.Params, p.param()) })
	fn.Throws = p.throws()
	if p.at(lexer.Arrow) {
		p.next()
		fn.Result = p.typeExpr()
	}
	if p.at(lexer.Throws) {
		p.fail(p.tok(), fmt.Sprintf("write fn %s(...) throws -> ...", fn.Name.Name), "throws comes before ->")
	}
	switch {
	case p.at(lexer.FatArrow):
		p.next()
		fn.ExprBody = p.expr()
	case p.at(lexer.LBrace):
		fn.Body = p.block()
	case p.at(lexer.Colon):
		p.fail(p.tok(), "write the return type as -> int", "expected '->' before the return type, found ':'")
	case p.inInterface && (p.at(lexer.Semi) || p.at(lexer.RBrace)):
	case p.inExtern && (p.at(lexer.Semi) || p.at(lexer.As) || p.at(lexer.EOF)):
	default:
		p.fail(p.tok(), "", "expected '{' or '=>' to start the body of %s, found %s", fn.Name.Name, describe(p.tok()))
	}
	fn.Span = p.span(start)
	return fn
}

// Parses `extern fn name(params) -> T`, optionally followed by
// `as "c_name"`.
func (p *parser) externDecl() *ast.FuncDecl {
	start := p.next().Pos
	if !p.at(lexer.Fn) {
		p.fail(p.tok(), "write extern fn name(...)", "expected 'fn' after 'extern', found %s", describe(p.tok()))
	}
	p.inExtern = true
	fn := func() *ast.FuncDecl {
		defer func() { p.inExtern = false }()
		return p.funcDecl(false)
	}()
	fn.Extern = true
	if len(fn.TypeParams) > 0 {
		p.errorAt(lexer.Token{Pos: fn.TypeParams[0].Start, Text: fn.TypeParams[0].Name.Name}, "C has no generics; declare one extern fn per type", "extern functions can't be generic")
		fn.TypeParams = nil
	}
	if fn.Throws != nil {
		p.errorAt(lexer.Token{Pos: fn.Throws.Start, Text: "throws"}, "C functions report failure through their result; check it in a Vex wrapper that throws", "extern functions can't throw")
		fn.Throws = nil
	}
	if fn.Body != nil || fn.ExprBody != nil {
		p.errorAt(lexer.Token{Pos: fn.Name.Start, Text: fn.Name.Name}, "remove the body; the function is written in C", "extern functions have no body")
	}
	fn.CName = p.cName()
	fn.Span = p.span(start)
	return fn
}

// Parses `extern type Name`, optionally followed by `as "c_name"`.
func (p *parser) externType() *ast.ExternTypeDecl {
	start := p.next().Pos
	p.next()
	d := &ast.ExternTypeDecl{Name: p.ident("a type name")}
	if p.at(lexer.Lt) {
		p.fail(p.tok(), "declare one extern type per C type", "extern types can't be generic")
	}
	if p.at(lexer.LBrace) {
		p.fail(p.tok(), "C structs are reached through extern fn calls on the handle", "extern types have no body")
	}
	d.CName = p.cName()
	d.Span = p.span(start)
	return d
}

// Parses `export fn name(params) -> T { ... }`, a function C can call.
func (p *parser) exportDecl() *ast.FuncDecl {
	start := p.next().Pos
	if p.at(lexer.Async) {
		p.fail(p.tok(), "export a plain fn that starts the async work", "export functions can't be async")
	}
	fn := p.funcDecl(false)
	fn.Export = true
	if len(fn.TypeParams) > 0 {
		p.errorAt(lexer.Token{Pos: fn.TypeParams[0].Start, Text: fn.TypeParams[0].Name.Name}, "C has no generics; export one fn per type", "export functions can't be generic")
		fn.TypeParams = nil
	}
	if fn.Throws != nil {
		p.errorAt(lexer.Token{Pos: fn.Throws.Start, Text: "throws"}, "catch errors inside and return a status instead", "export functions can't throw")
		fn.Throws = nil
	}
	fn.Span = p.span(start)
	return fn
}

// Parses an optional `as "c_name"`, returning the name or "".
func (p *parser) cName() string {
	if !p.at(lexer.As) {
		return ""
	}
	p.next()
	t := p.tok()
	if t.Kind != lexer.String {
		p.fail(t, `e.g. as "c_name"`, "expected the C name in quotes, found %s", describe(t))
	}
	p.next()
	if !cIdent.MatchString(t.Value) {
		p.fail(t, "use letters, digits and _, not starting with a digit", "'%s' isn't a valid C name", t.Value)
	}
	return t.Value
}

var cIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (p *parser) param() *ast.Param {
	name := p.ident("a parameter name")
	if !p.at(lexer.Colon) {
		p.fail(p.tok(), fmt.Sprintf("write it as %s: int", name.Name), "parameter '%s' needs a type", name.Name)
	}
	p.next()
	param := &ast.Param{Name: name, Type: p.typeExpr()}
	if p.at(lexer.Assign) {
		p.next()
		param.Default = p.expr()
	}
	param.Span = p.span(name.Start)
	return param
}

// Parses `interface Name { fn method() -> T ... }`.
func (p *parser) interfaceDecl() *ast.InterfaceDecl {
	start := p.tok().Pos
	p.expect(lexer.Interface)
	d := &ast.InterfaceDecl{Name: p.ident("an interface name")}
	d.TypeParams = p.typeParams()
	d.Supers = p.ifaceList()
	open := p.expect(lexer.LBrace)
	p.inInterface = true
	defer func() { p.inInterface = false }()
	for {
		for p.at(lexer.Semi) || p.at(lexer.Comma) {
			p.next()
		}
		if p.at(lexer.RBrace) {
			break
		}
		if p.at(lexer.EOF) {
			p.fail(open, "add a matching }", "unclosed interface body")
		}
		p.guard(func() {
			switch t := p.tok(); t.Kind {
			case lexer.Fn:
				d.Methods = append(d.Methods, p.funcDecl(false))
			case lexer.Async:
				p.fail(t, "make the struct's method async instead, and call it from a non-async interface method", "interface methods can't be async yet")
			case lexer.Static:
				p.fail(t, "", "interfaces can't have static methods")
			case lexer.Pub:
				p.fail(t, "remove pub: interface methods are public whenever the interface is", "interface methods can't be marked pub")
			case lexer.Ident, lexer.Let, lexer.Var:
				if (t.Text == "get" || t.Text == "set") && p.peek(1).Kind == lexer.Ident {
					d.Methods = append(d.Methods, p.accessor())
					return
				}
				p.fail(t, "require a getter instead, e.g. get name -> string", "interfaces can't have fields")
			default:
				p.fail(t, "", "expected a method, found %s", describe(t))
			}
		}, p.syncStmt)
	}
	p.next()
	d.Span = p.span(start)
	return d
}

func (p *parser) structDecl() *ast.StructDecl {
	start := p.tok().Pos
	p.expect(lexer.Struct)
	s := &ast.StructDecl{Name: p.ident("a struct name")}
	s.TypeParams = p.typeParams()
	s.Interfaces = p.ifaceList()
	open := p.expect(lexer.LBrace)
	for {
		for p.at(lexer.Semi) || p.at(lexer.Comma) {
			p.next()
		}
		if p.at(lexer.RBrace) {
			break
		}
		if p.at(lexer.EOF) {
			p.fail(open, "add a matching }", "unclosed struct body")
		}
		p.guard(func() { p.member(s) }, p.syncStmt)
	}
	p.next()
	s.Span = p.span(start)
	return s
}

func (p *parser) enumDecl() *ast.EnumDecl {
	start := p.tok().Pos
	p.expect(lexer.Enum)
	e := &ast.EnumDecl{Name: p.ident("an enum name")}
	e.TypeParams = p.typeParams()
	e.Interfaces = p.ifaceList()
	open := p.expect(lexer.LBrace)
	for {
		for p.at(lexer.Semi) || p.at(lexer.Comma) {
			p.next()
		}
		if p.at(lexer.RBrace) {
			break
		}
		if p.at(lexer.EOF) {
			p.fail(open, "add a matching }", "unclosed enum body")
		}
		p.guard(func() { p.enumMember(e) }, p.syncStmt)
	}
	p.next()
	e.Span = p.span(start)
	return e
}

// Parses one case or method inside an enum body.
func (p *parser) enumMember(e *ast.EnumDecl) {
	pub := false
	if p.at(lexer.Pub) {
		p.next()
		pub = true
	}
	t := p.tok()
	switch t.Kind {
	case lexer.Fn, lexer.Async:
		m := p.funcDecl(false)
		m.Pub = pub
		e.Methods = append(e.Methods, m)
		return
	case lexer.Static:
		p.next()
		if !p.at(lexer.Fn) && !p.at(lexer.Async) {
			p.fail(p.tok(), "", "expected 'fn' after 'static', found %s", describe(p.tok()))
		}
		m := p.funcDecl(true)
		m.Pub = pub
		e.Methods = append(e.Methods, m)
		return
	case lexer.Ident:
		if t.Text == "set" && p.peek(1).Kind == lexer.Ident {
			p.fail(t, "use a method that returns a new value", "enums can't have setters, since their values never change")
		}
		if t.Text == "get" && p.peek(1).Kind == lexer.Ident {
			m := p.accessor()
			m.Pub = pub
			e.Methods = append(e.Methods, m)
			return
		}
		if pub {
			p.fail(t, "remove pub: cases are public whenever their enum is", "cases can't be marked pub")
		}
	default:
		p.fail(t, "", "expected a case or method, found %s", describe(t))
	}
	c := &ast.EnumCase{Name: p.ident("a case name")}
	if p.at(lexer.Colon) {
		p.fail(p.tok(), fmt.Sprintf("enum cases carry values in parentheses: %s(value: int)", c.Name.Name), "enum cases don't have a type")
	}
	if p.at(lexer.LParen) {
		open := p.next()
		p.list(open, lexer.RParen, func() {
			name := p.ident("a value name")
			if !p.at(lexer.Colon) {
				p.fail(p.tok(), fmt.Sprintf("write it as %s: int", name.Name), "case value '%s' needs a type", name.Name)
			}
			p.next()
			f := &ast.Field{Name: name, Type: p.typeExpr()}
			if p.at(lexer.Assign) {
				p.fail(p.tok(), "", "case values can't have default values")
			}
			f.Span = p.span(name.Start)
			c.Fields = append(c.Fields, f)
		})
	}
	c.Span = p.span(t.Pos)
	if !p.at(lexer.Semi) && !p.at(lexer.Comma) && !p.at(lexer.RBrace) {
		p.fail(p.tok(), "put each case on its own line", "expected end of case, found %s", describe(p.tok()))
	}
	e.Cases = append(e.Cases, c)
}

// Parses `match subject { pattern => body ... }`.
func (p *parser) matchExpr() ast.Expr {
	start := p.next().Pos
	m := &ast.MatchExpr{}
	saved := p.noTrailing
	p.noTrailing = true
	m.Subject = p.expr()
	p.noTrailing = false
	defer func() { p.noTrailing = saved }()
	if !p.at(lexer.LBrace) {
		hint := ""
		if p.tok().IsAutoSemi() {
			hint = "put { on the same line as match"
		}
		p.fail(p.tok(), hint, "expected '{' after the match subject, found %s", describe(p.tok()))
	}
	open := p.next()
	for {
		for p.at(lexer.Semi) || p.at(lexer.Comma) {
			p.next()
		}
		if p.at(lexer.RBrace) {
			break
		}
		if p.at(lexer.EOF) {
			p.fail(open, "add a matching }", "unclosed match")
		}
		arm := &ast.MatchArm{Pattern: p.pattern()}
		if !p.at(lexer.FatArrow) {
			p.fail(p.tok(), "write pattern => result", "expected '=>' after the pattern, found %s", describe(p.tok()))
		}
		p.next()
		switch p.tok().Kind {
		case lexer.LBrace:
			arm.Block = p.block()
		case lexer.Return, lexer.Break, lexer.Continue, lexer.Throw:
			st := p.stmt()
			arm.Block = &ast.Block{Span: ast.Span{Start: st.Pos(), End: st.EndOffset()}, Stmts: []ast.Stmt{st}}
		default:
			st := p.simpleStmt()
			if es, ok := st.(*ast.ExprStmt); ok {
				arm.Expr = es.X
			} else {
				arm.Block = &ast.Block{Span: ast.Span{Start: st.Pos(), End: st.EndOffset()}, Stmts: []ast.Stmt{st}}
			}
		}
		arm.Span = p.span(arm.Pattern.Pos())
		if !p.at(lexer.Semi) && !p.at(lexer.Comma) && !p.at(lexer.RBrace) {
			p.fail(p.tok(), "put each arm on its own line", "expected end of match arm, found %s", describe(p.tok()))
		}
		m.Arms = append(m.Arms, arm)
	}
	p.next()
	m.Span = p.span(start)
	return m
}

// Parses a match pattern: `_`, a literal, or a case with optional bindings.
func (p *parser) pattern() ast.Pattern {
	t := p.tok()
	switch t.Kind {
	case lexer.Ident:
		p.next()
		if t.Text == "_" {
			return &ast.WildcardPattern{Span: p.span(t.Pos)}
		}
		cp := &ast.CasePattern{Name: &ast.Ident{Span: ast.Span{Start: t.Pos, End: t.End}, Name: t.Text}}
		if p.at(lexer.LParen) {
			open := p.next()
			cp.Binds = []*ast.Ident{}
			p.list(open, lexer.RParen, func() { cp.Binds = append(cp.Binds, p.ident("a name to bind")) })
		}
		cp.Span = p.span(t.Pos)
		return cp
	case lexer.Int, lexer.Float, lexer.String, lexer.True, lexer.False, lexer.Minus:
		v := p.literalPattern()
		return &ast.LiteralPattern{Span: ast.Span{Start: t.Pos, End: v.EndOffset()}, Value: v}
	case lexer.Dot:
		p.fail(t, "write the case name without the dot", "patterns don't start with '.'")
	}
	p.fail(t, "for example circle(r), 0, \"yes\" or _", "expected a pattern, found %s", describe(t))
	return nil
}

// Parses the value of a literal pattern.
func (p *parser) literalPattern() ast.Expr {
	t := p.tok()
	if t.Kind == lexer.Minus {
		p.next()
		if k := p.tok().Kind; k != lexer.Int && k != lexer.Float {
			p.fail(p.tok(), "", "expected a number after '-' in a pattern, found %s", describe(p.tok()))
		}
		x := p.primary()
		return &ast.UnaryExpr{Span: ast.Span{Start: t.Pos, End: x.EndOffset()}, Op: lexer.Minus, X: x}
	}
	return p.primary()
}

// Parses one field or method inside a struct body.
func (p *parser) member(s *ast.StructDecl) {
	pub := false
	if p.at(lexer.Pub) {
		p.next()
		pub = true
	}
	t := p.tok()
	switch t.Kind {
	case lexer.Fn, lexer.Async:
		m := p.funcDecl(false)
		m.Pub = pub
		s.Methods = append(s.Methods, m)
		return
	case lexer.Static:
		p.next()
		if !p.at(lexer.Fn) && !p.at(lexer.Async) {
			p.fail(p.tok(), "", "expected 'fn' after 'static', found %s", describe(p.tok()))
		}
		m := p.funcDecl(true)
		m.Pub = pub
		s.Methods = append(s.Methods, m)
		return
	case lexer.Pub:
		p.fail(t, "one pub is enough", "unexpected 'pub'")
	case lexer.Let, lexer.Var:
		p.fail(t, "write fields as name: type", "fields don't use '%s'", t.Text)
	case lexer.Ident:
		if (t.Text == "get" || t.Text == "set") && p.peek(1).Kind == lexer.Ident {
			m := p.accessor()
			m.Pub = pub
			s.Methods = append(s.Methods, m)
			return
		}
	default:
		p.fail(t, "", "expected a field or method, found %s", describe(t))
	}
	name := p.ident("a field name")
	if !p.at(lexer.Colon) {
		p.fail(p.tok(), fmt.Sprintf("write it as %s: int", name.Name), "field '%s' needs a type", name.Name)
	}
	p.next()
	f := &ast.Field{Pub: pub, Name: name, Type: p.typeExpr()}
	if p.at(lexer.Assign) {
		p.next()
		f.Default = p.expr()
	}
	f.Span = p.span(name.Start)
	if p.at(lexer.LBrace) {
		s.Methods = append(s.Methods, p.observer(f))
	}
	if !p.at(lexer.Semi) && !p.at(lexer.Comma) && !p.at(lexer.RBrace) {
		p.fail(p.tok(), "put each field on its own line", "expected end of field, found %s", describe(p.tok()))
	}
	s.Fields = append(s.Fields, f)
}

// Parses `get name -> T` or `set name(value: T)` with its body.
func (p *parser) accessor() *ast.FuncDecl {
	start := p.tok().Pos
	kind := p.next().Text
	fn := &ast.FuncDecl{Name: p.ident("a property name"), Accessor: ast.Getter}
	name := fn.Name.Name
	if kind == "get" {
		if p.at(lexer.LParen) {
			p.fail(p.tok(), fmt.Sprintf("write get %s -> T, or make it a method: fn %s(...)", name, name), "getters take no parameters")
		}
		if !p.at(lexer.Arrow) {
			p.fail(p.tok(), fmt.Sprintf("write get %s -> int => ...", name), "getter '%s' needs a result type", name)
		}
		p.next()
		fn.Result = p.typeExpr()
	} else {
		fn.Accessor = ast.Setter
		if !p.at(lexer.LParen) {
			p.fail(p.tok(), fmt.Sprintf("write set %s(value: int) { ... }", name), "expected '(' after 'set %s', found %s", name, describe(p.tok()))
		}
		open := p.next()
		p.list(open, lexer.RParen, func() { fn.Params = append(fn.Params, p.param()) })
		if len(fn.Params) != 1 {
			p.fail(open, fmt.Sprintf("write set %s(value: int) { ... }", name), "setter '%s' takes exactly one parameter, the new value", name)
		}
		if p.at(lexer.Arrow) {
			p.fail(p.tok(), "remove the result type", "setters don't return a value")
		}
	}
	if p.at(lexer.Throws) {
		p.fail(p.tok(), "make it a method that throws: fn ...", "%sters can't throw", kind)
	}
	switch {
	case p.at(lexer.FatArrow):
		p.next()
		fn.ExprBody = p.expr()
	case p.at(lexer.LBrace):
		fn.Body = p.block()
	case p.inInterface && (p.at(lexer.Semi) || p.at(lexer.RBrace)):
	default:
		p.fail(p.tok(), "", "expected '{' or '=>' to start the body of %s, found %s", name, describe(p.tok()))
	}
	fn.Span = p.span(start)
	return fn
}

// Parses the `{ didSet { ... } }` block after field [f].
func (p *parser) observer(f *ast.Field) *ast.FuncDecl {
	p.next()
	p.skipNewlines()
	t := p.tok()
	if t.Kind != lexer.Ident || t.Text != "didSet" {
		hint := "write { didSet { ... } } after the field"
		if t.Text == "willSet" {
			hint = "use didSet, which has the old value in oldValue"
		}
		p.fail(t, hint, "expected 'didSet' in the block of field '%s', found %s", f.Name.Name, describe(t))
	}
	p.next()
	fn := &ast.FuncDecl{
		Name:     &ast.Ident{Span: f.Name.Span, Name: f.Name.Name},
		Params:   []*ast.Param{{Span: ast.Span{Start: t.Pos, End: t.End}, Name: &ast.Ident{Span: ast.Span{Start: t.Pos, End: t.End}, Name: "oldValue"}, Type: f.Type}},
		Accessor: ast.Observer,
		Body:     p.block(),
	}
	fn.Span = p.span(t.Pos)
	p.skipNewlines()
	if !p.at(lexer.RBrace) {
		p.fail(p.tok(), "a field has one didSet block", "expected '}' after didSet, found %s", describe(p.tok()))
	}
	p.next()
	return fn
}

func (p *parser) typeExpr() ast.TypeExpr {
	start := p.tok().Pos
	te := p.baseType()
	if p.at(lexer.QuestionQuestion) {
		p.fail(p.tok(), "one ? is enough", "a type can only be optional once")
	}
	for p.at(lexer.Question) {
		q := p.next()
		if _, ok := te.(*ast.OptionalType); ok {
			p.fail(q, "one ? is enough", "a type can only be optional once")
		}
		te = &ast.OptionalType{Span: p.span(start), Elem: te}
	}
	return te
}

func (p *parser) baseType() ast.TypeExpr {
	t := p.tok()
	switch t.Kind {
	case lexer.LParen:
		p.next()
		inner := p.typeExpr()
		p.expect(lexer.RParen)
		return inner
	case lexer.LBracket:
		p.next()
		elem := p.typeExpr()
		if p.at(lexer.Colon) {
			p.next()
			value := p.typeExpr()
			p.expect(lexer.RBracket)
			return &ast.MapType{Span: p.span(t.Pos), Key: elem, Value: value}
		}
		p.expect(lexer.RBracket)
		return &ast.ListType{Span: p.span(t.Pos), Elem: elem}
	case lexer.Ident:
		p.next()
		nt := &ast.NamedType{Name: t.Text}
		if p.at(lexer.Dot) && p.peek(1).Kind == lexer.Ident {
			p.next()
			nt.Module, nt.Name = t.Text, p.next().Text
		}
		if p.at(lexer.Lt) {
			nt.Args = p.typeArgs()
		}
		nt.Span = p.span(t.Pos)
		return nt
	case lexer.Fn:
		p.next()
		ft := &ast.FuncType{}
		open := p.expect(lexer.LParen)
		p.list(open, lexer.RParen, func() { ft.Params = append(ft.Params, p.typeExpr()) })
		ft.Throws = p.throws()
		if p.at(lexer.Arrow) {
			p.next()
			ft.Result = p.typeExpr()
		}
		ft.Span = p.span(t.Pos)
		return ft
	}
	p.fail(t, "for example int, string, [int] or fn(int) -> bool", "expected a type, found %s", describe(t))
	return nil
}

// Parses comma-separated items up to [end], allowing line breaks and a
// trailing comma. [open] is the opening bracket, used when it is never closed.
func (p *parser) list(open lexer.Token, end lexer.Kind, item func()) {
	saved, savedHead := p.noTrailing, p.elemHead
	p.noTrailing, p.elemHead = false, false
	defer func() { p.noTrailing, p.elemHead = saved, savedHead }()
	p.skipNewlines()
	for !p.at(end) {
		item()
		p.skipNewlines()
		if p.at(lexer.Comma) {
			p.next()
			p.skipNewlines()
			continue
		}
		if p.at(lexer.RBrace) || p.at(lexer.EOF) {
			p.fail(open, "add a matching "+closing[open.Kind], "unclosed %s", open.Kind)
		}
		if !p.at(end) {
			hint := ""
			if startsExpr(p.tok().Kind) {
				hint = "add a , between items"
			}
			p.fail(p.tok(), hint, "expected ',' or %s, found %s", end, describe(p.tok()))
		}
	}
	p.next()
}

// The closing bracket for each opening bracket.
var closing = map[lexer.Kind]string{lexer.LParen: ")", lexer.LBracket: "]", lexer.LBrace: "}", lexer.Lt: ">"}

// Parses the type parameters of a declaration, if any: `<T, U: Comparable>`.
func (p *parser) typeParams() []*ast.TypeParam {
	if !p.at(lexer.Lt) {
		return nil
	}
	open := p.next()
	params := []*ast.TypeParam{}
	p.list(open, lexer.Gt, func() {
		tp := &ast.TypeParam{Name: p.ident("a type parameter name")}
		if p.at(lexer.Colon) {
			p.next()
			tp.Constraints = append(tp.Constraints, p.ifaceRef("a constraint such as Comparable"))
			for p.at(lexer.Amp) {
				p.next()
				tp.Constraints = append(tp.Constraints, p.ifaceRef("a constraint after '&'"))
			}
		}
		tp.Span = p.span(tp.Name.Start)
		params = append(params, tp)
	})
	if len(params) == 0 {
		p.fail(open, "remove the <>, or name a type parameter: <T>", "empty type parameter list")
	}
	return params
}

// Parses type arguments: `<int, string>`.
func (p *parser) typeArgs() []ast.TypeExpr {
	open := p.next()
	var args []ast.TypeExpr
	p.list(open, lexer.Gt, func() { args = append(args, p.typeExpr()) })
	if len(args) == 0 {
		p.fail(open, "name the types, e.g. <int>", "empty type argument list")
	}
	return args
}

// Reports whether the `<` at the current token starts type arguments
// followed by a call, as in `first<int>(xs)`, rather than a comparison.
// Type arguments touch the name before them and the `(` after them, so
// spaced comparisons like `a < b, c > (d)` are never mistaken for them.
// Reports whether [x] can take type arguments: a name, or a name from a
// module as in `models.Box`.
func qualifiable(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident:
		return true
	case *ast.MemberExpr:
		_, ok := x.X.(*ast.Ident)
		return ok
	}
	return false
}

func (p *parser) typeArgsAhead() bool {
	if p.tok().Pos.Offset != p.lastEnd {
		return false
	}
	depth := 0
	for i := 0; ; i++ {
		t := p.peek(i)
		switch t.Kind {
		case lexer.Lt:
			depth++
		case lexer.Gt:
			depth--
			if depth == 0 {
				// A call, `Result<int>.ok`, or a value such as `first<int>`.
				switch next := p.peek(i + 1); next.Kind {
				case lexer.LParen, lexer.Dot:
					return next.Pos.Offset == t.End
				case lexer.Semi, lexer.Comma, lexer.RParen, lexer.RBracket, lexer.RBrace, lexer.EOF:
					return true
				}
				return false
			}
		case lexer.Ident, lexer.LBracket, lexer.RBracket, lexer.Colon, lexer.Comma, lexer.Question,
			lexer.Fn, lexer.LParen, lexer.RParen, lexer.Arrow, lexer.Throws:
		default:
			return false
		}
	}
}

// --- Statements ---

func (p *parser) block() *ast.Block {
	saved, savedHead := p.noTrailing, p.elemHead
	p.noTrailing, p.elemHead = false, false
	defer func() { p.noTrailing, p.elemHead = saved, savedHead }()

	open := p.expect(lexer.LBrace)
	b := &ast.Block{}
	for {
		for p.at(lexer.Semi) {
			p.next()
		}
		if p.at(lexer.RBrace) {
			break
		}
		if p.at(lexer.EOF) {
			p.fail(open, "add a matching }", "unclosed block")
		}
		p.guard(func() {
			s := p.stmt()
			b.Stmts = append(b.Stmts, s)
			p.endStmt()
		}, p.syncStmt)
	}
	p.next()
	b.Span = p.span(open.Pos)
	return b
}

// Checks that a statement is followed by a line break, `;` or `}`.
func (p *parser) endStmt() {
	switch t := p.tok(); t.Kind {
	case lexer.Semi:
		if t.Text == ";" {
			if next := p.peek(1); next.Pos.Line > t.Pos.Line || next.Kind == lexer.RBrace || next.Kind == lexer.EOF {
				p.warn(t, "remove it", "a ; isn't needed at the end of a line")
			}
		}
	case lexer.RBrace:
	case lexer.DotDot, lexer.DotDotEq:
		p.fail(t, "for i in 0..10 { ... }", "ranges can only be used in for loops")
	case lexer.Else:
		p.fail(t, "", "'else' must follow the closing } of an if block")
	default:
		p.fail(t, "put each statement on its own line or separate them with ;", "expected end of statement, found %s", describe(t))
	}
}

func (p *parser) stmt() ast.Stmt {
	t := p.tok()
	switch t.Kind {
	case lexer.Let, lexer.Var:
		return p.letStmt()
	case lexer.If:
		return p.ifStmt()
	case lexer.While:
		p.next()
		cond := p.header("while")
		return &ast.WhileStmt{Cond: cond, Body: p.block(), Span: p.span(t.Pos)}
	case lexer.For:
		return p.forStmt()
	case lexer.Guard:
		return p.guardStmt()
	case lexer.Return:
		p.next()
		r := &ast.ReturnStmt{}
		if !p.at(lexer.Semi) && !p.at(lexer.RBrace) && !p.at(lexer.Comma) {
			r.Value = p.expr()
		}
		r.Span = p.span(t.Pos)
		return r
	case lexer.Throw:
		p.next()
		if p.at(lexer.Semi) || p.at(lexer.RBrace) {
			p.fail(p.tok(), `e.g. throw Error("file not found")`, "throw needs an error to throw")
		}
		value := p.expr()
		return &ast.ThrowStmt{Value: value, Span: p.span(t.Pos)}
	case lexer.Break:
		p.next()
		return &ast.BreakStmt{Span: p.span(t.Pos)}
	case lexer.Ellipsis:
		return &ast.ExprStmt{X: p.spread()}
	case lexer.Continue:
		p.next()
		return &ast.ContinueStmt{Span: p.span(t.Pos)}
	case lexer.LBrace:
		return p.block()
	case lexer.Fn:
		if p.peek(1).Kind == lexer.Ident {
			name := p.peek(1).Text
			p.fail(t, fmt.Sprintf("declare it at the top level, or store a closure: let %s = fn(...) { ... }", name),
				"functions can't be declared inside other functions")
		}
	case lexer.Struct:
		p.fail(t, "declare it at the top level of the file", "structs can't be declared inside functions")
	case lexer.Enum:
		p.fail(t, "declare it at the top level of the file", "enums can't be declared inside functions")
	case lexer.Else:
		p.fail(t, "", "'else' without a matching 'if'")
	case lexer.Ident:
		if p.peek(1).Kind == lexer.Colon {
			p.fail(t, fmt.Sprintf("write let %s = ... or var %s = ...", t.Text, t.Text), "missing 'let' or 'var' before '%s'", t.Text)
		}
	}
	return p.simpleStmt()
}

func (p *parser) letStmt() *ast.LetStmt {
	t := p.next()
	s := &ast.LetStmt{Mutable: t.Kind == lexer.Var, Name: p.ident("a variable name")}
	if p.at(lexer.Colon) {
		p.next()
		s.Type = p.typeExpr()
	}
	if !p.at(lexer.Assign) {
		hint := fmt.Sprintf("write %s %s = ...", t.Text, s.Name.Name)
		if s.Type != nil {
			hint = fmt.Sprintf("give it a starting value, e.g. %s %s: %s = ...", t.Text, s.Name.Name, ast.TypeString(s.Type))
		}
		p.fail(p.tok(), hint, "'%s %s' needs a value", t.Text, s.Name.Name)
	}
	p.next()
	s.Value = p.expr()
	s.Span = p.span(t.Pos)
	return s
}

func (p *parser) ifStmt() *ast.IfStmt {
	start := p.expect(lexer.If).Pos
	s := &ast.IfStmt{}
	if p.at(lexer.Let) || p.at(lexer.Var) {
		if p.at(lexer.Var) {
			p.errorAt(p.tok(), "write if let; copy it into a var inside the block", "'if var' isn't supported")
		}
		p.next()
		s.Bind = p.ident("a name after 'if let'")
		if !p.at(lexer.Assign) {
			p.fail(p.tok(), fmt.Sprintf("write if let %s = optionalValue { ... }", s.Bind.Name), "expected '=' after 'if let %s', found %s", s.Bind.Name, describe(p.tok()))
		}
		p.next()
	}
	s.Cond = p.header("if")
	s.Then = p.block()
	if p.tok().IsAutoSemi() && p.peek(1).Kind == lexer.Else {
		p.errorAt(p.peek(1), "write } else {", "'else' must be on the same line as the closing }")
		p.next()
	}
	if p.at(lexer.Else) {
		p.next()
		switch {
		case p.at(lexer.If):
			s.Else = p.ifStmt()
		case p.at(lexer.LBrace):
			s.Else = p.block()
		default:
			p.fail(p.tok(), "", "expected '{' or 'if' after 'else', found %s", describe(p.tok()))
		}
	}
	s.Span = p.span(start)
	return s
}

func (p *parser) forStmt() *ast.ForStmt {
	start := p.expect(lexer.For).Pos
	s := &ast.ForStmt{Elem: p.ident("a loop variable")}
	if p.at(lexer.Comma) {
		p.next()
		s.Index, s.Elem = s.Elem, p.ident("a loop variable")
	}
	if !p.at(lexer.In) {
		p.fail(p.tok(), "write for x in items { ... }", "expected 'in', found %s", describe(p.tok()))
	}
	p.next()

	saved := p.noTrailing
	p.noTrailing = true
	iter := p.expr()
	if p.at(lexer.DotDot) || p.at(lexer.DotDotEq) {
		inclusive := p.next().Kind == lexer.DotDotEq
		hi := p.expr()
		iter = &ast.RangeExpr{Span: ast.Span{Start: iter.Pos(), End: p.lastEnd}, Lo: iter, Hi: hi, Inclusive: inclusive}
	}
	p.noTrailing = saved
	s.Iter = iter
	p.bodyStart("for")
	s.Body = p.block()
	s.Span = p.span(start)
	return s
}

// Parses the condition of an `if` or `while` and checks that the body follows.
// Records a warning at token [t].
func (p *parser) warn(t lexer.Token, hint, format string, args ...any) {
	p.warns = append(p.warns, diag.Diagnostic{
		Pos:     t.Pos,
		Len:     max(1, utf8.RuneCountInString(p.file.Src[t.Pos.Offset:t.End])),
		Message: fmt.Sprintf(format, args...),
		Hint:    hint,
		Warning: true,
	})
}

// Warns when the condition of [keyword], tokens [first] to [last], is
// wrapped in one needless pair of parentheses.
func (p *parser) checkParens(keyword string, first, last int) {
	if last <= first || p.toks[first].Kind != lexer.LParen || p.toks[last].Kind != lexer.RParen {
		return
	}
	depth := 0
	for i := first; i <= last; i++ {
		switch p.toks[i].Kind {
		case lexer.LParen:
			depth++
		case lexer.RParen:
			depth--
			if depth == 0 && i != last {
				return
			}
		}
	}
	inner := strings.TrimSpace(p.file.Src[p.toks[first].End:p.toks[last].Pos.Offset])
	p.warn(p.toks[first], fmt.Sprintf("write %s %s {", keyword, inner), "the %s condition doesn't need parentheses", keyword)
}

// Parses the optional `throws` or `throws(E)` of a function.
func (p *parser) throws() *ast.Throws {
	if !p.at(lexer.Throws) {
		return nil
	}
	start := p.next().Pos
	th := &ast.Throws{}
	if p.at(lexer.LParen) {
		p.next()
		th.Type = p.typeExpr()
		p.expect(lexer.RParen)
	}
	th.Span = p.span(start)
	return th
}

// Parses `try x`, `try? x` or `try x catch e { ... }`.
func (p *parser) tryExpr() ast.Expr {
	start := p.next().Pos
	e := &ast.TryExpr{}
	if p.at(lexer.Question) && p.tok().Pos.Offset == p.lastEnd {
		p.next()
		e.Optional = true
	}
	if p.at(lexer.Await) {
		t := p.next()
		x := p.postfix(p.primary())
		e.X = &ast.AwaitExpr{Span: ast.Span{Start: t.Pos, End: x.EndOffset()}, X: x}
	} else {
		e.X = p.postfix(p.primary())
	}
	if p.at(lexer.Catch) {
		ct := p.next()
		if e.Optional {
			p.fail(ct, "use try ... catch, or try? on its own", "try? gives nil on an error, so it can't have a catch")
		}
		c := &ast.Catch{}
		if p.at(lexer.Ident) {
			c.Name = p.ident("a name for the error")
		}
		if !p.at(lexer.LBrace) {
			p.fail(p.tok(), "e.g. catch e { println(e.message) }", "expected '{' after catch, found %s", describe(p.tok()))
		}
		saved := p.noTrailing
		p.noTrailing = false
		c.Body = p.block()
		p.noTrailing = saved
		c.Span = p.span(ct.Pos)
		e.Catch = c
	}
	e.Span = p.span(start)
	return e
}

// Parses `guard let name = value else { ... }`.
func (p *parser) guardStmt() *ast.GuardStmt {
	start := p.next().Pos
	if !p.at(lexer.Let) {
		p.fail(p.tok(), "write guard let name = optionalValue else { return }", "expected 'let' after 'guard', found %s", describe(p.tok()))
	}
	p.next()
	g := &ast.GuardStmt{Bind: p.ident("a name after 'guard let'")}
	if !p.at(lexer.Assign) {
		p.fail(p.tok(), fmt.Sprintf("write guard let %s = optionalValue else { return }", g.Bind.Name), "expected '=' after 'guard let %s', found %s", g.Bind.Name, describe(p.tok()))
	}
	p.next()
	saved := p.noTrailing
	p.noTrailing = true
	g.Value = p.expr()
	p.noTrailing = saved
	if !p.at(lexer.Else) {
		p.fail(p.tok(), "add else { return } after the value", "expected 'else' after the guard value, found %s", describe(p.tok()))
	}
	p.next()
	if !p.at(lexer.LBrace) {
		p.fail(p.tok(), "", "expected '{' after 'else', found %s", describe(p.tok()))
	}
	g.Else = p.block()
	g.Span = p.span(start)
	return g
}

func (p *parser) header(keyword string) ast.Expr {
	saved := p.noTrailing
	p.noTrailing = true
	defer func() { p.noTrailing = saved }()
	first := p.p
	cond := p.expr()
	p.checkParens(keyword, first, p.p-1)
	if p.at(lexer.Assign) {
		// Report the mistake but read it as `==` so the rest of the
		// statement, including any `else`, still parses.
		p.errorAt(p.next(), "= assigns a value; == compares", "use == to compare in a condition")
		rhs := p.expr()
		cond = &ast.BinaryExpr{Span: ast.Span{Start: cond.Pos(), End: rhs.EndOffset()}, Op: lexer.Eq, X: cond, Y: rhs}
	}
	p.bodyStart(keyword)
	return cond
}

// Reports a missing `{` after the header of [keyword].
func (p *parser) bodyStart(keyword string) {
	if p.at(lexer.LBrace) {
		return
	}
	t := p.tok()
	hint := ""
	if t.IsAutoSemi() {
		hint = "put { on the same line as " + keyword
	}
	p.fail(t, hint, "expected '{' after the %s condition, found %s", keyword, describe(t))
}

func (p *parser) simpleStmt() ast.Stmt {
	x := p.expr()
	switch k := p.tok().Kind; k {
	case lexer.Assign, lexer.PlusAssign, lexer.MinusAssign, lexer.StarAssign, lexer.SlashAssign, lexer.PercentAssign,
		lexer.AmpPlusAssign, lexer.AmpMinusAssign, lexer.AmpStarAssign:
		opTok := p.next()
		switch x.(type) {
		case *ast.Ident, *ast.IndexExpr, *ast.MemberExpr:
		default:
			p.errs = append(p.errs, diag.Diagnostic{
				Pos:     x.Pos(),
				Len:     max(1, utf8.RuneCountInString(p.file.Src[x.Pos().Offset:x.EndOffset()])),
				Message: "can't assign to this expression",
				Hint:    "assign to a variable, list element or field",
			})
		}
		v := p.expr()
		return &ast.AssignStmt{Span: ast.Span{Start: x.Pos(), End: p.lastEnd}, Target: x, Op: opTok.Kind, Value: v}
	}
	return &ast.ExprStmt{Span: ast.Span{Start: x.Pos(), End: p.lastEnd}, X: x}
}

// --- Expressions ---

// Reports whether a token of kind [k] can begin an expression.
func startsExpr(k lexer.Kind) bool {
	switch k {
	case lexer.Ident, lexer.Int, lexer.Float, lexer.String, lexer.StringHead, lexer.True, lexer.False,
		lexer.LParen, lexer.LBracket, lexer.Minus, lexer.Not, lexer.Fn, lexer.Match, lexer.Dot, lexer.Nil, lexer.Try, lexer.Await:
		return true
	}
	return false
}

// Binding power of binary operators; 0 means the token is not one.
func precedence(k lexer.Kind) int {
	switch k {
	case lexer.OrOr:
		return 1
	case lexer.AndAnd:
		return 2
	case lexer.Eq, lexer.NotEq:
		return 3
	case lexer.Lt, lexer.LtEq, lexer.Gt, lexer.GtEq:
		return 4
	case lexer.QuestionQuestion:
		return 5
	case lexer.Plus, lexer.Minus, lexer.AmpPlus, lexer.AmpMinus:
		return 6
	case lexer.Star, lexer.Slash, lexer.Percent, lexer.AmpStar:
		return 7
	}
	return 0
}

func (p *parser) expr() ast.Expr { return p.binary(1) }

func (p *parser) binary(min int) ast.Expr {
	x := p.unary()
	for {
		k := p.tok().Kind
		if k == lexer.Amp {
			p.fail(p.tok(), "use && for logical and", "unexpected '&'")
		}
		prec := precedence(k)
		if prec == 0 || prec < min {
			return x
		}
		if k == lexer.Minus && p.spacedInHead() && p.peek(1).Pos.Offset == p.tok().End {
			return x
		}
		p.next()
		p.skipNewlines()
		next := prec + 1
		if k == lexer.QuestionQuestion {
			// a ?? b ?? c groups as a ?? (b ?? c).
			next = prec
		}
		y := p.binary(next)
		if (prec == 3 || prec == 4) && precedence(p.tok().Kind) == prec {
			p.fail(p.tok(), "use a < b && b < c", "comparisons can't be chained")
		}
		x = &ast.BinaryExpr{Span: ast.Span{Start: x.Pos(), End: y.EndOffset()}, Op: k, X: x, Y: y}
	}
}

func (p *parser) unary() ast.Expr {
	t := p.tok()
	if t.Kind == lexer.Try {
		return p.tryExpr()
	}
	if t.Kind == lexer.Await {
		p.next()
		x := p.unary()
		return &ast.AwaitExpr{Span: ast.Span{Start: t.Pos, End: x.EndOffset()}, X: x}
	}
	if t.Kind == lexer.Minus || t.Kind == lexer.Not {
		p.next()
		x := p.unary()
		return &ast.UnaryExpr{Span: ast.Span{Start: t.Pos, End: x.EndOffset()}, Op: t.Kind, X: x}
	}
	return p.postfix(p.primary())
}

func (p *parser) postfix(x ast.Expr) ast.Expr {
	for {
		var typeArgs []ast.TypeExpr
		if _, member := x.(*ast.MemberExpr); (qualifiable(x) || member) && p.at(lexer.Lt) && p.typeArgsAhead() {
			typeArgs = p.typeArgs()
		}
		if typeArgs != nil && !p.at(lexer.LParen) {
			x = &ast.TypeArgsExpr{Span: p.span(x.Pos()), X: x, Args: typeArgs}
			typeArgs = nil
		}
		switch p.tok().Kind {
		case lexer.LParen:
			if p.spacedInHead() {
				return x
			}
			open := p.next()
			call := &ast.CallExpr{Fn: x, TypeArgs: typeArgs}
			p.list(open, lexer.RParen, func() { call.Args = append(call.Args, p.arg()) })
			if !p.noTrailing && p.at(lexer.LBrace) {
				call.Trailing = p.block()
			}
			call.Span = p.span(x.Pos())
			x = call
		case lexer.LBracket:
			if p.spacedInHead() {
				return x
			}
			p.next()
			saved, savedHead := p.noTrailing, p.elemHead
			p.noTrailing, p.elemHead = false, false
			p.skipNewlines()
			idx := p.expr()
			p.skipNewlines()
			p.noTrailing, p.elemHead = saved, savedHead
			p.expect(lexer.RBracket)
			x = &ast.IndexExpr{Span: p.span(x.Pos()), X: x, Index: idx}
		case lexer.LBrace:
			// `Column { ... }` is a call with only a trailing block.
			if p.noTrailing || !qualifiable(x) {
				return x
			}
			x = &ast.CallExpr{Fn: x, TypeArgs: typeArgs, Trailing: p.block(), Span: p.span(x.Pos())}
		case lexer.Dot, lexer.QuestionDot:
			opt := p.next().Kind == lexer.QuestionDot
			name := p.ident("a member name")
			x = &ast.MemberExpr{Span: p.span(x.Pos()), X: x, Name: name, Optional: opt}
		case lexer.Question:
			if next := p.peek(1); startsExpr(next.Kind) && !next.IsAutoSemi() {
				p.fail(p.tok(), "write match cond { true => a, false => b }", "Vex has no ? : operator")
			}
			p.fail(p.tok(), "use ?. to read a member, or ?? to give a default", "unexpected '?'")
		default:
			return x
		}
	}
}

func (p *parser) arg() *ast.Arg {
	start := p.tok().Pos
	a := &ast.Arg{}
	if p.at(lexer.Ident) && p.peek(1).Kind == lexer.Colon {
		a.Name = p.ident("an argument name")
		p.next()
	}
	a.Value = p.expr()
	a.Span = p.span(start)
	return a
}

func (p *parser) primary() ast.Expr {
	t := p.tok()
	sp := ast.Span{Start: t.Pos, End: t.End}
	switch t.Kind {
	case lexer.Ident:
		p.next()
		return &ast.Ident{Span: sp, Name: t.Text}
	case lexer.Int:
		p.next()
		return &ast.IntLit{Span: sp, Value: t.Value}
	case lexer.Float:
		p.next()
		return &ast.FloatLit{Span: sp, Value: t.Value}
	case lexer.True, lexer.False:
		p.next()
		return &ast.BoolLit{Span: sp, Value: t.Kind == lexer.True}
	case lexer.Nil:
		p.next()
		return &ast.NilLit{Span: sp}
	case lexer.String:
		p.next()
		return &ast.StringLit{Span: sp, Value: t.Value}
	case lexer.StringHead:
		return p.interpolated()
	case lexer.LParen:
		p.next()
		saved, savedHead := p.noTrailing, p.elemHead
		p.noTrailing, p.elemHead = false, false
		p.skipNewlines()
		if p.at(lexer.RParen) {
			p.fail(p.tok(), "", "expected an expression inside ( )")
		}
		x := p.expr()
		p.skipNewlines()
		p.noTrailing, p.elemHead = saved, savedHead
		p.expect(lexer.RParen)
		return x
	case lexer.Fn:
		return p.funcLit()
	case lexer.Match:
		return p.matchExpr()
	case lexer.Dot:
		p.next()
		name := p.ident("a case name after '.'")
		return &ast.ImplicitMember{Span: p.span(t.Pos), Name: name}
	case lexer.LBracket:
		return p.bracketLit()
	}
	if t.Kind == lexer.Ellipsis {
		p.fail(t, "use it inside a list or map literal, e.g. [...xs, 4]", "'...' can only be used in a list or map literal")
	}
	p.fail(t, "", "expected an expression, found %s", describe(t))
	return nil
}

// Parses a list literal `[1, 2]`, or a map literal `["a": 1]` or `[:]`.
func (p *parser) bracketLit() ast.Expr {
	open := p.next()
	if p.at(lexer.Colon) && p.peek(1).Kind == lexer.RBracket {
		p.next()
		p.next()
		return &ast.MapLit{Span: p.span(open.Pos)}
	}
	var elems []ast.Stmt
	kind := &litKind{first: true}
	p.list(open, lexer.RBracket, func() { elems = append(elems, p.element(kind)) })
	sp := p.span(open.Pos)
	plain := true
	for _, s := range elems {
		if x, ok := s.(*ast.ExprStmt); !ok {
			plain = false
		} else if _, ok := x.X.(*ast.SpreadExpr); ok {
			plain = false
		}
	}
	if !plain {
		body := &ast.Block{Span: sp, Stmts: elems}
		if kind.isMap {
			return &ast.MapLit{Span: sp, Body: body}
		}
		return &ast.ListLit{Span: sp, Body: body}
	}
	if kind.isMap {
		m := &ast.MapLit{Span: sp}
		for _, s := range elems {
			e := s.(*ast.ExprStmt).X.(*ast.MapEntry)
			m.Keys = append(m.Keys, e.Key)
			m.Values = append(m.Values, e.Value)
		}
		return m
	}
	list := &ast.ListLit{Span: sp}
	for _, s := range elems {
		list.Elems = append(list.Elems, s.(*ast.ExprStmt).X)
	}
	return list
}

// Whether a list or map literal is being parsed, decided by its first
// element that isn't `if`, `for` or `...`.
type litKind struct {
	first, isMap bool
}

// Parses one element of a list or map literal as a statement: a value or
// `key: value` entry, `...xs`, or an `if` or `for` that produces elements.
func (p *parser) element(kind *litKind) ast.Stmt {
	start := p.tok().Pos
	switch p.tok().Kind {
	case lexer.Ellipsis:
		return &ast.ExprStmt{X: p.spread()}
	case lexer.If:
		p.next()
		s := &ast.IfStmt{}
		if p.at(lexer.Let) {
			p.next()
			s.Bind = p.ident("a name after 'if let'")
			if !p.at(lexer.Assign) {
				p.fail(p.tok(), fmt.Sprintf("write if let %s = optionalValue %s", s.Bind.Name, s.Bind.Name), "expected '=' after 'if let %s', found %s", s.Bind.Name, describe(p.tok()))
			}
			p.next()
		}
		s.Cond = p.elemHeader(func() ast.Expr { return p.expr() })
		s.Then = p.elemBody(kind)
		if p.newlinesThen(lexer.Else) {
			p.skipNewlines()
		}
		if p.at(lexer.Else) {
			p.next()
			p.skipNewlines()
			if p.at(lexer.If) {
				s.Else = p.element(kind)
			} else {
				s.Else = p.elemBody(kind)
			}
		}
		s.Span = p.span(start)
		return s
	case lexer.For:
		p.next()
		s := &ast.ForStmt{Elem: p.ident("a loop variable")}
		if p.at(lexer.Comma) {
			p.next()
			s.Index, s.Elem = s.Elem, p.ident("a loop variable")
		}
		if !p.at(lexer.In) {
			p.fail(p.tok(), fmt.Sprintf("write for %s in items %s", s.Elem.Name, s.Elem.Name), "expected 'in', found %s", describe(p.tok()))
		}
		p.next()
		s.Iter = p.elemHeader(func() ast.Expr {
			iter := p.expr()
			if p.at(lexer.DotDot) || p.at(lexer.DotDotEq) {
				inclusive := p.next().Kind == lexer.DotDotEq
				hi := p.expr()
				iter = &ast.RangeExpr{Span: ast.Span{Start: iter.Pos(), End: p.lastEnd}, Lo: iter, Hi: hi, Inclusive: inclusive}
			}
			return iter
		})
		s.Body = p.elemBody(kind)
		s.Span = p.span(start)
		return s
	}
	x := p.expr()
	if kind.first {
		kind.first, kind.isMap = false, p.at(lexer.Colon)
	}
	switch {
	case kind.isMap:
		if !p.at(lexer.Colon) {
			p.fail(p.tok(), "write key: value", "expected ':' after a map key, found %s", describe(p.tok()))
		}
		p.next()
		v := p.expr()
		return &ast.ExprStmt{X: &ast.MapEntry{Span: p.span(start), Key: x, Value: v}}
	case p.at(lexer.Colon):
		p.fail(p.tok(), "a list holds values, a map holds key: value pairs", "unexpected ':' in a list")
	}
	return &ast.ExprStmt{X: x}
}

// Parses the condition of an `if` element or the sequence of a `for`
// element with [parse].
func (p *parser) elemHeader(parse func() ast.Expr) ast.Expr {
	saved, savedHead := p.noTrailing, p.elemHead
	p.noTrailing, p.elemHead = true, true
	x := parse()
	p.noTrailing, p.elemHead = saved, savedHead
	p.skipNewlines()
	if !startsExpr(p.tok().Kind) && !p.at(lexer.If) && !p.at(lexer.For) && !p.at(lexer.Ellipsis) {
		p.fail(p.tok(), "e.g. [if ok 1 else 2] or [for x in xs x * 2]", "expected an element after the condition or loop, found %s", describe(p.tok()))
	}
	return x
}

// Parses the single element that an `if` or `for` element produces, as a
// block.
func (p *parser) elemBody(kind *litKind) *ast.Block {
	start := p.tok().Pos
	s := p.element(kind)
	return &ast.Block{Span: p.span(start), Stmts: []ast.Stmt{s}}
}

// Reports whether the next tokens are line breaks followed by [k].
func (p *parser) newlinesThen(k lexer.Kind) bool {
	i := 0
	for p.peek(i).IsAutoSemi() {
		i++
	}
	return i > 0 && p.peek(i).Kind == k
}

// Reports whether the current token is preceded by a space inside the
// header of an `if` or `for` element, so it starts the element.
func (p *parser) spacedInHead() bool {
	return p.elemHead && p.tok().Pos.Offset != p.lastEnd
}

// Parses `...xs` or `...?xs`.
func (p *parser) spread() *ast.SpreadExpr {
	t := p.expect(lexer.Ellipsis)
	s := &ast.SpreadExpr{}
	if p.at(lexer.Question) && p.tok().Pos.Offset == t.End {
		p.next()
		s.Optional = true
	}
	s.X = p.expr()
	s.Span = p.span(t.Pos)
	return s
}

// Parses a closure: `fn(x: int) -> int => x * 2` or `fn() { ... }`.
func (p *parser) funcLit() ast.Expr {
	start := p.next().Pos
	lit := &ast.FuncLit{}
	if p.at(lexer.Ident) {
		p.fail(p.tok(), "closures have no name; store one with let name = fn(...) { ... }", "expected '(' after 'fn' in an expression")
	}
	open := p.expect(lexer.LParen)
	p.list(open, lexer.RParen, func() {
		name := p.ident("a parameter name")
		param := &ast.Param{Name: name}
		if p.at(lexer.Colon) {
			p.next()
			param.Type = p.typeExpr()
		}
		if p.at(lexer.Assign) {
			p.fail(p.tok(), "", "closure parameters can't have default values")
		}
		param.Span = p.span(name.Start)
		lit.Params = append(lit.Params, param)
	})
	lit.Throws = p.throws()
	if p.at(lexer.Arrow) {
		p.next()
		lit.Result = p.typeExpr()
	}
	switch {
	case p.at(lexer.FatArrow):
		p.next()
		lit.ExprBody = p.expr()
	case p.at(lexer.LBrace):
		lit.Body = p.block()
	default:
		p.fail(p.tok(), "", "expected '{' or '=>' to start the closure body, found %s", describe(p.tok()))
	}
	lit.Span = p.span(start)
	return lit
}

// Parses `"text {expr} text"` from its head token to its tail token.
func (p *parser) interpolated() ast.Expr {
	head := p.next()
	s := &ast.InterpolatedString{Texts: []string{head.Value}}
	saved := p.noTrailing
	p.noTrailing = false
	defer func() { p.noTrailing = saved }()
	for {
		if k := p.tok().Kind; k == lexer.StringMid || k == lexer.StringTail {
			p.fail(p.tok(), `put a value inside, or write \{ for a literal brace`, "empty {} in string")
		}
		s.Exprs = append(s.Exprs, p.expr())
		t := p.tok()
		switch t.Kind {
		case lexer.StringMid:
			p.next()
			s.Texts = append(s.Texts, t.Value)
			continue
		case lexer.StringTail:
			p.next()
			s.Texts = append(s.Texts, t.Value)
			s.Span = p.span(head.Pos)
			return s
		}
		p.fail(t, "", "expected } to end the interpolation, found %s", describe(t))
	}
}
