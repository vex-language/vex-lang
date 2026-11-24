# Vex Backlog

Vex is a compiled, statically typed language for building truly native mobile apps:
Flutter-style tree syntax, real UIKit / Android widgets, tiny binaries, no VM or JS bridge.

Pipeline: `.vx` → Lexer → Parser → Type checker → C codegen → clang → native binary.
The compiler is written in Go. The approved syntax is in [docs/SYNTAX.md](docs/SYNTAX.md).

**Status:** `[ ]` todo · `[~]` in progress · `[x]` done
**Size:** S (≤ ½ day) · M (1–2 days) · L (3–5 days) · XL (1–2 weeks)

---

## Phase 1: Core language (v0.1)

Goal: `vex run hello.vx` compiles and runs native programs with structs, lists, methods and string interpolation.

### 1.1 Project setup
- [x] **V-001** Repo layout, `go.mod`, `.gitignore`, git init (S)
- [x] **V-002** `vex` CLI skeleton: `version`, `help`; `run`, `build`, `emit-c` added with V-045 (S)
- [x] **V-003** Error type with file:line:col, a source snippet, a caret and an optional hint (S)

### 1.2 Lexer
- [x] **V-010** Tokens: identifiers, keywords, int/float/string/bool literals, operators (M)
- [x] **V-011** Automatic statement end at newline (Go-style rules) (S)
- [x] **V-012** Comments `//`, `///` and `/* */`, string escapes, `1_000` digit separators (S)
- [x] **V-013** String interpolation tokens: `"Hi {name}, {count + 1}"` with nesting (M)
- [x] **V-014** Lexer unit tests (S)

### 1.3 Parser and AST
- [x] **V-020** AST node types for declarations, statements and expressions (S)
- [x] **V-021** Top level: `fn`, `struct` with fields, default values, methods and `static fn` (M)
- [x] **V-022** Functions: typed parameters, default values, `-> Type`, one-line `=> expr` bodies (M)
- [x] **V-023** Statements: `let`, `var`, assignment and `+= -= *= /= %=`, `if/else if/else` without parentheses, `while`, `return`, `break`, `continue` (M)
- [x] **V-024** Loops: `for x in list`, `for i, x in list`, `for i in a..b`, `for i in a..=b` (S)
- [x] **V-025** Expressions with a Pratt parser: precedence, unary, call, index, member access, list literals `[1, 2]` (M)
- [x] **V-026** Call syntax shared by functions, constructors and future widgets: positional plus named arguments `User(name: "Ann")`, trailing blocks `f(x) { ... }` (M)
- [x] **V-027** Helpful messages for common mistakes, e.g. `else` on a new line, `;` or `()` around `if` conditions (S)
- [x] **V-028** Parser unit tests (S)

### 1.4 Type checker
- [x] **V-030** Types: `int`, `float`, `bool`, `string`, `[T]` lists, structs (S)
- [x] **V-031** Scopes, name resolution, shadowing rules (M)
- [x] **V-032** Type inference for `let x = ...`; annotation for empty lists (`let xs: [int] = []`) (M)
- [x] **V-033** `let` (immutable) vs `var` (mutable) enforcement (S)
- [x] **V-034** Named and default arguments: matching, missing or unknown names, duplicates (M)
- [x] **V-035** Structs: constructor `User(...)` from fields, field defaults, methods with implicit `self` (fields used without `self.`), `static fn` called as `User.guest()` (L)
- [x] **V-036** Missing-return detection on all paths (S)
- [x] **V-037** Built-in functions: `print`, `println`, `input`, `panic`, `assert`, `int()`, `float()`, `str()` (S)
- [x] **V-038** Built-in members: lists `.len`, `.push()`, `.pop()`, `.contains()`; strings `.len`, `.upper()`, `.lower()`, `.contains()`, `.split()` (M)
- [x] **V-039** Clear type errors with hints, e.g. int + string suggests `"{count} items"`, int/float mix suggests `float()` (S)

### 1.5 C code generation and runtime
- [x] **V-040** C runtime: lists, strings, allocation, printing (M)
- [x] **V-041** Runtime safety: bounds checks, divide by zero, bad number parsing, empty `pop()`, all with file and line (S)
- [x] **V-042** Codegen: functions, defaults, named arguments, structs, methods, all statements and expressions (L)
- [x] **V-043** String interpolation codegen for every printable type (S)
- [x] **V-044** Printing lists and structs: `[1, 2]`, `User(name: "Ann", age: 30)` (S)
- [x] **V-045** Call clang with `-fwrapv -O2`; respect the `$CC` environment variable (S)

### 1.6 Tests, examples, docs
- [x] **V-050** Golden tests: `examples/*.vx` + `*.out`, run by `go test` (S)
- [x] **V-051** Error tests: `tests/errors/*.vx` with an expected message (S)
- [x] **V-052** Examples: hello, fibonacci, structs/methods, prime sieve, sorting, input (S)
- [x] **V-053** `README.md`: install, CLI, language tour (M)

**Phase 1 done when:** all examples run, all tests pass, and `vex build` produces a standalone binary.

---

## Phase 2: Language for apps (v0.2)

Goal: the language features real apps need before any UI work.

- [x] **V-100** Enums with data plus `match` (exhaustive) (L)
- [x] **V-101** Optionals `User?` with `if let`, `??` and `?.`; no null crashes (L)
- [ ] **V-102** Errors: `throws` functions, `try`, `catch` (L)
- [x] **V-103** Closures / lambdas, needed for UI event handlers (L)
- [ ] **V-104** Generics for functions and structs (XL)
- [ ] **V-105** Modules and imports (`import "ui"`), multi-file projects (L)
- [ ] **V-106** Maps `[string: int]` (M)
- [x] **V-107** Memory management: tracing GC, stage 1 mark and sweep ([design](docs/design/memory.md)) (XL)
- [x] **V-113** Hardening: fuzzing, sanitizers, Linux and macOS CI, feature-combination tests (M)
- [x] **V-114** Hardening 2: random program generator with known output, stack overflow errors, independent review (M)
- [ ] **V-115** Print values through one growing buffer; deeply nested values print in quadratic time today (S)
- [ ] **V-116** Unicode-aware strings: `upper`/`lower` beyond A–Z, and counting visible characters (grapheme clusters) (M)
- [ ] **V-111** GC stage 2: precise roots and object layouts (L)
- [ ] **V-112** GC stage 3: generational collection, returning memory to the OS (XL)
- [ ] **V-108** Standard library: strings, math, time, files (L)
- [ ] **V-109** JSON encode/decode built in, no codegen step (L)
- [ ] **V-110** C interop: call C functions directly (`extern fn`) (M)
- [ ] **V-117** Interfaces: a struct declares which interfaces it implements (L)
- [ ] **V-118** Builder blocks: several values in a trailing `{ }` become a list argument (M)
- [ ] **V-119** Property setters: code that runs when a field is assigned (M)
- [ ] **V-120** Integer overflow stops the program by default; explicit wrapping operators for hot code; benchmarked against Go (M)

---

## Phase 3: UI core (v0.3)

Goal: declarative UI that works and can be tested on the Mac before any phone.

Phases 3 to 5 are built in the separate `vex-ui` repo, created once Phase 2 is complete. The framework is plain Vex code: no UI keywords in the compiler ([architecture](docs/design/architecture.md)).

- [ ] **V-200** Design doc: component interface, state model, view tree, diffing (M)
- [ ] **V-201** `View` interface and `body()`, written as a Vex library on top of V-117 and V-118 (L)
- [ ] **V-202** Reactive state: `State<T>` whose setter schedules a re-render (V-119) (L)
- [ ] **V-203** C UI runtime: virtual view tree plus diff into minimal patch operations (XL)
- [ ] **V-204** Core widgets API: `Text`, `Button`, `Column`, `Row`, `Image`, `TextField`, `List`, `Spacer` (L)
- [ ] **V-205** Modifiers: `.size()`, `.color()`, `.padding()`, `.onTap {}` (M)
- [ ] **V-206** Layout model (flex-style) shared by both platforms (L)
- [ ] **V-207** Headless test backend: render the tree to text so UI can be unit-tested (M)

---

## Phase 4: iOS backend (v0.4)

Goal: a Vex app running on the iOS Simulator with real UIKit widgets.

- [ ] **V-300** Cross-compile the C output for `arm64-apple-ios` and the simulator (M)
- [ ] **V-301** Objective-C platform layer: map patch operations to `UILabel`, `UIButton`, `UIStackView`, … (XL)
- [ ] **V-302** Event path: UIKit tap → C runtime → handler → re-render (M)
- [ ] **V-303** `vex build --ios`: produce a `.app`, install on the simulator, launch (L)
- [ ] **V-304** Demo app: counter + todo list; measure app size and startup time (M)
- [ ] **V-305** Real device build, signing, `.ipa` (L)

---

## Phase 5: Android backend (v0.5)

Goal: the same Vex app running on Android with real Android views.

- [ ] **V-400** Cross-compile with the Android NDK into `libapp.so` (M)
- [ ] **V-401** Kotlin + JNI platform layer: patch operations to `TextView`, `Button`, `LinearLayout`, … (XL)
- [ ] **V-402** Event path: Android → JNI → C runtime (M)
- [ ] **V-403** `vex build --android`: build an APK with no Gradle setup for the user (L)
- [ ] **V-404** Same demo app on both platforms; compare size and startup with Flutter (M)

---

## Phase 6: Developer experience (v0.6)

- [ ] **V-500** Hot reload on the simulator: reload code as a dynamic library, keep state (XL)
- [ ] **V-501** VS Code extension: syntax highlighting, then an LSP (errors, go-to-definition, autocomplete) (L)
- [ ] **V-502** `vex fmt` code formatter (M)
- [ ] **V-503** `vex new myapp` project template (S)
- [ ] **V-504** Debugging: line info in the generated C (`#line`) so lldb shows `.vx` lines (M)

---

## Phase 7: App essentials (v0.7+)

- [ ] **V-600** Navigation and routing (L)
- [ ] **V-601** HTTP client with async/await or tasks (XL)
- [ ] **V-602** Local storage (key-value plus SQLite) (L)
- [ ] **V-603** Images (network and assets), fonts, theming, dark mode (L)
- [ ] **V-604** Direct native API calls from Vex (Objective-C / JNI) without bridges (XL)
- [ ] **V-605** Package manager (`vex get`) (XL)
- [ ] **V-606** Accessibility checks (L)

---

## Ideas / later
- Self-hosting: rewrite the Vex compiler in Vex
- LLVM or custom backend instead of C
- Desktop (macOS) and web targets
- Benchmark page: Vex vs Flutter vs React Native (size, startup, memory)

## Decisions log
| Date | Decision |
|---|---|
| 2026-11-24 | Compiler in Go; output C, then clang to native |
| 2026-11-24 | Static types with inference; `let` / `var`; newline ends a statement |
| 2026-11-24 | Mobile UI is the long-term focus: tree syntax + real native widgets |
| 2026-11-24 | iOS first, then Android |
| 2026-11-24 | v0.1 has no GC (memory is leaked); revisit in V-107 |
| 2026-11-24 | Developer-friendly syntax approved (see docs/SYNTAX.md): `User(name:)` constructors, methods inside structs with implicit `self`, `static fn`, named/default arguments, `=>` one-liners, `"{expr}"` interpolation, `[T]` list type, member calls like `nums.push()` |
| 2026-11-24 | Repo: github.com/irshadio/vex-lang (private) |
| 2026-11-24 | Structs are shared references; `let` fixes the name, not the fields |
| 2026-11-24 | No implicit int/float mixing; whole-number literals adapt to `float` where one is expected |
| 2026-11-24 | `==` only on `int`, `float`, `bool`, `string` for v0.1 |
| 2026-11-24 | Generated C uses GNU statement expressions (clang and gcc); arguments run left to right in source order |
| 2026-11-24 | Runtime errors exit with code 1 and print `file:line` |
| 2026-11-24 | Closures use `fn` syntax (`fn(x: int) => x * 2`); function types `fn(int) -> int`; parameter types inferred from context |
| 2026-11-24 | Closures capture by reference; captured `var`s move to the heap; each loop iteration has its own variable |
| 2026-11-24 | A trailing block is a `fn()` closure passed as the last argument |
| 2026-11-24 | Enum cases are lowercase and carry named values: `circle(radius: float)`; `.case` when the enum is known |
| 2026-11-24 | `match` works on enums, `int`, `bool` and `string`; it must be exhaustive or end with `_`; bindings are positional |
| 2026-11-24 | Enum values are immutable references; `==` only on enums whose cases carry no values |
| 2026-11-24 | Optionals: `T?`, `nil`, `if let`, `guard let ... else`, `?.` and `??`; no force unwrap |
| 2026-11-24 | Each step after an optional needs its own `?.` (`a?.b?.c`); optionals compare only with `nil` |
| 2026-11-24 | Speed is the top goal: runtime and allocator changes are benchmarked against the same program in Go |
| 2026-11-24 | Memory: tracing GC (not reference counting), so closures capturing `self` need no `weak`; stage 1 is conservative mark and sweep with size-class pages |
| 2026-11-24 | Harden before building more: every PR runs fuzzers, sanitizers and Linux/macOS CI |
| 2026-11-24 | Warnings (which still build) for habits from C-style languages: `;` at line ends, parentheses around conditions |
| 2026-11-24 | A loop over a list visits the items present when it started (like Go); removing items ends it early |
| 2026-11-25 | Integer overflow will stop the program by default, with explicit wrapping operators for hot code (V-120) |
| 2026-11-25 | Two projects, like Dart and Flutter: the language in `vex-lang`, the UI framework in a separate `vex-ui` repo created once the language part is complete |
| 2026-11-25 | No UI keywords in the compiler: `component` / `state` / `view` are dropped; the framework is built on interfaces, generics, builder blocks and property setters ([architecture](docs/design/architecture.md)) |
