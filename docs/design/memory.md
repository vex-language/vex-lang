# Memory management

Status: stage 1 done (V-107); stage 2, precise object layouts, done (V-111); stage 3, generational collection and returning memory to the system, done (V-112).

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
- **Object layouts.** Every allocation passes the layout of its words: a 64-bit mask with a bit for each 8-byte word that may hold a pointer, repeated for each element of an array. The compiler emits one as a C constant for each struct, enum case, record, optional, closure environment, captured variable and task or generator argument block, built with `offsetof` so the C compiler places the fields; lists, maps, sets and futures get theirs from the element, key and value types. Pages are kept apart by layout as well as size class, so a page stores its layout once, in its header, and allocating writes nothing but the object. Each class has its pages for objects with no pointers and for objects that are all pointers, the two common cases, whose inlined allocation takes the same instructions as an allocation without a layout. Every other layout of a class gets pages of its own, found through a table of 256 per heap, and its inlined allocation adds one comparison; once the table is three quarters full, a new layout is treated as all pointers. A large object keeps its mask in its header. Words that hold an int or a float are never read by the collector, so a number can't keep an object alive by looking like its address. A word that is a pointer in one enum case and a number in another is only read in objects of the first case; an object whose case isn't known when it is made, while an isolate message is decoded, uses the union of its cases. A value over 512 bytes that holds pointers has every word scanned.
- **Layout.** An optional struct or enum is a pointer that is NULL for nil, since those values are never NULL; other optionals carry a flag.
- **Finding objects.** A hash table maps a page number to its page, so any word can be checked with a shift and one lookup. Large objects are found by binary search.
- **Marking.** Roots are the C stacks of the program and its tasks and the registers, which are spilled with `setjmp`, scanned conservatively: any word there that points into an object keeps it alive, including pointers into the middle of an object. Globals, constants and the runtime's own roots (timers, futures waiting for another isolate, closures handed to C) are read precisely, by the layout of each global. Marked objects are scanned by their layout, using an explicit work list rather than recursion. A word read this way still has to point into an object to keep it alive, which makes a layout bit set for a word that never holds a pointer harmless.
- **Generations.** Marks stay set after a collection, so the objects that survived are old. A *minor* collection marks only from the roots and from old objects written since the last collection, and stops at anything already marked, so it doesn't trace long-lived data again. A *major* collection clears every mark and traces everything.
- **Write barrier.** After the compiled code or the runtime stores a pointer into an object (a field, a list element, a map entry, a captured variable), it records the object in the remembered set, which the next minor collection scans again. Plain local variables are roots and need no barrier, and neither do stores of numbers and bools. Writing one object repeatedly records it once; when the set fills, it is compacted to one entry per object.
- **Sweeping.** Unmarked objects go back onto the free lists. Pages with no live objects are reused for any size class, pages whose objects are all live are skipped, and unmarked large objects are freed.
- **Pacing.** A minor collection runs once the young objects reach the size of the heap after the last collection, and never below 4 MB. A major one runs instead once the old objects have doubled since the last major collection, or after 16 minor ones in a row in which the old objects grew by less than a quarter, so that old objects that died are found even when the old objects stop growing, as when a screen full of objects is closed. Boehm's collector makes a similar trade, with a full collection every 19 partial ones.
- **Returning memory.** Pages are cut from 1 MB chunks taken from the system with `mmap`, or `VirtualAlloc` on Windows. At each collection the collector counts the pages it needs: those in use, and those the young objects may fill before the next collection. A major collection keeps as many as were needed at any collection since the last major one, and gives the memory of the other empty pages back with `madvise` (`MADV_FREE_REUSABLE` on Apple systems, which the system's memory accounting and the iOS memory limit see at once, `MADV_DONTNEED` on Linux), or decommits it with `VirtualFree` on Windows. The page keeps its addresses, and is used again before any new memory is taken. Waiting for a major collection, and keeping the most pages needed since the last one, is the hysteresis: a heap that grows and shrinks between major collections keeps its pages instead of giving them back and faulting them in again. Large objects of 1 MB or more are mapped on their own and unmapped when they die; smaller ones come from `malloc`. The memory from the system is reached only through four small functions (allocate, free, release, reuse), each with a version per system. On Windows a decommitted page faults when touched, so a released page is always committed again before it is used.

Scanning the stacks conservatively can keep a dead object alive when an unrelated number on the stack happens to look like its address, or when a stack slot still holds a pointer the program no longer uses. It never frees a live object.

**Why the stacks stay conservative.** A precise stack needs a shadow stack: each function that holds pointers links a frame of root slots into a thread-local chain and stores each pointer it holds there, which also keeps those pointers out of registers across calls. Measured by hand on the generated C of the binary-trees benchmark, that costs 13% more cycles when only the functions that allocate keep slots, and 27% when every function with a pointer does, for no gain in a collector that doesn't move objects. Go and Dart find stack pointers precisely through stack maps that their own code generators emit; a compiler that emits C gets no stack maps, which leaves a shadow stack, as Kotlin/Native uses, or conservative scanning. A collector that moves objects needs the former, but Vex's never moves one, so conservative stacks with precise objects keep almost all of the benefit, as in the collectors of WebKit and Mono.

### Environment variables

| Variable | Effect |
|---|---|
| `VEX_GC_STATS=1` | Prints collections, total bytes allocated, the peak heap and the live heap at exit, and the memory held from the system then, the most it held, and how much was given back |
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

Returning memory lowers the peak memory of the sorting benchmark from 143 MB to 120 MB, since its large lists are unmapped when they die, and of the strings benchmark from 12.7 MB to 8.7 MB. A program that builds 60 MB of objects, drops them and goes on allocating falls back to 18 MB of memory footprint, where before it stayed at 72 MB. Forcing major collections when the old objects stop growing costs nothing measurable: taking pages in 1 MB chunks more than makes up for it, and binary trees runs about 3% faster.

Precise object layouts leave the benchmarks of `bench/` within 1% in cycles, except for those whose objects mix numbers with pointers: hash maps run about 6% faster, sorting 4% and JSON 1%, since the collector no longer reads those numbers. A layout of its own holds a partly used page per size class: the JSON benchmark uses 5 pages, 320 KB, more than before. Setting a bit per word on each allocation instead, in a bitmap at the end of each page, cost binary trees 7%.

## Next stages

1. **Finalizers** for native handles once the UI bridge exists.
2. **Threads**, when Vex gets them: per-thread allocation caches and stopping threads for a collection.
