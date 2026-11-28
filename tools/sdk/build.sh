#!/bin/sh
# Builds the Vex SDK archive for one target.
#
#   tools/sdk/build.sh <target> <outdir>
#
# Targets: windows-x64 (a .zip with LLVM-MinGW), linux-x64 (a .tar.xz with
# clang, a musl sysroot and a static libcrypto; must run on Linux x86-64),
# macos-arm64 and macos-x64 (a .tar.xz without a toolchain; programs are
# built with the Xcode Command Line Tools).
#
# Environment:
#   VEX_VERSION   the version to stamp, such as 0.2.0 (default: a dev version)
#   VEX_SDK_CACHE where downloads are kept between runs (default: <outdir>/cache)
#   VEX_SDK_SIGN  set to 1 to sign vex with tools/sdk/sign.sh before packing
set -eu

# Pinned third-party sources, each with its SHA-256.
LLVM_MINGW_VERSION=20261117
LLVM_MINGW_SHA256=4d002e330a0f703625aa5ccbc37e96f8f824413e163bfc7336774aabd2c3a412
# LLVM-MinGW $LLVM_MINGW_VERSION is built from the same LLVM release.
LLVM_VERSION=23.1.6
LLVM_SHA256=59e14234f145927f982b50627e8bafaf2fca50151b7d973d6a0b40796ddcfe9a
MUSL_VERSION=1.2.6
MUSL_SHA256=d585fd3b613c66151fc3249e8ed44f77020cb5e6c1e635a616d3f9f82460512a
OPENSSL_VERSION=3.5.9
OPENSSL_SHA256=603f5602e2eef00d77fbd429d34dcd5822bb301757a1bc9cdb24c670f1eb859a

usage() {
	echo "usage: $0 <windows-x64|linux-x64|macos-arm64|macos-x64> <outdir>" >&2
	exit 2
}

[ $# -eq 2 ] || usage
target=$1
case $target in
windows-x64) goos=windows goarch=amd64 ;;
linux-x64) goos=linux goarch=amd64 ;;
macos-arm64) goos=darwin goarch=arm64 ;;
macos-x64) goos=darwin goarch=amd64 ;;
*) usage ;;
esac

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
mkdir -p "$2"
out=$(cd "$2" && pwd)
cache=${VEX_SDK_CACHE:-$out/cache}
work=$out/work-$target
sdk=$work/vex-sdk
mkdir -p "$cache"
rm -rf "$work"
mkdir -p "$sdk/bin" "$sdk/docs/api"

commit=$(git -C "$repo" rev-parse HEAD 2>/dev/null || echo unknown)
version=${VEX_VERSION:-0.1.0-dev+$(echo "$commit" | cut -c1-7)}
exe=
[ "$goos" = windows ] && exe=.exe

# Prints the SHA-256 of a file.
sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# Downloads a URL into the cache once and checks its SHA-256.
fetch() {
	url=$1 sum=$2 file=$cache/$(basename "$1")
	if [ ! -f "$file" ] || [ "$(sha256 "$file")" != "$sum" ]; then
		echo "Downloading $url"
		curl -fL --retry 3 -o "$file.part" "$url"
		mv "$file.part" "$file"
	fi
	got=$(sha256 "$file")
	if [ "$got" != "$sum" ]; then
		echo "error: $file has SHA-256 $got, want $sum" >&2
		exit 1
	fi
}

echo "Building vex $version for $target"
(cd "$repo" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath \
	-ldflags "-s -w -X main.version=$version" -o "$sdk/bin/vex$exe" ./cmd/vex)
host=$work/host-vex
(cd "$repo" && CGO_ENABLED=0 go build -o "$host" ./cmd/vex)

echo "Writing the documentation"
cp "$repo/docs/SYNTAX.md" "$sdk/docs/SYNTAX.md"
cp "$repo/README.md" "$sdk/docs/README.md"
cp "$repo/docs/design/sdk.md" "$sdk/docs/sdk.md"
{
	echo '<!doctype html><meta charset="utf-8"><title>Vex standard library</title>'
	echo '<h1>Vex standard library</h1><ul>'
	for dir in "$repo"/compiler/modules/std/*/; do
		m=$(basename "$dir")
		"$host" doc "vex:$m" -o "$sdk/docs/api/$m" >/dev/null
		echo "<li><a href=\"$m/index.html\">vex:$m</a></li>"
	done
	echo '</ul>'
} >"$sdk/docs/api/index.html"

cp "$repo/LICENSE-APACHE" "$repo/LICENSE-MIT" "$repo/LICENSE-RUNTIME-EXCEPTION" "$sdk/"

# Adds a section for one component to THIRD_PARTY_LICENSES.
notice() {
	{
		printf '%s\n' "================================================================================"
		printf '%s\n' "$1"
		printf '%s\n\n' "================================================================================"
		cat "$2"
		printf '\n\n'
	} >>"$sdk/THIRD_PARTY_LICENSES"
}

: >"$sdk/THIRD_PARTY_LICENSES"
notice "Go runtime and standard library, built into bin/vex (BSD-3-Clause)" "$(go env GOROOT)/LICENSE"
notice "Monocypher, built into programs that import vex:crypto (BSD-2-Clause or CC0-1.0)" \
	"$repo/compiler/modules/std/crypto/monocypher/LICENCE.md"
notice "Unicode Character Database, tables built into programs (Unicode License v3)" "$here/licenses/unicode.txt"

toolchain_json='{}'
case $target in
windows-x64)
	. "$here/windows-toolchain.sh"
	;;
linux-x64)
	. "$here/linux-toolchain.sh"
	;;
esac

cat >"$sdk/version" <<EOF
{
  "name": "vex-sdk",
  "version": "$version",
  "commit": "$commit",
  "target": "$target",
  "toolchain": $toolchain_json
}
EOF

if [ "${VEX_SDK_SIGN:-}" = 1 ]; then
	"$here/sign.sh" "$target" "$sdk/bin/vex$exe"
fi

name=vex-sdk-$version-$target
rm -f "$out/$name.zip" "$out/$name.tar.xz"
cd "$work"
if [ "$goos" = windows ]; then
	zip -qr -9 "$out/$name.zip" vex-sdk
	archive=$out/$name.zip
else
	# Without macOS metadata or the builder's user, so any tar unpacks it cleanly.
	if tar --version | grep -q bsdtar; then
		COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata --uid 0 --gid 0 -cf - vex-sdk | xz -9 -T0 >"$out/$name.tar.xz"
	else
		tar --owner=0 --group=0 --numeric-owner -cf - vex-sdk | xz -9 -T0 >"$out/$name.tar.xz"
	fi
	archive=$out/$name.tar.xz
fi
cd "$out"
rm -rf "$work"
echo "Wrote $archive ($(du -h "$archive" | cut -f1))"
