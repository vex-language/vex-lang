#!/bin/sh
# Unpacks an SDK archive into a folder with spaces and checks that it
# builds programs on its own: vex doctor, hello world, a vex:crypto
# program and vex test, with nothing but the SDK and the system's own
# folders on PATH.
#
#   tools/sdk/test.sh <archive> <workdir>
set -eu

[ $# -eq 2 ] || { echo "usage: $0 <archive> <workdir>" >&2; exit 2; }
here=$(cd "$(dirname "$0")" && pwd)
archive=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
mkdir -p "$2"
dir=$(cd "$2" && pwd)/"Vex SDK test"
rm -rf "$dir"
mkdir -p "$dir"
case $archive in
*.zip) unzip -q "$archive" -d "$dir" ;;
*) tar -xJf "$archive" -C "$dir" ;;
esac
sdk=$dir/vex-sdk
cp -R "$here/smoke" "$dir/smoke"
cd "$dir/smoke"

case $(uname -s) in
MINGW* | MSYS* | CYGWIN*)
	exe=.exe
	clean=/c/Windows/System32:/c/Windows
	;;
*)
	exe=
	clean=/usr/bin:/bin
	;;
esac

# Runs vex with a clean PATH and no $CC.
vex() {
	env -u CC -u VEX_CFLAGS PATH="$sdk/bin:$clean" "$sdk/bin/vex$exe" "$@"
}

# Fails unless [got] equals [want].
same() {
	if [ "$1" != "$2" ]; then
		printf 'error: %s\ngot:\n%s\nwant:\n%s\n' "$3" "$1" "$2" >&2
		exit 1
	fi
}

echo "== $sdk"
cat "$sdk/version"
vex version
status=0
vex doctor -v >doctor.txt || status=$?
cat doctor.txt
[ "$status" = 0 ] || { echo "error: vex doctor failed" >&2; exit 1; }
if [ -d "$sdk/toolchain" ] && ! grep -q "bundled with the Vex SDK" doctor.txt; then
	echo "error: vex didn't choose the SDK's toolchain" >&2
	exit 1
fi

same "$(vex run hello.vx | tr -d '\r')" "Hello, world!" "hello world"

vex build crypto.vx -o crypto
want='ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad
cf4ab791c62b8d2b2109c90275287816
16'
same "$(env PATH="$clean" ./crypto$exe | tr -d '\r')" "$want" "vex:crypto"
if [ "$(uname -s)" = Linux ] && ldd ./crypto >/dev/null 2>&1; then
	echo "error: the SDK built a dynamic executable" >&2
	exit 1
fi

vex test project | tr -d '\r' | tee test.txt
grep -q "1 test: 1 passed, 0 failed" test.txt

echo "SDK works: $(basename "$archive")"
