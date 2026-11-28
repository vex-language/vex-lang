#!/bin/sh
# Builds every benchmark in Vex and in Go, checks that both print the same
# result, times them and prints a Markdown table.
#
# Usage: bench/run.sh [-n runs] [--json file] [name ...]
set -eu

usage() {
	echo "Usage: bench/run.sh [-n runs] [--json file] [name ...]" >&2
	exit 2
}

runs=5
json=
while [ $# -gt 0 ]; do
	case $1 in
	-n)
		[ $# -ge 2 ] || usage
		runs=$2
		shift 2
		;;
	-n*)
		runs=${1#-n}
		shift
		;;
	--json)
		[ $# -ge 2 ] || usage
		json=$2
		shift 2
		;;
	--json=*)
		json=${1#--json=}
		shift
		;;
	--)
		shift
		break
		;;
	-*) usage ;;
	*) break ;;
	esac
done
case $runs in
'' | *[!0-9]* | 0)
	echo "bench: -n needs a number above 0" >&2
	exit 2
	;;
esac
if [ -n "$json" ]; then
	case $json in
	/*) ;;
	*) json=$(pwd)/$json ;;
	esac
fi

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
"$root/build.sh" >&2
vex=$root/bin/vex

for name in "$@"; do
	echo "Building $name" >&2
	"$vex" build "$bench/$name/main.vx" -o "$work/$name-vex" >/dev/null
	(cd "$root" && go build -o "$work/$name-go" "./bench/$name")
	"$work/$name-vex" >"$work/$name-vex.out"
	"$work/$name-go" >"$work/$name-go.out"
	if ! cmp -s "$work/$name-vex.out" "$work/$name-go.out"; then
		echo "bench: $name prints different results in Vex and Go:" >&2
		diff "$work/$name-vex.out" "$work/$name-go.out" >&2 || true
		exit 1
	fi
done

if command -v hyperfine >/dev/null 2>&1; then
	timer=hyperfine
else
	timer=loop
fi

# Prints the current time in milliseconds.
now_ms() {
	if command -v perl >/dev/null 2>&1; then
		perl -MTime::HiRes=time -e 'printf "%.3f\n", time * 1000'
	else
		python3 -c 'import time; print("%.3f" % (time.time() * 1000))'
	fi
}

# Times the program at $1 over $runs runs and prints its median, minimum
# and standard deviation in ms, separated by spaces.
times_ms() {
	if [ "$timer" = hyperfine ]; then
		hyperfine -N -w 1 -r "$runs" --export-csv "$work/times.csv" "$1" >/dev/null 2>&1
		# Hyperfine 1 writes columns such as "median" in seconds; hyperfine 2
		# writes "time_wall_clock_median" with its unit in another column.
		awk -F, 'NR == 1 {
			for (i = 1; i <= NF; i++) {
				if ($i == "median" || $i == "time_wall_clock_median") med = i
				if ($i == "min" || $i == "time_wall_clock_min") min = i
				if ($i == "stddev" || $i == "time_wall_clock_stddev") sd = i
				if ($i == "time_wall_clock_unit") u = i
			}
			next
		}
		NR == 2 {
			unit = u ? $u : "s"
			scale = unit == "ms" ? 1 : unit == "us" ? 0.001 : unit == "ns" ? 0.000001 : 1000
			printf "%.1f %.1f %.1f\n", $med * scale, $min * scale, $sd * scale
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
		echo "$end - $start" | awk '{ printf "%.3f\n", $1 - $3 }' >>"$work/times.txt"
		i=$((i + 1))
	done
	sort -n "$work/times.txt" | awk '{ t[NR] = $1; sum += $1 } END {
		med = NR % 2 ? t[(NR + 1) / 2] : (t[NR / 2] + t[NR / 2 + 1]) / 2
		mean = sum / NR
		for (i = 1; i <= NR; i++) ss += (t[i] - mean) ^ 2
		sd = NR > 1 ? sqrt(ss / (NR - 1)) : 0
		printf "%.1f %.1f %.1f\n", med, t[1], sd
	}'
}

# Prints the peak resident memory of the program at $1 in bytes, or
# nothing when /usr/bin/time can't measure it.
peak_bytes() {
	if [ "$(uname)" = Darwin ]; then
		/usr/bin/time -l "$1" 2>&1 >/dev/null |
			awk '/maximum resident set size/ { print $1 }'
	elif /usr/bin/time -v true >/dev/null 2>&1; then
		/usr/bin/time -v "$1" 2>&1 >/dev/null |
			awk '/Maximum resident set size/ { print $NF * 1024 }'
	fi
}

# Prints a size in bytes as MB, or "-" when it is empty.
mb() {
	if [ -n "$1" ]; then
		echo "$1" | awk '{ printf "%.1f\n", $1 / 1048576 }'
	else
		echo -
	fi
}

# Prints $1 as a JSON string.
json_str() {
	printf '%s' "$1" | awk 'BEGIN { ORS = "" } {
		gsub(/\\/, "\\\\"); gsub(/"/, "\\\""); gsub(/\t/, "\\t")
		if (NR > 1) print "\\n"
		print
	} END { print "\n" }' | sed 's/^/"/; s/$/"/'
}

# Prints the program's numbers as a JSON object: the median, minimum and
# standard deviation in ms, then the binary size and peak memory in bytes.
# The deviation is null for a single run, and the memory when unknown.
json_run() {
	printf '{"median_ms": %s, "min_ms": %s, "stddev_ms": %s, "binary_bytes": %s, "peak_rss_bytes": %s}' \
		"$1" "$2" "$(if [ "$runs" -gt 1 ]; then echo "$3"; else echo null; fi)" "$4" "${5:-null}"
}

if [ "$timer" = hyperfine ]; then
	echo "Timing $runs runs of each with hyperfine" >&2
else
	echo "Timing $runs runs of each with a shell loop" >&2
fi
echo
echo "| Benchmark | Vex ms | Go ms | Vex/Go | Vex KB | Go KB | Vex MB RSS | Go MB RSS |"
echo "|---|---:|---:|---:|---:|---:|---:|---:|"
: >"$work/entries.json"
for name in "$@"; do
	read -r vex_med vex_min vex_sd <<EOF
$(times_ms "$work/$name-vex")
EOF
	read -r go_med go_min go_sd <<EOF
$(times_ms "$work/$name-go")
EOF
	vex_size=$(wc -c <"$work/$name-vex" | tr -d ' ')
	go_size=$(wc -c <"$work/$name-go" | tr -d ' ')
	vex_rss=$(peak_bytes "$work/$name-vex")
	go_rss=$(peak_bytes "$work/$name-go")
	ratio=$(echo "$vex_med $go_med" | awk '{ if ($2 > 0) printf "%.2f\n", $1 / $2; else print "-" }')
	echo "| $name | $vex_med | $go_med | $ratio | $((vex_size / 1024)) | $((go_size / 1024)) | $(mb "$vex_rss") | $(mb "$go_rss") |"

	if [ -n "$json" ]; then
		title=$(awk -F '\t' -v id="$name" '$1 == id { print $2 }' "$bench/benchmarks.tsv")
		detail=$(awk -F '\t' -v id="$name" '$1 == id { print $3 }' "$bench/benchmarks.tsv")
		[ -s "$work/entries.json" ] && echo "," >>"$work/entries.json"
		{
			printf '    {\n'
			printf '      "id": %s,\n' "$(json_str "$name")"
			printf '      "title": %s,\n' "$(json_str "${title:-$name}")"
			printf '      "detail": %s,\n' "$(json_str "$detail")"
			printf '      "checksum": %s,\n' "$(json_str "$(tail -n 1 "$work/$name-vex.out")")"
			printf '      "vex": %s,\n' "$(json_run "$vex_med" "$vex_min" "$vex_sd" "$vex_size" "$vex_rss")"
			printf '      "go": %s\n' "$(json_run "$go_med" "$go_min" "$go_sd" "$go_size" "$go_rss")"
			printf '    }'
		} >>"$work/entries.json"
	fi
done

if [ -z "$json" ]; then
	exit 0
fi

PATH=$PATH:/usr/sbin:/sbin
if [ "$(uname)" = Darwin ]; then
	cpu=$(sysctl -n machdep.cpu.brand_string)
	cores=$(sysctl -n hw.ncpu)
	memory=$(sysctl -n hw.memsize)
	os="$(sw_vers -productName) $(sw_vers -productVersion)"
else
	cpu=$(awk -F ': ' '/^model name/ { print $2; exit }' /proc/cpuinfo)
	cores=$(nproc 2>/dev/null || getconf _NPROCESSORS_ONLN)
	memory=$(awk '/^MemTotal/ { print $2 * 1024 }' /proc/meminfo)
	os=$(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME" || uname -sr)
fi
os="$os ($(uname -m))"
[ -n "$cpu" ] || cpu=$(uname -m)

if [ -n "${CC:-}" ]; then
	ccname=${CC%% *}
elif command -v clang >/dev/null 2>&1; then
	ccname=clang
elif command -v cc >/dev/null 2>&1; then
	ccname=cc
else
	ccname=gcc
fi

plural=s
[ "$runs" -eq 1 ] && plural=
if [ "$timer" = hyperfine ]; then
	method="Median wall time of $runs run$plural after one warm-up run, timed with $(hyperfine --version)."
else
	method="Median wall time of $runs run$plural after one warm-up run, timed with a shell loop."
fi

{
	printf '{\n'
	printf '  "date": "%s",\n' "$(date +%Y-%m-%d)"
	printf '  "machine": {"cpu": %s, "cores": %s, "memory_bytes": %s, "os": %s},\n' \
		"$(json_str "$cpu")" "$cores" "$memory" "$(json_str "$os")"
	printf '  "method": %s,\n' "$(json_str "$method")"
	printf '  "tools": {"vex": %s, "go": %s, "c_compiler": %s},\n' \
		"$(json_str "$("$vex" version)")" \
		"$(json_str "$(go version | awk '{ print $3 }')")" \
		"$(json_str "$($ccname --version 2>/dev/null | head -n 1)")"
	printf '  "benchmarks": [\n'
	cat "$work/entries.json"
	printf '\n  ]\n'
	printf '}\n'
} >"$json"
echo "Wrote $json" >&2
