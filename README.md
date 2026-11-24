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
component Counter {
    state count = 0

    view {
        Column(spacing: 12) {
            Text("Count: {count}").size(24)
            Button("Add") { count += 1 }
        }
    }
}

fn main() {
    app(Counter())
}
```

When `count` changes, Vex rebuilds the view, compares it with the previous one, and updates only the native widgets that changed.

## How it works

```
 main.vx ─► Lexer ─► Parser ─► Type checker ─► C code ─► clang ─► native binary
```

The compiler is written in Go. It turns Vex source into portable C, then uses the system C compiler to produce a native executable. Because the output is C, the same pipeline can target macOS, iOS (`arm64-apple-ios`) and Android (NDK).

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
git clone https://github.com/irshadio/vex-lang.git
cd vex-lang
./build.sh
```

This builds `bin/vex`. To run `vex` from anywhere, add the folder to your `PATH` (for zsh):

```sh
echo 'export PATH="'"$PWD"'/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

**Usage**

```sh
vex check hello.vx          # check for errors without building
vex run hello.vx            # compile and run
vex build hello.vx -o hello # build a standalone native binary
vex emit-c hello.vx         # print the generated C
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
| [input.vx](examples/input.vx) | Reading from the keyboard with `input()` |

```sh
vex run examples/primes.vx
```

## Language tour

**Variables.** `let` values can't be reassigned, `var` values can. Types are inferred, or written after a colon.

```swift
let name = "Vex"            // string
var count = 0               // int
let price: float = 9.99
let tags: [string] = []     // an empty list needs its type
count += 1
```

**Types.** `int` and `float` are 64-bit; `bool`, `string`, lists `[T]` and structs complete the set. Numbers never convert silently: write `float(count)` or `int(price)`. Text joins with interpolation: `"{count} items"`.

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

**Strings.** `s.len` counts characters. Also `.upper()`, `.lower()`, `.contains(x)` and `.split(sep)`.

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

**Built-ins.** `print`, `println`, `input`, `int`, `float`, `str`, `panic` and `assert`.

**Errors.** The compiler explains mistakes and suggests a fix:

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

This includes golden tests: every program in [examples](examples) is compiled and run, and its output must match its `.out` file (with `.in` as keyboard input, if present). Each program in [tests/errors](tests/errors) must fail with exactly the errors listed in its `// error:` comments. After an intended change to an example's output, refresh the `.out` files with `go test ./tests -update`.

## Status

| Phase | Version | Goal | State |
|---|---|---|---|
| 1 | v0.1 | Core language: lexer, parser, type checker, C backend, CLI | Done |
| 2 | v0.2 | Enums and `match`, optionals, errors, closures, generics, modules | Planned |
| 3 | v0.3 | UI core: `component`, `state`, `view`, view diffing | Planned |
| 4 | v0.4 | iOS backend with UIKit widgets | Planned |
| 5 | v0.5 | Android backend with native views | Planned |
| 6 | v0.6 | Hot reload, VS Code extension, formatter | Planned |
| 7 | v0.7+ | Navigation, HTTP, storage, packages | Planned |

The detailed task list lives in [BACKLOG.md](BACKLOG.md).

## Project layout

```
cmd/vex/       the vex command
compiler/      lexer, parser, type checker, C code generator, runtime
examples/      sample programs with expected output
tests/         compiler tests, including programs that must fail
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
