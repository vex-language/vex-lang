# Architecture: the language and the UI framework

Vex is two projects, built in this order:

| Project | Repo | Contains |
|---|---|---|
| **Vex**, the language | `vex-lang` (this repo) | Compiler, runtime (GC, runtime errors), standard library, `vex` command |
| **Vex UI**, the framework | `vex-ui`, created once the language part is complete | Components, state, layout, native widget bindings, app packaging |

This is the same split as Dart and Flutter: anyone can use Vex without the UI framework, for tools, servers or games, and the framework is ordinary Vex code that ships and versions on its own.

## How Vex UI differs from Flutter

Flutter draws every pixel itself, so its SDK includes a graphics engine, a layout system and text rendering. Vex UI uses the real platform widgets (`UILabel`, `UIButton` on iOS; `TextView`, `Button` on Android), so it is much smaller:

- **Component tree and diffing.** When state changes, components build a new tree, which is compared with the previous one. Only the native views that changed are updated.
- **Platform layers.** Thin code that turns those updates into UIKit and Android calls. Vex compiles to native code and calls C directly, so there is no bridge or message queue between Vex and the platform.
- **Layout.** One flex-style layout model, computed in Vex, so both platforms place views the same way.
- **Packaging.** Building an `.app` / `.ipa` or an APK from a Vex project.

```
 your app (.vx)
      │
 Vex UI           components, State, diffing, layout        ── vex-ui repo
      │
 platform layer   UIKit (Objective-C) · Android (JNI)       ── vex-ui repo
      │
 Vex runtime      GC, errors, strings, lists                ── vex-lang repo
      │
 native binary    clang / Android NDK
```

## The rule: no UI in the language

The compiler has no UI keywords and no knowledge of widgets. The UI framework is built from general language features, and those features must also be useful outside UI. Dart has no Flutter keywords at all, and SwiftUI is built on general Swift features (result builders, property wrappers).

Because of this rule, the earlier `component`, `state` and `view { }` keywords are dropped. The language features Vex UI needs instead:

| Feature | What it gives Vex UI | Useful elsewhere for |
|---|---|---|
| Trailing blocks (done) | `Button("Add") { count += 1 }` | Callbacks, `repeat(3) { }` |
| Closures (done) | Event handlers | `filter`, `map`, any callback |
| Interfaces | Any struct can be a component | Plugins, testing with fakes |
| Generics | `State<T>`, typed lists of children | Collections, `Result<T>` |
| Builder blocks | Several children in `{ }` become a list | HTML and SQL builders, config |
| Property setters | Setting a state value schedules a re-render | Validation, computed fields |
| Modules | Vex UI is a separate package | Every multi-file project |
| C interop | Calls into UIKit and Android | Any C library (SQLite, zlib) |

A sketch of what this allows. It is **not approved syntax**: each feature's syntax is approved when it is built.

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

Here `View` is an interface, `State` is a generic struct from the `ui` package whose setter schedules a re-render, and the `{ }` after `Column` is a builder block. None of these names are known to the compiler.

## What stays in this repo

Some app work belongs to the language and toolchain, not the framework:

- Cross-compiling the C output for iOS and Android (`arm64-apple-ios`, the NDK).
- Line information in the generated C, so debuggers show `.vx` lines.
- The formatter, the language server and the package manager ([packages.md](packages.md)).
- Hot reload support in the runtime (reloading code as a dynamic library).

## Order

1. **Language foundation** (this repo, now): errors, maps, generics, interfaces, modules, C interop, standard library, the GC stages, plus the features in the table above.
2. **`vex-ui` repo**: created when step 1 is complete. It starts with a headless backend that renders the tree to text, so components can be unit-tested on a Mac before any phone.
3. **iOS layer**, then **Android layer**, then the developer tools that need both (hot reload, `vex new`).
