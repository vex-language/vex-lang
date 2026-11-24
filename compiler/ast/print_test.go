package ast

import (
	"strings"
	"testing"

	"github.com/irshadio/vex-lang/compiler/lexer"
)

func TestPrinters(t *testing.T) {
	fn := &FuncDecl{
		Name:   &Ident{Name: "double"},
		Params: []*Param{{Name: &Ident{Name: "x"}, Type: &NamedType{Name: "int"}}},
		Result: &ListType{Elem: &NamedType{Name: "int"}},
		Body: &Block{Stmts: []Stmt{
			&ReturnStmt{Value: &BinaryExpr{Op: lexer.Star, X: &Ident{Name: "x"}, Y: &IntLit{Value: "2"}}},
		}},
	}
	if got, want := Sexpr(fn), "(fn double param x: int -> [int] (block (return (* x 2))))"; got != want {
		t.Errorf("Sexpr\n got: %s\nwant: %s", got, want)
	}
	var b strings.Builder
	Fprint(&b, fn)
	want := "fn double\n  param x: int\n  -> [int]\n  block\n    return\n      *\n        x\n        2\n"
	if b.String() != want {
		t.Errorf("Fprint\n got:\n%s\nwant:\n%s", b.String(), want)
	}
}
