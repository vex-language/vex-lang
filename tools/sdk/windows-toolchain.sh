# Adds LLVM-MinGW (UCRT, x86-64 host) to the SDK at $sdk/toolchain, keeping
# what builds x86-64 Windows programs. Sourced by build.sh.

mingw=llvm-mingw-$LLVM_MINGW_VERSION-ucrt-x86_64
fetch "https://github.com/mstorsjo/llvm-mingw/releases/download/$LLVM_MINGW_VERSION/$mingw.zip" "$LLVM_MINGW_SHA256"
echo "Unpacking $mingw"
unzip -q "$cache/$mingw.zip" -d "$work"
mv "$work/$mingw" "$sdk/toolchain"
(
	cd "$sdk/toolchain"
	# Other target architectures, the debugger with its Python, and editor
	# tools: none of them builds x86-64 programs.
	rm -rf aarch64-w64-mingw32 armv7-w64-mingw32 i686-w64-mingw32 python
	rm -f bin/aarch64-* bin/armv7-* bin/arm64ec-* bin/i686-* \
		bin/lldb* bin/liblldb.dll bin/libpython*.dll bin/libffi-*.dll \
		bin/clangd.exe bin/clang-tidy.exe bin/run-clang-tidy
	rm -f lib/clang/*/lib/windows/*-i386.* lib/clang/*/lib/windows/*-arm.a \
		lib/clang/*/lib/windows/*-aarch64.a
)
notice "LLVM, in toolchain/ (Apache-2.0 WITH LLVM-exception)" "$sdk/toolchain/LICENSE.TXT"
notice "MinGW-w64 runtime and headers, in toolchain/ and linked into Windows programs" "$here/licenses/mingw-w64.txt"
toolchain_json="{ \"name\": \"llvm-mingw\", \"version\": \"$LLVM_MINGW_VERSION\", \"clang\": \"$LLVM_VERSION\", \"crt\": \"ucrt\" }"
