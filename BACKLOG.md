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

**Progress:** 87 of 89 tasks done. The language has enums and `match`, optionals, errors, closures, generics (functions, structs, enums and methods), interfaces (generic, extending others, with properties, static requirements, `Self`, generic methods and extensions, for structs and enums), maps, modules, C interop (handles, sized numbers, callbacks, exports, Objective-C), async/await with an event loop (async closures, function values and interface methods, cancellation, timeouts and streams included), isolates and channels, records, patterns (struct, list and record patterns, `when` guards, `if case`, checked for exhaustiveness and unreachable arms), constants (`const`, `static const`, frozen collections), type aliases (`typedef`, generic ones included), multi-line and raw strings, extensions (methods, getters and static members for structs, enums and built-in types), tear-offs (methods, static methods, cases and constructors as values), operator overloading for structs, bitwise operators, safe number parsing, list sorting and searching, `if` as a value, type tests with smart casts, derived equality and hashing with struct and record map keys, top-level and static variables, platform conditionals, tests with `vex test`, debug and sanitizer builds, warnings for unused variables and unreachable code, collection `if`/`for`/spread, builder blocks, trailing blocks with parameters (`xs.map { x in x * 2 }`, `it`), custom printing with `toString()`, local functions, comma conditions with `if let x` shorthand, `guard cond else` and `?? return`, `defer`, named loops and `repeat { } while`, ranges as values with `step`, `reversed` and slicing (`xs[1..3]`, `s[2..]`), `Iterable<T>` with generators and lazy `map`/`filter`/`take`, `let` and `private(set)` fields with `init` blocks and `copy`, getters, setters and `didSet`, and a first standard library (`vex:math`, `vex:strings`, `vex:time`, `vex:fs`, `vex:os`, and `vex:json` with `Codable` types). A full QA pass has been fixed: compiler crashes and hangs, generated C that failed to compile, wrong runtime behaviour, valid code the parser and type checker rejected, and unclear diagnostics. A second QA pass compared the language with Dart 3, Swift and Kotlin; its gaps are V-150 to V-178. Next come the features the `vex-ui` framework is built on (V-150 to V-159; bitwise operators, safe parsing, list essentials, conditional values, type tests, equality, shared state, async values, cancellation, platform conditionals, `vex test`, build modes, warnings and streams are done), then the features developers expect from a modern language. Each new piece of syntax is approved before it is built.

### Done
- [x] **V-100** Enums with data plus `match` (exhaustive) (L)
- [x] **V-101** Optionals `User?` with `if let`, `??` and `?.`; no null crashes (L)
- [x] **V-102** Errors: `throws` / `throws(E)`, `throw`, `try`, `catch`, `try?` (L)
- [x] **V-103** Closures / lambdas, needed for UI event handlers (L)
- [x] **V-104** Generics for functions and structs (XL)
- [x] **V-106** Maps `[string: int]` (M)
- [x] **V-117** Interfaces: a struct declares which interfaces it implements (L)
- [x] **V-105** Modules: a folder is a module; `import "vex:math"`, `import "models"`, `as`; `pub` visibility; `package.toml`; first `vex:math` and `vex:strings` (L)
- [x] **V-107** Memory management: tracing GC, stage 1 mark and sweep ([design](docs/design/memory.md)) (XL)
- [x] **V-113** Hardening: fuzzing, sanitizers, Linux and macOS CI, feature-combination tests (M)
- [x] **V-114** Hardening 2: random program generator with known output, stack overflow errors, independent review (M)
- [x] **V-120** Integer overflow stops the program by default; `&+` `&-` `&*` wrap; benchmarked against Go (M)
- [x] **V-110** C interop: `extern fn` with `as "c_name"`, string conversion, `[c]` sources, libs and flags in `package.toml` (M)
- [x] **V-127** Standard library: float math in `vex:math` through C (`sqrt`, `sin`, …) and `vex:time` (M)
- [x] **V-129** Standard library: files, processes and environment (`vex:fs`, `vex:os`); `vex run` passes arguments (M)
- [x] **V-131** Strings: `replace`, `trim`, `trimStart`, `trimEnd`, `startsWith`, `endsWith`, `indexOf`, `substring`, `chars`, `lines` (M)
- [x] **V-108** Standard library: strings, math, time and files, delivered by V-127, V-129 and V-131 (L)
- [x] **V-118** Builder blocks: a trailing `{ }` for a list parameter builds the list, one element per line, with `let`, `if`, `for` and `...`; `Column { }` without `()` (M)
- [x] **V-133** Collection `if`, `for` and spread `...`/`...?` inside list and map literals, as in Dart (M)
- [x] **V-119** Setters `set name(v: T)` and `didSet` blocks on fields with `oldValue` (M)
- [x] **V-134** Getters `get name -> T`, read like fields (S)
- [x] **V-125** Generics: generic enums such as `Result<T>`, methods with their own type parameters, generic functions as values (`first<int>` or from the expected type) (M)
- [x] **V-126** Interfaces: generic interfaces such as `Ordered<T>`, interfaces that extend others, enums that implement interfaces, getters and setters in interfaces, met by accessors or fields (M)
- [x] **V-130** C interop: sized numbers (`int32`, `uint8`, `float32`...) in extern signatures, `extern type` pointer handles, `fn` callbacks with a kept context, `export fn`, Objective-C `.m` sources and Apple frameworks (M)

### Needed by the UI framework (Phase 3 starts after these)
- [x] **V-135** `async`/`await` with an event loop: `async fn`, `Future<T>`, `await`, `all`/`any`, `run`, `time.sleep`, and `vex_poll` for platform run loops; each task runs on its own stack (XL)
- [x] **V-148** Streams: `Stream<T>`, `async fn ... -> Stream<T>` with `yield`, `for await`, and `StreamController<T>`, for UI events and HTTP bodies (L)
- [x] **V-147** Isolates: `isolate.run` and `isolate.spawn` run code in parallel on threads with their own heaps; `Channel<T>` carries deep-copied messages between them; `while let` (XL)
- [x] **V-150** Bitwise operators `& | ^ ~ << >>` and `&= |= ^= <<= >>=` on `int`, with `&` binding tighter than `==`; shifts outside 0–63 stop the program (M)
- [x] **V-151** List essentials: `sort()`, `sort(by:)`, `sorted`, `reversed`, `insert(x, at:)`, `remove(at:)`, `removeWhere`, `indexOf`, `first`, `last`, `isEmpty`, `find`, `any`, `all`, `reduce`, `join`, `flatMap`, `clear`, `min`, `max`, `sum`, `slice`; sorting is stable (M)
- [x] **V-152** Safe parsing and number basics: `int.parse(s)` and `float.parse(s)` returning optionals, `int.parse(s, radix: 16)`, `int.max`/`int.min`, `float.infinity`/`float.nan`, `isNaN`/`isFinite`, `toFixed`, `str(n, radix:)` (S)
- [x] **V-153** Conditional values: `if … else …` as an expression, with `else` required (S)
- [x] **V-154** Type tests on interface values: `x is T`, `x as? T`, and type patterns in `match` and `if case` for any interface; shares the pattern work with V-144 (M)
- [x] **V-155** Equality and hashing: synthesised `Equatable`/`Hashable` for structs and enums, hand-written `hash()`, structs, records and enums as map keys, `==` on lists and maps of Equatable values, `===` for identity (L)
- [x] **V-156** Top-level and `static` `var`/`let`, initialised lazily, one copy per isolate (M)
- [x] **V-157** Async everywhere: async closures, async function values, async interface methods and async tear-offs; extends the closure part of V-148 (L)
- [x] **V-158** Cancellation and timeouts: `future.cancel()`, cancellation reaching `await` points as an error, `time.timeout(future, ms)`, task groups that cancel their children (L)
- [x] **V-159** Platform conditionals: `os.target` and `os.arch` as compile-time constants whose dead branches are skipped, and per-target `[c]` sources in `package.toml` (M)

### Modern language features
- [x] **V-136** Records in the Dart 3 style: `(int, string)`, `({int x, int y})`, `.$1` and named fields, value equality, and `let`/`var`/`for` destructuring (M)
- [x] **V-137** Destructuring and richer patterns: `let (a, b) = pair`, struct and list patterns, guards in `match` arms, ranges, `if case` (L)
- [x] **V-138** Constants: top-level `const` values computed at compile time (S)
- [x] **V-139** Extensions: add methods to existing types, built-in ones included (M)
- [x] **V-140** Type aliases for long types (S)
- [x] **V-141** Multi-line and raw strings (S)
- [x] **V-142** Operator overloading for structs: arithmetic, `==`, ordering and indexing (M)
- [x] **V-143** Tear-offs: methods and constructors as values, as in `names.map(user.greet)` (S)
- [x] **V-144** Sealed interfaces: a closed set of structs that `match` checks exhaustively, with type patterns (M)
- [x] **V-145** Null-aware elements in collections and `?[ ]` indexing (S)
- [x] **V-146** Extension types: zero-cost wrappers that make `UserId` and `int` different types (M)
- [x] **V-160** Trailing blocks with parameters: `xs.map { x in x * 2 }`, and an implicit name for a single parameter (M)
- [x] **V-161** Fields: `let` fields set once at creation, field types inferred from defaults, `init` blocks for validation, a generated `copy(field:)`, fields readable everywhere but set only inside the type (L)
- [x] **V-162** Optional ergonomics: `if let x` shorthand, comma-separated conditions in `if`/`while`/`guard`, `guard cond else`, and `?? return`/`throw`/`break`/`continue` (M)
- [x] **V-163** Control flow: `defer`, labelled `break`/`continue`, `repeat { } while` (M)
- [x] **V-164** Ranges as values: a `Range` type with `contains`, `step` and `reversed`, and list slicing `xs[1..3]` (M)
- [x] **V-165** Iteration: an `Iterable<T>` interface that `for in` accepts, and generators with `yield`; shares machinery with V-148 (L)
- [x] **V-166** Enum reflection: `Type.cases`, `.name`, raw values `enum C: int { red = 1 }` with `C(rawValue:)` returning an optional, `==` on enums with data, operators on enums (M)
- [x] **V-167** Interfaces: static requirements, `Self`, generic methods and extensions of interfaces; needed for typed JSON (V-109) (L)
- [x] **V-168** Custom printing: a `toString()` method used by `println`, `str` and interpolation (S)
- [x] **V-169** Local named functions, recursive ones included (S)
- [x] **V-170** List conversion between element types: `[B]` passed as `[A]` for a fresh copy, and `cast<A>()` (S)

### Tools and packages
- [x] **V-121** Package manager: `vex get` from public git repos, `package.lock` with hashes ([design](docs/design/packages.md)) (L)
- [x] **V-122** `vex doc`: API docs from `///` comments (M)
- [x] **V-124** Benchmark suite in the repo (`bench/`): Vex programs with Go equivalents and a script that compares them (M)
- [x] **V-123** Stack traces: runtime errors list the chain of calls with their lines (M)
- [x] **V-128** Modules, next steps: warn about unused imports, `pub` names whose types are private, and errors sorted with the root module first (S)
- [x] **V-176** `vex test`: test blocks or `_test.vx` files, filtering by name, and failures that show both values (M)
- [x] **V-177** Build modes: `--debug` (`-O0 -g`, keeps the C) and `--sanitize=address,undefined` alongside today's `-O3`, and `$VEX_CFLAGS` documented in `vex help build` (S)
- [x] **V-178** Warnings for unused local variables and unreachable code, and diagnostics that name unsupported features (`is`, labels, `defer`, `x!`) (S)
- [x] **V-179** Docs fixes: stale lines about properties, tear-offs and `==` in SYNTAX.md, typed fields in the UI sketch, and the conditional-value, overloading and inheritance decisions in the log (S)

### Runtime and libraries
- [x] **V-109** JSON encode/decode built in, no codegen step (L)
- [x] **V-132** Bytes: a type for binary data, and reading and writing binary files (M)
- [x] **V-115** Print values through one growing buffer; deeply nested values print in quadratic time today (S)
- [x] **V-116** Unicode-aware strings: `upper`/`lower` beyond A–Z, and counting visible characters (grapheme clusters) (M)
- [x] **V-111** GC stage 2: precise roots and object layouts (L)
- [ ] **V-112** GC stage 3: generational collection (done), returning memory to the OS (XL)
- [x] **V-149** Leave out the overflow and bounds checks the compiler proves can't fail, and the capacity check of presized list fills (M)
- [x] **V-171** `Set<T>` of Hashable values, with literals, `insert`, `remove`, `contains`, `union`, `intersection` and `subtracting` (M)
- [x] **V-172** Map helpers: `isEmpty`, `getOrPut`, `update`, `filter`, `mapValues`, `entries` (S)
- [x] **V-173** String helpers: `isEmpty`, `lastIndexOf`, `count`, a `split` limit, `padStart`/`padEnd`, `reversed`, `capitalize`, and `vex:strings` functions as methods (S)
- [x] **V-174** Format specs in interpolation: `"{x:.2}"`, `"{n:04}"`, `"{n:x}"` (M)
- [x] **V-175** Standard library: `vex:random`, `vex:datetime` (`now`, `Duration`, ISO 8601, calendar fields), `eprintln` and `os.stderr`, and `vex:regex` (L)
- [x] **V-176** `vex:crypto`: SHA-2, HMAC, HKDF, secure random bytes, UUID v4, constant-time compare, Argon2id and PBKDF2, `seal`/`open` with ChaCha20-Poly1305 or AES-256-GCM, Ed25519 and P-256 signatures. Only audited code (Monocypher) or each system's provider (CryptoKit, Conscrypt, CNG, libcrypto), never our own primitives; keys are their own types; MD5 and SHA-1 only under `legacy`; RFC and Wycheproof test vectors in CI on every system (L)

### Windows

Goal: the compiler, `vex` and programs built with it run on Windows 10 and 11 (x86-64 and arm64), using clang from LLVM-MinGW so no Visual Studio is needed.

- [x] **V-180** `vex` on Windows: paths, temporary folders, finding clang, `.exe` outputs, `run`, `build`, `test`, `doc` and `get`, and CRLF source files (M)
- [x] **V-181** Runtime threads on Windows: isolates, channels and thread-local storage on Win32 (M)
- [x] **V-182** Async tasks and generators on Windows: switching stacks allocated with `VirtualAlloc`, with guard pages (L)
- [x] **V-183** Collector on Windows: heap memory from `VirtualAlloc`, thread stack bounds and saved registers for root scanning (M)
- [x] **V-184** Standard library on Windows: `vex:fs`, `vex:os` (arguments, environment, exit codes, console UTF-8), time, `vex:datetime` local offsets, and `BCryptGenRandom` for random seeds (M)
- [x] **V-185** Windows CI: every PR runs the suite and the sanitizer-free battery on `windows-latest` (S)
- [ ] **V-186** SDK archives for Windows, Linux and macOS: `vex`, the standard library, runtime, docs and the C toolchain wherever its license allows (LLVM-MinGW on Windows, clang with a musl sysroot on Linux; macOS uses the Xcode Command Line Tools). The layout is relocatable, finding everything next to the `vex` binary, so the UI framework's archive can embed it as Flutter's embeds Dart. Release CI builds, signs and checksums them, and tests each archive on a clean machine; a `winget` package points at the Windows one (M)
- [x] **V-187** `vex doctor`: checks the toolchain and, for app builds, Xcode and Android Studio with its SDK and NDK, saying what is missing and how to install it; it never installs those itself (S)

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

Goal: editor support and a debugger on par with Flutter's. Tools that inspect running UI apps, such as DevTools, belong to the UI framework in the `vex-ui` repo.

### Language tools (this repo)

- [ ] **V-500** Hot reload on the simulator: reload code as a dynamic library, keep state (XL)
- [ ] **V-501** Language server, `vex lsp`: errors as you type, hover, go-to-definition, find references, rename, autocomplete, signature help and quick fixes from the compiler's hints, for every editor (L)
- [ ] **V-502** `vex fmt` code formatter (M)
- [ ] **V-503** `vex new myapp` project template (S)
- [ ] **V-505** `vex repl`: type a line, see its result (compiled behind the scenes) (M)
- [ ] **V-504** Debugging: line info in the generated C (`#line`) so lldb shows `.vx` lines (M)
- [ ] **V-506** Debug adapter, `vex dap`: breakpoints, stepping, call stacks and variables shown as Vex values over lldb, for any editor that speaks the Debug Adapter Protocol (L)

### Editor plugins

- [ ] **V-510** VS Code extension: highlighting, snippets, the language server, formatting on save, run and debug buttons, tasks for `vex build`, `run` and `test`, and a test explorer (L)
- [ ] **V-511** JetBrains plugin for IntelliJ IDEA, Android Studio, CLion and the other JetBrains IDEs: highlighting, live templates, the language server, run configurations, the debugger and a test runner (XL)
- [ ] **V-512** Snippets shared by both plugins: `struct`, `enum`, `interface`, `match`, `test`, `async fn` and the like (S)
- [ ] **V-513** Device picker in both plugins: choose the Mac, a simulator, an emulator or a phone, then run and hot reload from the editor (M)
- [ ] **V-514** Publish the plugins to the VS Code Marketplace, Open VSX and the JetBrains Marketplace (S)

---

## Phase 7: App essentials (v0.7+)

- [ ] **V-600** Navigation and routing (L)
- [ ] **V-601** HTTP client with async/await or tasks (XL)
- [ ] **V-602** Local storage (key-value plus SQLite) (L)
- [ ] **V-603** Images (network and assets), fonts, theming, dark mode (L)
- [ ] **V-604** Direct native API calls from Vex (Objective-C / JNI) without bridges (XL)
- [ ] **V-605** Package website: searchable index of packages in public git repos, with docs and scores (XL)
- [ ] **V-607** Package cache and public checksum log (L)
- [ ] **V-606** Accessibility checks (L)

---

## Phase 8: Graphics and animation (v0.8)

Goal: what Flutter apps do beyond standard widgets (animation, custom drawing, shaders and 3D) next to the native widgets. Drawing goes to each platform's own graphics APIs (Core Animation, Core Graphics and Metal on iOS; the Android animators, Canvas and Vulkan or OpenGL ES on Android) instead of a bundled engine, so apps stay small.

### Animation

- [ ] **V-700** Design doc: implicit and explicit animations, the frame clock, and how animations drive `State` (M)
- [ ] **V-701** Frame clock: `CADisplayLink` and `Choreographer` tick a `frames()` stream at the display's rate, 60 to 120 Hz (M)
- [ ] **V-702** Implicit animations: `.animate(duration:curve:)` on opacity, position, size, scale, rotation and colour, run by the platform's own animators (L)
- [ ] **V-703** Explicit animations: `AnimationController`, tweens, curves, springs, sequences and repeats, in Vex on the frame clock (L)
- [ ] **V-704** Transitions: animated insert and removal of views, page transitions for navigation (V-600), shared-element (hero) transitions (L)
- [ ] **V-705** Gesture-driven animation: drag, swipe, pinch and rotate with velocity, flings and springs (L)
- [ ] **V-706** Vector animations: play Lottie files natively (L)

### Custom drawing

- [ ] **V-710** `Canvas` widget, like `CustomPaint`: paths, shapes, arcs, text, images, gradients, clipping and transforms, replayed on Core Graphics and Android Canvas (XL)
- [ ] **V-711** Canvas repaints only when its inputs change; hit testing for taps on drawn shapes (M)
- [ ] **V-712** Headless canvas backend that renders to PNG, so drawings can be tested on the Mac (M)

### Shaders and effects

- [ ] **V-720** Fragment shaders: one shader language compiled to Metal and to SPIR-V or GLSL, used as canvas paints and view effects (XL)
- [ ] **V-721** View effects: blur, shadows, colour filters, blend modes and masks (M)
- [ ] **V-722** GPU canvas on Metal and Vulkan for scenes with many shapes, such as charts and 2D games (XL)

### 3D

- [ ] **V-730** 3D view: scene, camera, lights, meshes and materials on Metal and Vulkan or OpenGL ES (XL)
- [ ] **V-731** Models: load glTF with textures and skeletal animation (L)
- [ ] **V-732** 3D interaction: orbit, pan and zoom gestures, and picking objects by tap (M)

### Tools

- [ ] **V-740** Performance overlay: frame times, dropped frames and GPU time, like Flutter's (M)
- [ ] **V-741** Demo gallery app: animations, canvas, shaders and 3D on both platforms, compared with Flutter for size and frame rate (M)

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
| 2026-11-24 | Repo: github.com/vex-language/vex-lang (private) |
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
| 2026-11-25 | Integer overflow stops the program by default; `&+`, `&-`, `&*` (and `&+=`, `&-=`, `&*=`) wrap around, as in Swift (V-120) |
| 2026-11-25 | Errors: `throws` before `->`; plain `throws` uses the built-in `Error(message)`, `throws(E)` names an enum; every throwing call needs `try`; handled with `try`, `try ... catch e { }` (last line gives the value) or `try?`; errors are returned through a hidden argument, not by unwinding |
| 2026-11-25 | Two projects, like Dart and Flutter: the language in `vex-lang`, the UI framework in a separate `vex-ui` repo created once the language part is complete |
| 2026-11-25 | No UI keywords in the compiler: `component` / `state` / `view` are dropped; the framework is built on interfaces, generics, builder blocks and property setters ([architecture](docs/design/architecture.md)) |
| 2026-11-25 | Packages are public git repos with version tags, fetched directly by `vex get`; a website indexes them like pub.dev but stores no code ([packages](docs/design/packages.md)) |
| 2026-11-25 | Maps: `[K: V]` types and `["a": 1]` / `[:]` literals; `m[k]` gives an optional; loops follow insertion order; keys are int, string, bool or simple enums; values can't be optional (V-106) |
| 2026-11-25 | Generics: `<T>` type parameters on functions and structs, inferred from arguments or the expected type, or written as `f<int>(...)` touching the name; constraints `<T: Comparable & Hashable>` from built-in Equatable, Comparable and Hashable (interfaces join them in V-117); compiled by specialising each use, checked once generically (V-104) |
| 2026-11-25 | Interfaces: `interface I { fn m() }`, methods with a body are defaults; structs opt in with `struct S: I, J` and are checked there; `[I]`, `I` and `I?` hold any implementing struct, called through a per-struct method table (thunks keep each call type-exact); interfaces also work as generic constraints `<T: I>` (V-117) |
| 2026-11-25 | Imports name packages, as in Dart: `vex:name` for the standard library, `package:name` for dependencies, a relative folder for the project's own code; a folder is one module; `package.toml` maps each package name to a git URL and version, so no domain is needed ([packages](docs/design/packages.md)) |
| 2026-11-25 | The project file is `package.toml`, with `package.lock` beside it, matching `package:` imports (replaces `vex.toml`) |
| 2026-11-25 | Modules: a folder is a module; names are private unless marked `pub` and are used as `module.name`, with `as` to rename; imports belong to each file; modules may import each other; C names of other modules get a tag starting with a digit, so they can't collide |
| 2026-11-25 | `vex:math` leaves out float functions such as `sqrt` until standard library modules can call native code: Newton's method in Vex can't round the last digit correctly |
| 2026-11-25 | C interop: `extern fn` without a body, `as "c_name"` for another C name; strings cross as NUL-terminated `const char *` copies; a project's C code goes in a `[c]` section of `package.toml`. Externs are called through an assembly-label alias, so their declarations can't clash with system headers, and well-known math functions use the C compiler's built-ins |
| 2026-11-25 | `vex:fs` and `vex:os` are Vex wrappers over small C helpers in the runtime; failures throw `Error`s naming the path and the reason. Files are text for now; binary data waits for a bytes type |
| 2026-11-25 | String indexes (`indexOf`, `substring`) count characters, like `len`; `indexOf` returns `int?` rather than -1; substrings share the original text |
| 2026-11-25 | New features follow modern languages, Dart 3 first (records, patterns, collection `if`/`for`, extensions, sealed types, async), so Phase 2 gains V-133 to V-146; each still needs its syntax approved |
| 2026-11-25 | Collection `if`/`for`/`...`/`...?` in literals follow Dart, without braces; a `(`, `[` or `-` after a space in their header starts the element. Builder blocks: a trailing block for a list parameter builds the list (decided by the parameter type, no annotation); lines may be elements, `let`/`var`, `if`, `for` and `...`, and anything else is an error. `Name { }` is a call with only a trailing block. Both compile to one list filled in place (V-118, V-133) |
| 2026-11-25 | Properties follow Dart: `get name -> T` and `set name(v: T)`, with Swift's `didSet { }` after a field (`oldValue` in scope; not run on creation or inside its own block). `get`, `set` and `didSet` are contextual, not keywords. Accessors compile to plain C functions that the C compiler inlines (V-119, V-134) |
| 2026-11-25 | Concurrency will use Dart-style isolates (separate heaps, message passing), which keeps the garbage collector free of locks; planned as V-147 next to async/await (V-135) |
| 2026-11-25 | Generic enums take their type arguments from case values or the expected type, else `Result<int>.err(...)`; generic methods declare `<U>` like functions and are instantiated per receiver type; generic functions become values from the expected function type or as `first<int>`. A `>` closing type arguments that touch a name ends a line like a name does (V-125) |
| 2026-11-25 | Interfaces use the same forms as structs: `interface Store<T>`, `interface Shape: Drawable`, `enum Suit: Drawable`. `get`/`set` in an interface is met by an accessor or a stored field. An interface value is still a plain pointer: implementers carry tables for every interface they implement, including extended ones, so using a `Shape` as a `Drawable` costs nothing (V-126) |
| 2026-11-25 | C interop: sized numbers exist only in extern and export signatures and convert to `int`/`float` at the call, so Vex code keeps one int and one float. `extern type` gives typed opaque pointers. A `fn` parameter becomes a C function pointer plus a `void *` context, kept as a GC root until C calls `vex_release`. `export fn` gives C a function under its own name. `.m` sources compile with ARC (V-130) |
| 2026-11-25 | Async: `async fn` results are plain types, and a call without `await` gives a `Future<T>` (or `Future<T, E>`), as in Dart. Each task runs on its own stack, switched by a small assembly routine for arm64 and x86-64, so functions aren't rewritten into state machines and code that doesn't await stays as fast as before. The loop is built in: it runs after `main`, and `vex_poll` lets platform run loops drive it. Streams come later as their own step (V-135) |
| 2026-11-25 | Isolates: each is a thread with its own heap, collector and loop, so the runtime's state is thread-local and needs no locks. The heap's fields share one thread-local struct, so an allocation looks up thread-local storage once; on macOS that keeps the cost to about 4% in allocation-heavy code. Values are copied as messages, keeping sharing and cycles, and the compiler rejects functions, futures, handles and interface values. Channels live outside the heaps and are shared; `isolate.run` follows Dart 3's `Isolate.run`. `while let` came with it, for receive loops (V-147) |
| 2026-11-25 | Records follow Dart 3 exactly: `(int, string)`, named fields in braces as `({int x, int y})`, positional fields read as `$1`. They are C structs passed by value, typed by their shape, with equality generated per type. Simple `let`, `var` and `for` destructuring came with them; patterns in `match` stay in V-137 (V-136) |
| 2026-11-25 | Patterns follow Dart 3 with Vex's own ranges: struct patterns `Point(x: 0, :y)`, list patterns with `...rest`, `when` guards, `\|\|` alternatives, comparisons, and `if value case pattern`. Ranges use `1..=9` as in loops. Coverage is checked over nested shapes with a usefulness algorithm, which also finds unreachable arms. Matching on `float` is now allowed (V-137) |
| 2026-11-25 | Constants follow Dart: `const`, `static const`, `pub const`, usable as patterns. Int, bool and string constants are folded by the compiler, with overflow and division by zero as compile errors. Lists, maps and other values are built once before `main` on the main heap, kept as roots, and shared read-only with isolates. Const lists and maps are frozen: changes are compile errors when visible, otherwise runtime errors (a frozen list has a negative capacity, so `push` costs nothing extra). `.copy()` came with them (V-138) |
| 2026-11-25 | Enums whose cases carry no values are ordered as their cases are declared, so `<`, `<=`, `>`, `>=` work on them and they meet `Comparable`, as in Kotlin and Swift. The spec already promised this |
| 2026-11-25 | Type aliases use Dart's `typedef Name = Type`, with type parameters and constraints as in Swift, Kotlin and Dart. An alias is the same type as the one it names; an alias of a struct creates values, one of an enum reaches its cases, and one of an interface works after `:` and as a constraint. Distinct wrapper types stay with extension types (V-146) |
| 2026-11-26 | Multi-line strings follow Swift: `"""` ends its line, the closing `"""` sits on its own line and its indentation is removed from every line, and `\` at a line end joins lines. Raw strings use Dart's `r"..."` and `r"""..."""`, with no escapes or interpolation |
| 2026-11-26 | Extensions follow Swift: `extension Type { ... }`, with members used without `self.`. Structs and enums get methods, accessors, statics and constants; built-in types get methods and getters, and lists and maps take type parameters as in Dart (`extension<T: Comparable> [T]`). No fields, no replacing existing members. An extension applies in its own module and, for its `pub` members, in files that import that module |
| 2026-11-26 | Tear-offs follow Dart: a method named without a call is a function bound to its receiver, `x?.m` is nil for a nil `x`, and constructors are torn off as `Type.new`. Built-in members tear off too; async and generic methods wait until function values can be async and generic |
| 2026-11-26 | Operators follow Dart: `operator +(other: T) -> T` inside a struct, with `self` as the left operand. Structs define `+ - * / %`, unary `-`, `==`, `<`, `[]` and `[]=`; `!=` comes from `==` and the other orderings from `<`, and `==` and `<` make a struct Equatable and Comparable. Enums, interfaces and built-in types don't define operators yet |
| 2026-11-26 | GC: generational with sticky mark bits, since a conservative collector can't move objects: survivors stay marked, minor collections trace only from roots and a remembered set filled by write barriers after each pointer store, and `VEX_GC_VERIFY` checks for missed barriers under stress (part of V-112) |
| 2026-11-26 | Ints stay overflow-checked and indexes bounds-checked, as in Swift; wrapping is opt-in with `&+`. The compiler leaves out only the checks it proves can't fail, from constants, `for` ranges, loop conditions, values that only grow or shrink, and lists that keep their length (V-149) |
| 2026-11-26 | Bitwise operators use the C symbols `& \| ^ ~ << >>` and their compound assignments, as in Dart, Swift, Go and Rust, with Swift's precedence: `<<` and `>>` bind tightest, `&` like `*`, `\|` and `^` like `+`, all tighter than comparisons, so `flags & HIDDEN != 0` reads as written. Shifts outside 0–63 stop the program (V-150) |
| 2026-11-26 | Safe parsing uses `int.parse(s)` and `float.parse(s)`, which return optionals, like Dart's `tryParse` and Kotlin's `toIntOrNull`; `int(s)` still stops the program on bad input (V-152) |
| 2026-11-26 | Conditional values use `if … else …` as an expression, as in Kotlin, Rust and Swift 5.9, with `else` required. `?:` stays rejected, since `?` already marks optionals (V-153) |
| 2026-11-27 | Type tests use `x is T` and `x as? T`, as in Swift, Kotlin and Dart, with no unchecked `as`. After `is`, a `let`, parameter or pattern name has the tested type, as in Kotlin and Dart; a `var` doesn't, since it may change. A pattern binds the whole value with `t: Text`, the declaration syntax (V-154) |
| 2026-11-27 | Structs and enums opt in to `==` and hashing by listing `Equatable` or `Hashable`, as in Swift; structs stay compared by reference otherwise, and `===`/`!==` test identity, as in Swift and Kotlin. A struct with its own `==` writes a matching `fn hash() -> int`, helped by a built-in `hash(...)`. Lists, maps, optionals and records compare by value when their parts do, and an optional compares with a plain value. This replaces the v0.1 limit of `==` on primitives and simple enums (V-155) |
| 2026-11-27 | `let` and `var` outside functions, and `static let`/`static var` in structs and enums, hold state the program shares. Each is set up lazily on first use, as in Dart and Swift, so declaration order doesn't matter and unused ones cost nothing; a cycle stops the program. Each isolate has its own copy, as in Dart, which keeps isolates free of locks (V-156) |
| 2026-11-27 | Async function values have the type `async fn(T) -> R`, mirroring the declaration as Swift's `() async -> T` and Kotlin's `suspend () -> T` do, with the plain result type as for async functions. A closure is async when written `async fn` or when the context expects an async function, so trailing blocks need no marker. A plain function can stand in for an async one, and interface methods can be async (V-157) |
| 2026-11-27 | Cancellation stops a task at its next await, discarding its stack, as Kotlin does without exceptions; a task awaiting a cancelled future stops too, so no types change. Cancelling a task cancels the tasks it started, like structured concurrency without new syntax. `time.timeout(f, ms)` gives `T?`, `nil` on timeout, and cancels the late task (V-158) |
| 2026-11-27 | Platform conditionals use the enums `os.Target` and `os.Arch` rather than strings, so typos are errors and a `match` must cover every system. Branches for other systems are type-checked but generate no C, unlike Swift's `#if`, so errors show on every machine while target-only C functions still link. Per-target C code lives in `[c.macos]`-style sections that add to `[c]` (V-159) |
| 2026-11-27 | Tests are `test "name" { ... }` blocks, as in Zig and Swift Testing, in any file or in `_test.vx` files that only `vex test` reads. One `expect(cond)` built-in, like Swift's `#expect`, shows both sides of a failed comparison and lets the test go on. Each test runs in a forked process, so a crash fails only that test (V-176) |
| 2026-11-27 | Build modes are flags on `build`, `run` and `test`: `--debug` (`-O0 -g`; `build` keeps the C as `<out>.c` for a debugger) and `--sanitize=address,undefined,thread`, as Go and Rust spell them; `thread` can't be combined with `address`. `$VEX_CFLAGS` still comes last, so it overrides them (V-177) |
| 2026-11-28 | Windows CI uses a pinned LLVM-MinGW release zip checked by SHA-256 and cached by release name, rather than winget, which runner images don't reliably provide and which can't be pinned to a checksum. |
| 2026-11-28 | On Windows `vex:crypto` takes SHA-2, HMAC, PBKDF2, HKDF, AES-GCM, P-256 and random bytes from CNG, which ships with the system, so nothing extra is installed. |
| 2026-11-28 | Windows programs work in UTF-8 throughout (the CRT's UTF-8 locale, wide Win32 calls for arguments, environment and folders, a UTF-8 console and binary stdout), and golden tests may add a per-system `NAME.windows.out`. |
| 2026-11-28 | On Windows the runtime uses Win32 threads, SRW locks and condition variables rather than winpthreads, so built `.exe` files need no extra DLL; task switching keeps our own x64 assembly, swapping the thread block's stack fields instead of using Fibers, so one design serves every system. |
| 2026-11-28 | `vex doctor` follows `flutter doctor`: it fails only when Vex can't build programs, reports git, Xcode and Android problems as warnings with a fix line, and never installs anything. |
| 2026-11-28 | `vex:crypto` keys are extension types that never print their secrets; `seal` is XChaCha20-Poly1305 with a random nonce, and AES-GCM, PBKDF2 and P-256 exist for interop. On Apple systems AES-GCM and HKDF come from CryptoKit through a small Swift file, since CommonCrypto has no public GCM. |
| 2026-11-28 | Stack traces are a build option: on for `vex run`, `vex test`, `--debug` and `--traces`, off for plain `vex build`, which keeps release speed. |
| 2026-11-28 | Strings stay UTF-8 bytes; `count` is code points, `graphemeCount` and `graphemes` follow Unicode 15.0 extended grapheme clusters, and case mapping and normalization use generated Unicode tables. |
| 2026-11-28 | The benchmark suite compares Vex with Go on the same programs, each task run alone with `GOMAXPROCS(1)` on the Go side, and `bench/run.sh --json` feeds the website. |
| 2026-11-28 | Doc comments are `///` above declarations, as in Dart and Swift, with `[Name]` links; `vex doc` writes HTML or Markdown for `pub` declarations only. |
| 2026-11-28 | Packages are git repositories pinned by tag in `package.toml`, resolved by minimal version selection as in Go (the oldest version that satisfies every requirement), locked in `package.lock` and imported as `package:name/path`. |
| 2026-11-28 | Windows uses clang from LLVM-MinGW with no Visual Studio, and every PR runs on `windows-latest`. Like Flutter, the SDK archive bundles everything its licenses allow and leaves Xcode and Android Studio to the user. `vex:crypto` wraps only audited code (Monocypher) or each system's own provider for AES and P-256, with a `seal`/`open` API that picks nonces itself, and is tested with Wycheproof vectors. |
| 2026-11-28 | Random uses `nextInt(range)` style names; regex uses `regex.compile(p) throws` with its own UTF-8 engine instead of POSIX, which differs between systems; `DateTime` stores microseconds as in Dart. |
| 2026-11-28 | `Bytes` is a shared, mutable byte buffer like a list, compared and hashed by contents; strings convert with `toBytes()` and `decodeUtf8()`, and JSON holds it as base64. |
| 2026-11-28 | Extension types follow Dart 3, `extension type UserId(value: int)`, and compile to the wrapped value itself: no implicit conversion either way, only their own members, and only derived Equatable, Hashable, Comparable and Codable. |
| 2026-11-28 | `Set<T>` follows Swift: a list literal becomes a set where a set is expected, `insert` and `remove` return whether the set changed, and sets print like lists in insertion order. |
| 2026-11-28 | Null-aware elements follow Dart: `[a, ?b]` and `[?k: v]` leave out nil, and `xs?[i]` indexes an optional; `?[` is written without a space. |
| 2026-11-28 | Format specs follow Python and Rust, `[[fill]align][+][#][0][width][,][.precision][type]` after a top-level `:` in the braces, checked when compiling and compiled to direct calls. |
| 2026-11-28 | Enum raw values follow Swift: `enum Color: int { red = 1 }`, `Color(rawValue:)` giving `Color?`, `.rawValue`, `.name` and `allCases`; enums with data get `==` the struct way, by listing `Equatable`. |
| 2026-11-28 | `sealed` is a contextual modifier on `interface`: only types in the same module may implement it, and a match over it is exhaustive without `_` when every non-generic implementing struct and enum is covered. |
| 2026-11-28 | Values are shown by writing into one growing buffer, so nested values print in linear time; user `toString` results are appended to it. |
| 2026-11-28 | A `[B]` used as `[A]` is copied with each element converted when every B converts implicitly to A (map values too, keys identical); `cast<T>()` does the same, and downcasts use `as?` per element. |
| 2026-11-28 | A `pub` declaration that names a private type of its own module is an error, as in Swift and Kotlin; an unused import is only a warning. |
| 2026-11-28 | Map `update(key) { old in new }` returns the new value as `V?` and never adds a key; `getOrPut` is the way to insert a default. |
| 2026-11-28 | Strings keep `len` for their length; `count(text)` counts occurrences, and `s.count` without a call is an error that points to `.len`, since Swift developers expect `count` to be the length. |
| 2026-11-28 | Editor support follows Flutter's: a language server and a debug adapter in `vex`, used by a VS Code extension and a JetBrains plugin (IntelliJ IDEA, Android Studio and the rest). DevTools for running apps is part of the UI framework and is planned in `vex-ui`, not here. |
| 2026-11-27 | Graphics get their own phase, after the app essentials: animation, a `Canvas` like Flutter's `CustomPaint`, shaders and 3D. They use each platform's graphics APIs rather than a bundled engine like Skia or Impeller, keeping native widgets and small binaries; one shader language compiles to Metal and SPIR-V. |
| 2026-11-27 | Iteration follows Swift and Kotlin: `Iterable<T>` gives a fresh `Iterator<T>` whose `next()` returns `T?`, so a value can be looped many times. Generators are plain functions returning `Iterable<T>` that use `yield`, as in Python and C#, matching `async fn ... -> Stream<T>` without a `sync*` marker. They run as coroutines on task stacks; a paused generator isn't a root, so the collector frees one that is abandoned. `map`, `filter`, `take`, `skip` and `enumerated` are lazy, Kotlin-sequence style, and are written in Vex as an extension of the generic interface in a core module every file sees. Optionals can't be elements, since Vex flattens `T??`. |
| 2026-11-27 | Ranges are values of a built-in `Range` type of ints, as in Kotlin; `step(n)` and `reversed()` are methods rather than keywords or a `stride` function. `..` binds as in Swift, looser than arithmetic and tighter than comparisons. Slicing (`xs[1..3]`, `s[1..3]`) copies, with Swift's open ends `xs[2..]` and `xs[..2]` allowed only inside `[ ]`; views that share storage were left out as a source of surprises. A struct called Range hides the built-in one. |
| 2026-11-27 | JSON follows Swift's `Codable`: listing it makes the compiler write the conversion from the fields, so there is no generated code to keep in step, and only types that opt in can be written out. `vex:json` gives `encode` and `decode<T>`, which throws a `JsonError` naming the path, and `parse` gives a `Json` enum for JSON of unknown shape. Enum cases with values use Swift's one-key object form. A type that writes `encode` and `static fn decode` replaces the generated pair. |
| 2026-11-27 | Interfaces take Swift's `static fn` requirements and `Self`, called as `T.parse(...)` in generic code; such interfaces, and ones with generic methods, are constraints only, since interface values dispatch through a table. `extension Shape { }` adds methods to every implementing type, which can't replace them (V-167) |
| 2026-11-27 | Fields use `let` for set-once, as in Swift and Kotlin; plain fields stay changeable so existing code keeps working, and a default lets the type be left out. `pub private(set)` (Swift) limits assignment to the struct's methods. A Kotlin-style `init { }` block checks and finishes creation without replacing the memberwise constructor, and every struct gets Kotlin's `copy(field:)` (V-161) |
| 2026-11-27 | `defer { }` runs when its block ends, last first, as in Swift; it can't return, throw or leave an outer loop. Loops are named Swift-style (`outer: for ...`, `break outer`), and the loop that runs at least once is `repeat { } while cond`, keeping `do` free (V-163) |
| 2026-11-27 | Headers join conditions with commas as in Swift (`if let a = x, a > 0`), with `if let x` for `if let x = x` and `guard cond else`. The right side of `??` can leave with `return`, `throw`, `break` or `continue`, Kotlin's `?: return` spelled with Vex's `??` (V-162) |
| 2026-11-27 | Local functions are plain `fn` declarations inside a body, as in Swift, Kotlin and Dart. They are closures held in a variable named after them, so they capture by reference and can recurse; as in Kotlin, they must be declared before use (V-169) |
| 2026-11-27 | Custom printing is a `toString()` method, the name Dart and Kotlin use, rather than Swift's `description`. It is found by name, with no interface to declare, and applies everywhere a value is shown (V-168) |
| 2026-11-27 | Trailing blocks name parameters Swift's way, `{ a, b in ... }`, and a single unnamed parameter is Kotlin's `it`; a declared `it` wins. A block's last expression is its value when the function returns one, as in Kotlin. Calls with only a block, such as `xs.map { ... }`, leave out the `()` (V-160) |
| 2026-11-27 | Streams follow Swift's `for await` and Dart's generators: an `async fn` returning `Stream<T>` gives values with `yield`, runs lazily from the first read and pauses at each `yield`. `StreamController<T>` fills a stream from callbacks, as in Dart. Leaving a `for await` early cancels the stream function (V-148) |
| 2026-11-27 | The checker warns about unused local variables (a `_` prefix opts out, as in Swift and Rust) and code after `return`, `throw`, `break`, `continue`, `panic` or an endless loop. Missing features that developers of other languages reach for (`x!`, `++`, `defer`, loop labels, `do`/`while`, `switch`, `class`) get errors that name them and show the Vex way (V-178) |
| 2026-11-26 | No function overloading by parameter types, as in Dart and Go: named parameters and defaults cover the usual cases, and calls stay unambiguous (V-179) |
| 2026-11-26 | No inheritance between structs: shared behaviour comes from interfaces and extensions (V-179) |
