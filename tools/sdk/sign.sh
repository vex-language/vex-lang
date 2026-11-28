#!/bin/sh
# Signs the vex binary of an SDK before it is packed.
#
#   tools/sdk/sign.sh <target> <file>
#
# macOS: codesigns with the hardened runtime when MACOS_SIGN_IDENTITY names
# a certificate in the keychain, then notarizes when APPLE_ID,
# APPLE_TEAM_ID and APPLE_APP_PASSWORD are set.
# Windows: Authenticode-signs with osslsigncode when WINDOWS_CERTIFICATE
# (a base64 .pfx) and WINDOWS_CERTIFICATE_PASSWORD are set.
# Without them it skips signing and says so.
set -eu

[ $# -eq 2 ] || { echo "usage: $0 <target> <file>" >&2; exit 2; }
target=$1 file=$2

skip() {
	echo "::notice::$1; $file is not signed"
	exit 0
}

case $target in
macos-*)
	[ -n "${MACOS_SIGN_IDENTITY:-}" ] || skip "MACOS_SIGN_IDENTITY isn't set"
	codesign --force --options runtime --timestamp --sign "$MACOS_SIGN_IDENTITY" "$file"
	codesign --verify --strict "$file"
	if [ -z "${APPLE_ID:-}" ] || [ -z "${APPLE_TEAM_ID:-}" ] || [ -z "${APPLE_APP_PASSWORD:-}" ]; then
		echo "::notice::Apple notarization credentials aren't set; $file is signed but not notarized"
		exit 0
	fi
	# A bare executable can't be stapled; Gatekeeper looks its ticket up online.
	zip=$(mktemp -d)/vex.zip
	ditto -c -k "$file" "$zip"
	xcrun notarytool submit "$zip" --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" \
		--password "$APPLE_APP_PASSWORD" --wait
	rm -rf "$(dirname "$zip")"
	;;
windows-*)
	[ -n "${WINDOWS_CERTIFICATE:-}" ] || skip "WINDOWS_CERTIFICATE isn't set"
	dir=$(mktemp -d)
	echo "$WINDOWS_CERTIFICATE" | base64 -d >"$dir/cert.pfx"
	osslsigncode sign -pkcs12 "$dir/cert.pfx" -pass "${WINDOWS_CERTIFICATE_PASSWORD:-}" \
		-n "Vex" -i "https://github.com/vex-language/vex-lang" -h sha256 \
		-t http://timestamp.digicert.com -in "$file" -out "$dir/signed.exe"
	mv "$dir/signed.exe" "$file"
	rm -rf "$dir"
	osslsigncode verify -in "$file" >/dev/null || echo "::notice::the signature of $file couldn't be verified against a trusted root"
	;;
*)
	skip "$target binaries aren't signed"
	;;
esac
