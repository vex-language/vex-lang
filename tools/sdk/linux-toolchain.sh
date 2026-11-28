# Adds clang and lld from the LLVM release, a musl sysroot and a static
# OpenSSL libcrypto to the SDK at $sdk/toolchain, so programs are built as
# static executables that run on any x86-64 Linux. Sourced by build.sh on
# an x86-64 Linux machine with curl, zstd, make, perl and patchelf.

[ "$(uname -s)-$(uname -m)" = Linux-x86_64 ] || {
	echo "error: the linux-x64 SDK must be built on x86-64 Linux" >&2
	exit 1
}

llvm=LLVM-$LLVM_VERSION-Linux-X64
fetch "https://github.com/llvm/llvm-project/releases/download/llvmorg-$LLVM_VERSION/$llvm.tar.zst" "$LLVM_SHA256"
fetch "https://musl.libc.org/releases/musl-$MUSL_VERSION.tar.gz" "$MUSL_SHA256"
fetch "https://github.com/openssl/openssl/releases/download/openssl-$OPENSSL_VERSION/openssl-$OPENSSL_VERSION.tar.gz" "$OPENSSL_SHA256"

echo "Unpacking $llvm"
top=$(tar --zstd -tf "$cache/$llvm.tar.zst" | head -n1 | cut -d/ -f1)
# LLVM's own headers, documentation and static libraries are most of it.
tar --zstd -xf "$cache/$llvm.tar.zst" -C "$work" --no-wildcards-match-slash \
	--exclude="$top/include" --exclude="$top/share" --exclude="$top/lib/*.a"
src=$work/$top
tc=$sdk/toolchain
mkdir -p "$tc/bin" "$tc/lib"
for f in clang clang-[0-9]* ld.lld lld llvm-ar llvm-ranlib; do
	cp -P "$src/bin/"$f "$tc/bin/"
done
ln -sf llvm-ar "$tc/bin/ar"
ln -sf llvm-ranlib "$tc/bin/ranlib"

# The resource folder: clang's own headers and compiler-rt. The builtins
# don't depend on the C library, so the musl target shares them.
res=lib/clang/$(echo "$LLVM_VERSION" | cut -d. -f1)
mkdir -p "$tc/$res/lib"
cp -R "$src/$res/include" "$tc/$res/"
cp -R "$src/$res/lib/x86_64-unknown-linux-gnu" "$tc/$res/lib/"
cp -R "$src/$res/lib/x86_64-unknown-linux-gnu" "$tc/$res/lib/x86_64-unknown-linux-musl"

# Shared libraries the tools need beyond glibc, such as libLLVM and
# libstdc++, go to toolchain/lib, which the tools find through their
# RPATH. glibc itself stays the host's.
for bin in "$tc/bin/"*; do
	[ -L "$bin" ] && continue
	ldd "$bin" | awk '$2 == "=>" && $3 ~ /^\// { print $3 }'
done | sort -u | while read -r lib; do
	case $(basename "$lib") in
	libc.so.* | libm.so.* | libpthread.so.* | libdl.so.* | librt.so.* | ld-linux-*) continue ;;
	esac
	cp -L "$lib" "$tc/lib/"
	case $(realpath "$lib") in "$src"/*) ;; *) echo "$lib" >>"$work/host-libs" ;; esac
done
for f in "$tc/bin/"* "$tc/lib/"*; do
	[ -L "$f" ] && continue
	"$src/bin/llvm-strip" --strip-unneeded "$f" 2>/dev/null || true
	case $f in "$tc/bin/"*) patchelf --set-rpath '$ORIGIN/../lib' "$f" ;; esac
done

sysroot=$tc/sysroot
echo "Building musl $MUSL_VERSION"
tar -xzf "$cache/musl-$MUSL_VERSION.tar.gz" -C "$work"
(
	cd "$work/musl-$MUSL_VERSION"
	./configure --prefix="$sysroot/usr" --syslibdir="$sysroot/lib" --disable-shared \
		CC="$tc/bin/clang --target=x86_64-unknown-linux-musl" \
		AR="$tc/bin/llvm-ar" RANLIB="$tc/bin/llvm-ranlib" >/dev/null
	make -j"$(nproc)" >/dev/null
	make install >/dev/null
)

# From here on, plain `clang` builds static musl programs.
cat >"$tc/bin/clang.cfg" <<'EOF'
--target=x86_64-unknown-linux-musl
--sysroot=<CFGDIR>/../sysroot
-static
-fuse-ld=lld
--rtlib=compiler-rt
--unwindlib=none
EOF

echo "Building OpenSSL $OPENSSL_VERSION"
tar -xzf "$cache/openssl-$OPENSSL_VERSION.tar.gz" -C "$work"
# The kernel's API headers, needed only while OpenSSL is built.
kernel=$work/kernel-headers
mkdir -p "$kernel"
cp -R /usr/include/linux /usr/include/asm-generic "$kernel/"
cp -R /usr/include/x86_64-linux-gnu/asm "$kernel/"
(
	cd "$work/openssl-$OPENSSL_VERSION"
	./Configure linux-x86_64 no-shared no-module no-dso no-engine no-async no-afalgeng \
		no-tests no-docs --prefix="$sysroot/usr" --libdir=lib --openssldir=/etc/ssl \
		CC="$tc/bin/clang" AR="$tc/bin/llvm-ar" RANLIB="$tc/bin/llvm-ranlib" \
		CFLAGS="-O2 -idirafter $kernel" >/dev/null
	make -j"$(nproc)" build_libs >/dev/null
	make install_dev >/dev/null
)
rm -rf "$sysroot/usr/lib/pkgconfig" "$sysroot/usr/lib/cmake"

echo "Checking the toolchain"
cat >"$work/check.c" <<'EOF'
#include <math.h>
#include <openssl/evp.h>
#include <pthread.h>
#include <stdio.h>

int main(void) {
    printf("%.3f %d\n", sqrt(2.0), EVP_MD_fetch(NULL, "SHA256", NULL) != NULL);
    return 0;
}
EOF
"$tc/bin/clang" -O2 -pthread -o "$work/check" "$work/check.c" -lcrypto -lm
[ "$("$work/check")" = "1.414 1" ]
if ldd "$work/check" >/dev/null 2>&1; then
	echo "error: the toolchain built a dynamic executable" >&2
	exit 1
fi

notice "LLVM (clang, lld and compiler-rt), in toolchain/ (Apache-2.0 WITH LLVM-exception)" "$here/licenses/llvm.txt"
notice "musl libc, in toolchain/sysroot and linked into Linux programs (MIT)" "$work/musl-$MUSL_VERSION/COPYRIGHT"
notice "OpenSSL libcrypto, in toolchain/sysroot and linked into programs that import vex:crypto (Apache-2.0)" \
	"$work/openssl-$OPENSSL_VERSION/LICENSE.txt"
# Libraries copied from the build machine, such as libstdc++, with the
# notices of the packages they come from.
touch "$work/host-libs"
while read -r lib; do
	pkg=$({ dpkg -S "$lib" || dpkg -S "$(realpath "$lib")" || dpkg -S "/usr$lib"; } 2>/dev/null | head -n1 | cut -d: -f1)
	if [ -n "$pkg" ] && [ -f "/usr/share/doc/$pkg/copyright" ]; then
		notice "$(basename "$lib"), in toolchain/lib, from the $pkg package" "/usr/share/doc/$pkg/copyright"
	else
		echo "error: no license found for $lib" >&2
		exit 1
	fi
done <"$work/host-libs"
toolchain_json="{ \"name\": \"llvm\", \"clang\": \"$LLVM_VERSION\", \"libc\": \"musl $MUSL_VERSION\", \"openssl\": \"$OPENSSL_VERSION\" }"
