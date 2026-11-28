# Memory management

Status: stage 1 done (V-107); stage 2, precise object layouts, done (V-111); generational collection done (part of V-112).

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
- **Object layouts.** Every allocation passes the layout of its words: a 64-bit mask with a bit for each 8-byte word that may hold a pointer, repeated for each element of an array. The compiler emits one as a C constant for each struct, enum case, record, optional, closure environment, captured variable and task or generator argument block, built with `offsetof` so the C compiler places the fields; lists, maps, sets and futures get theirs from the element, key and value types. A page that holds pointers keeps a bit per word in its last 1 KB, which the inlined allocation sets with a few instructions, since for a constant size and layout the bits are a constant. A large object keeps its mask in its header. Words that hold an int or a float are never read by the collector, so a number can't keep an object alive by looking like its address. A word that is a pointer in one enum case and a number in another is only read in objects of the first case; an object whose case isn't known when it is made, while an isolate message is decoded, uses the union of its cases. A value over 512 bytes that holds pointers has every word scanned.
- **Layout.** An optional struct or enum is a pointer that is NULL for nil, since those values are never NULL; other optionals carry a flag.
- **Finding objects.** A hash table maps a page number to its page, so any word can be checked with a shift and one lookup. Large objects are found by binary search.
- **Marking.** Roots are the C stacks of the program and its tasks and the registers, which are spilled with `setjmp`, scanned conservatively: any word there that points into an object keeps it alive, including pointers into the middle of an object. Globals, constants and the runtime's own roots (timers, futures waiting for another isolate, closures handed to C) are read precisely, by the layout of each global. Marked objects are scanned by their layout, using an explicit work list rather than recursion. A word read this way still has to point into an object to keep it alive, which makes a layout bit set for a word that never holds a pointer harmless.
- **Generations.** Marks stay set after a collection, so the objects that survived are old. A *minor* collection marks only from the roots and from old objects written since the last collection, and stops at anything already marked, so it doesn't trace long-lived data again. A *major* collection clears every mark and traces everything.
- **Write barrier.** After the compiled code or the runtime stores a pointer into an object (a field, a list element, a map entry, a captured variable), it records the object in the remembered set, which the next minor collection scans again. Plain local variables are roots and need no barrier, and neither do stores of numbers and bools. Writing one object repeatedly records it once; when the set fills, it is compacted to one entry per object.
- **Sweeping.** Unmarked objects go back onto the free lists. Pages with no live objects are reused for any size class, pages whose objects are all live are skipped, and unmarked large objects are freed.
- **Pacing.** A minor collection runs once the young objects reach the size of the heap after the last collection, and never below 4 MB. A major one runs instead once the old objects have doubled since the last major collection.

Scanning the stacks conservatively can keep a dead object alive when an unrelated number on the stack happens to look like its address, or when a stack slot still holds a pointer the program no longer uses. It never frees a live object.

**Why the stacks stay conservative.** A precise stack needs a shadow stack: each function that holds pointers links a frame of root slots into a thread-local chain and stores each pointer it holds there, which also keeps those pointers out of registers across calls. Measured by hand on the generated C of the binary-trees benchmark, that costs 13% more cycles when only the functions that allocate keep slots, and 27% when every function with a pointer does, for no gain in a collector that doesn't move objects. Go and Dart find stack pointers precisely through stack maps that their own code generators emit; a compiler that emits C gets no stack maps, which leaves a shadow stack, as Kotlin/Native uses, or conservative scanning. A collector that moves objects needs the former, but Vex's never moves one, so conservative stacks with precise objects keep almost all of the benefit, as in the collectors of WebKit and Mono.

### Environment variables

| Variable | Effect |
|---|---|
| `VEX_GC_STATS=1` | Prints collections, total bytes allocated, the peak heap and the live heap at exit |
| `VEX_GC_STRESS=N` | Collects every N allocations; `1` collects on every one, and every fourth collection is major. The tests run every program this way to catch objects freed too early |
| `VEX_GC_VERIFY=1` | Each collection keeps a copy of the old objects, and the next minor collection fails if an old object changed since to point at a young one isn't in the remembered set, which would mean a write barrier is missing. The tests set it along with `VEX_GC_STRESS` |

### Benchmarks

On an Apple Silicon Mac, best of several runs, against the same program written in Go:

| Program | Vex | Go |
|---|---|---|
| 2 million iterations allocating structs, enums, closures, strings and lists (530 MB allocated in total) | 0.33 s, 6 MB peak memory | 0.35 s, 9 MB |
| `fib(38)` plus a sieve over 20 million booleans | 0.17 s | 0.16 s |

Before the collector, the first program leaked all 387 MB it allocated.

Binary trees of depth 18 (about 67 million short-lived nodes beside one long-lived tree) take 0.26 s and 42 MB: Kotlin on the JVM takes 0.28 s and 438 MB, Dart 0.35 s and 78 MB, Go 1.26 s and 45 MB. Minor collections are what make the difference: before them it took 0.45 s.

Precise object layouts cost the binary-trees benchmark of `bench/` about 7% in cycles: its nodes hold nothing but pointers, so it pays for setting the layout bits on each allocation and gains nothing. The other benchmarks stay within about 2%; hash maps and sorting, whose map entries and strings hold numbers next to pointers, often come out slightly faster, since those numbers are no longer read.

## Next stages

1. **Returning memory to the OS**, and finalizers for native handles once the UI bridge exists.
2. **Threads**, when Vex gets them: per-thread allocation caches and stopping threads for a collection.
