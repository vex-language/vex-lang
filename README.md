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

> **Status: early development.** The language design is in place and the compiler is being built.
> Nothing here is ready for real use yet. Commands and examples below show where the project is heading.

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

See the full language tour in [docs/SYNTAX.md](docs/SYNTAX.md).

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

> Vex is pre-release (v0.1 in progress). Programs compile and run; the language is still growing.

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

## Status

| Phase | Version | Goal | State |
|---|---|---|---|
| 1 | v0.1 | Core language: lexer, parser, type checker, C backend, CLI | In progress |
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
