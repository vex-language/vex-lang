# Memory management

Status: stage 1 done (V-107).

## Decision

Vex uses a **tracing garbage collector**, like Go, Dart and Kotlin/Native, rather than reference counting like Swift.

- **UI code is full of reference cycles.** A button stores an `onTap` closure, the closure uses the screen, and the screen holds the button. Reference counting leaks this unless one side is marked `weak`. A tracing collector frees the whole cycle, so Vex needs no `weak` keyword.
- **Declarative UIs make many short-lived objects.** A view tree rebuilt on every state change suits an allocator where allocating is a free-list pop and dead objects cost nothing to free. Reference counting would add work to every copy and every scope exit.
- **Speed matches Go.** Allocation-heavy and compute-heavy benchmarks run as fast as the same programs in Go (see below).
- **Native widgets work with either model.** The bridge will keep a handle table. A Vex object holding a native widget releases it from a finalizer, and a Vex closure held by a native widget stays alive until the widget lets go.
- **Precedent.** Kotlin/Native started with reference counting and moved to a tracing collector in 2021 because the old model was painful to use. Dart/Flutter and React Native both use collectors and run at 60–120 fps.

Trade-offs: memory isn't freed the moment it becomes unreachable, and collections pause the program briefly. Files and other resources will need an explicit `close()` or a scope-exit block.

## Stage 1: mark and sweep (current)

The collector is part of the runtime (`compiler/codegen/runtime/vexrt.h`); the compiler only calls `vx_gc_init` at the start of `main`.

- **Allocation.** Objects up to 2 KB come from 64 KB pages that each hold one of 24 size classes, and are taken from a free list per class. Larger objects get their own allocation. String text is allocated as *atomic*: it never holds pointers, so it is never scanned.
- **Finding objects.** A hash table maps a page number to its page, so any word can be checked with a shift and one lookup. Large objects are found by binary search.
- **Marking.** Roots are the C stack and the registers, which are spilled with `setjmp`. Marking is conservative: any word that points into an object keeps it alive, including pointers into the middle of an object. Marked objects are scanned the same way, using an explicit work list rather than recursion.
- **Sweeping.** Unmarked objects go back onto the free lists. Pages with no live objects are reused for any size class, and unmarked large objects are freed.
- **Pacing.** A collection runs when the heap reaches twice the live size after the last collection, and never below 4 MB. This matches Go's default `GOGC=100`.

Conservative marking can keep a dead object alive when an unrelated number happens to look like its address. It never frees a live object.

### Environment variables

| Variable | Effect |
|---|---|
| `VEX_GC_STATS=1` | Prints collections, total bytes allocated, the peak heap and the live heap at exit |
| `VEX_GC_STRESS=N` | Collects every N allocations; `1` collects on every one. The tests run every program this way to catch objects freed too early |

### Benchmarks

On an Apple Silicon Mac, best of several runs, against the same program written in Go:

| Program | Vex | Go |
|---|---|---|
| 2 million iterations allocating structs, enums, closures, strings and lists (530 MB allocated in total) | 0.33 s, 6 MB peak memory | 0.35 s, 9 MB |
| `fib(38)` plus a sieve over 20 million booleans | 0.17 s | 0.16 s |

Before the collector, the first program leaked all 387 MB it allocated.

## Next stages

1. **Precise roots and objects.** The compiler records where pointers live in each struct, enum and stack frame, so nothing is kept alive by accident and scanning skips plain numbers.
2. **Generational collection.** Collect young objects often and cheaply, so UI frames never wait for a full collection.
3. **Returning memory to the OS**, and finalizers for native handles once the UI bridge exists.
4. **Threads**, when Vex gets them: per-thread allocation caches and stopping threads for a collection.
