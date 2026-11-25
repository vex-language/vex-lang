# Packages

A Vex package is a public git repository with version tags. There is no upload step and no central storage: `vex get` downloads straight from the repository, and a website lists the packages it knows about.

Code imports a package by its name, and the project file says where that name comes from, as in Dart and Flutter. Moving a repository only changes one line in the project file, never the imports.

## Imports

| Form | Imports | Example |
|---|---|---|
| `vex:name` | The standard library, built in; no download and no project file entry | `import "vex:math"` |
| `package:name` | A package listed in the project file | `import "package:ui"` |
| `package:name/folder` | A folder inside a package | `import "package:charts/line"` |
| `"folder"` | A folder of the current project, relative to the importing file | `import "models"` |

```swift
import "vex:math"
import "package:ui"
import "models"

fn main() {
    println(math.max(2, 3))
    let u = models.User(name: "Ann")
}
```

A folder is one module: all `.vx` files in it share their names and use each other without imports. Code in another module refers to a name through the module, as in `math.max` or `models.User`, and only names marked `pub` are visible there. See [Modules](../SYNTAX.md#modules).

## Using a package

```sh
vex get https://github.com/ann/charts           # the newest tag
vex get https://github.com/ann/charts@0.3.1     # a tagged version
```

`vex get` adds the dependency to `package.toml` under the package's name and records its exact hash in `package.lock`. `vex run` and `vex build` fetch whatever is missing, so a fresh clone builds with no extra step.

## Files

| File | Written by | Purpose |
|---|---|---|
| `package.toml` | The developer, and `vex get` | Package name, Vex version, dependencies |
| `package.lock` | `vex get`, never by hand | Exact version and content hash of every dependency, including indirect ones |

```toml
[package]
name = "myapp"
vex = "0.2"

[dependencies]
ui = { git = "https://github.com/vex-lang/ui", version = "0.3" }
camera = { git = "https://github.com/vex-lang/packages", path = "camera", version = "0.2" }
charts = { git = "https://github.com/ann/charts", version = "0.3.1" }
```

- The key is the name used in `import "package:..."`. It is usually the package's own name from its `package.toml`, and can be changed to resolve a clash between two packages with the same name.
- `path` names a folder inside the repository, for repositories that hold several packages.
- The name `package.toml` matches the `package:` imports and the `[package]` section, the way Flutter's `pubspec.yaml` is named after its package tool. `pub.toml` was left out because `pub` is a keyword, and `spec.toml` because it doesn't say what it describes.
- TOML was chosen because it is simple, allows comments and has no indentation rules. YAML (as in `pubspec.yaml`) was left out for its indentation mistakes and surprising values (`no` becomes false), and JSON because it allows no comments.

The lock file is committed, so a build that works today builds identically later, and a dependency whose content changed under the same tag is rejected.

Downloads go into one shared cache, `~/.vex/pkg/<repository>@<version>`, not a folder inside each project.

## Publishing a package

1. Put the code in a public git repository with a `package.toml` at the root (or at its `path`).
2. Tag a release: `git tag v0.3.1 && git push --tags`. For a repository with several packages, tags carry the folder: `camera/v0.2.0`.

Versions follow semantic versioning: `v1.4.2` is major.minor.patch, and a new major version may break code that uses the package.

## Official packages

Where a package made by the Vex project lives depends on what it needs:

| Kind | Where | Imported as |
|---|---|---|
| Useful to every program, needs no phone | Standard library, inside `vex-lang` | `vex:http`, `vex:json`, `vex:fs`, `vex:time`, `vex:math`, `vex:strings` |
| Draws on screen | `vex-ui` | `package:ui` |
| Needs native iOS or Android code | `vex-packages`, one repo with a folder per package | `package:camera`, `package:image_picker`, `package:location`, `package:storage`, `package:notifications`, `package:maps` |

Official repositories live in a GitHub organisation rather than a personal account. `vex new` writes their entries into `package.toml`, and the website lists them first.

## Choosing versions

When two packages need different versions of a third, Vex picks the lowest version that satisfies all of them (minimal version selection, as in Go). A build never picks up a newer release by itself; versions change only when someone runs `vex get`. A build holds one version of each package, so packages that need different major versions of the same dependency can't be used together, as in Dart.

## The website

A searchable package index. It stores no code.

- **Listing.** A package appears when someone submits its repository, when `vex get` fetches it for the first time, or when its repository has the `vex` topic on GitHub.
- **Pages.** README, versions, licence, dependencies and the packages that use it, the `package.toml` line to copy, plus API docs generated from `///` comments by `vex doc`.
- **Scores.** Whether it compiles with the current Vex, has docs and tests, which platforms it supports (iOS, Android, desktop), and how widely it is used.

If the website is down, `vex get` still works, because it talks to git directly.

## Later: cache and checksum log

Once Vex has outside users:

- **Cache.** A server that keeps a copy of every published version, so an app still builds if the author deletes the repository or GitHub is unreachable.
- **Checksum log.** A public, append-only record of every version's hash, so nobody can quietly replace the code behind an existing tag.

Both are optional for `vex get`, which can always fall back to git.

## Order

1. V-105 modules: folders as modules, `import` of project folders and `vex:` libraries, `package.toml`. Done.
2. V-121 `vex get` and `package.lock`, fetching `package:` dependencies from git.
3. V-122 `vex doc`, which the website reuses.
4. V-605 the website, together with the `vex-ui` repo, its first real package.
5. V-607 cache and checksum log.
