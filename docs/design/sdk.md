# The Vex SDK

The SDK is one download with everything needed to build Vex programs: the `vex` command, its standard library and runtime, the documentation, and on Windows and Linux a C toolchain. Unpack it anywhere and add its `bin` folder to `PATH`; nothing is installed elsewhere and nothing needs administrator rights.

| Archive | Contents |
|---|---|
| `vex-sdk-<version>-windows-x64.zip` | `vex.exe` and [LLVM-MinGW](https://github.com/mstorsjo/llvm-mingw) (clang, lld, the MinGW-w64 headers and libraries, UCRT) |
| `vex-sdk-<version>-linux-x64.tar.xz` | `vex`, clang and lld from the LLVM release, a [musl](https://musl.libc.org) sysroot and a static OpenSSL libcrypto |
| `vex-sdk-<version>-macos-arm64.tar.xz` | `vex`; programs are built with the Xcode Command Line Tools |
| `vex-sdk-<version>-macos-x64.tar.xz` | The same, for Intel Macs |

On macOS the C compiler, the system headers and swiftc (for `vex:crypto`) come from Apple's Command Line Tools, whose license doesn't allow passing them on; `xcode-select --install` installs them. Xcode, Android Studio and the Android SDK are never bundled either: `vex doctor` says how to install them.

## Layout

```
vex-sdk/
  bin/vex[.exe]           the command, with the standard library and runtime built in
  toolchain/              the C toolchain (Windows and Linux only)
    bin/clang[.exe]
    sysroot/              Linux: musl and libcrypto, headers and static libraries
  docs/
    SYNTAX.md             the language guide
    README.md
    sdk.md                this page
    api/index.html        the standard library's API documentation
  version                 what this SDK is, as JSON
  LICENSE-APACHE, LICENSE-MIT, LICENSE-RUNTIME-EXCEPTION
  THIRD_PARTY_LICENSES    the notices of everything bundled
```

There is no `lib` folder: the standard library's `.vx` modules, the C runtime (`vexrt.h`) and the C half of `vex:crypto`, Monocypher included, are embedded in the `vex` binary when it's built, so a lone `vex` binary works too.

The `version` file describes the SDK for tools that embed or update it:

```json
{
  "name": "vex-sdk",
  "version": "0.2.0",
  "commit": "c0ede82...",
  "target": "linux-x64",
  "toolchain": { "name": "llvm", "clang": "23.1.6", "libc": "musl 1.2.6", "openssl": "3.5.9" }
}
```

`toolchain` is `{}` on macOS. On Windows it names the LLVM-MinGW release: `{ "name": "llvm-mingw", "version": "20261117", "clang": "23.1.6", "crt": "ucrt" }`.

## Finding the C compiler

`vex` looks for the C compiler in this order:

1. `$CC`, which may be a full path with spaces or a command with arguments.
2. `toolchain/bin/clang` of the SDK `vex` belongs to: the folder above the real `vex` binary, after following symbolic links, so a link to `vex` from `/usr/local/bin` still finds it.
3. `clang`, `cc` or `gcc` on `PATH`; on macOS that is the Command Line Tools' clang.

Nothing is looked up by absolute path, so the folder can be moved, renamed, put on another drive or in a path with spaces. `vex doctor -v` shows which compiler was chosen and says "bundled with the Vex SDK" for the SDK's own.

## Linux: static programs

The Linux toolchain targets `x86_64-linux-musl`. A `clang.cfg` next to clang sets the target, the sysroot, `-static`, lld and compiler-rt, so every program it builds is a static executable that runs on any x86-64 Linux, whatever its C library. `vex:crypto` links the sysroot's static libcrypto from OpenSSL 3.5 (LTS), so no `libssl-dev` is needed; it adds a few megabytes to programs that import it.

clang itself is the LLVM release build, which needs glibc 2.35 or newer on the machine building programs (Ubuntu 22.04, Debian 12, Fedora 36 or newer). Sanitizers need the system's dynamic C library, so `--sanitize` asks for `CC=clang` with the distribution's clang instead.

## Embedding the SDK

A framework built on Vex, such as the native UI framework, ships its own archive with this SDK inside it, the way Flutter's archive carries the Dart SDK:

```
ui-sdk/
  bin/ui                  the framework's own tool
  bin/cache/vex-sdk/      this SDK, unpacked as released
```

The contract:

- The framework embeds one released SDK archive, unchanged, at a version it pins, and checks its SHA-256 from the release's `SHA256SUMS`.
- It runs `<vex-sdk>/bin/vex` by path. It doesn't add the SDK to `PATH`, set `$CC`, or copy files out of it; `vex` finds its toolchain on its own.
- It reads `<vex-sdk>/version` to show and compare versions, and to decide when to replace the folder on update. A folder is replaced whole, never patched.
- Anything the framework needs beyond the SDK, such as Xcode or Android Studio, stays the user's to install; the framework checks for it as `vex doctor` does.

## Releases

Pushing a `v*` tag runs `.github/workflows/release.yml`. It builds each archive with `tools/sdk/build.sh`, signs `vex` where the secrets for it are set (codesigning and notarization on macOS, Authenticode on Windows), writes `SHA256SUMS` and signs it with minisign, then unpacks each archive on a fresh runner, in a folder with spaces and with no other compiler on `PATH`, and runs `vex doctor`, a hello world, a `vex:crypto` program and `vex test`. Only then are the archives attached to the GitHub release and a `winget` manifest offered for the Windows one.

An archive can be built locally the same way:

```sh
tools/sdk/build.sh macos-arm64 dist      # writes dist/vex-sdk-<version>-macos-arm64.tar.xz
tools/sdk/build.sh windows-x64 dist      # on any machine with curl and zip
tools/sdk/build.sh linux-x64 dist        # on x86-64 Linux
```

Downloads are pinned by version and SHA-256 at the top of `tools/sdk/build.sh`.
