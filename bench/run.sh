#!/bin/sh
# Builds every benchmark in Vex and in Go, checks that both print the same
# result, times them and prints a Markdown table.
#
# Usage: bench/run.sh [-n runs] [name ...]
set -eu

runs=5
while getopts n:h opt; do
	case $opt in
	n) runs=$OPTARG ;;
	*)
		echo "Usage: bench/run.sh [-n runs] [name ...]" >&2
		exit 2
		;;
	esac
done
shift $((OPTIND - 1))
case $runs in
'' | *[!0-9]* | 0)
	echo "bench: -n needs a number above 0" >&2
	exit 2
	;;
esac

bench=$(cd "$(dirname "$0")" && pwd)
root=$(dirname "$bench")
work=$(mktemp -d "${TMPDIR:-/tmp}/vex-bench.XXXXXX")
trap 'rm -rf "$work"' EXIT

if [ $# -eq 0 ]; then
	for dir in "$bench"/*/; do
		[ -f "$dir/main.vx" ] && set -- "$@" "$(basename "$dir")"
	done
fi
for name in "$@"; do
	if [ ! -f "$bench/$name/main.vx" ] || [ ! -f "$bench/$name/main.go" ]; then
		echo "bench: no benchmark named '$name'" >&2
		exit 2
	fi
done

echo "Building the compiler" >&2
(cd "$root" && go build -o "$work/vex" ./cmd/vex)

for name in "$@"; do
	echo "Building $name" >&2
	"$work/vex" build "$bench/$name/main.vx" -o "$work/$name-vex" >/dev/null
	(cd "$root" && go build -o "$work/$name-go" "./bench/$name")
	"$work/$name-vex" >"$work/$name-vex.out"
	"$work/$name-go" >"$work/$name-go.out"
	if ! cmp -s "$work/$name-vex.out" "$work/$name-go.out"; then
		echo "bench: $name prints different results in Vex and Go:" >&2
		diff "$work/$name-vex.out" "$work/$name-go.out" >&2 || true
		exit 1
	fi
done

# Prints the current time in milliseconds.
now_ms() {
	if command -v perl >/dev/null 2>&1; then
		perl -MTime::HiRes=time -e 'printf "%.3f\n", time * 1000'
	else
		python3 -c 'import time; print("%.3f" % (time.time() * 1000))'
	fi
}

# Prints the median wall time of the program at $1 over $runs runs, in ms.
median_ms() {
	if command -v hyperfine >/dev/null 2>&1; then
		hyperfine -N -w 1 -r "$runs" --export-csv "$work/times.csv" "$1" >/dev/null 2>&1
		# Hyperfine 1 writes a "median" column in seconds; hyperfine 2 writes
		# "time_wall_clock_median" with its unit in another column.
		awk -F, 'NR == 1 {
			for (i = 1; i <= NF; i++) {
				if ($i == "median" || $i == "time_wall_clock_median") m = i
				if ($i == "time_wall_clock_unit") u = i
			}
			next
		}
		NR == 2 {
			unit = u ? $u : "s"
			scale = unit == "ms" ? 1 : unit == "us" ? 0.001 : unit == "ns" ? 0.000001 : 1000
			printf "%.1f\n", $m * scale
		}' "$work/times.csv"
		return
	fi
	"$1" >/dev/null
	: >"$work/times.txt"
	i=0
	while [ "$i" -lt "$runs" ]; do
		start=$(now_ms)
		"$1" >/dev/null
		end=$(now_ms)
		echo "$end - $start" | awk '{ printf "%.1f\n", $1 - $3 }' >>"$work/times.txt"
		i=$((i + 1))
	done
	sort -n "$work/times.txt" | awk '{ t[NR] = $1 } END {
		if (NR % 2) printf "%.1f\n", t[(NR + 1) / 2]
		else printf "%.1f\n", (t[NR / 2] + t[NR / 2 + 1]) / 2
	}'
}

# Prints the peak resident memory of the program at $1 in MB, or "-" when
# /usr/bin/time can't measure it.
peak_mb() {
	if [ "$(uname)" = Darwin ]; then
		/usr/bin/time -l "$1" 2>&1 >/dev/null |
			awk '/maximum resident set size/ { printf "%.1f\n", $1 / 1048576; found = 1 } END { if (!found) print "-" }'
	elif /usr/bin/time -v true >/dev/null 2>&1; then
		/usr/bin/time -v "$1" 2>&1 >/dev/null |
			awk '/Maximum resident set size/ { printf "%.1f\n", $NF / 1024; found = 1 } END { if (!found) print "-" }'
	else
		echo -
	fi
}

# Prints the size of the file at $1 in KB.
size_kb() {
	wc -c <"$1" | awk '{ printf "%d\n", $1 / 1024 }'
}

if command -v hyperfine >/dev/null 2>&1; then
	timer=hyperfine
else
	timer="a shell loop"
fi
echo "Timing $runs runs of each with $timer" >&2
echo
echo "| Benchmark | Vex ms | Go ms | Vex/Go | Vex KB | Go KB | Vex MB RSS | Go MB RSS |"
echo "|---|---:|---:|---:|---:|---:|---:|---:|"
for name in "$@"; do
	vex_ms=$(median_ms "$work/$name-vex")
	go_ms=$(median_ms "$work/$name-go")
	ratio=$(echo "$vex_ms $go_ms" | awk '{ if ($2 > 0) printf "%.2f\n", $1 / $2; else print "-" }')
	echo "| $name | $vex_ms | $go_ms | $ratio | $(size_kb "$work/$name-vex") | $(size_kb "$work/$name-go") | $(peak_mb "$work/$name-vex") | $(peak_mb "$work/$name-go") |"
done
