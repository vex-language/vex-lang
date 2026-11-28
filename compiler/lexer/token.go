// Package lexer turns Vex source text into tokens.
package lexer

import "github.com/vex-language/vex-lang/compiler/diag"

// The category of a [Token].
type Kind uint8

const (
	EOF Kind = iota

	// A statement end: an explicit `;` or a line break inserted by the lexer.
	Semi

	Ident
	Int
	Float

	// A string literal without interpolation.
	String

	// The text before the first `{` of an interpolated string.
	StringHead

	// The text between a `}` and the next `{` of an interpolated string.
	StringMid

	// The text after the last `}` of an interpolated string.
	StringTail

	// The format spec after a `:` in an interpolation, as in `{x:.2}`; the
	// value is the text after the colon.
	FormatSpec

	keywordsStart
	Fn
	Let
	Var
	If
	Else
	While
	For
	In
	Return
	Break
	Continue
	Struct
	Enum
	Interface
	Import
	Extern
	Pub
	As
	Is
	Match
	Static
	Const
	Typedef
	Guard
	True
	False
	Nil
	Throw
	Throws
	Try
	Catch
	Async
	Await
	keywordsEnd

	LParen
	RParen
	LBrace
	RBrace
	LBracket
	RBracket
	Comma
	Colon
	Dot
	DotDot
	DotDotEq
	Ellipsis
	Arrow
	FatArrow
	Question
	QuestionDot
	QuestionQuestion

	Plus
	Minus
	Star
	Slash
	Percent
	AmpPlus
	AmpMinus
	AmpStar
	Not
	AndAnd
	OrOr
	Amp
	Pipe
	Caret
	Tilde
	Shl
	// Never produced by the lexer, which keeps `>>` as two `>` so nested
	// type arguments close; the parser joins them.
	Shr

	Assign
	PlusAssign
	MinusAssign
	StarAssign
	SlashAssign
	PercentAssign
	AmpPlusAssign
	AmpMinusAssign
	AmpStarAssign
	AmpAssign
	PipeAssign
	CaretAssign
	ShlAssign
	// Joined by the parser from `>` and `>=`, like [Shr].
	ShrAssign

	Eq
	NotEq
	EqEq
	NotEqEq
	Lt
	LtEq
	Gt
	GtEq
)

// Reports whether [s] is a keyword, which can't be used as a name.
func IsKeyword(s string) bool {
	_, ok := keywords[s]
	return ok
}

// Keywords mapped to their kinds.
var keywords = map[string]Kind{
	"fn":        Fn,
	"let":       Let,
	"var":       Var,
	"if":        If,
	"else":      Else,
	"while":     While,
	"for":       For,
	"in":        In,
	"return":    Return,
	"break":     Break,
	"continue":  Continue,
	"struct":    Struct,
	"static":    Static,
	"const":     Const,
	"typedef":   Typedef,
	"enum":      Enum,
	"interface": Interface,
	"import":    Import,
	"extern":    Extern,
	"pub":       Pub,
	"as":        As,
	"is":        Is,
	"match":     Match,
	"guard":     Guard,
	"nil":       Nil,
	"throw":     Throw,
	"throws":    Throws,
	"try":       Try,
	"catch":     Catch,
	"async":     Async,
	"await":     Await,
	"true":      True,
	"false":     False,
}

// Operators and punctuation, longest first so `..=` wins over `..`.
var operators = []struct {
	text string
	kind Kind
}{
	{"...", Ellipsis},
	{"===", EqEq},
	{"!==", NotEqEq},
	{"..=", DotDotEq},
	{"&+=", AmpPlusAssign},
	{"&-=", AmpMinusAssign},
	{"&*=", AmpStarAssign},
	{"<<=", ShlAssign},
	{"?.", QuestionDot},
	{"??", QuestionQuestion},
	{"..", DotDot},
	{"->", Arrow},
	{"=>", FatArrow},
	{"==", Eq},
	{"!=", NotEq},
	{"<=", LtEq},
	{">=", GtEq},
	{"&&", AndAnd},
	{"||", OrOr},
	{"&+", AmpPlus},
	{"&-", AmpMinus},
	{"&*", AmpStar},
	{"&=", AmpAssign},
	{"|=", PipeAssign},
	{"^=", CaretAssign},
	{"<<", Shl},
	{"&", Amp},
	{"|", Pipe},
	{"^", Caret},
	{"~", Tilde},
	{"+=", PlusAssign},
	{"-=", MinusAssign},
	{"*=", StarAssign},
	{"/=", SlashAssign},
	{"%=", PercentAssign},
	{"(", LParen},
	{")", RParen},
	{"{", LBrace},
	{"}", RBrace},
	{"[", LBracket},
	{"]", RBracket},
	{",", Comma},
	{":", Colon},
	{";", Semi},
	{".", Dot},
	{"+", Plus},
	{"-", Minus},
	{"*", Star},
	{"/", Slash},
	{"%", Percent},
	{"!", Not},
	{"=", Assign},
	{"<", Lt},
	{">", Gt},
	{"?", Question},
}

var kindNames = map[Kind]string{
	EOF:        "end of file",
	Semi:       "end of statement",
	Ident:      "identifier",
	Int:        "integer",
	Float:      "float",
	String:     "string",
	StringHead: "string",
	StringMid:  "string",
	StringTail: "string",
	FormatSpec: "format spec",
	Shr:        "'>>'",
	ShrAssign:  "'>>='",
}

func init() {
	for text, k := range keywords {
		kindNames[k] = "'" + text + "'"
	}
	for _, op := range operators {
		if _, ok := kindNames[op.kind]; !ok {
			kindNames[op.kind] = "'" + op.text + "'"
		}
	}
}

// Returns a readable name for error messages, such as `identifier` or `'('`.
func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "unknown"
}

// Reports whether the kind is a keyword such as [Fn] or [Return].
func (k Kind) IsKeyword() bool { return k > keywordsStart && k < keywordsEnd }

// A single lexical unit of Vex source.
type Token struct {
	Kind Kind

	// The exact source text of the token. An inserted statement end has `\n`.
	Text string

	// The decoded value: string contents with escapes resolved, or a number
	// without digit separators. Empty for other kinds.
	Value string

	Pos diag.Pos

	// The byte offset just after the token.
	End int
}

// Reports whether the token is a statement end inserted at a line break.
func (t Token) IsAutoSemi() bool { return t.Kind == Semi && t.Text == "\n" }
