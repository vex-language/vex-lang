package doc

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vex-language/vex-lang/compiler/modules"
)

// Returns the documentation of a program made of the files in [files],
// with `main.vx` as its root.
func load(t *testing.T, files map[string]string) []*Module {
	t.Helper()
	isDir := files["package.toml"] != ""
	root := "main.vx"
	if isDir {
		root = "app"
	}
	fsys := fstest.MapFS{}
	for name, src := range files {
		if isDir {
			name = "app/" + name
		}
		fsys[name] = &fstest.MapFile{Data: []byte(src)}
	}
	prog, err := modules.LoadFS(fsys, root, root, isDir)
	if err != nil {
		t.Fatal(err)
	}
	return FromProgram(prog)
}

// Returns the signatures of [decls] and their members, one per line,
// members indented.
func signatures(decls []*Decl) string {
	var b strings.Builder
	for _, d := range decls {
		b.WriteString(d.Signature + "\n")
		for _, m := range d.Members {
			b.WriteString("  " + m.Signature + "\n")
		}
	}
	return b.String()
}

func TestSignatures(t *testing.T) {
	mods := load(t, map[string]string{"main.vx": `
pub struct Box<T: Equatable & Hashable>: Codable {
    pub let id: int
    pub count: int = 0
    pub private(set) items: [T] = []
    pub tags: [string: int] = [:]
    pub label = "a \"b\" \{c\}"
    hidden: int = 0

    pub static const limit: int = 1 << 4
    pub static var made = 0
    pub fn add(item: T, at: int = -1, mode: Mode = .fast) throws -> bool => true
    pub async fn load(path: string?) throws(Fail) -> [T]? => nil
    pub get size -> int => items.len
    pub set size(value: int) {}
    pub operator +(other: Box<T>) -> Box<T> => self
    pub operator -() -> Box<T> => self
    pub static fn of<U>(f: fn(U) -> T, g: async fn() throws -> (int, string)) -> Box<T> => Box<T>(id: 0)
    fn secret() {}
}

pub enum Mode { fast
    slow(level: int, why: string) }

pub enum Fail { bad }

pub enum Color: int, Codable {
    red = 1
    green
}

pub sealed interface Load {}

pub extension type UserId(value: int): Hashable {
    pub fn isAdmin() -> bool => value < 100
}

pub interface Store<T>: Codable {
    fn get(key: string) -> T?
    static fn make() -> Self
}

pub typedef Pair<T> = (T, {T first, T second})
pub extern type Db
pub extern fn cOpen(path: string) -> Db as "open_db"
pub let start = 1.5
pub const names = ["a", "b"]
pub const big = compute(1, x: [1, 2]) + 1
pub const long = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

fn compute(a: int, x: [int]) -> int => a

pub extension<T> [T] {
    fn second() -> T? => nil
}

fn main() {}
`})
	want := `struct Box<T: Equatable & Hashable>: Codable
  let id: int
  count: int = 0
  private(set) items: [T] = []
  tags: [string: int] = [:]
  label = "a \"b\" \{c\}"
  static const limit: int = 1 << 4
  static var made: int
  fn add(item: T, at: int = -1, mode: Mode = .fast) throws -> bool
  async fn load(path: string?) throws(Fail) -> [T]?
  get size -> int
  set size(value: int)
  operator +(other: Box<T>) -> Box<T>
  operator -() -> Box<T>
  static fn of<U>(f: fn(U) -> T, g: async fn() throws -> (int, string)) -> Box<T>
enum Mode
  fast
  slow(level: int, why: string)
enum Fail
  bad
enum Color: int, Codable
  red = 1
  green
sealed interface Load
extension type UserId(value: int): Hashable
  fn isAdmin() -> bool
interface Store<T>: Codable
  fn get(key: string) -> T?
  static fn make() -> Self
typedef Pair<T> = (T, {T first, T second})
extern type Db
extern fn cOpen(path: string) -> Db
let start: float
const names = ["a", "b"]
const big = compute(1, x: [1, 2]) + 1
const long = …
extension<T> [T]
  fn second() -> T?
`
	if got := signatures(mods[0].Decls); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestProgramWithoutPubDocumentsEverything(t *testing.T) {
	mods := load(t, map[string]string{"main.vx": `
/// A point.
struct Point {
    x: int
    fn len() -> int => x
}

fn main() {}

test "works" {}
`})
	if got, want := signatures(mods[0].Decls), "struct Point\n  x: int\n  fn len() -> int\nfn main()\n"; got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if mods[0].Name != "main" || mods[0].Decls[0].Doc != "A point." {
		t.Errorf("name %q, doc %q", mods[0].Name, mods[0].Decls[0].Doc)
	}
}

func TestResolve(t *testing.T) {
	mods := load(t, map[string]string{
		"package.toml": "[package]\nname = \"app\"\n",
		"main.vx":      "import \"geo\"\n\nfn main() {}\n",
		"geo/geo.vx": `
pub struct Point<T> {
    pub x: int
    pub fn moved(by: int) -> Point<T> => self
}
pub fn origin() -> Point<int> => Point<int>(x: 0)
pub extension string {
    fn shout() -> string => self
}
`,
	})
	set := NewSet(mods, nil)
	app, geo := mods[0], mods[1]
	point := geo.Lookup("Point")
	moved := point.Member("moved")
	cases := []struct {
		m    *Module
		d    *Decl
		ref  string
		href string
		ok   bool
	}{
		{geo, nil, "Point", "#Point", true},
		{geo, nil, "Point.x", "#Point.x", true},
		{geo, nil, "Point.missing", "", false},
		{geo, nil, "origin", "#origin", true},
		{geo, nil, "string.shout", "#extension-string.shout", true},
		{geo, point, "x", "#Point.x", true},
		{geo, point, "T", "", true},
		{geo, moved, "x", "#Point.x", true},
		{geo, moved, "by", "", true},
		{geo, moved, "T", "", true},
		{geo, nil, "by", "", false},
		{geo, nil, "int", "", true},
		{geo, nil, "Iterable", "", true},
		{geo, nil, "string.len", "", true},
		{app, nil, "geo.Point", "geo.html#Point", true},
		{app, nil, "geo.Point.moved", "geo.html#Point.moved", true},
		{app, nil, "Point", "", false},
		{app, nil, "geo.Nothing", "", false},
	}
	for _, c := range cases {
		href, ok := set.Resolver(c.m, c.d)(c.ref)
		if href != c.href || ok != c.ok {
			t.Errorf("[%s] in %s: got %q, %v; want %q, %v", c.ref, c.m.Name, href, ok, c.href, c.ok)
		}
	}
}

func TestUnresolvedReferencesWarnOnce(t *testing.T) {
	mods := load(t, map[string]string{"main.vx": `
/// Uses [Nothing] and [Nothing] and [count].
pub fn f(count: int) {}

fn main() {}
`})
	var warn bytes.Buffer
	set := NewSet(mods, &warn)
	set.ModuleHTML(mods[0])
	want := "main.vx:3: warning: [Nothing] in the doc of f doesn't name a declaration\n"
	if warn.String() != want {
		t.Errorf("got %q, want %q", warn.String(), want)
	}
}

func TestTextOfMember(t *testing.T) {
	mods := load(t, map[string]string{"main.vx": `
pub struct Point {
    /// The x coordinate.
    ///
    /// Counted from the left.
    pub x: int
}

fn main() {}
`})
	set := NewSet(mods, nil)
	var b bytes.Buffer
	WriteText(&b, set.Find("Point.x"))
	if want := "x: int\n\n    The x coordinate.\n\n    Counted from the left.\n"; b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
	if set.Find("Point.y") != nil || set.Find("main.Point") == nil {
		t.Error("Find")
	}
}
