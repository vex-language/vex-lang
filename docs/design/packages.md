# Packages

A Vex package is a public git repository with version tags. There is no upload step and no central storage: `vex get` downloads straight from the repository, and a website lists the packages it knows about, as pkg.go.dev does for Go.

## Using a package

```sh
vex get github.com/ann/charts@0.3.1   # a tagged version
vex get github.com/ann/charts         # the newest tag
```

```swift
import "github.com/ann/charts"

fn main() {
    charts.bar([3, 5, 2])
}
```

`vex get` records the dependency in the project file and its exact hash in the lock file. `vex run` and `vex build` fetch whatever is missing, so a fresh clone builds with no extra step.

## Publishing a package

1. Put the code in a public git repository with a project file at the root.
2. Tag a release: `git tag v0.3.1 && git push --tags`.

Versions follow semantic versioning: `v1.4.2` is major.minor.patch, and a new major version may break code that uses the package.

## Files

| File | Written by | Purpose |
|---|---|---|
| Project file | The developer, and `vex get` | Package name, version, dependencies |
| `vex.lock` | `vex get`, never by hand | Exact version and content hash of every dependency, including indirect ones |

The lock file is committed, so a build that works today builds identically later, and a dependency whose content changed under the same tag is rejected.

Downloads go into one shared cache, `~/.vex/pkg/<host>/<path>@<version>`, not a folder inside each project.

### Project file format

To be approved together with V-105. The candidates:

| Format | Example | For | Against |
|---|---|---|---|
| TOML, `vex.toml` | Rust (`Cargo.toml`), Python | Simple, allows comments, no indentation rules, existing libraries | One more format to learn |
| YAML, `vex.yaml` | Flutter (`pubspec.yaml`) | Familiar to Flutter developers | Indentation mistakes, surprising values (`no` becomes false) |
| Own format, `vex.mod` | Go (`go.mod`) | Tiny, made for exactly this, no dependency | A custom parser to maintain |
| Vex code, `project.vx` | Swift, Zig | One language for everything | Tools must run the compiler to read it; config can grow into a program |

JSON is left out because it allows no comments.

## Official packages

Where a package made by the Vex project lives depends on what it needs:

| Kind | Where | Examples |
|---|---|---|
| Useful to every program, needs no phone | Standard library, inside `vex-lang`; imported with no `vex get` | `http`, `json`, `fs`, `time`, `math`, `strings` |
| Draws on screen | `vex-ui` | Widgets, layout, state, `Image`, navigation, theming |
| Needs native iOS or Android code | `vex-packages`, one repo with a folder per package | `camera`, `image_picker`, `location`, `storage`, `notifications`, `maps` |

Each folder in `vex-packages` is a separate package with its own tags (`camera/v0.2.0`), so an app carries only the packages it imports.

### Addresses

The import path is where a package is found, so moving a repo would break every app that imports it. Official packages therefore use addresses on the project's own domain:

```swift
import "vexlang.com/ui"
import "vexlang.com/packages/camera"
```

Each address serves a small page with a tag naming the real repository, which `vex get` follows (Go uses the same scheme). Official repositories live in a GitHub organisation rather than a personal account, and can move without breaking any import.

## Choosing versions

When two packages need different versions of a third, Vex picks the lowest version that satisfies all of them (minimal version selection, as in Go). A build never picks up a newer release by itself; versions change only when someone runs `vex get`. Two major versions of the same package can be used side by side, because their import paths differ (`.../charts` and `.../charts/v2`).

## The website

A searchable index at `pkg.vexlang.com` (or `vexlang.com/packages`). It stores no code.

- **Listing.** A package appears when someone submits its URL, when `vex get` fetches it for the first time, or when its repository has the `vex` topic on GitHub.
- **Pages.** README, versions, licence, dependencies and the packages that use it, plus API docs generated from `///` comments by `vex doc`.
- **Scores.** Whether it compiles with the current Vex, has docs and tests, which platforms it supports (iOS, Android, desktop), and how widely it is used.

If the website is down, `vex get` still works, because it talks to git directly.

## Later: cache and checksum log

Once Vex has outside users:

- **Cache.** A server that keeps a copy of every published version, so an app still builds if the author deletes the repository or GitHub is unreachable.
- **Checksum log.** A public, append-only record of every version's hash, so nobody can quietly replace the code behind an existing tag.

Both are optional for `vex get`, which can always fall back to git.

## Order

1. V-105 modules: multi-file projects and `import`.
2. V-121 `vex get`, the project file and `vex.lock`, fetching from git.
3. V-122 `vex doc`, which the website reuses.
4. V-605 the website, together with the `vex-ui` repo, its first real package.
5. V-607 cache and checksum log.
