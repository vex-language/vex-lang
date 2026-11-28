<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/brand/vex-lockup-dark.svg">
    <img src="assets/brand/vex-lockup-light.svg" alt="Vex" width="280">
  </picture>
</p>

<p align="center">
  <strong>Native apps. One language.</strong><br>
  A compiled, statically typed language for building truly native mobile apps.
</p>

<p align="center">
  <a href="docs/SYNTAX.md">Syntax</a> ·
  <a href="BACKLOG.md">Roadmap</a> ·
  <a href="#status">Status</a>
</p>

---

> **Status: v0.1, early development.** The core language compiles to native binaries and runs on macOS and Linux.
> Mobile UI is the next big step and is still a design (see [Where it's going](#where-its-going-native-ui)). Not ready for production use.

## What is Vex?

Vex is a programming language for writing iOS and Android apps with **one codebase** and **real native widgets**.

- **Flutter-style tree syntax** for building UI, built into the language
- **Real platform widgets:** UIKit on iOS, Android views on Android, so apps look and feel exactly native
- **Compiled to native machine code:** no virtual machine, no JavaScript bridge, no engine to ship
- **Tiny apps and fast start-up,** because only your code ships
- **Static types with inference:** errors are caught at compile time, without type noise
- **Developer friendly:** familiar to anyone who knows Dart, Swift, Kotlin, Go or TypeScript

## A taste of Vex

```swift
struct User {
    name: string
    age: int = 18

    fn greet() {
        println("Hi, I'm {name}")
    }

    fn isAdult() -> bool => age >= 18
}

fn main() {
    let users = [
        User(name: "Ann", age: 30),
        User(name: "Sam"),
    ]

    for i, user in users {
        user.greet()
        println("{i}: adult = {user.isAdult()}")
    }
}
```

- `let` can't change, `var` can, and types are inferred
- No semicolons, and no parentheses around conditions
- Named and default arguments: `User(name: "Sam")`
- String interpolation: `"Hi, I'm {name}"`
- Methods inside structs, with no `self.` needed

Jump to the [language tour](#language-tour) below, or read the full reference in [docs/SYNTAX.md](docs/SYNTAX.md).

## Where it's going: native UI

```swift
import "package:ui"

struct Counter: View {
    let count = State(0)

    fn body() -> View => Column(spacing: 12) {
        Text("Count: {count.value}").size(24)
        Button("Add") { count.value += 1 }
    }
}

fn main() {
    app(Counter())
}
```

When `count` changes, the view is rebuilt, compared with the previous one, and only the native widgets that changed are updated. This is a sketch, not final syntax.

The UI framework will be a separate project, Vex UI, built once the language is complete, in the same way Flutter is built on Dart. The compiler has no UI keywords: the framework uses general features such as interfaces, generics and builder blocks. See [docs/design/architecture.md](docs/design/architecture.md).

## How it works

```
 main.vx ─► Lexer ─► Parser ─► Type checker ─► C code ─► clang ─► native binary
```

The compiler is written in Go. It turns Vex source into portable C, then uses the system C compiler to produce a native executable. Because the output is C, the same pipeline can target macOS, iOS (`arm64-apple-ios`) and Android (NDK).

Memory is managed by a garbage collector built into the runtime, with Go-like speed: an allocation-heavy benchmark runs in 0.33 s against 0.35 s for the same program in Go. See [docs/design/memory.md](docs/design/memory.md).

```
┌──────────────────────────────────────┐
│ Your app (.vx)                        │  components, state, views
├──────────────────────────────────────┤
│ Vex UI runtime (C)                    │  view tree, diffing, events
├───────────────────┬──────────────────┤
│ iOS layer (UIKit) │ Android layer     │  real native widgets
└───────────────────┴──────────────────┘
```

## Getting started

> Vex is pre-release. Programs compile and run on macOS and Linux; the language is still growing.

**Requirements**
- Go 1.23 or newer, to build the compiler
- A C compiler (`clang` or `gcc`), to build Vex programs

**Build from source**

```sh
git clone https://github.com/vex-language/vex-lang.git
cd vex-lang
./build.sh
```

This builds `bin/vex`. To run `vex` from anywhere, add the folder to your `PATH` (for zsh):

```sh
echo 'export PATH="'"$PWD"'/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

Then run `vex doctor` to check the tools Vex builds with: the C compiler, git for `vex get`, and, for phone apps, Xcode and the Android SDK, NDK and JDK. It says what is missing and how to install it, and never installs anything itself; `vex doctor -v` shows where each tool was found.

**Usage**

```sh
vex check hello.vx          # check for errors without building
vex test                    # run the tests of the project in this folder
vex run hello.vx            # compile and run
vex build hello.vx -o hello # build a standalone native binary
vex emit-c hello.vx         # print the generated C
vex doc                     # write HTML API docs from /// comments to doc/
vex doc vex:json Json       # print one declaration's signature and docs
vex run                     # run the project in this folder (with a package.toml)
vex run app.vx one two      # arguments after the program go to it (os.args())
vex build --debug app.vx    # -O0 -g, keeping the generated C as app.c for a debugger
vex build --traces app.vx   # runtime errors list the calls that led to them
vex run --sanitize=address,undefined app.vx  # stop at memory errors and undefined behaviour
vex get https://github.com/ann/charts  # add a package from git and lock it in package.lock
vex doctor                  # check the C compiler, git, Xcode and Android tools
vex version
```

**Hello, world**

```swift
fn main() {
    println("Hello, world!")
}
```

Save it as `hello.vx` and run `vex run hello.vx`.

**Examples**

The [examples](examples) folder has small complete programs, each with its expected output:

| Example | Shows |
|---|---|
| [hello.vx](examples/hello.vx) | The smallest program |
| [fizzbuzz.vx](examples/fizzbuzz.vx) | `if`/`else if`, ranges |
| [fibonacci.vx](examples/fibonacci.vx) | Recursion and building a list |
| [primes.vx](examples/primes.vx) | Sieve of Eratosthenes over a million numbers |
| [sorting.vx](examples/sorting.vx) | Bubble sort and insertion sort, in place |
| [structs.vx](examples/structs.vx) | Fields with defaults, methods, `static fn`, shared references |
| [closures.vx](examples/closures.vx) | Closures, trailing blocks, functions returning functions, `filter` and `map` |
| [enums.vx](examples/enums.vx) | Enums with values, methods, `match` and `.case` shorthand |
| [optionals.vx](examples/optionals.vx) | `T?`, `nil`, `if let`, `guard let`, `?.` and `??` |
| [errors.vx](examples/errors.vx) | `throws`, `throw`, `try`, `catch`, `try?` and typed errors |
| [input.vx](examples/input.vx) | Reading from the keyboard with `input()` |

```sh
vex run examples/primes.vx
```

## Language tour

**Variables.** `let` values can't be reassigned, `var` values can. Types are inferred, or written after a colon. Outside functions, and as `static var` in a type, they hold shared state, set up on first use with one copy per isolate.

```swift
let name = "Vex"            // string
var count = 0               // int
let price: float = 9.99
let tags: [string] = []     // an empty list needs its type
count += 1

var visits = 0              // at the top level: shared by the whole program
struct Router { static var stack: [string] = [] }
```

**Types.** `int` and `float` are 64-bit; `bool`, `string`, lists `[T]`, structs and function types `fn(T) -> R` complete the set. Numbers never convert silently: write `float(count)` or `int(price)`. Text joins with interpolation: `"{count} items"`.

**Functions.** Parameters have types, `-> T` gives the result, and `=>` makes a one-liner. Arguments can be named and have defaults.

```swift
fn add(a: int, b: int) -> int {
    return a + b
}

fn square(x: int) -> int => x * x

fn greet(name: string, greeting: string = "Hi") {
    println("{greeting}, {name}!")
}

greet("Sam")
greet("Sam", greeting: "Welcome")
```

**Control flow.** No parentheses around conditions, and braces are always required.

```swift
if score >= 90 {
    println("A")
} else if score >= 75 {
    println("B")
} else {
    println("keep going")
}

let color = if selected { blue } else { gray }   // if gives a value; there is no ? :

for i in 0..5 { }           // 0 to 4
for i in 1..=5 { }          // 1 to 5
for fruit in fruits { }
for i, fruit in fruits { }  // with the index

while running {
    if done() { break }
}
```

**Lists.**

```swift
var nums = [3, 1, 2]
nums.push(4)
let last = nums.pop()
nums[0] = 10
println(nums, nums.len, nums.contains(2))   // [10, 1, 2] 3 true
```

**Maps.** Keys stay in the order they were added. Reading a key gives an optional, since it may be missing.

```swift
var ages = ["ann": 31, "bob": 25]
ages["cat"] = 40
println(ages["zoe"] ?? 0, ages.len)          // 0 3
for name, age in ages { println(name, age) }
```

**Collection `if`, `for` and spread.** As in Dart, literals can include elements conditionally, loop to produce them, and spread other collections.

```swift
let menu = [home, if loggedIn profile else login, for t in tabs Tab(t), ...extras]
let config = ["debug": false, if verbose "log": true, ...overrides]
```

**Strings.** `s.len` counts Unicode code points and `s.graphemeCount` the characters a reader sees. Also `.upper()` and `.lower()` (for every script), `.contains(x)`, `.split(sep)`, `.replace(old, with)`, `.trim()`, `.startsWith(x)`, `.endsWith(x)`, `.indexOf(x)` (an `int?`), `.substring(from, to)`, `.chars()` and `.lines()`.

**Structs.** Fields can have defaults, methods use fields directly, and `static fn` belongs to the type. Struct values are shared references.

```swift
struct User {
    name: string
    age: int = 18

    fn isAdult() -> bool => age >= 18
    fn birthday() { age += 1 }

    static fn guest() -> User => User(name: "Guest")
}

let ann = User(name: "Ann", age: 30)
ann.birthday()
println(ann)                 // User(name: "Ann", age: 31)
println(User.guest().age)    // 18
```

**Getters, setters and didSet.** Computed properties read like fields; `didSet` reacts to changes, which is what UI state needs.

```swift
struct Rect {
    width: float
    height: float
    get area -> float => width * height
}

struct Counter {
    count: int = 0 {
        didSet { println("{oldValue} -> {count}") }
    }
}
```

**Generics.** Functions, structs, enums and methods take type parameters, usually inferred. Each use compiles to its own specialised copy, so there's no runtime cost.

```swift
fn largest<T: Comparable>(xs: [T]) -> T? { ... }

struct Stack<T> {
    items: [T] = []
    fn push(item: T) { items.push(item) }
}

println(largest([4, 17, 9]), largest(["kiwi", "apple"]))   // 17 kiwi
let s = Stack<string>()

enum Result<T> {
    ok(value: T)
    err(message: string)
    fn map<U>(f: fn(T) -> U) -> Result<U> => match self {
        ok(v) => .ok(f(v))
        err(m) => .err(m)
    }
}

let r = Result.ok(5).map(fn(x) => "#{x}")   // Result<string>
let pick = first<string>                    // a generic function as a value
```

**Interfaces.** A struct or enum names the interfaces it implements, and the compiler checks it there. Methods with a body are defaults. Interfaces can be generic, extend others, and require properties.

```swift
interface Shape {
    fn area() -> float
    fn name() -> string => "shape"
}

struct Circle: Shape {
    radius: float
    fn area() -> float => 3.14 * radius * radius
}

let shapes: [Shape] = [Circle(radius: 1), Rect(width: 2, height: 3)]
for s in shapes { println(s.name(), s.area()) }
for s in shapes {
    if s is Circle { println(s.radius) }    // s is a Circle here
}
let rect = shapes[1] as? Rect                // Rect?

struct Point: Hashable { x: int; y: int }   // == compares fields; usable as a map key
let seen = [Point(x: 1, y: 2): "home"]
println(Point(x: 1, y: 2) == Point(x: 1, y: 2), seen[Point(x: 1, y: 2)] ?? "?")

interface Ordered<T> { fn compare(other: T) -> int }
interface Sized { get size -> int }

enum Suit: Sized {
    hearts
    spades
    get size -> int => 1
}

fn largest<T: Ordered<T>>(xs: [T]) -> T? { ... }
```

**Modules.** A folder is a module, and a project is a folder with a `package.toml`. Names are used through their module, and only those marked `pub` are visible outside it.

```swift
import "vex:math"
import "models"

fn main() {
    let cart = models.Cart(owner: "Ana")     // pub struct Cart in models/
    println(math.max(cart.total(), 10))
}
```

**C functions.** `extern fn` calls C directly: no header and no glue code. Strings and sized numbers convert on the way in and out, C pointers get their own types, closures can be passed as callbacks, and C can call `export fn` functions. A project can build its own C and Objective-C files.

```swift
extern fn getenv(name: string) -> string?
extern fn next() -> int as "counter_next"   // from native/counter.c

extern type View as "UIView"
extern fn on_tap(view: View, handler: fn())
extern fn set_alpha(view: View, alpha: float32)

on_tap(button, fn() => println("tapped"))
export fn vex_frame(t: float) -> int => render(t)
```

**Closures.** Functions without a name, written like `fn` declarations. They capture variables by reference, and a trailing block passes one as the last argument.

```swift
let double = fn(x: int) -> int => x * 2
let evens = nums.filter { x in x % 2 == 0 }
let names = people.map { it.name }

var taps = 0
let add = Button(title: "Add") { taps += 1 }   // onTap: fn()
add.onTap()                                    // taps is 1
```

**Builder blocks.** When the last parameter is a list, the trailing block builds it, one element per line, with `if`, `for` and `let` inside.

```swift
Column(spacing: 12) {                          // children: [View]
    Text("Hello")
    if loggedIn {
        Avatar(user)
    }
    for item in items {
        Row(item.name)
    }
}
```

**Enums and match.** Lowercase cases that can carry values, read with an exhaustive `match` whose patterns nest, take guards, ranges and alternatives, and work on records, structs and lists too.

```swift
enum Shape {
    circle(radius: float)
    rect(width: float, height: float)
    empty

    fn area() -> float => match self {
        circle(r) => 3.14 * r * r
        rect(w, h) => w * h
        empty => 0.0
    }
}

let s: Shape = .rect(width: 2, height: 3)
println(s.area())                              // 6.0
```

**Optionals.** `T?` holds a value or `nil`, and must be checked before use, so there are no null crashes.

```swift
fn findUser(id: int) -> User? { ... }

if let user = findUser(1) {
    println(user.name)
}
guard let user = findUser(2) else { return }
let boss = findUser(3)?.manager?.name ?? "nobody"
```

**Errors.** A function that can fail is marked `throws`, and every call to it needs `try`, so you can see each line that may fail. Errors are returned like values, with no slow unwinding.

```swift
fn parsePort(s: string) throws -> int {
    let n = int(s)
    if n < 1 || n > 65535 { throw Error("port {n} out of range") }
    return n
}

let port = try parsePort(text)                  // passes the error up
let safe = try parsePort(text) catch { 8080 }   // handles it here
let maybe = try? parsePort(text)                // int?: nil on error
```

**Type aliases.** `typedef` names a long type once, generic ones included.

```swift
typedef Handler = fn(Event) throws -> bool
typedef Cache<K: Hashable> = [K: Future<string>]

fn on(handler: Handler) { ... }
var images: Cache<string> = [:]
```

**Extensions.** Add methods and getters to any struct, enum or built-in type, from any module.

```swift
extension string {
    fn shout() -> string => upper() + "!"
}
extension<T: Comparable> [T] {
    fn largest() -> T? { ... }
}

println("hi".shout(), [3, 9, 2].largest())   // HI! 9
```

**Tear-offs.** Methods, static methods, enum cases and constructors are function values when named without a call.

```swift
let greetings = names.map(host.greet)   // bound to host
let users = names.map(User.new)
let shapes = radii.map(Shape.circle)
```

**Operators.** Structs define arithmetic, `==`, `<` and indexing; `!=`, `>`, `<=` and `>=` follow, and the struct works in `Comparable` code.

```swift
struct Vec {
    x: float
    y: float
    operator +(other: Vec) -> Vec => Vec(x: x + other.x, y: y + other.y)
    operator *(k: float) -> Vec => Vec(x: x * k, y: y * k)
}

let v = a + b * 2.0
```

**Multi-line and raw strings.** `"""` strings drop the indentation of their closing quotes, and `r"..."` keeps `\` and `{` as written.

```swift
let usage = """
    Usage: vex <command>
      run    build and run {name}
    """
let number = r"\d+\.\d+"
```

**Built-ins.** `print`, `println`, `input`, `int`, `float`, `str`, `panic` and `assert`.

**Error messages.** The compiler explains mistakes and suggests a fix:

```
error: can't use '+' with string and int
 --> main.vx:4:13
  |
4 |     println("Items: " + count)
  |             ^^^^^^^^^^^^^^^^^ put the value inside the string instead: "Items: {count}"
```

Mistakes that can only be found while running, such as an index out of range or division by zero, stop the program with the line:

```
error: index 3 is out of range for a list of length 3
 --> main.vx:7
```

## Running the tests

```sh
go test ./...
```

This includes golden tests: every program in [examples](examples) and [tests/run](tests/run) is compiled and run, and its output must match its `.out` file (with `.in` as keyboard input, if present). Each program also runs with the garbage collector forced to collect far more often than usual, to catch memory freed too early. Each program in [tests/errors](tests/errors) must fail with exactly the errors listed in its `// error:` comments. After an intended change to an example's output, refresh the `.out` files with `go test ./tests -update`.

Deeper checks, which CI runs on every pull request on Linux with clang and gcc (run them locally on macOS before merging):

```sh
# Feed random programs to the compiler: it must report errors, never crash.
go test ./tests -run '^$' -fuzz FuzzCompiler -fuzztime 5m

# Programs that pass the type checker must produce C that compiles.
go test ./tests -run '^$' -fuzz FuzzGeneratedC -fuzztime 5m

# Random valid programs whose exact output is known in advance.
go test ./tests -run TestGeneratedPrograms -programs 200

# Run every test program under AddressSanitizer and UndefinedBehaviorSanitizer.
ASAN_OPTIONS=detect_stack_use_after_return=0 \
VEX_CFLAGS="-fsanitize=address,undefined -fno-sanitize-recover=all" \
go test ./compiler/codegen ./tests
```

`VEX_CFLAGS` adds flags to every C compile, after the others, so they can override them. To debug or check one program, `--debug` and `--sanitize` on `vex build`, `vex run` and `vex test` are simpler; `vex help build` lists them.

To compare speed with Go, `bench/run.sh` builds the programs in [bench](bench) in both languages, checks that they agree and prints a table of times, sizes and memory; `go test ./...` keeps them compiling. See [bench/README.md](bench/README.md).

## Status

| Phase | Version | Goal | State |
|---|---|---|---|
| 1 | v0.1 | Core language: lexer, parser, type checker, C backend, CLI | Done |
| 2 | v0.2 | Enums and `match`, optionals, errors, closures, generics, interfaces, maps, modules, C interop, async/await and streams, isolates, records, patterns, constants, type aliases, multi-line and raw strings, extensions, tear-offs, operator overloading, builder blocks, properties, standard library | In progress: 80 of 89 tasks; next are the P1 items |
| 3 | v0.3 | UI core in the `vex-ui` repo: components, state, view diffing | Planned |
| 4 | v0.4 | iOS backend with UIKit widgets | Planned |
| 5 | v0.5 | Android backend with native views | Planned |
| 6 | v0.6 | Hot reload, language server, VS Code and JetBrains plugins, debugger, formatter | Planned |
| 7 | v0.7+ | Navigation, HTTP, storage, packages | Planned |
| 8 | v0.8 | Animation, custom drawing, shaders and 3D | Planned |

The detailed task list lives in [BACKLOG.md](BACKLOG.md).

## Project layout

```
cmd/vex/       the vex command
compiler/      lexer, parser, type checker, C code generator, runtime
examples/      sample programs with expected output
tests/         compiler tests, including programs that must fail
bench/         benchmarks in Vex and Go, and a script that compares them
docs/          language documentation
assets/brand/  logo files
bin/           built binaries (not tracked)
BACKLOG.md     roadmap and tasks
```

## Brand

The logo files are in [assets/brand](assets/brand):

| File | Use |
|---|---|
| `vex-lockup-light.svg` | Logo for light backgrounds |
| `vex-lockup-dark.svg` | Logo for dark backgrounds |
| `vex-mark.svg` | Mark only: icons, avatars, favicons |

Colours: Ocean Blue `#2563EB`, Sky `#38BDF8`, Deep `#1E3A8A`. The wordmark is set in Outfit ExtraLight.

## License

Vex is licensed under either of

- [Apache License, Version 2.0](LICENSE-APACHE)
- [MIT License](LICENSE-MIT)

at your option, with the [Runtime Library Exception](LICENSE-RUNTIME-EXCEPTION): programs compiled with Vex may be distributed without attribution, even though parts of the Vex runtime are built into them.

Unless you explicitly state otherwise, any contribution you intentionally submit for inclusion in Vex, as defined in the Apache-2.0 license, shall be dual licensed as above, without any additional terms or conditions.
