# Memory management

Status: stage 1 done (V-107); generational collection done (part of V-112).

## Decision

Vex uses a **tracing garbage collector**, like Go, Dart and Kotlin/Native, rather than reference counting like Swift.

- **UI code is full of reference cycles.** A button stores an `onTap` closure, the closure uses the screen, and the screen holds the button. Reference counting leaks this unless one side is marked `weak`. A tracing collector frees the whole cycle, so Vex needs no `weak` keyword.
- **Declarative UIs make many short-lived objects.** A view tree rebuilt on every state change suits an allocator where allocating is a free-list pop and dead objects cost nothing to free. Reference counting would add work to every copy and every scope exit.
- **Speed matches Go.** Allocation-heavy and compute-heavy benchmarks run as fast as the same programs in Go (see below).
- **Native widgets work with either model.** The bridge will keep a handle table. A Vex object holding a native widget releases it from a finalizer, and a Vex closure held by a native widget stays alive until the widget lets go.
- **Precedent.** Kotlin/Native started with reference counting and moved to a tracing collector in 2021 because the old model was painful to use. Dart/Flutter and React Native both use collectors and run at 60–120 fps.

Trade-offs: memory isn't freed the moment it becomes unreachable, and collections pause the program briefly. Files and other resources will need an explicit `close()` or a scope-exit block.

## The collector (current)

The collector is part of the runtime (`compiler/codegen/runtime/vexrt.h`); the compiler calls `vx_gc_init` at the start of `main` and emits write barriers.

- **Allocation.** Objects up to 2 KB come from 64 KB pages that each hold one of 24 size classes. An object is taken from its class's free list, or else from the page the class is filling in order. Allocations of up to 128 bytes are inlined where they happen. Larger objects get their own allocation. String text, and lists of numbers and bools, are allocated as *atomic*: they never hold pointers, so they are never scanned.
- **Layout.** An optional struct or enum is a pointer that is NULL for nil, since those values are never NULL; other optionals carry a flag.
- **Finding objects.** A hash table maps a page number to its page, so any word can be checked with a shift and one lookup. Large objects are found by binary search.
- **Marking.** Roots are the C stack and the registers, which are spilled with `setjmp`. Marking is conservative: any word that points into an object keeps it alive, including pointers into the middle of an object. Marked objects are scanned the same way, using an explicit work list rather than recursion.
- **Generations.** Marks stay set after a collection, so the objects that survived are old. A *minor* collection marks only from the roots and from old objects written since the last collection, and stops at anything already marked, so it doesn't trace long-lived data again. A *major* collection clears every mark and traces everything.
- **Write barrier.** After the compiled code or the runtime stores a pointer into an object (a field, a list element, a map entry, a captured variable), it records the object in the remembered set, which the next minor collection scans again. Plain local variables are roots and need no barrier, and neither do stores of numbers and bools. Writing one object repeatedly records it once; when the set fills, it is compacted to one entry per object.
- **Sweeping.** Unmarked objects go back onto the free lists. Pages with no live objects are reused for any size class, pages whose objects are all live are skipped, and unmarked large objects are freed.
- **Pacing.** A minor collection runs once the young objects reach the size of the heap after the last collection, and never below 4 MB. A major one runs instead once the old objects have doubled since the last major collection.

Conservative marking can keep a dead object alive when an unrelated number happens to look like its address. It never frees a live object.

### Environment variables

| Variable | Effect |
|---|---|
| `VEX_GC_STATS=1` | Prints collections, total bytes allocated, the peak heap and the live heap at exit |
| `VEX_GC_STRESS=N` | Collects every N allocations; `1` collects on every one, and every fourth collection is major. The tests run every program this way to catch objects freed too early |
| `VEX_GC_VERIFY=1` | After each minor collection, checks that no marked object points to an unmarked one, which would mean a write barrier is missing. The tests set it along with `VEX_GC_STRESS` |

### Benchmarks

On an Apple Silicon Mac, best of several runs, against the same program written in Go:

| Program | Vex | Go |
|---|---|---|
| 2 million iterations allocating structs, enums, closures, strings and lists (530 MB allocated in total) | 0.33 s, 6 MB peak memory | 0.35 s, 9 MB |
| `fib(38)` plus a sieve over 20 million booleans | 0.17 s | 0.16 s |

Before the collector, the first program leaked all 387 MB it allocated.

Binary trees of depth 18 (about 67 million short-lived nodes beside one long-lived tree) take 0.26 s and 42 MB: Kotlin on the JVM takes 0.28 s and 438 MB, Dart 0.35 s and 78 MB, Go 1.26 s and 45 MB. Minor collections are what make the difference: before them it took 0.45 s.

## Next stages

1. **Precise roots and objects.** The compiler records where pointers live in each struct, enum and stack frame, so nothing is kept alive by accident and scanning skips plain numbers.
2. **Returning memory to the OS**, and finalizers for native handles once the UI bridge exists.
3. **Threads**, when Vex gets them: per-thread allocation caches and stopping threads for a collection.
