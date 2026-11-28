# Benchmarks

Small, classic programs written twice, in Vex (`main.vx`) and in Go (`main.go`), doing the same work and printing the same result. They show roughly where Vex stands against a mature compiled language with a garbage collector, and catch performance regressions in the compiler and runtime.

## Running

```sh
bench/run.sh                 # every benchmark, 5 runs each
bench/run.sh fib nbody       # only these
bench/run.sh -n 10 sort      # 10 runs
```

The script builds the compiler into `bin/vex` with `build.sh`, builds each Vex program with `vex build` (optimized with `-O3`, the default; `--debug` is the unoptimized mode) and each Go program with `go build`, and runs both once to check that they print exactly the same output. It stops with a diff if they don't. It then times each program and prints a Markdown table:

| Column | Meaning |
|---|---|
| Vex ms, Go ms | Median wall time of the runs |
| Vex/Go | Vex time divided by Go time: below 1 means Vex was faster |
| Vex KB, Go KB | Size of the executable |
| Vex MB RSS, Go MB RSS | Peak resident memory of one run, from `/usr/bin/time` |

It needs Go and a C compiler. With [hyperfine](https://github.com/sharkdp/hyperfine) installed it does the timing, after one warm-up run; otherwise a shell loop does, using `perl` or `python3` for the clock. Peak memory uses `/usr/bin/time -l` on macOS and `/usr/bin/time -v` (GNU time) on Linux, and shows `-` when that isn't available.

`--json FILE` also writes the results as JSON, for publishing: the date, the machine (CPU, cores, memory, system), the method, the versions of `vex`, Go and the C compiler, and for each benchmark its id, title and description from [benchmarks.tsv](benchmarks.tsv), the line it prints, and for Vex and Go the median, minimum and standard deviation in ms, the executable's size and the peak memory in bytes (`null` when unknown):

```sh
bench/run.sh --json results.json
```

Each program takes its problem size as an optional first argument, so one can also be run by hand:

```sh
go run ./cmd/vex run bench/nbody/main.vx -- 1000
go run ./bench/nbody 1000
```

`go test ./bench` builds every Vex program and runs it at a tiny size, checking the result it prints, so the suite keeps compiling as the language changes. It takes a few seconds.

## What each one measures

| Benchmark | Work | Exercises |
|---|---|---|
| `fib` | Recursive Fibonacci of 40 | Function calls, int arithmetic with overflow checks |
| `nbody` | 10 million steps of the Jovian planets | Float math on struct fields |
| `spectral-norm` | Spectral norm of a 3000 × 3000 matrix | Float math, list indexing |
| `binary-trees` | Builds and walks binary trees up to depth 17 | Allocation and garbage collection |
| `fannkuch` | Fannkuch-redux of 10 | Swapping list elements in place |
| `mandelbrot` | Mandelbrot set on a 3000 × 3000 grid | Tight float loops |
| `strings` | Builds, joins, splits, replaces and searches strings | Interpolation, string methods, `+` |
| `maps` | 2 million inserts, 4 million lookups and 1 million removals, then word counts | Hash maps with int and string keys |
| `sort` | Sorts 3 million ints and 750,000 strings | `sort()`, `sorted()` |
| `json` | Encodes 1000 records to JSON and decodes them, 200 times | `vex:json` against `encoding/json` |
| `tasks` | A binary tree of 2 million async tasks, each awaiting the one it started | Task creation and `await` against goroutines and channels |
| `generators` | Filters, maps and sums 10 million values from an endless generator | Generators and lazy `Iterable` helpers against `iter.Seq` |

## Caveats

- These are small programs, each stressing one thing. They say little about a real app, whose time often goes to I/O, the UI and libraries.
- Timings move with the machine's load, temperature and power settings. Compare numbers taken on the same machine, close together, and rerun with more runs when a difference is small.
- Each version is written the plain, idiomatic way in its language, not tuned: no unsafe code, no hand-written SIMD, and no parallelism except where noted.
- Vex checks int overflow and list bounds; Go checks bounds but wraps on overflow. Hashes use the wrapping operators `&+` and `&*` in Vex, to compute the same numbers as Go.
- Vex's async tasks run on one thread, so `tasks` limits Go to one thread too (`GOMAXPROCS(1)`). It measures the cost of starting and awaiting tasks, not parallelism.
- `sort` uses Vex's stable sort and Go's `slices.Sort`, which isn't stable; with ints and strings the results are the same.
- Go's executables carry Go's runtime and aren't stripped. Vex's carry their small C runtime and use the system's C library.
- Results that print floats are rounded to 9 places. Compilers may fuse a multiply and an add into one instruction, which changes the last bits; at these sizes it doesn't change the printed digits on the machines tested.

## One measurement

On an Apple M3 (8 cores, 16 GB), macOS 26.4, Apple clang 21, Go 1.23.2, on 2026-11-28, with `bench/run.sh` and hyperfine. Other work was running on the machine at the time, so treat the numbers as rough.

| Benchmark | Vex ms | Go ms | Vex/Go | Vex KB | Go KB | Vex MB RSS | Go MB RSS |
|---|---:|---:|---:|---:|---:|---:|---:|
| binary-trees | 139.1 | 746.8 | 0.19 | 69 | 2207 | 22.2 | 21.9 |
| fannkuch | 221.1 | 192.0 | 1.15 | 70 | 2207 | 1.5 | 3.5 |
| fib | 378.5 | 575.0 | 0.66 | 69 | 2206 | 1.5 | 3.6 |
| generators | 721.3 | 132.3 | 5.45 | 72 | 2223 | 1.5 | 3.5 |
| json | 341.6 | 225.8 | 1.51 | 88 | 2722 | 22.2 | 9.7 |
| mandelbrot | 354.0 | 424.5 | 0.83 | 69 | 2206 | 1.5 | 3.5 |
| maps | 414.1 | 479.0 | 0.86 | 69 | 2207 | 195.1 | 156.7 |
| nbody | 439.6 | 501.4 | 0.88 | 69 | 2208 | 1.5 | 3.7 |
| sort | 681.6 | 449.9 | 1.52 | 69 | 2242 | 145.3 | 61.6 |
| spectral-norm | 492.9 | 493.7 | 1.00 | 69 | 2206 | 1.8 | 3.6 |
| strings | 548.0 | 508.5 | 1.08 | 69 | 2223 | 12.7 | 9.7 |
| tasks | 181.6 | 622.9 | 0.29 | 70 | 2207 | 7.4 | 127.6 |

## Adding a benchmark

Make a folder with `main.vx` and `main.go` that print the same lines, ending with a `name(size) = result` line, and take the size as an optional first argument. Add a line to `benchmarks.tsv` with its id, title and description, separated by tabs, and add the folder to the `small` table in `bench_test.go` with a tiny size and the line the Go version prints at that size.
