// Runtime library compiled into every Vex program.

#ifdef _WIN32
// Windows 10 and later, and file sizes past 2 GB.
#define _WIN32_WINNT 0x0A00
#define _FILE_OFFSET_BITS 64
#endif

#include <ctype.h>
#include <errno.h>
#include <inttypes.h>
#include <math.h>
#include <setjmp.h>
#include <signal.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <string.h>
#include <dirent.h>
#include <sys/stat.h>
#include <unistd.h>
#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <bcrypt.h>
#include <direct.h>
#include <fcntl.h>
#include <io.h>
#include <locale.h>
#include <process.h>
#include <shellapi.h>
#else
#include <pthread.h>
#include <sys/mman.h>
#include <sys/resource.h>
#include <sys/wait.h>
#endif

// Runtime state is per thread, so each isolate has its own heap and loop.
#define VX_TLS _Thread_local

// An immutable UTF-8 string. [ptr] is not NUL-terminated.
typedef struct {
    const char *ptr;
    int64_t len;
} vx_str;

// A growable list of fixed-size elements.
typedef struct {
    int64_t len;
    int64_t cap;
    int64_t esz;
    char *data;
} vx_list;

// A function value: a closure's code and the variables it captured.
typedef struct {
    void *fn;
    void *env;
} vx_fn;

// The layout of a value: one bit for each of its 8-byte words that may
// hold a pointer, so the collector scans only those. An array repeats the
// layout of its element. A value over 512 bytes that holds pointers has
// every bit set, and the collector then scans all of its words.
#define VX_SH(m, off) ((off) / 8 < 64 ? (uint64_t)(m) << ((off) / 8) : 0)
#define VX_FIELD(T, f, m) VX_SH(m, offsetof(T, f))
#define VX_LAYOUT(T, m) (sizeof(T) > 512 ? ~(uint64_t)0 : (uint64_t)(m))
#define VX_ONES(n) ((n) >= 512 ? ~(uint64_t)0 : ((uint64_t)1 << (((n) + 7) / 8)) - 1)
#define VX_PM_STR ((uint64_t)1)
#define VX_PM_FN ((uint64_t)2)
#define VX_PM_LIST VX_FIELD(vx_list, data, 1)

// The source file names used in runtime error messages. A line passed to
// the runtime holds the index of its file above the low 20 bits.
static const char *const vx_main_src[] = {"main.vx"};
static const char *const *vx_files = vx_main_src;

#define VX_LIT(s) ((vx_str){(s), (int64_t)(sizeof(s) - 1)})

// --- Tests ---
//
// `vex test` runs each test in a child process, so a runtime error fails
// only that test. The child prints why it failed; [vx_test_marked] lives in
// memory shared with the parent, which prints the line for a test that
// passed or crashed. On Windows, which can't fork, the child is the
// program started again, told the test to run in VEX_TEST_CHILD.

static const char *vx_test_name;   // the test this process runs, or NULL
static int *vx_test_marked;        // set once the test is reported as failed

// Reports the running test as failed, printing its name the first time.
static void vx_test_mark(void) {
    if (!vx_test_name || *vx_test_marked) return;
    *vx_test_marked = 1;
    printf("\u2717 %s\n", vx_test_name);
    fflush(stdout);
}

// --- Errors ---

// A call of a Vex function in progress, kept on the C stack. Each thread
// and each task has its own chain, from the innermost call out. Programs
// built without traces define no VX_TRACES and keep no frames.
typedef struct vx_frame {
    struct vx_frame *prev;
    const char *name;
    int line;                 // where this function last called another
} vx_frame;

#if defined(__APPLE__)
static VX_TLS vx_frame *vx_top;
#else
static VX_TLS vx_frame *vx_top __attribute__((tls_model("initial-exec")));
#endif

static inline void vx_leave(vx_frame *f) { vx_top = f->prev; }

// Starts the frame of the function [name]. The frame ends with the C
// scope, however the function returns.
#define VX_ENTER(name) \
    vx_frame vx_fr __attribute__((cleanup(vx_leave))) = {vx_top, (name), 0}; \
    vx_top = &vx_fr

// The calls that led to a runtime error or a throw, innermost first. A
// call repeated by recursion is kept once, with a count.
#define VX_TRACE_MAX 64
typedef struct {
    const char *name;
    int line;
    int repeat;               // how many times the same call follows it
    bool task;                // where the task running the calls above started
} vx_trace_entry;

typedef struct {
    int n;
    bool more;                // calls past the last entry were left out
    vx_trace_entry e[VX_TRACE_MAX];
} vx_trace_buf;

// The last error thrown on this thread and the calls that threw it, shown
// if it is passed up out of main.
static VX_TLS void *vx_thrown;
static VX_TLS int vx_thrown_line;
static VX_TLS vx_trace_buf vx_thrown_trace;

static void vx_trace(vx_frame *f, int line);

// Prints a runtime error with the source line and the calls that led to
// it, and stops the program.
__attribute__((noreturn, format(printf, 2, 3)))
static void vx_fail(int line, const char *fmt, ...) {
    vx_test_mark();
    fflush(stdout);
    fputs("error: ", stderr);
    va_list ap;
    va_start(ap, fmt);
    vfprintf(stderr, fmt, ap);
    va_end(ap);
    fprintf(stderr, "\n --> %s:%d\n", vx_files[line >> 20], line & 0xFFFFF);
#ifdef VX_TRACES
    vx_trace(vx_top, line);
#endif
    exit(1);
}

// --- Memory ---
//
// A mark-and-sweep garbage collector. The stacks and registers are scanned
// conservatively: any word there that points into an object keeps that
// object alive. Objects are scanned precisely: each allocation gives the
// layout of its words, and only the words that may hold pointers are read.
// Everything not reached is reused.
//
// Small objects live in 64 KB pages, each holding objects of one size class,
// and are handed out from per-class free lists. Larger objects get their own
// allocation.

#define VX_PAGE_SHIFT 16
#define VX_PAGE_SIZE ((size_t)1 << VX_PAGE_SHIFT)
#define VX_SMALL_MAX 2048
#define VX_CLASSES 24
// Mixed layouts that pages can hold, beyond no pointers and all pointers.
#define VX_MIXED 256

static const uint32_t vx_class_size[VX_CLASSES] = {
    16, 32, 48, 64, 80, 96, 112, 128, 160, 192, 224, 256,
    320, 384, 448, 512, 640, 768, 896, 1024, 1280, 1536, 1792, 2048,
};

// Size class of each multiple of 16 bytes up to VX_SMALL_MAX.
static uint8_t vx_class_of[VX_SMALL_MAX / 16 + 1];

// The objects of one size class and one layout, and the pages they come
// from. A page holds objects of a single kind, so its layout is stored
// once, in the page, and allocating sets nothing but the object.
typedef struct {
    void *free;           // free objects
    char *bump, *bump_end; // the unused end of the page allocated from in order
} vx_kind;

// A kind whose objects mix pointers with other words.
typedef struct {
    vx_kind kind;
    uint64_t mask;
    uint32_t key;         // vx_kind_key of it; 0 for a free slot
    uint32_t tag;         // vx_kind_tag of it, for a layout of up to 16 words
} vx_mixed;

// The mixed kinds of a heap that has none yet.
static const vx_mixed vx_no_mixed[VX_MIXED];

typedef struct {
    char *start;
    uint32_t size;    // object size; 0 while the page is unused
    uint32_t count;   // objects that fit
    uint32_t atomic;  // its objects hold no pointers, so they aren't scanned
    uint32_t live;
    // The layout of each object: word i may hold a pointer when bit
    // i % period of mask is set. A period of 1 means every word may.
    uint64_t mask;
    uint32_t period;
    vx_kind *kind;
    // ceil(2^32 / size): the object holding byte `off` is (off * recip) >> 32,
    // exact since size * VX_PAGE_SIZE < 2^32.
    uint32_t recip;
    uint8_t marks[VX_PAGE_SIZE / 16];
} vx_page;

// A large object's header. Its size keeps objects 16-byte aligned.
typedef struct {
    size_t size;
    uint32_t atomic;
    uint32_t mark;
    uint64_t mask;    // the layout of its words, repeating every esz bytes
    size_t esz;
} vx_large;

// An object marked but not yet scanned. [info] is the page of an object
// with a mixed layout, or the size shifted left by 2 and tagged with
// VX_SPAN_ALL, when every word may hold a pointer, or VX_SPAN_LARGE, for a
// large object laid out by its header.
typedef struct {
    char *ptr;
    uintptr_t info;
} vx_span;

#define VX_SPAN_ALL 1
#define VX_SPAN_LARGE 2

// The heap of one thread. It is a single thread-local struct, rather than
// one variable per field, so that an allocation looks up thread-local
// storage once.
typedef struct {
    size_t heap_bytes;         // bytes in objects, live or not
    size_t gc_next;            // collect once heap_bytes passes this
    vx_kind kinds[2][VX_CLASSES]; // objects with all pointers and with none, by class
    size_t gc_countdown;
    size_t gc_total, gc_peak, gc_count;
    size_t gc_base;            // heap_bytes when the last collection ended
    uintptr_t lo, hi;          // bounds of all objects
    vx_page **pages;           // every page
    size_t pages_len, pages_cap;
    vx_page **page_table;      // open-addressing map from page number to page
    size_t page_table_cap;
    vx_large **larges;         // every large object; sorted during a collection
    size_t larges_len, larges_cap;
    vx_span *gray;             // objects marked but not yet scanned
    size_t gray_len, gray_cap;
    vx_mixed *mixed;           // VX_MIXED kinds with mixed layouts, by vx_kind_slot
    size_t mixed_len;
    size_t page_scan;          // pages before this one aren't empty
    const void **rem;          // addresses written since the last collection
    size_t rem_len, rem_cap;
    size_t rem_done;           // entries before this one name distinct objects
    size_t major_next;         // a major collection is due once gc_base passes this
} vx_heap;


static VX_TLS vx_heap vx_h = {.gc_next = 4 << 20, .lo = UINTPTR_MAX, .mixed = (vx_mixed *)vx_no_mixed};

#define vx_heap_bytes vx_h.heap_bytes
#define vx_gc_next vx_h.gc_next
#define vx_gc_countdown vx_h.gc_countdown
#define vx_gc_total vx_h.gc_total
#define vx_gc_peak vx_h.gc_peak
#define vx_gc_count vx_h.gc_count
#define vx_lo vx_h.lo
#define vx_hi vx_h.hi
#define vx_pages vx_h.pages
#define vx_pages_len vx_h.pages_len
#define vx_pages_cap vx_h.pages_cap
#define vx_page_table vx_h.page_table
#define vx_page_table_cap vx_h.page_table_cap
#define vx_larges vx_h.larges
#define vx_larges_len vx_h.larges_len
#define vx_larges_cap vx_h.larges_cap
#define vx_gray vx_h.gray
#define vx_gray_len vx_h.gray_len
#define vx_gray_cap vx_h.gray_cap

static VX_TLS char *vx_stack_base;

// The entries of the folder read by the last vx_fs_list_open, sorted. The
// collector marks them, since only the stack is scanned otherwise.
static VX_TLS char **vx_fs_entries;
static VX_TLS int64_t vx_fs_entry_count;

// The text read by the last vx_json_scan, which its events point into.
static VX_TLS const char *vx_json_src;

// Closures handed to C as callback contexts. They stay alive, since C
// may keep them, until C passes the context to vex_release.
static VX_TLS void **vx_kept;

// The globals holding the program's constants, built by the main thread
// before anything else runs and kept alive from then on, and the parts of
// them that may hold pointers.
static const char *vx_roots_lo;
static const struct vx_root *vx_roots_parts;
static size_t vx_roots_len;
// This isolate's copy of the variables declared outside functions.
static VX_TLS const char *vx_vars_lo;
static const struct vx_root *vx_vars_parts;
static size_t vx_vars_len;
static VX_TLS size_t vx_kept_len, vx_kept_cap;

// A task: async work running on its own stack. The program's own stack
// is the main task. A task that isn't running keeps its registers on its
// stack, below [sp].
typedef struct vx_task vx_task;
struct vx_future;
struct vx_task {
    void *sp;
    char *stack;              // the mapping, starting with a guard page
    size_t size;
    char *top;                // where the stack starts; it grows down
    vx_task *caller;          // the task that resumed it, which it returns to
    vx_task *next;            // in the ready queue or a future's waiters
    void (*entry)(void *);
    void *arg;
    bool done;
    bool cancelled;           // stops at its next await
    struct vx_future *future; // the future it settles, or NULL
    struct vx_future *waiting;// the future it waits for, while it waits
    vx_task **parked;         // where it is kept while it waits on a stream
    vx_task *parent;          // the task that started it; cancelling one cancels the other
    vx_frame *frames;         // its innermost call, while it isn't running
    const char *origin;       // the function that started it, for traces
    int origin_line;
    bool gen;                 // a generator's body, run by the task that asks for values
    void *asan_fake;
    const void *asan_bottom;
    size_t asan_size;
};

static VX_TLS vx_task vx_main_task;
static VX_TLS vx_task *vx_cur;
static VX_TLS vx_task **vx_tasks;          // every task that has started and not finished
static VX_TLS size_t vx_tasks_len, vx_tasks_cap;

static void vx_trace_add(vx_trace_buf *b, const char *name, int line, bool task) {
    if (b->n > 0) {
        vx_trace_entry *last = &b->e[b->n - 1];
        if (!task && !last->task && last->name == name && last->line == line) {
            last->repeat++;
            return;
        }
    }
    if (b->n == VX_TRACE_MAX) {
        b->more = true;
        return;
    }
    b->e[b->n++] = (vx_trace_entry){name, line, 0, task};
}

// Collects into [b] the calls from the frame [f] of the running task out,
// where [f] is at [line]. A generator's calls go on with those of the task
// asking it for a value; a task's end with where it was started.
static void vx_trace_collect(vx_trace_buf *b, vx_frame *f, int line) {
    b->n = 0;
    b->more = false;
    vx_task *t = vx_cur;
    for (int steps = 0; steps < 100000 && !b->more; steps++) {
        if (f) {
            vx_trace_add(b, f->name, line, false);
            f = f->prev;
            if (f) line = f->line;
            continue;
        }
        if (!t || t == &vx_main_task) break;
        if (t->gen && t->caller) {
            t = t->caller;
            f = t->frames;
            if (f) line = f->line;
            continue;
        }
        if (t->origin) vx_trace_add(b, t->origin, t->origin_line, true);
        break;
    }
}

static void vx_trace_print(const vx_trace_buf *b) {
    // A lone call of main says nothing the source line doesn't.
    if (b->n == 0 || (b->n == 1 && !b->more && !b->e[0].task && b->e[0].repeat == 0 && strcmp(b->e[0].name, "main") == 0)) return;
    for (int i = 0; i < b->n; i++) {
        const vx_trace_entry *e = &b->e[i];
        fprintf(stderr, e->task ? "  in a task started by %s (%s" : "  at %s (%s", e->name, vx_files[e->line >> 20]);
        if (e->line & 0xFFFFF) fprintf(stderr, ":%d", e->line & 0xFFFFF);
        fputs(")\n", stderr);
        if (e->repeat) fprintf(stderr, "  ... repeated %d more time%s\n", e->repeat, e->repeat == 1 ? "" : "s");
    }
    if (b->more) fputs("  ... and more calls\n", stderr);
}

static void vx_trace(vx_frame *f, int line) {
    vx_trace_buf b;
    vx_trace_collect(&b, f, line);
    vx_trace_print(&b);
}

// Records [err], thrown at [line], with the calls that led to it.
static void *vx_throw(void *err, int line) {
    vx_thrown = err;
    vx_thrown_line = line;
    vx_trace_collect(&vx_thrown_trace, vx_top, line);
    return err;
}

// Returns [err], an error made from [old] as it was passed up, which takes
// over the record of where [old] was thrown.
static void *vx_rethrow(void *old, void *err) {
    if (old == vx_thrown) vx_thrown = err;
    return err;
}


static size_t vx_gc_stress;         // VEX_GC_STRESS=N: collect every N allocations

__attribute__((noreturn))
static void vx_out_of_memory(void) {
    fputs("error: out of memory\n", stderr);
    exit(1);
}

static void *vx_grow(void *p, size_t *cap, size_t esz) {
    *cap = *cap ? *cap * 2 : 256;
    p = realloc(p, *cap * esz);
    if (!p) vx_out_of_memory();
    return p;
}

static size_t vx_page_hash(uintptr_t n, size_t cap) {
    return (size_t)((n * 0x9E3779B97F4A7C15ull) >> 7) & (cap - 1);
}

static void vx_page_table_put(vx_page *pg) {
    uintptr_t n = (uintptr_t)pg->start >> VX_PAGE_SHIFT;
    size_t i = vx_page_hash(n, vx_page_table_cap);
    while (vx_page_table[i]) i = (i + 1) & (vx_page_table_cap - 1);
    vx_page_table[i] = pg;
}

static vx_page *vx_page_of(uintptr_t p) {
    if (!vx_page_table) return NULL;
    uintptr_t n = p >> VX_PAGE_SHIFT;
    for (size_t i = vx_page_hash(n, vx_page_table_cap);; i = (i + 1) & (vx_page_table_cap - 1)) {
        vx_page *pg = vx_page_table[i];
        if (!pg) return NULL;
        if ((uintptr_t)pg->start >> VX_PAGE_SHIFT == n) return pg;
    }
}

static void vx_bounds(uintptr_t lo, uintptr_t hi) {
    if (lo < vx_lo) vx_lo = lo;
    if (hi > vx_hi) vx_hi = hi;
}

// Returns the memory of a new page, aligned to its size, or NULL.
static char *vx_page_memory(void) {
#ifdef _WIN32
    // Windows hands out memory in blocks of 64 KB, aligned to that size.
    return VirtualAlloc(NULL, VX_PAGE_SIZE, MEM_RESERVE | MEM_COMMIT, PAGE_READWRITE);
#else
    void *p;
    return posix_memalign(&p, VX_PAGE_SIZE, VX_PAGE_SIZE) == 0 ? p : NULL;
#endif
}

static void vx_page_memory_free(char *p) {
#ifdef _WIN32
    VirtualFree(p, 0, MEM_RELEASE);
#else
    free(p);
#endif
}

// Gives the objects of [pg] to kind [k] of class [cls], laid out as [mask]
// every [period] words, which allocates them in order.
static void vx_page_format(vx_page *pg, vx_kind *k, size_t cls, uint64_t mask, uint32_t period) {
    pg->size = vx_class_size[cls];
    pg->count = (uint32_t)(VX_PAGE_SIZE / pg->size);
    pg->recip = (uint32_t)((((uint64_t)1 << 32) + pg->size - 1) / pg->size);
    pg->atomic = period == 0;
    pg->mask = mask;
    pg->period = period;
    pg->kind = k;
    k->bump = pg->start;
    k->bump_end = pg->start + (size_t)pg->count * pg->size;
}

static void vx_page_new(vx_kind *k, size_t cls, uint64_t mask, uint32_t period) {
    for (size_t i = vx_h.page_scan; i < vx_pages_len; i++) {
        if (vx_pages[i]->size == 0) {
            vx_h.page_scan = i + 1;
            vx_page_format(vx_pages[i], k, cls, mask, period);
            return;
        }
    }
    vx_h.page_scan = vx_pages_len + 1;
    vx_page *pg = calloc(1, sizeof(vx_page));
    if (!pg || !(pg->start = vx_page_memory())) vx_out_of_memory();
    if (vx_pages_len == vx_pages_cap) vx_pages = vx_grow(vx_pages, &vx_pages_cap, sizeof(vx_page *));
    vx_pages[vx_pages_len++] = pg;
    if (vx_pages_len * 2 > vx_page_table_cap) {
        free(vx_page_table);
        vx_page_table_cap = vx_page_table_cap ? vx_page_table_cap * 2 : 1024;
        vx_page_table = calloc(vx_page_table_cap, sizeof(vx_page *));
        if (!vx_page_table) vx_out_of_memory();
        for (size_t i = 0; i < vx_pages_len; i++) vx_page_table_put(vx_pages[i]);
    } else {
        vx_page_table_put(pg);
    }
    vx_bounds((uintptr_t)pg->start, (uintptr_t)pg->start + VX_PAGE_SIZE);
    vx_page_format(pg, k, cls, mask, period);
}

static int vx_large_cmp(const void *a, const void *b) {
    uintptr_t x = (uintptr_t)*(vx_large *const *)a, y = (uintptr_t)*(vx_large *const *)b;
    return (x > y) - (x < y);
}

static vx_large *vx_large_of(uintptr_t p) {
    size_t lo = 0, hi = vx_larges_len;
    while (lo < hi) {
        size_t mid = lo + (hi - lo) / 2;
        if ((uintptr_t)(vx_larges[mid] + 1) <= p) lo = mid + 1;
        else hi = mid;
    }
    if (lo == 0) return NULL;
    vx_large *l = vx_larges[lo - 1];
    return p < (uintptr_t)(l + 1) + l->size ? l : NULL;
}

// A mark byte holds the mark in its low bit. Bit 1 flags an object already
// in the remembered set, from when the set is compacted until the next
// collection, which clears it before marking.
#define VX_REMEMBERED 2

// Compacts the full remembered set to one entry per object, and grows it
// unless that freed half of it.
__attribute__((noinline, cold))
static void vx_rem_compact(void) {
    vx_heap *h = &vx_h;
    size_t k = h->rem_done;
    for (size_t i = h->rem_done; i < h->rem_len; i++) {
        uintptr_t p = (uintptr_t)h->rem[i];
        vx_page *pg = p >= h->lo && p < h->hi ? vx_page_of(p) : NULL;
        if (pg && pg->size) {
            size_t j = (size_t)(((uint64_t)(p - (uintptr_t)pg->start) * pg->recip) >> 32);
            if (j < pg->count) {
                if (pg->marks[j] & VX_REMEMBERED) continue;
                pg->marks[j] |= VX_REMEMBERED;
                h->rem[k++] = pg->start + j * pg->size;
                continue;
            }
        }
        h->rem[k++] = (const void *)p;
    }
    h->rem_len = h->rem_done = k;
    if (h->rem_len * 2 > h->rem_cap) h->rem = vx_grow(h->rem, &h->rem_cap, sizeof(void *));
}

// Makes room in the full remembered set.
__attribute__((noinline, cold))
static void vx_rem_full(void) {
    vx_heap *h = &vx_h;
    if (h->rem_cap < 4096) h->rem = vx_grow(h->rem, &h->rem_cap, sizeof(void *));
    else vx_rem_compact();
}

// The write barrier: records that the object holding [p] was written, so
// the next minor collection scans it again if it is old. It runs after the
// write, so that no collection can come between them. Writing the same
// object over and over records it once.
__attribute__((always_inline))
static inline void vx_remember(const void *p) {
    vx_heap *h = &vx_h;
    if (h->rem_len > 0 && h->rem[h->rem_len - 1] == p) return;
    if (__builtin_expect(h->rem_len == h->rem_cap, 0)) vx_rem_full();
    h->rem[h->rem_len++] = p;
}

static void vx_gray_push(char *ptr, uintptr_t info) {
    if (vx_gray_len == vx_gray_cap) vx_gray = vx_grow(vx_gray, &vx_gray_cap, sizeof(vx_span));
    vx_gray[vx_gray_len++] = (vx_span){ptr, info};
}

// Returns the span info of the page object [pg] holds, which holds pointers.
static inline uintptr_t vx_page_info(const vx_page *pg) {
    return pg->period == 1 ? (uintptr_t)pg->size << 2 | VX_SPAN_ALL : (uintptr_t)pg;
}

// Returns the span info of the large object [l], which holds pointers.
static inline uintptr_t vx_large_info(const vx_large *l) {
    bool all = l->esz > 512 || l->esz < 8 || l->mask == ~(uint64_t)0;
    return (uintptr_t)l->size << 2 | (all ? VX_SPAN_ALL : VX_SPAN_LARGE);
}

// Reads whole stack frames and objects on purpose, so it isn't checked by
// AddressSanitizer.
//
// The state of marking. It is a local copy of the heap's bounds and page
// table, so the compiler keeps them in registers rather than reading the
// thread-local heap again after each store to the work list.
//
// Objects near each other are usually on the same page, so the page last
// found is kept in [n] and [pg] and checked before the page table.
typedef struct {
    vx_heap *h;
    uintptr_t lo, hi;
    vx_page *const *table;
    size_t mask;
    uintptr_t n;
    vx_page *pg;
} vx_marker;

static inline vx_marker vx_marker_of(vx_heap *h) {
    return (vx_marker){h, h->lo, h->hi, h->page_table, h->page_table_cap - 1, 0, NULL};
}

// Marks the object that the word [p] points into, if any.
__attribute__((always_inline))
static inline void vx_mark_word(vx_marker *m, uintptr_t p) {
    if (p < m->lo || p >= m->hi) return;
    vx_heap *h = m->h;
    uintptr_t n = p >> VX_PAGE_SHIFT;
    vx_page *pg = NULL;
    if (n == m->n) {
        pg = m->pg;
    } else if (m->table) {
        for (size_t j = vx_page_hash(n, m->mask + 1);; j = (j + 1) & m->mask) {
            vx_page *t = m->table[j];
            if (!t || (uintptr_t)t->start >> VX_PAGE_SHIFT == n) {
                pg = t;
                break;
            }
        }
        if (pg) {
            m->n = n;
            m->pg = pg;
        }
    }
    if (pg) {
        if (pg->size == 0) return;
        size_t i = (size_t)(((uint64_t)(p - (uintptr_t)pg->start) * pg->recip) >> 32);
        if (i >= pg->count || pg->marks[i]) return;
        pg->marks[i] = 1;
        pg->live++;
        if (!pg->atomic) {
            if (h->gray_len == h->gray_cap) h->gray = vx_grow(h->gray, &h->gray_cap, sizeof(vx_span));
            h->gray[h->gray_len++] = (vx_span){pg->start + i * pg->size, vx_page_info(pg)};
        }
        return;
    }
    vx_large *l = vx_large_of(p);
    if (!l || l->mark) return;
    l->mark = 1;
    if (!l->atomic) vx_gray_push((char *)(l + 1), vx_large_info(l));
}

// Scans every word from [lo] to [hi], as for a stack.
__attribute__((no_sanitize_address, always_inline))
static inline void vx_scan(vx_marker *m, const char *lo, const char *hi) {
    uintptr_t start = ((uintptr_t)lo + sizeof(void *) - 1) & ~(uintptr_t)(sizeof(void *) - 1);
    for (uintptr_t a = start; a + sizeof(void *) <= (uintptr_t)hi; a += sizeof(void *)) vx_mark_word(m, *(uintptr_t *)a);
}

// Reports whether word [i] of the large object [l] may hold a pointer.
static inline bool vx_large_word(const vx_large *l, size_t i) {
    if (l->esz > 512 || l->esz < 8) return l->mask != 0;
    return (l->mask >> (i % (l->esz / 8))) & 1;
}

// Scans the words of the marked object [obj] that may hold pointers, as
// told by the span info [info].
__attribute__((no_sanitize_address, always_inline))
static inline void vx_scan_object(vx_marker *m, char *obj, uintptr_t info) {
    if (info & VX_SPAN_ALL) {
        vx_scan(m, obj, obj + (info >> 2));
        return;
    }
    size_t n, period;
    uint64_t mask;
    if (info & VX_SPAN_LARGE) {
        const vx_large *l = (const vx_large *)obj - 1;
        n = (info >> 2) / 8;
        period = l->esz / 8;
        mask = l->mask & (period == 64 ? ~(uint64_t)0 : ((uint64_t)1 << period) - 1);
    } else {
        const vx_page *pg = (const vx_page *)info;
        n = pg->size / 8;
        period = pg->period;
        mask = pg->mask;
    }
    for (size_t e = 0; e < n; e += period) {
        for (uint64_t b = mask; b; b &= b - 1) {
            size_t i = e + (size_t)__builtin_ctzll(b);
            if (i < n) vx_mark_word(m, ((uintptr_t *)obj)[i]);
        }
    }
}

// Reports whether the word at offset [off] of the object [obj], which
// holds pointers, may hold one.
static bool vx_is_pointer_word(char *obj, size_t off) {
    vx_page *pg = vx_page_of((uintptr_t)obj);
    if (!pg) return vx_large_word((vx_large *)obj - 1, off / 8);
    return (pg->mask >> (off / 8 % pg->period)) & 1;
}

__attribute__((no_sanitize_address))
static void vx_mark_range(const char *lo, const char *hi) {
    vx_marker m = vx_marker_of(&vx_h);
    vx_scan(&m, lo, hi);
}

// A part of a struct of globals that may hold pointers: the [size] bytes
// at [off], laid out as [mask].
typedef struct vx_root {
    uint32_t off, size;
    uint64_t mask;
} vx_root;

// Marks what the struct of globals at [base] points to, as laid out by the
// [n] parts at [parts].
__attribute__((no_sanitize_address))
static void vx_mark_roots_at(const char *base, const vx_root *parts, size_t n) {
    vx_marker m = vx_marker_of(&vx_h);
    for (size_t i = 0; i < n; i++) {
        const char *p = base + parts[i].off;
        if (parts[i].size > 512) {
            vx_scan(&m, p, p + parts[i].size);
            continue;
        }
        for (uint64_t b = parts[i].mask; b; b &= b - 1) vx_mark_word(&m, ((const uintptr_t *)p)[__builtin_ctzll(b)]);
    }
}

// Scans the marked objects until none are left.
__attribute__((no_sanitize_address))
static void vx_mark_gray(void) {
    vx_heap *h = &vx_h;
    vx_marker m = vx_marker_of(h);
    while (h->gray_len > 0) {
        vx_span s = h->gray[--h->gray_len];
        vx_scan_object(&m, s.ptr, s.info);
    }
}

static bool vx_gc_verify;           // VEX_GC_VERIFY=1: check every minor collection

__attribute__((noinline, no_sanitize_address))
static void vx_mark_stack(void) {
    // Spill callee-saved registers onto the stack so they are scanned too.
    jmp_buf regs;
    setjmp(regs);
    char here;
    const char *lo = (const char *)&regs < &here ? (const char *)&regs : &here;
    vx_mark_range(lo, vx_stack_base);
}

static void vx_async_mark(void);
static void vx_gens_mark(void);
static void vx_gens_sweep(void);

// Finds the object holding the address [p], giving its start and size, or
// returns false when [p] isn't in an object. [marked] tells whether the
// object is marked, and [atomic] whether it holds no pointers.
static bool vx_object_at(uintptr_t p, char **start, size_t *size, bool *marked, bool *atomic) {
    if (p < vx_lo || p >= vx_hi) return false;
    vx_page *pg = vx_page_of(p);
    if (pg) {
        if (pg->size == 0) return false;
        size_t i = (size_t)(((uint64_t)(p - (uintptr_t)pg->start) * pg->recip) >> 32);
        if (i >= pg->count) return false;
        *start = pg->start + i * pg->size;
        *size = pg->size;
        *marked = pg->marks[i] & 1;
        *atomic = pg->atomic;
        return true;
    }
    vx_large *l = vx_large_of(p);
    if (!l) return false;
    *start = (char *)(l + 1);
    *size = l->size;
    *marked = l->mark;
    *atomic = l->atomic;
    return true;
}

// Empties the remembered set, clearing its flags. Unless [major], the
// marked objects in it are scanned again, so what they now point to is
// marked too.
static void vx_mark_remembered(bool major) {
    for (size_t i = 0; i < vx_h.rem_len; i++) {
        char *start;
        size_t size;
        bool marked, atomic;
        if (!vx_object_at((uintptr_t)vx_h.rem[i], &start, &size, &marked, &atomic)) continue;
        vx_page *pg = vx_page_of((uintptr_t)start);
        if (pg) pg->marks[(size_t)(start - pg->start) / pg->size] &= 1;
        if (!major && marked && !atomic) vx_gray_push(start, pg ? vx_page_info(pg) : vx_large_info((vx_large *)start - 1));
    }
    vx_h.rem_len = vx_h.rem_done = 0;
}

// Marks everything the program can reach directly, and what that reaches.
static void vx_mark_roots(void) {
    vx_mark_stack();
    vx_mark_range((const char *)&vx_fs_entries, (const char *)(&vx_fs_entries + 1));
    vx_mark_range((const char *)&vx_json_src, (const char *)(&vx_json_src + 1));
    vx_mark_range((const char *)&vx_thrown, (const char *)(&vx_thrown + 1));
    if (vx_kept) vx_mark_range((const char *)vx_kept, (const char *)(vx_kept + vx_kept_len));
    if (vx_roots_lo) vx_mark_roots_at(vx_roots_lo, vx_roots_parts, vx_roots_len);
    if (vx_vars_lo) vx_mark_roots_at(vx_vars_lo, vx_vars_parts, vx_vars_len);
    // The stacks of the tasks that aren't running, the program's own
    // included, from their saved registers up.
    if (vx_cur != &vx_main_task) vx_mark_range(vx_main_task.sp, vx_main_task.top);
    for (size_t i = 0; i < vx_tasks_len; i++) {
        if (vx_tasks[i] != vx_cur) vx_mark_range(vx_tasks[i]->sp, vx_tasks[i]->top);
    }
    vx_async_mark();
    vx_mark_gray();
    vx_gens_mark();
}

// Under VEX_GC_VERIFY, each collection ends by copying the old objects, and
// the next minor collection checks that each old object since changed to
// point at a young object is in the remembered set; otherwise a write
// skipped the write barrier. Comparing with the copy, rather than tracing
// from the roots, keeps dead objects that the stack still seems to point
// to from being checked: they never change.
static VX_TLS char **vx_verify_pages;     // a copy of each page, NULL when it holds no old objects that hold pointers
static VX_TLS size_t vx_verify_pages_len;
static VX_TLS vx_large **vx_verify_larges; // the old large objects that hold pointers, and their copies
static VX_TLS char **vx_verify_large_copies;
static VX_TLS size_t vx_verify_larges_len;

static int vx_uintptr_cmp(const void *a, const void *b) {
    uintptr_t x = *(const uintptr_t *)a, y = *(const uintptr_t *)b;
    return (x > y) - (x < y);
}

// Checks the old object at [obj] against its copy [was].
static void vx_verify_object(char *obj, const char *was, size_t size, const uintptr_t *rem, size_t rem_len) {
    if (memcmp(obj, was, size) == 0) return;
    uintptr_t key = (uintptr_t)obj;
    if (bsearch(&key, rem, rem_len, sizeof(uintptr_t), vx_uintptr_cmp)) return;
    for (size_t off = 0; off + sizeof(void *) <= size; off += sizeof(void *)) {
        if (!vx_is_pointer_word(obj, off)) continue;
        uintptr_t p = *(uintptr_t *)(obj + off);
        char *start;
        size_t n;
        bool marked, atomic;
        if (p != *(const uintptr_t *)(was + off) && vx_object_at(p, &start, &n, &marked, &atomic) && !marked) {
            fprintf(stderr, "error: the collector missed a write: the old object at %p (%zu bytes) was changed at offset %zu "
                "to point at the young object at %p, but isn't in the remembered set\n", (void *)obj, size, off, (void *)start);
            abort();
        }
    }
}

// Runs at the start of a minor collection, before the remembered set is
// emptied.
static void vx_verify_writes(void) {
    uintptr_t *rem = malloc((vx_h.rem_len + 1) * sizeof(uintptr_t));
    if (!rem) vx_out_of_memory();
    size_t rem_len = 0;
    for (size_t i = 0; i < vx_h.rem_len; i++) {
        char *start;
        size_t size;
        bool marked, atomic;
        if (vx_object_at((uintptr_t)vx_h.rem[i], &start, &size, &marked, &atomic)) rem[rem_len++] = (uintptr_t)start;
    }
    qsort(rem, rem_len, sizeof(uintptr_t), vx_uintptr_cmp);
    for (size_t p = 0; p < vx_verify_pages_len; p++) {
        if (!vx_verify_pages[p]) continue;
        vx_page *pg = vx_pages[p];
        for (uint32_t i = 0; i < pg->count; i++) {
            size_t off = (size_t)i * pg->size;
            if (pg->marks[i] & 1) vx_verify_object(pg->start + off, vx_verify_pages[p] + off, pg->size, rem, rem_len);
        }
    }
    for (size_t i = 0; i < vx_verify_larges_len; i++) {
        vx_large *l = vx_verify_larges[i];
        vx_verify_object((char *)(l + 1), vx_verify_large_copies[i], l->size, rem, rem_len);
    }
    free(rem);
}

// Runs at the end of every collection.
static void vx_verify_copy(void) {
    for (size_t p = 0; p < vx_verify_pages_len; p++) free(vx_verify_pages[p]);
    for (size_t i = 0; i < vx_verify_larges_len; i++) free(vx_verify_large_copies[i]);
    free(vx_verify_pages);
    free(vx_verify_larges);
    free(vx_verify_large_copies);
    vx_verify_pages = calloc(vx_pages_len + 1, sizeof(char *));
    vx_verify_larges = calloc(vx_larges_len + 1, sizeof(vx_large *));
    vx_verify_large_copies = calloc(vx_larges_len + 1, sizeof(char *));
    if (!vx_verify_pages || !vx_verify_larges || !vx_verify_large_copies) vx_out_of_memory();
    vx_verify_pages_len = vx_pages_len;
    vx_verify_larges_len = 0;
    for (size_t p = 0; p < vx_pages_len; p++) {
        vx_page *pg = vx_pages[p];
        if (!pg->size || pg->atomic || !pg->live) continue;
        vx_verify_pages[p] = malloc(VX_PAGE_SIZE);
        if (!vx_verify_pages[p]) vx_out_of_memory();
        memcpy(vx_verify_pages[p], pg->start, (size_t)pg->count * pg->size);
    }
    for (size_t i = 0; i < vx_larges_len; i++) {
        vx_large *l = vx_larges[i];
        if (!l->mark || l->atomic) continue;
        char *copy = malloc(l->size + 1);
        if (!copy) vx_out_of_memory();
        memcpy(copy, l + 1, l->size);
        vx_verify_larges[vx_verify_larges_len] = l;
        vx_verify_large_copies[vx_verify_larges_len++] = copy;
    }
}


// Collects garbage. Objects that survive stay marked, which makes them old:
// a minor collection marks only from the roots and the old objects written
// since, so it doesn't trace the old objects again. A major collection
// clears every mark first and traces everything, once the old objects
// have doubled since the last one.
static void vx_collect(void) {
    if (!vx_stack_base) return;
    vx_gc_count++;
    vx_gc_total += vx_heap_bytes - vx_h.gc_base;
    if (vx_heap_bytes > vx_gc_peak) vx_gc_peak = vx_heap_bytes;
    if (vx_larges_len > 1) qsort(vx_larges, vx_larges_len, sizeof(vx_large *), vx_large_cmp);
    bool major = vx_gc_stress ? vx_gc_count % 4 == 0 : vx_h.gc_base >= vx_h.major_next;
    if (vx_gc_verify && !major) vx_verify_writes();
    vx_mark_remembered(major);
    if (major) {
        for (size_t p = 0; p < vx_pages_len; p++) {
            vx_page *pg = vx_pages[p];
            if (pg->size == 0 || pg->live == 0) continue;
            memset(pg->marks, 0, pg->count);
            pg->live = 0;
        }
        for (size_t i = 0; i < vx_larges_len; i++) vx_larges[i]->mark = 0;
    }

    vx_mark_roots();
    vx_gens_sweep();

    // Sweep: rebuild the free lists from the unmarked objects, keeping the
    // marks. A page with nothing live is empty again, and a full one has
    // nothing to add.
    for (size_t c = 0; c < VX_CLASSES; c++) {
        for (int a = 0; a < 2; a++) vx_h.kinds[a][c].free = vx_h.kinds[a][c].bump = vx_h.kinds[a][c].bump_end = NULL;
    }
    if (vx_h.mixed_len > 0) {
        for (size_t i = 0; i < VX_MIXED; i++) vx_h.mixed[i].kind = (vx_kind){NULL, NULL, NULL};
    }
    vx_h.page_scan = 0;
    vx_heap_bytes = 0;
    for (size_t p = 0; p < vx_pages_len; p++) {
        vx_page *pg = vx_pages[p];
        if (pg->size == 0) continue;
        if (pg->live == 0) {
            pg->size = 0;
            continue;
        }
        vx_heap_bytes += (size_t)pg->live * pg->size;
        if (pg->live == pg->count) continue;
        void **list = &pg->kind->free;
        for (uint32_t i = pg->count; i-- > 0;) {
            if (pg->marks[i]) continue;
            void **obj = (void **)(pg->start + (size_t)i * pg->size);
            *obj = *list;
            *list = obj;
        }
    }
    size_t kept = 0;
    for (size_t i = 0; i < vx_larges_len; i++) {
        vx_large *l = vx_larges[i];
        if (l->mark) {
            vx_heap_bytes += l->size;
            vx_larges[kept++] = l;
        } else {
            free(l);
        }
    }
    vx_larges_len = kept;
    vx_h.gc_base = vx_heap_bytes;
    if (major) vx_h.major_next = vx_heap_bytes * 2 > (size_t)(4 << 20) ? vx_heap_bytes * 2 : (size_t)(4 << 20);
    // The young objects may grow as large as the old ones before a minor
    // collection.
    vx_gc_next = vx_heap_bytes + (vx_heap_bytes > (size_t)(4 << 20) ? vx_heap_bytes : (size_t)(4 << 20));
    if (vx_gc_verify) vx_verify_copy();
}

static void *vx_alloc_large(size_t n, uint64_t mask, size_t esz) {
    vx_large *l = calloc(1, sizeof(vx_large) + n);
    if (!l) vx_out_of_memory();
    l->size = n;
    l->atomic = mask == 0;
    l->mask = mask;
    l->esz = esz;
    if (vx_larges_len == vx_larges_cap) vx_larges = vx_grow(vx_larges, &vx_larges_cap, sizeof(vx_large *));
    vx_larges[vx_larges_len++] = l;
    vx_bounds((uintptr_t)(l + 1), (uintptr_t)(l + 1) + n);
    return l + 1;
}

// Returns the period in words of the layout [mask] repeating every [esz]
// bytes, as a page holds it: 0 when no word holds a pointer, 1 when every
// word may, and otherwise the words per element.
__attribute__((always_inline))
static inline uint32_t vx_period(uint64_t mask, size_t esz) {
    if (mask == 0) return 0;
    if (esz > 512 || esz < 16 || esz % 8 != 0) return 1;
    size_t period = esz / 8;
    uint64_t low = period == 64 ? ~(uint64_t)0 : ((uint64_t)1 << period) - 1;
    return (mask & low) == low || (mask & low) == 0 ? 1 : (uint32_t)period;
}

static inline uint32_t vx_kind_key(size_t cls, uint32_t period) { return (uint32_t)(cls + 1) | period << 8; }

// Returns a number that tells apart the mixed kinds whose layouts repeat
// within 16 words, so the inlined allocation checks its kind with one
// comparison.
static inline uint32_t vx_kind_tag(size_t cls, uint64_t mask, uint32_t period) {
    return period <= 16 ? vx_kind_key(cls, period) << 16 | (uint32_t)mask : 0;
}

// Returns the slot of [VX_MIXED] where the kind of class [cls] with the
// mixed layout [mask] every [period] words is looked for first.
__attribute__((always_inline))
static inline size_t vx_kind_slot(size_t cls, uint64_t mask, uint32_t period) {
    return (size_t)(((mask ^ (uint64_t)vx_kind_key(cls, period) << 52) * 0x9E3779B97F4A7C15ull) >> 56) & (VX_MIXED - 1);
}

// Returns the kind of class [cls] with layout [mask] every [period] words,
// adding it to the mixed kinds if it is new. When they are three quarters
// full, a new mixed layout is treated as all pointers.
static vx_kind *vx_kind_get(vx_heap *h, size_t cls, uint64_t *mask, uint32_t *period) {
    if (*period > 1) {
        uint32_t key = vx_kind_key(cls, *period);
        uint64_t m = *mask & (*period == 64 ? ~(uint64_t)0 : ((uint64_t)1 << *period) - 1);
        for (size_t i = vx_kind_slot(cls, m, *period);; i = (i + 1) & (VX_MIXED - 1)) {
            vx_mixed *k = &h->mixed[i];
            if (k->key == key && k->mask == m) {
                *mask = m;
                return &k->kind;
            }
            if (k->key != 0) continue;
            if (h->mixed_len * 4 >= VX_MIXED * 3) break;
            if (h->mixed_len++ == 0) {
                h->mixed = calloc(VX_MIXED, sizeof(vx_mixed));
                if (!h->mixed) vx_out_of_memory();
                k = &h->mixed[i];
            }
            k->key = key;
            k->tag = vx_kind_tag(cls, m, *period);
            k->mask = *mask = m;
            return &k->kind;
        }
        *period = 1;
    }
    *mask = *period;
    return &h->kinds[*period == 0][cls];
}

static void *vx_alloc_block(size_t n, uint64_t mask, size_t esz) {
    vx_heap *h = &vx_h;
    bool atomic = mask == 0;
    if (vx_gc_stress && --h->gc_countdown == 0) {
        h->gc_countdown = vx_gc_stress;
        vx_collect();
    } else if (h->heap_bytes + n > h->gc_next) {
        vx_collect();
    }
    void *p;
    if (n > VX_SMALL_MAX) {
        p = vx_alloc_large(n, mask, esz);
    } else {
        size_t cls = vx_class_of[(n + 15) / 16];
        n = vx_class_size[cls];
        uint32_t period = vx_period(mask, esz);
        vx_kind *k = vx_kind_get(h, cls, &mask, &period);
        if (k->free) {
            p = k->free;
            k->free = *(void **)p;
        } else {
            if (k->bump == k->bump_end) vx_page_new(k, cls, mask, period);
            p = k->bump;
            k->bump += n;
        }
        if (atomic) {
            *(void **)p = NULL;
        } else if (n <= 64) {
            // Small objects, the most common, are cleared without a call.
            uint64_t *q = p;
            q[0] = q[1] = 0;
            if (n > 16) q[2] = q[3] = 0;
            if (n > 32) q[4] = q[5] = 0;
            if (n > 48) q[6] = q[7] = 0;
        } else {
            memset(p, 0, n);
        }
    }
    h->heap_bytes += n;
    return p;
}

// Allocates [n] zeroed bytes laid out as [mask], repeated every [esz]
// bytes; a mask of 0 means they never hold pointers.
//
// The common case is inlined where it happens: an object of up to 128
// bytes, from its kind's free list or the page the kind allocates from in
// order, when no collection is due. The class and the kind are worked out
// here rather than looked up, so for a constant size and layout they are
// constants, and objects with no pointers or only pointers take the same
// instructions as an untyped allocation.
__attribute__((always_inline))
static inline void *vx_alloc_t(size_t n, uint64_t mask, size_t esz) {
    uint32_t period = vx_period(mask, esz);
    if (n - 1 < 128 && !vx_gc_stress) {
        vx_heap *h = &vx_h;
        size_t cls = (n - 1) >> 4, size = (cls + 1) << 4;
        vx_kind *k;
        if (period <= 1) {
            k = &h->kinds[period == 0][cls];
        } else if (period <= 16) {
            uint64_t m = mask & (((uint64_t)1 << period) - 1);
            vx_mixed *x = &h->mixed[vx_kind_slot(cls, m, period)];
            k = x->tag == vx_kind_tag(cls, m, period) ? &x->kind : NULL;
        } else {
            k = NULL;
        }
        if (__builtin_expect(k != NULL && h->heap_bytes + size <= h->gc_next, 1)) {
            void *p = k->free;
            if (p) {
                k->free = *(void **)p;
            } else if (k->bump != k->bump_end) {
                p = k->bump;
                k->bump += size;
            }
            if (__builtin_expect(p != NULL, 1)) {
                h->heap_bytes += size;
                uint64_t *q = p;
                q[0] = 0;
                if (period != 0) {
                    q[1] = 0;
                    for (size_t i = 2; i < size / 8; i++) q[i] = 0;
                }
                return p;
            }
        }
    }
    return vx_alloc_block(n ? n : 1, mask, esz);
}

// Allocates [n] zeroed bytes, each word of which may hold a pointer.
static inline void *vx_alloc(size_t n) { return vx_alloc_t(n, 1, 8); }

// Allocates an object of [n] bytes laid out as [mask].
static inline void *vx_alloc_obj(size_t n, uint64_t mask) { return vx_alloc_t(n, mask, n); }

// Returns a context for passing closure [f] to C, kept alive until C
// passes it to vex_release.
__attribute__((unused))
static void *vx_keep_fn(vx_fn f) {
    vx_fn *box = vx_alloc_obj(sizeof(vx_fn), VX_PM_FN);
    *box = f;
    for (size_t i = 0; i < vx_kept_len; i++) {
        if (!vx_kept[i]) {
            vx_kept[i] = box;
            return box;
        }
    }
    if (vx_kept_len == vx_kept_cap) vx_kept = vx_grow(vx_kept, &vx_kept_cap, sizeof(void *));
    vx_kept[vx_kept_len++] = box;
    return box;
}

// Lets the collector free the callback context [ctx] once C no longer
// needs it. C code declares it as void vex_release(void *ctx).
void vex_release(void *ctx) {
    for (size_t i = 0; i < vx_kept_len; i++) {
        if (vx_kept[i] == ctx) {
            vx_kept[i] = NULL;
            return;
        }
    }
}

// Allocates [n] bytes that never hold pointers, such as string text.
static inline void *vx_alloc_atomic(size_t n) { return vx_alloc_t(n, 0, 1); }

// Returns the layout of the first element of [esz] bytes of the array [p].
static uint64_t vx_mask_of(const void *p, size_t esz) {
    vx_page *pg = vx_page_of((uintptr_t)p);
    if (pg) {
        if (pg->atomic) return 0;
        if (esz > 512 || esz < 8 || pg->period == 1) return ~(uint64_t)0;
        uint64_t mask = 0;
        for (size_t i = 0; i < esz / 8; i++) mask |= ((pg->mask >> (i % pg->period)) & 1) << i;
        return mask;
    }
    const vx_large *l = (const vx_large *)p - 1;
    if (l->atomic) return 0;
    if (esz > 512 || esz < 8) return ~(uint64_t)0;
    uint64_t mask = 0;
    for (size_t i = 0; i < esz / 8; i++) mask |= (uint64_t)vx_large_word(l, i) << i;
    return mask;
}

// Returns a copy of the array [p] of elements of [esz] bytes resized to
// [n] bytes. The old array is left for the collector, since other code may
// still be reading it.
static void *vx_realloc(void *p, size_t n, size_t esz) {
    vx_page *pg = vx_page_of((uintptr_t)p);
    size_t old = pg ? pg->size : ((vx_large *)p - 1)->size;
    char *q = vx_alloc_t(n, vx_mask_of(p, esz), esz);
    memcpy(q, p, old < n ? old : n);
    return q;
}

static void vx_gc_report(void) {
    vx_gc_total += vx_heap_bytes - vx_h.gc_base;
    vx_h.gc_base = vx_heap_bytes;
    if (vx_heap_bytes > vx_gc_peak) vx_gc_peak = vx_heap_bytes;
    fprintf(stderr, "gc: %zu collections, %zu KB allocated, %zu KB peak heap, %zu KB live\n",
        vx_gc_count, vx_gc_total / 1024, vx_gc_peak / 1024, vx_heap_bytes / 1024);
}

// Starts the collector of the running thread; [base] is the top of the
// stack to scan.
static void vx_gc_thread_init(void *base) {
#ifdef _WIN32
    // A frame address on Windows can point into the middle of the frame's
    // locals, so the scan starts at the top of the thread's stack.
    ULONG_PTR lo, hi;
    GetCurrentThreadStackLimits(&lo, &hi);
    base = (void *)hi;
#endif
    vx_stack_base = base;
    vx_main_task.top = base;
    vx_cur = &vx_main_task;
    vx_gc_countdown = vx_gc_stress;
}

// Starts the collector; [base] is the top of the stack to scan.
static void vx_gc_init(void *base) {
    for (int c = 0, i = 0; i <= VX_SMALL_MAX / 16; i++) {
        while (vx_class_size[c] < (uint32_t)i * 16) c++;
        vx_class_of[i] = (uint8_t)c;
    }
    const char *stress = getenv("VEX_GC_STRESS");
    if (stress) vx_gc_stress = (size_t)strtoull(stress, NULL, 10);
    vx_gc_thread_init(base);
    const char *verify = getenv("VEX_GC_VERIFY");
    vx_gc_verify = verify && *verify && strcmp(verify, "0") != 0;
    const char *stats = getenv("VEX_GC_STATS");
    if (stats && *stats && strcmp(stats, "0") != 0) atexit(vx_gc_report);
}

// --- Printing ---

// The structs being printed, so one that refers back to itself prints as
// Name(...) instead of recursing forever. An open-addressing set: entries
// are added and removed in stack order while printing nests.
static VX_TLS const void **vx_repr_set;
static VX_TLS size_t vx_repr_cap, vx_repr_count;

static bool vx_repr_enter(const void *p) {
    if ((vx_repr_count + 1) * 2 > vx_repr_cap) {
        const void **old = vx_repr_set;
        size_t old_cap = vx_repr_cap;
        vx_repr_cap = old_cap ? old_cap * 2 : 64;
        vx_repr_set = calloc(vx_repr_cap, sizeof(void *));
        if (!vx_repr_set) vx_out_of_memory();
        for (size_t i = 0; i < old_cap; i++) {
            if (!old[i]) continue;
            size_t j = vx_page_hash((uintptr_t)old[i], vx_repr_cap);
            while (vx_repr_set[j]) j = (j + 1) & (vx_repr_cap - 1);
            vx_repr_set[j] = old[i];
        }
        free(old);
    }
    size_t i = vx_page_hash((uintptr_t)p, vx_repr_cap);
    for (; vx_repr_set[i]; i = (i + 1) & (vx_repr_cap - 1)) {
        if (vx_repr_set[i] == p) return true;
    }
    vx_repr_set[i] = p;
    vx_repr_count++;
    return false;
}

static void vx_repr_leave(const void *p) {
    size_t mask = vx_repr_cap - 1, i = vx_page_hash((uintptr_t)p, vx_repr_cap);
    while (vx_repr_set[i] != p) i = (i + 1) & mask;
    // Backward-shift deletion keeps every probe chain unbroken.
    for (size_t j = i;;) {
        vx_repr_set[i] = NULL;
        for (;;) {
            j = (j + 1) & mask;
            if (!vx_repr_set[j]) {
                vx_repr_count--;
                return;
            }
            size_t k = vx_page_hash((uintptr_t)vx_repr_set[j], vx_repr_cap);
            if (i <= j ? (i < k && k <= j) : (i < k || k <= j)) continue;
            break;
        }
        vx_repr_set[i] = vx_repr_set[j];
        i = j;
    }
}

// --- Startup ---

static const char vx_overflow_msg[] = "error: stack overflow: too many nested function calls, "
                                      "probably a function that calls itself without stopping\n";

#ifdef _WIN32
// The bottom of a task's stack on Windows: room for vx_on_fault, which
// runs on the stack that overflowed, then a guard page. Pages are 4 KB on
// every CPU Windows runs on.
#define VX_STACK_GUARD ((size_t)36 << 10)
#define VX_GUARD_PAGE ((size_t)4 << 10)

// Turns running out of stack into a readable error: it means the program
// ran out of stack, almost always through endless recursion. Windows
// reports it for tasks too, since a switch to a task gives the thread its
// stack bounds; touching a task's guard page is caught as well.
static LONG WINAPI vx_on_fault(EXCEPTION_POINTERS *e) {
    DWORD code = e->ExceptionRecord->ExceptionCode;
    uintptr_t addr = (uintptr_t)e->ExceptionRecord->ExceptionInformation[1];
    uintptr_t guard = vx_cur && vx_cur->stack ? (uintptr_t)vx_cur->stack + VX_STACK_GUARD - VX_GUARD_PAGE : 0;
    bool task_guard = code == STATUS_GUARD_PAGE_VIOLATION && guard && addr >= guard && addr < guard + VX_GUARD_PAGE;
    if (code != EXCEPTION_STACK_OVERFLOW && !task_guard) return EXCEPTION_CONTINUE_SEARCH;
    fflush(stdout);
    DWORD n;
    WriteFile(GetStdHandle(STD_ERROR_HANDLE), vx_overflow_msg, sizeof(vx_overflow_msg) - 1, &n, NULL);
    _exit(1);
}

// Keeps room at the end of the running thread's stack for vx_on_fault.
static void vx_stack_reserve(void) {
    ULONG size = 32 << 10;
    SetThreadStackGuarantee(&size);
}

static void vx_catch_stack_overflow(void) {
    vx_stack_reserve();
    AddVectoredExceptionHandler(1, vx_on_fault);
}
#else
// The guard page below a task's stack.
#define VX_STACK_GUARD ((size_t)getpagesize())

static VX_TLS size_t vx_stack_limit;

// Turns a crash just below the stack into a readable error: it means the
// program ran out of stack, almost always through endless recursion.
static void vx_on_fault(int sig, siginfo_t *info, void *ctx) {
    (void)ctx;
    uintptr_t addr = (uintptr_t)info->si_addr, base = (uintptr_t)vx_stack_base;
    bool task_guard = vx_cur && vx_cur->stack && addr >= (uintptr_t)vx_cur->stack && addr < (uintptr_t)vx_cur->stack + VX_STACK_GUARD;
    if (task_guard || ((!vx_cur || vx_cur == &vx_main_task) && addr < base && base - addr <= vx_stack_limit + ((size_t)1 << 20))) {
        fflush(stdout);
        ssize_t n = write(STDERR_FILENO, vx_overflow_msg, sizeof(vx_overflow_msg) - 1);
        (void)n;
        _exit(1);
    }
    // Anything else is a real crash: let it happen as usual.
    signal(sig, SIG_DFL);
}

// Gives the running thread a stack for the fault handler, since the
// faulting stack has no room left. Returns it, for freeing when the thread
// ends, and stores the one it replaces in [old].
static void *vx_signal_stack(stack_t *old) {
    size_t n = 64 * 1024;
    void *p = malloc(n);
    stack_t ss = {.ss_sp = p, .ss_size = n};
    if (!p || sigaltstack(&ss, old) != 0) {
        free(p);
        return NULL;
    }
    return p;
}

static void vx_catch_stack_overflow(void) {
    struct rlimit rl;
    vx_stack_limit = getrlimit(RLIMIT_STACK, &rl) == 0 && rl.rlim_cur != RLIM_INFINITY
        ? (size_t)rl.rlim_cur : (size_t)1 << 30;
    if (!vx_signal_stack(NULL)) return;
    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_sigaction = vx_on_fault;
    sa.sa_flags = SA_SIGINFO | SA_ONSTACK;
    sigemptyset(&sa.sa_mask);
    sigaction(SIGSEGV, &sa, NULL);
    sigaction(SIGBUS, &sa, NULL);
}
#endif

// Prepares the runtime; [base] is the top of the stack.
static void vx_inbox_init(void);
#ifdef _WIN32
static void vx_windows_init(void);
#endif

static void vx_init(void *base) {
#ifdef _WIN32
    vx_windows_init();
#endif
    vx_gc_init(base);
    vx_catch_stack_overflow();
    vx_inbox_init();
}

// --- Strings ---

// Returns a NUL-terminated copy of [s] for C code.
static const char *vx_cstr(vx_str s) {
    char *buf = vx_alloc_atomic((size_t)s.len + 1);
    if (s.len > 0) memcpy(buf, s.ptr, (size_t)s.len);
    buf[s.len] = 0;
    return buf;
}

// The symbol name of C function [name], for calling it under another name
// in Vex code.
#define VX_STR_(x) #x
#define VX_STR(x) VX_STR_(x)
#define VX_SYM(name) VX_STR(__USER_LABEL_PREFIX__) name

// Returns a string that owns a copy of [n] bytes at [p].
static vx_str vx_str_new(const char *p, int64_t n) {
    char *buf = vx_alloc_atomic((size_t)n + 1);
    memcpy(buf, p, (size_t)n);
    buf[n] = 0;
    return (vx_str){buf, n};
}

// Returns a Vex string holding a copy of the C string [p]; NULL gives "".
static vx_str vx_str_from_c(const char *p) {
    return p ? vx_str_new(p, (int64_t)strlen(p)) : VX_LIT("");
}

// Nanoseconds from a fixed point in the past, for measuring time.
static int64_t vx_time_ns(void) {
#ifdef _WIN32
    static int64_t freq;
    LARGE_INTEGER now;
    if (!freq) {
        LARGE_INTEGER f;
        QueryPerformanceFrequency(&f);
        freq = f.QuadPart;
    }
    QueryPerformanceCounter(&now);
    return now.QuadPart / freq * 1000000000 + now.QuadPart % freq * 1000000000 / freq;
#else
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec;
#endif
}

// Seconds since 1970-01-01 UTC.
static int64_t vx_time_unix(void) { return (int64_t)time(NULL); }

// Microseconds since 1970-01-01 UTC, for vex:datetime.
static int64_t vx_datetime_now(void) {
#ifdef _WIN32
    // In 100 ns steps since 1601-01-01 UTC.
    FILETIME ft;
    GetSystemTimePreciseAsFileTime(&ft);
    int64_t t = (int64_t)(((uint64_t)ft.dwHighDateTime << 32) | ft.dwLowDateTime);
    return t / 10 - INT64_C(11644473600000000);
#else
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    return (int64_t)ts.tv_sec * 1000000 + ts.tv_nsec / 1000;
#endif
}

// Returns how many seconds the local time zone is ahead of UTC at
// [seconds] after 1970-01-01 UTC. TZ is read again each time, so changes
// to it take effect.
static int64_t vx_datetime_offset(int64_t seconds) {
    tzset();
    time_t t = (time_t)seconds;
    struct tm tm;
#ifdef _WIN32
    // The local time read back as UTC is ahead of [t] by the offset.
    if (localtime_s(&tm, &t) != 0) return 0;
    return (int64_t)(_mkgmtime(&tm) - t);
#else
    if (!localtime_r(&t, &tm)) return 0;
    return (int64_t)tm.tm_gmtoff;
#endif
}

// Returns 64 bits from the operating system's random source, to seed an
// isolate's generator in vex:random.
static int64_t vx_random_seed(void) {
    uint64_t seed = 0;
#if defined(__APPLE__)
    arc4random_buf(&seed, sizeof seed);
#elif defined(_WIN32)
    BCryptGenRandom(NULL, (PUCHAR)&seed, sizeof seed, BCRYPT_USE_SYSTEM_PREFERRED_RNG);
#else
    FILE *f = fopen("/dev/urandom", "rb");
    bool ok = f && fread(&seed, sizeof seed, 1, f) == 1;
    if (f) fclose(f);
    if (!ok) {
        struct timespec ts;
        clock_gettime(CLOCK_REALTIME, &ts);
        seed = (uint64_t)ts.tv_sec * 1000000007u ^ (uint64_t)ts.tv_nsec ^ (uint64_t)(uintptr_t)&seed;
    }
#endif
    return (int64_t)seed;
}

static vx_str vx_str_concat(vx_str a, vx_str b) {
    if (a.len == 0) return b;
    if (b.len == 0) return a;
    char *buf = vx_alloc_atomic((size_t)(a.len + b.len) + 1);
    memcpy(buf, a.ptr, (size_t)a.len);
    memcpy(buf + a.len, b.ptr, (size_t)b.len);
    return (vx_str){buf, a.len + b.len};
}

// Joins [n] strings, as used by string interpolation.
static vx_str vx_str_join(int n, const vx_str *parts) {
    int64_t total = 0;
    for (int i = 0; i < n; i++) total += parts[i].len;
    char *buf = vx_alloc_atomic((size_t)total + 1);
    int64_t at = 0;
    for (int i = 0; i < n; i++) {
        memcpy(buf + at, parts[i].ptr, (size_t)parts[i].len);
        at += parts[i].len;
    }
    return (vx_str){buf, total};
}

static bool vx_str_eq(vx_str a, vx_str b) {
    return a.len == b.len && memcmp(a.ptr, b.ptr, (size_t)a.len) == 0;
}

// Compares [a] and [b] byte by byte; returns <0, 0 or >0.
static int vx_str_cmp(vx_str a, vx_str b) {
    int64_t n = a.len < b.len ? a.len : b.len;
    int c = memcmp(a.ptr, b.ptr, (size_t)n);
    if (c != 0) return c;
    return a.len < b.len ? -1 : a.len > b.len;
}

// Returns the byte length of the character starting at byte [i] of [s]. A
// byte that doesn't begin a valid UTF-8 sequence is a character of its
// own, so every way of counting characters agrees.
static inline int64_t vx_utf8_width(vx_str s, int64_t i) {
    unsigned char c = (unsigned char)s.ptr[i];
    int64_t n = c < 0x80 ? 1 : c >= 0xC2 && c < 0xE0 ? 2 : c >= 0xE0 && c < 0xF0 ? 3 : c >= 0xF0 && c < 0xF5 ? 4 : 1;
    if (n == 1 || i + n > s.len) return 1;
    for (int64_t k = 1; k < n; k++) {
        if (((unsigned char)s.ptr[i + k] & 0xC0) != 0x80) return 1;
    }
    return n;
}

// Returns the number of characters (code points) in [s].
static int64_t vx_str_len(vx_str s) {
    int64_t n = 0;
    for (int64_t i = 0; i < s.len; i += vx_utf8_width(s, i)) n++;
    return n;
}

// --- Unicode ---

#include "vexuni.h"

#define VX_LEN(a) ((int64_t)(sizeof(a) / sizeof((a)[0])))

// Returns the code point starting at byte [i] of [s], [w] bytes long, or -1
// for a byte that doesn't begin a valid UTF-8 sequence or an encoding that
// is too long or a surrogate.
static int32_t vx_utf8_decode(vx_str s, int64_t i, int64_t w) {
    const unsigned char *p = (const unsigned char *)s.ptr + i;
    int32_t cp;
    switch (w) {
    case 1: return p[0] < 0x80 ? p[0] : -1;
    case 2: return ((p[0] & 0x1F) << 6) | (p[1] & 0x3F);
    case 3:
        cp = ((p[0] & 0x0F) << 12) | ((p[1] & 0x3F) << 6) | (p[2] & 0x3F);
        return cp < 0x800 || (cp >= 0xD800 && cp < 0xE000) ? -1 : cp;
    default:
        cp = ((p[0] & 0x07) << 18) | ((p[1] & 0x3F) << 12) | ((p[2] & 0x3F) << 6) | (p[3] & 0x3F);
        return cp < 0x10000 || cp > 0x10FFFF ? -1 : cp;
    }
}

// Writes [cp] as UTF-8 to [out], if not NULL; returns its byte length.
static int vx_utf8_put(char *out, int32_t cp) {
    unsigned char b[4];
    int n;
    if (cp < 0x80) { b[0] = (unsigned char)cp; n = 1; }
    else if (cp < 0x800) { b[0] = (unsigned char)(0xC0 | cp >> 6); b[1] = (unsigned char)(0x80 | (cp & 0x3F)); n = 2; }
    else if (cp < 0x10000) { b[0] = (unsigned char)(0xE0 | cp >> 12); b[1] = (unsigned char)(0x80 | (cp >> 6 & 0x3F)); b[2] = (unsigned char)(0x80 | (cp & 0x3F)); n = 3; }
    else { b[0] = (unsigned char)(0xF0 | cp >> 18); b[1] = (unsigned char)(0x80 | (cp >> 12 & 0x3F)); b[2] = (unsigned char)(0x80 | (cp >> 6 & 0x3F)); b[3] = (unsigned char)(0x80 | (cp & 0x3F)); n = 4; }
    if (out) memcpy(out, b, (size_t)n);
    return n;
}

// Returns [cp] mapped by the simple case mapping [t] of [n] runs.
static int32_t vx_uni_map(const int32_t (*t)[4], int64_t n, int32_t cp) {
    int64_t lo = 0, hi = n;
    while (lo < hi) {
        int64_t mid = (lo + hi) / 2;
        if (t[mid][0] <= cp) lo = mid + 1; else hi = mid;
    }
    if (lo == 0) return cp;
    const int32_t *r = t[lo - 1];
    return cp <= r[1] && (cp - r[0]) % r[2] == 0 ? cp + r[3] : cp;
}

// Returns the text [cp] maps to in the special mappings [t], or NULL.
static const char *vx_uni_special_find(const vx_uni_special *t, int64_t n, int32_t cp) {
    int64_t lo = 0, hi = n;
    while (lo < hi) {
        int64_t mid = (lo + hi) / 2;
        if (t[mid].cp == (uint32_t)cp) return t[mid].s;
        if (t[mid].cp < (uint32_t)cp) lo = mid + 1; else hi = mid;
    }
    return NULL;
}

enum { VX_CASE_UPPER, VX_CASE_LOWER, VX_CASE_TITLE };

// Writes the case mapping [kind] of [cp] to [out], if not NULL; returns
// its byte length.
static int64_t vx_uni_case(char *out, int32_t cp, int kind) {
    const char *sp = kind == VX_CASE_UPPER ? vx_uni_special_find(vx_uni_special_upper, VX_LEN(vx_uni_special_upper), cp)
                   : kind == VX_CASE_LOWER ? vx_uni_special_find(vx_uni_special_lower, VX_LEN(vx_uni_special_lower), cp)
                   : vx_uni_special_find(vx_uni_special_title, VX_LEN(vx_uni_special_title), cp);
    if (sp) {
        int64_t n = (int64_t)strlen(sp);
        if (out) memcpy(out, sp, (size_t)n);
        return n;
    }
    int32_t m;
    if (kind == VX_CASE_LOWER) m = vx_uni_map(vx_uni_lower, VX_LEN(vx_uni_lower), cp);
    else {
        m = vx_uni_map(vx_uni_upper, VX_LEN(vx_uni_upper), cp);
        if (kind == VX_CASE_TITLE) {
            for (int64_t i = 0; i < VX_LEN(vx_uni_title); i++) {
                if (vx_uni_title[i][0] == (uint32_t)cp) m = (int32_t)vx_uni_title[i][1];
            }
        }
    }
    return vx_utf8_put(out, m);
}

// Maps the characters of [s] from byte [from] up to [to] to the case
// [kind], writing them to [out], if not NULL; returns their byte length.
// Bytes that aren't valid UTF-8 stay as they are.
static int64_t vx_str_case_into(char *out, vx_str s, int64_t from, int64_t to, int kind) {
    int64_t n = 0;
    for (int64_t i = from; i < to;) {
        unsigned char c = (unsigned char)s.ptr[i];
        if (c < 0x80) {
            if (kind == VX_CASE_LOWER ? c >= 'A' && c <= 'Z' : c >= 'a' && c <= 'z') c ^= 0x20;
            if (out) out[n] = (char)c;
            n++, i++;
            continue;
        }
        int64_t w = vx_utf8_width(s, i);
        int32_t cp = vx_utf8_decode(s, i, w);
        if (cp < 0) {
            if (out) memcpy(out + n, s.ptr + i, (size_t)w);
            n += w;
        } else {
            n += vx_uni_case(out ? out + n : NULL, cp, kind);
        }
        i += w;
    }
    return n;
}

// Returns [s] with its first character mapped to the case [first] and the
// rest to [rest], or left as they are when [rest] is -1.
static vx_str vx_str_case(vx_str s, int first, int rest) {
    int64_t w = s.len > 0 ? vx_utf8_width(s, 0) : 0;
    vx_str tail = {s.ptr + w, s.len - w};
    int64_t a = vx_str_case_into(NULL, s, 0, w, first);
    int64_t b = rest < 0 ? tail.len : vx_str_case_into(NULL, tail, 0, tail.len, rest);
    char *buf = vx_alloc_atomic((size_t)(a + b) + 1);
    vx_str_case_into(buf, s, 0, w, first);
    if (rest < 0) memcpy(buf + a, tail.ptr, (size_t)tail.len);
    else vx_str_case_into(buf + a, tail, 0, tail.len, rest);
    buf[a + b] = 0;
    return (vx_str){buf, a + b};
}

// Returns [s] in upper case, with Unicode's full case mappings that need
// no context, such as ß to SS.
static vx_str vx_str_upper(vx_str s) { return vx_str_case(s, VX_CASE_UPPER, VX_CASE_UPPER); }

// Returns [s] in lower case.
static vx_str vx_str_lower(vx_str s) { return vx_str_case(s, VX_CASE_LOWER, VX_CASE_LOWER); }

// Returns [s] with its first character in title case, which is upper case
// for most letters.
static vx_str vx_str_capitalize(vx_str s) { return vx_str_case(s, VX_CASE_TITLE, -1); }

// Returns the grapheme cluster break property of [cp], or of a byte that
// isn't valid UTF-8 when -1.
static int vx_uni_gcb_of(int32_t cp) {
    if (cp < 0) return VX_GCB_OTHER;
    if (cp >= 0x20 && cp < 0x7F) return VX_GCB_OTHER;
    if (cp >= 0xAC00 && cp <= 0xD7A3) return (cp - 0xAC00) % 28 == 0 ? VX_GCB_LV : VX_GCB_LVT;
    int64_t lo = 0, hi = VX_LEN(vx_uni_gcb);
    while (lo < hi) {
        int64_t mid = (lo + hi) / 2;
        if (vx_uni_gcb[mid][0] <= (uint32_t)cp) lo = mid + 1; else hi = mid;
    }
    return lo > 0 && (uint32_t)cp <= vx_uni_gcb[lo - 1][1] ? (int)vx_uni_gcb[lo - 1][2] : VX_GCB_OTHER;
}

// Reports whether a grapheme cluster goes on from a character with the
// break property [prev] to one with [cur]. [pict] is 2 after an emoji, any
// Extend and a ZWJ; [ri] counts the regional indicators before [cur].
static bool vx_grapheme_joins(int prev, int cur, int pict, int ri) {
    if (prev == VX_GCB_CR && cur == VX_GCB_LF) return true;
    if (prev == VX_GCB_CR || prev == VX_GCB_LF || prev == VX_GCB_CONTROL) return false;
    if (cur == VX_GCB_CR || cur == VX_GCB_LF || cur == VX_GCB_CONTROL) return false;
    if (prev == VX_GCB_L && (cur == VX_GCB_L || cur == VX_GCB_V || cur == VX_GCB_LV || cur == VX_GCB_LVT)) return true;
    if ((prev == VX_GCB_LV || prev == VX_GCB_V) && (cur == VX_GCB_V || cur == VX_GCB_T)) return true;
    if ((prev == VX_GCB_LVT || prev == VX_GCB_T) && cur == VX_GCB_T) return true;
    if (cur == VX_GCB_EXTEND || cur == VX_GCB_ZWJ || cur == VX_GCB_SPACINGMARK || prev == VX_GCB_PREPEND) return true;
    if (pict == 2 && cur == VX_GCB_EXTENDED_PICTOGRAPHIC) return true;
    return prev == VX_GCB_REGIONAL_INDICATOR && cur == VX_GCB_REGIONAL_INDICATOR && ri % 2 == 1;
}

// Returns the byte offset where the grapheme cluster starting at byte [i]
// of [s] ends, following the rules of Unicode Standard Annex #29.
static int64_t vx_grapheme_end(vx_str s, int64_t i) {
    int64_t w = vx_utf8_width(s, i);
    int prev = vx_uni_gcb_of(vx_utf8_decode(s, i, w));
    // [pict] is 1 after an emoji and any Extend, 2 after a ZWJ that
    // follows them.
    int pict = prev == VX_GCB_EXTENDED_PICTOGRAPHIC, ri = prev == VX_GCB_REGIONAL_INDICATOR;
    for (i += w; i < s.len; i += w) {
        w = vx_utf8_width(s, i);
        int cur = vx_uni_gcb_of(vx_utf8_decode(s, i, w));
        if (!vx_grapheme_joins(prev, cur, pict, ri)) break;
        if (cur == VX_GCB_EXTENDED_PICTOGRAPHIC) pict = 1;
        else if (cur == VX_GCB_EXTEND && pict == 1) pict = 1;
        else if (cur == VX_GCB_ZWJ && pict == 1) pict = 2;
        else pict = 0;
        ri = cur == VX_GCB_REGIONAL_INDICATOR ? ri + 1 : 0;
        prev = cur;
    }
    return i;
}

// Returns the number of grapheme clusters, the characters a reader sees,
// in [s].
static int64_t vx_str_grapheme_count(vx_str s) {
    int64_t n = 0;
    for (int64_t i = 0; i < s.len; i = vx_grapheme_end(s, i)) n++;
    return n;
}

// Returns the byte offset of [sub] in [s] from [from], or -1.
static int64_t vx_str_find(vx_str s, vx_str sub, int64_t from) {
    if (sub.len == 0) return from;
    for (int64_t i = from; i + sub.len <= s.len; i++) {
        if (memcmp(s.ptr + i, sub.ptr, (size_t)sub.len) == 0) return i;
    }
    return -1;
}

static bool vx_str_contains(vx_str s, vx_str sub) { return vx_str_find(s, sub, 0) >= 0; }

// --- Lists ---

// Returns a list of the [n] elements at [items], with room for at least
// [cap]. Its elements are laid out as [mask]; the elements of a list with
// a mask of 0 hold no pointers, so the collector doesn't scan them.
static vx_list *vx_list_make(int64_t esz, int64_t n, int64_t cap, const void *items, uint64_t mask) {
    vx_list *l = vx_alloc_obj(sizeof(vx_list), VX_PM_LIST);
    if (cap < n) cap = n;
    l->esz = esz;
    l->len = n;
    l->cap = cap < 4 ? 4 : cap;
    size_t size = (size_t)(l->cap * esz);
    l->data = vx_alloc_t(size, mask, (size_t)esz);
    if (n > 0) memcpy(l->data, items, (size_t)(n * esz));
    vx_remember(l);
    vx_remember(l->data);
    return l;
}

static vx_list *vx_list_new(int64_t esz, uint64_t mask, int64_t n, const void *items) { return vx_list_make(esz, n, 0, items, mask); }

// Like [vx_list_new], for elements that hold no pointers.
static vx_list *vx_list_flat(int64_t esz, int64_t n, const void *items) { return vx_list_make(esz, n, 0, items, 0); }

// Returns the layout of the elements of [l].
static uint64_t vx_list_mask(vx_list *l);

// Returns the number of values in the range lo..hi, or lo..=hi when
// [inclusive], or 0 when it can't be counted.
static int64_t vx_range_len(int64_t lo, int64_t hi, bool inclusive) {
    int64_t n;
    if (__builtin_sub_overflow(hi, lo, &n) || (inclusive && __builtin_add_overflow(n, 1, &n))) return 0;
    return n > 0 ? n : 0;
}

// Reports whether the object [p] was allocated without pointers.
static bool vx_is_atomic(void *p) {
    vx_page *pg = vx_page_of((uintptr_t)p);
    return pg ? pg->atomic : ((vx_large *)p - 1)->atomic;
}

// The element [i] of list [l] of [T], or a stop at [line] when [i] is out
// of range. Unlike [vx_list_at], the element size is known, so indexing
// compiles to a single load.
#define VX_AT(T, l, i, line) ({ \
    vx_list *vx_at_l = (l); \
    int64_t vx_at_i = (i); \
    if (__builtin_expect((uint64_t)vx_at_i >= (uint64_t)vx_at_l->len, 0)) vx_list_at(vx_at_l, vx_at_i, line); \
    (T *)vx_at_l->data + vx_at_i; \
})

// Adds [v], a [T], to the end of [l]. Only a full or constant list takes
// the slow path through [vx_list_push].
#define VX_PUSH(T, l, v) ({ \
    vx_list *vx_push_l = (l); \
    T vx_push_v = (v); \
    if (__builtin_expect(vx_push_l->len < vx_push_l->cap, 1)) ((T *)vx_push_l->data)[vx_push_l->len++] = vx_push_v; \
    else vx_list_push(vx_push_l, &vx_push_v); \
})

// Like [VX_PUSH], for values that hold pointers: the list's elements are
// then recorded by the write barrier.
#define VX_PUSH_REF(T, l, v) ({ \
    vx_list *vx_ref_l = (l); \
    VX_PUSH(T, vx_ref_l, v); \
    vx_remember(vx_ref_l->data); \
})

// Returns a pointer to element [i], stopping the program if it is out of range.
static void *vx_list_at(vx_list *l, int64_t i, int line) {
    if (i < 0 || i >= l->len) {
        if (l->len == 0) vx_fail(line, "index %" PRId64 " is out of range: the list is empty", i);
        vx_fail(line, "index %" PRId64 " is out of range for a list of length %" PRId64, i, l->len);
    }
    return l->data + i * l->esz;
}

static void vx_list_push(vx_list *l, const void *item) {
    if (l->len >= l->cap) {
        if (l->cap < 0) vx_fail(0, "can't change a constant list");
        l->cap *= 2;
        l->data = vx_realloc(l->data, (size_t)(l->cap * l->esz), (size_t)l->esz);
        vx_remember(l);
    }
    memcpy(l->data + l->len * l->esz, item, (size_t)l->esz);
    l->len++;
    vx_remember(l->data);
}

// Removes the last element and returns a pointer to it, valid until the
// next push.
// Marks [l] as part of a constant: changing it is then an error. A frozen
// list has a negative capacity.
static vx_list *vx_list_freeze(vx_list *l) {
    l->cap = -1;
    return l;
}

// Returns [l], failing at [line] if it is part of a constant.
static inline vx_list *vx_list_mut(vx_list *l, int line) {
    if (__builtin_expect(l->cap < 0, 0)) vx_fail(line, "can't change a constant list; change a copy made with .copy()");
    return l;
}

static uint64_t vx_list_mask(vx_list *l) { return vx_mask_of(l->data, (size_t)l->esz); }

static vx_list *vx_list_copy(vx_list *l) { return vx_list_make(l->esz, l->len, 0, l->data, vx_list_mask(l)); }

static void *vx_list_pop(vx_list *l, int line) {
    if (l->len == 0) vx_fail(line, "can't pop from an empty list");
    l->len--;
    return l->data + l->len * l->esz;
}

// Reports whether the element at [a] goes before the one at [b].
typedef bool (*vx_less_fn)(void *ctx, const void *a, const void *b);

// Sorts [l] in place with a stable merge sort. The sort works on a copy, so
// a comparison that changes the list can't break it, but is an error.
static void vx_list_sort(vx_list *l, vx_less_fn less, void *ctx, int line) {
    int64_t n = l->len, esz = l->esz;
    if (n < 2) return;
    uint64_t mask = vx_list_mask(l);
    bool atomic = mask == 0;
    size_t size = (size_t)(n * esz);
    char *a = vx_alloc_t(size, mask, (size_t)esz);
    char *b = vx_alloc_t(size, mask, (size_t)esz);
    memcpy(a, l->data, size);
    for (int64_t w = 1; w < n; w *= 2) {
        for (int64_t lo = 0; lo < n; lo += 2 * w) {
            int64_t mid = lo + w < n ? lo + w : n, hi = lo + 2 * w < n ? lo + 2 * w : n;
            int64_t i = lo, j = mid, k = lo;
            // Takes from the right run only when it is strictly smaller,
            // which keeps equal elements in order.
            // A comparison may collect, after which [b] is old, so each
            // write goes through the barrier.
            while (i < mid && j < hi) {
                if (less(ctx, a + j * esz, a + i * esz)) memcpy(b + k++ * esz, a + j++ * esz, (size_t)esz);
                else memcpy(b + k++ * esz, a + i++ * esz, (size_t)esz);
                if (!atomic) vx_remember(b);
            }
            memcpy(b + k * esz, a + i * esz, (size_t)((mid - i) * esz));
            k += mid - i;
            memcpy(b + k * esz, a + j * esz, (size_t)((hi - j) * esz));
            if (!atomic) vx_remember(b);
        }
        char *t = a;
        a = b;
        b = t;
    }
    if (l->len != n) vx_fail(line, "the list changed while it was being sorted");
    memcpy(l->data, a, size);
    if (!atomic) vx_remember(l->data);
}

static void vx_list_insert(vx_list *l, const void *item, int64_t at, int line) {
    if (at < 0 || at > l->len) {
        vx_fail(line, "insert at %" PRId64 " is out of range for a list of length %" PRId64, at, l->len);
    }
    vx_list_push(l, item);
    char *p = l->data + at * l->esz;
    memmove(p + l->esz, p, (size_t)((l->len - 1 - at) * l->esz));
    memcpy(p, item, (size_t)l->esz);
}

// Removes the element at [at], storing it in [out].
static void vx_list_remove_at(vx_list *l, int64_t at, int line, void *out) {
    char *p = vx_list_at(l, at, line);
    memcpy(out, p, (size_t)l->esz);
    memmove(p, p + l->esz, (size_t)((l->len - 1 - at) * l->esz));
    l->len--;
    if (!vx_is_atomic(l->data)) vx_remember(l->data);
}

static vx_list *vx_list_slice(vx_list *l, int64_t from, int64_t to, int line) {
    if (from < 0 || to > l->len || from > to) {
        vx_fail(line, "slice(%" PRId64 ", %" PRId64 ") is out of range for a list of length %" PRId64, from, to, l->len);
    }
    return vx_list_make(l->esz, to - from, 0, l->data + from * l->esz, vx_list_mask(l));
}

static vx_list *vx_list_reversed(vx_list *l) {
    vx_list *r = vx_list_copy(l);
    for (int64_t i = 0, j = r->len - 1; i < j; i++, j--) {
        char t[r->esz];
        memcpy(t, r->data + i * r->esz, (size_t)r->esz);
        memcpy(r->data + i * r->esz, r->data + j * r->esz, (size_t)r->esz);
        memcpy(r->data + j * r->esz, t, (size_t)r->esz);
    }
    return r;
}

// Adds the elements of [src], which have the same size, to the end of [l].
static void vx_list_append(vx_list *l, vx_list *src) {
    int64_t n = src->len;
    if (l->len + n > l->cap) {
        if (l->cap < 0) vx_fail(0, "can't change a constant list");
        while (l->len + n > l->cap) l->cap *= 2;
        l->data = vx_realloc(l->data, (size_t)(l->cap * l->esz), (size_t)l->esz);
        vx_remember(l);
    }
    if (n > 0) memcpy(l->data + l->len * l->esz, src->data, (size_t)(n * l->esz));
    l->len += n;
    vx_remember(l->data);
}

// Splits [s] at each [sep]. A positive [limit] gives at most that many
// parts, the last holding the rest of [s]; 0 gives them all.
static vx_list *vx_str_split(vx_str s, vx_str sep, int64_t limit, int line) {
    if (sep.len == 0) vx_fail(line, "split separator can't be empty");
    if (limit < 0) vx_fail(line, "split limit can't be negative, found %" PRId64, limit);
    vx_list *l = vx_list_new(sizeof(vx_str), VX_PM_STR, 0, NULL);
    int64_t start = 0;
    while (limit == 0 || l->len < limit - 1) {
        int64_t at = vx_str_find(s, sep, start);
        if (at < 0) break;
        vx_str part = {s.ptr + start, at - start};
        vx_list_push(l, &part);
        start = at + sep.len;
    }
    vx_str last = {s.ptr + start, s.len - start};
    vx_list_push(l, &last);
    return l;
}

static vx_str vx_str_replace(vx_str s, vx_str old, vx_str with, int line) {
    if (old.len == 0) vx_fail(line, "replace needs text to look for, not an empty string");
    int64_t at = vx_str_find(s, old, 0);
    if (at < 0) return s;
    int64_t n = 0, cap = s.len + 16;
    char *buf = vx_alloc_atomic((size_t)cap + 1);
    int64_t start = 0;
    while (at >= 0) {
        int64_t need = n + (at - start) + with.len + (s.len - at);
        if (need > cap) {
            cap = need * 2;
            buf = vx_realloc(buf, (size_t)cap + 1, 1);
        }
        memcpy(buf + n, s.ptr + start, (size_t)(at - start));
        n += at - start;
        if (with.len > 0) memcpy(buf + n, with.ptr, (size_t)with.len);
        n += with.len;
        start = at + old.len;
        at = vx_str_find(s, old, start);
    }
    if (n + (s.len - start) > cap) buf = vx_realloc(buf, (size_t)(n + s.len - start) + 1, 1);
    memcpy(buf + n, s.ptr + start, (size_t)(s.len - start));
    n += s.len - start;
    buf[n] = 0;
    return (vx_str){buf, n};
}

static inline bool vx_is_space(char c) { return c == ' ' || (c >= '\t' && c <= '\r'); }

static vx_str vx_str_trim_start(vx_str s) {
    int64_t i = 0;
    while (i < s.len && vx_is_space(s.ptr[i])) i++;
    return (vx_str){s.ptr + i, s.len - i};
}

static vx_str vx_str_trim_end(vx_str s) {
    int64_t n = s.len;
    while (n > 0 && vx_is_space(s.ptr[n - 1])) n--;
    return (vx_str){s.ptr, n};
}

static vx_str vx_str_trim(vx_str s) { return vx_str_trim_end(vx_str_trim_start(s)); }

static bool vx_str_starts_with(vx_str s, vx_str p) {
    return p.len <= s.len && (p.len == 0 || memcmp(s.ptr, p.ptr, (size_t)p.len) == 0);
}

static bool vx_str_ends_with(vx_str s, vx_str p) {
    return p.len <= s.len && (p.len == 0 || memcmp(s.ptr + s.len - p.len, p.ptr, (size_t)p.len) == 0);
}

// Returns the character index of [sub] in [s], or -1.
static int64_t vx_str_index_of(vx_str s, vx_str sub) {
    int64_t at = vx_str_find(s, sub, 0);
    return at < 0 ? -1 : vx_str_len((vx_str){s.ptr, at});
}

// Returns the byte offset of character [i] in [s], or s.len when [i] is
// the length.
static int64_t vx_str_offset(vx_str s, int64_t i) {
    int64_t b = 0;
    for (; b < s.len; b += vx_utf8_width(s, b)) {
        if (i == 0) return b;
        i--;
    }
    return s.len;
}

// Returns the characters of [s] from index [from] up to, not including,
// [to].
static vx_str vx_str_substring(vx_str s, int64_t from, int64_t to, int line) {
    int64_t n = vx_str_len(s);
    if (from < 0 || to > n || from > to) {
        vx_fail(line, "substring(%" PRId64 ", %" PRId64 ") is out of range for a string of length %" PRId64, from, to, n);
    }
    int64_t a = vx_str_offset(s, from);
    int64_t b = a + vx_str_offset((vx_str){s.ptr + a, s.len - a}, to - from);
    return (vx_str){s.ptr + a, b - a};
}

// Returns the character index of the last [sub] in [s], or -1.
static int64_t vx_str_last_index_of(vx_str s, vx_str sub) {
    for (int64_t i = s.len - sub.len; i >= 0; i--) {
        if (memcmp(s.ptr + i, sub.ptr, (size_t)sub.len) == 0) return vx_str_len((vx_str){s.ptr, i});
    }
    return -1;
}

// Returns how many times [sub] appears in [s] without overlapping.
static int64_t vx_str_count(vx_str s, vx_str sub, int line) {
    if (sub.len == 0) vx_fail(line, "count needs text to look for, not an empty string");
    int64_t n = 0;
    for (int64_t at = vx_str_find(s, sub, 0); at >= 0; at = vx_str_find(s, sub, at + sub.len)) n++;
    return n;
}

// Returns [s] repeated [n] times; a count below 1 gives "".
static vx_str vx_str_repeat(vx_str s, int64_t n, int line) {
    if (n <= 0 || s.len == 0) return VX_LIT("");
    if (n > (INT64_MAX - 1) / s.len) vx_fail(line, "repeat(%" PRId64 ") makes a string too long", n);
    char *buf = vx_alloc_atomic((size_t)(s.len * n) + 1);
    for (int64_t i = 0; i < n; i++) memcpy(buf + i * s.len, s.ptr, (size_t)s.len);
    buf[s.len * n] = 0;
    return (vx_str){buf, s.len * n};
}

// Returns [s] with copies of [fill] added at the start, or at the end, until
// it is [width] characters long. The last copy is cut short when needed.
static vx_str vx_str_pad(vx_str s, int64_t width, vx_str fill, bool start, int line) {
    int64_t need = width - vx_str_len(s);
    if (need <= 0 || fill.len == 0) return s;
    int64_t per = vx_str_len(fill), whole = need / per;
    if (whole >= (INT64_MAX - s.len) / fill.len - 1) vx_fail(line, "padding to %" PRId64 " makes a string too long", width);
    int64_t rest = vx_str_offset(fill, need % per);
    int64_t pad = whole * fill.len + rest, n = s.len + pad;
    char *buf = vx_alloc_atomic((size_t)n + 1);
    char *p = start ? buf : buf + s.len;
    for (int64_t i = 0; i < whole; i++) memcpy(p + i * fill.len, fill.ptr, (size_t)fill.len);
    memcpy(p + whole * fill.len, fill.ptr, (size_t)rest);
    memcpy(start ? buf + pad : buf, s.ptr, (size_t)s.len);
    buf[n] = 0;
    return (vx_str){buf, n};
}

// Returns the grapheme clusters of [s] in reverse order, so accents and
// emoji sequences stay whole.
static vx_str vx_str_reversed(vx_str s) {
    char *buf = vx_alloc_atomic((size_t)s.len + 1);
    for (int64_t i = 0; i < s.len;) {
        int64_t j = vx_grapheme_end(s, i);
        memcpy(buf + s.len - j, s.ptr + i, (size_t)(j - i));
        i = j;
    }
    buf[s.len] = 0;
    return (vx_str){buf, s.len};
}

// --- Ranges ---

// A range of ints: the [n] values from [first] on, [step] apart, with
// [step] negative once reversed. [start], [end] and [incl] keep the bounds
// as written, for printing.
typedef struct { int64_t first, step, start, end; uint64_t n; bool incl; } vx_range;

static vx_range vx_range_new(int64_t start, int64_t end, bool incl) {
    uint64_t n;
    // The whole of int has one more value than n can count; it gives up
    // the last.
    if (incl) n = start > end ? 0 : start == INT64_MIN && end == INT64_MAX ? UINT64_MAX : (uint64_t)end - (uint64_t)start + 1;
    else n = start >= end ? 0 : (uint64_t)end - (uint64_t)start;
    return (vx_range){start, 1, start, end, n, incl};
}

// Returns the value at position [k] of [r].
static inline int64_t vx_range_at(vx_range r, uint64_t k) {
    return (int64_t)((uint64_t)r.first + k * (uint64_t)r.step);
}

static int64_t vx_range_count(vx_range r, int line) {
    if (r.n > (uint64_t)INT64_MAX) vx_fail(line, "the range has more than int.max values");
    return (int64_t)r.n;
}

// Returns every [by]th value of [r], starting from its first.
static vx_range vx_range_step(vx_range r, int64_t by, int line) {
    if (by <= 0) vx_fail(line, "a range's step must be more than 0, found %" PRId64, by);
    if (r.n > 0) r.n = (r.n - 1) / (uint64_t)by + 1;
    uint64_t mag = r.step < 0 ? -(uint64_t)r.step : (uint64_t)r.step, step;
    if (__builtin_mul_overflow(mag, (uint64_t)by, &step) || step > (uint64_t)INT64_MAX) {
        // With one value left the step no longer matters.
        if (r.n > 1) vx_fail(line, "a range's step can't be more than int.max");
        step = mag;
    }
    r.step = r.step < 0 ? -(int64_t)step : (int64_t)step;
    return r;
}

// Returns [r] with its values in the opposite order.
static vx_range vx_range_reversed(vx_range r) {
    if (r.n > 0) r.first = vx_range_at(r, r.n - 1);
    r.step = -r.step;
    return r;
}

static bool vx_range_contains(vx_range r, int64_t x) {
    if (r.n == 0) return false;
    int64_t a = r.first, b = vx_range_at(r, r.n - 1);
    int64_t lo = a < b ? a : b, hi = a < b ? b : a;
    uint64_t step = (uint64_t)(r.step < 0 ? -r.step : r.step);
    return x >= lo && x <= hi && ((uint64_t)x - (uint64_t)lo) % step == 0;
}

// Reports whether [a] and [b] give the same values.
static bool vx_range_eq(vx_range a, vx_range b) {
    return a.n == b.n && (a.n == 0 || (a.first == b.first && (a.n == 1 || a.step == b.step)));
}

// Writes into [buf] the range [start]..[end], or ..= when [incl], with
// `.step(step)` where step isn't 1 and `.reversed()` before or after it.
static int vx_range_form(char *buf, size_t size, int64_t start, int64_t end, bool incl, int64_t step, int reversed) {
    int n = snprintf(buf, size, incl ? "%" PRId64 "..=%" PRId64 : "%" PRId64 "..%" PRId64, start, end);
    if (step == 1 && !reversed) return n;
    char inner[64];
    memcpy(inner, buf, (size_t)n + 1);
    n = snprintf(buf, size, "(%s)", inner);
    if (reversed == 1) n += snprintf(buf + n, size - (size_t)n, ".reversed()");
    if (step != 1) n += snprintf(buf + n, size - (size_t)n, ".step(%" PRId64 ")", step);
    if (reversed == 2) n += snprintf(buf + n, size - (size_t)n, ".reversed()");
    return n;
}

// Returns code that gives the values of [r]: from the bounds as written
// when one of the forms using them does, otherwise from its first and
// last values.
static vx_str vx_range_str(vx_range r) {
    char buf[160];
    int64_t step = r.step < 0 ? -r.step : r.step;
    vx_range base = vx_range_new(r.start, r.end, r.incl);
    vx_range forms[2];
    int kinds[2], count = 0;
    if (r.step > 0) {
        forms[count] = vx_range_step(base, step, 0);
        kinds[count++] = 0;
    } else {
        forms[count] = vx_range_reversed(vx_range_step(base, step, 0));
        kinds[count++] = 2;
        forms[count] = vx_range_step(vx_range_reversed(base), step, 0);
        kinds[count++] = 1;
    }
    for (int i = 0; i < count; i++) {
        vx_range f = forms[i];
        if (f.n == r.n && f.first == r.first && f.step == r.step) {
            return vx_str_new(buf, vx_range_form(buf, sizeof buf, r.start, r.end, r.incl, step, kinds[i]));
        }
    }
    if (r.n == 0) return vx_str_new(buf, vx_range_form(buf, sizeof buf, r.start, r.start, false, 1, 0));
    int64_t last = vx_range_at(r, r.n - 1);
    if (r.step > 0) return vx_str_new(buf, vx_range_form(buf, sizeof buf, r.first, last, true, step, 0));
    return vx_str_new(buf, vx_range_form(buf, sizeof buf, last, r.first, true, step, 2));
}

static vx_list *vx_range_list(vx_range r, int line) {
    int64_t n = vx_range_count(r, line);
    vx_list *l = vx_list_make(sizeof(int64_t), 0, n, NULL, 0);
    for (int64_t k = 0; k < n; k++) ((int64_t *)l->data)[k] = vx_range_at(r, (uint64_t)k);
    l->len = n;
    return l;
}

// Returns the elements of [l] at the positions [r] gives. [what] names
// [l] in the message when [r] reaches outside it.
static vx_list *vx_slice_range(vx_list *l, vx_range r, int line, const char *what) {
    uint64_t flat = vx_list_mask(l);
    if (r.step == 1) {
        int64_t from = r.first, to = vx_range_at(r, r.n);
        if (from < 0 || from > l->len || (r.n > 0 && to > l->len) || r.n > (uint64_t)l->len) {
            vx_str s = vx_range_str(r);
            vx_fail(line, "range %.*s is out of range for %s of length %" PRId64, (int)s.len, s.ptr, what, l->len);
        }
        return vx_list_make(l->esz, (int64_t)r.n, 0, l->data + from * l->esz, flat);
    }
    int64_t n = vx_range_count(r, line);
    for (int64_t k = 0; k < n; k++) {
        int64_t i = vx_range_at(r, (uint64_t)k);
        if (i < 0 || i >= l->len) {
            vx_str s = vx_range_str(r);
            vx_fail(line, "range %.*s is out of range for %s of length %" PRId64, (int)s.len, s.ptr, what, l->len);
        }
    }
    vx_list *out = vx_list_make(l->esz, 0, n, NULL, flat);
    for (int64_t k = 0; k < n; k++) {
        memcpy(out->data + k * out->esz, l->data + vx_range_at(r, (uint64_t)k) * l->esz, (size_t)l->esz);
    }
    out->len = n;
    return out;
}

static vx_list *vx_list_slice_range(vx_list *l, vx_range r, int line) { return vx_slice_range(l, r, line, "a list"); }

// Returns the characters of [s] at the positions [r] gives.
static vx_str vx_str_slice_range(vx_str s, vx_range r, int line) {
    int64_t len = vx_str_len(s);
    if (r.step == 1) {
        int64_t from = r.first, to = vx_range_at(r, r.n);
        if (from < 0 || from > len || r.n > (uint64_t)len || (r.n > 0 && to > len)) {
            vx_str rs = vx_range_str(r);
            vx_fail(line, "range %.*s is out of range for a string of length %" PRId64, (int)rs.len, rs.ptr, len);
        }
        if (r.n == 0) return VX_LIT("");
        return vx_str_substring(s, from, to, line);
    }
    int64_t n = vx_range_count(r, line);
    int64_t *offs = vx_alloc_atomic((size_t)(len + 1) * sizeof(int64_t));
    int64_t b = 0;
    for (int64_t i = 0; i < len; i++) {
        offs[i] = b;
        b += vx_utf8_width(s, b);
    }
    offs[len] = s.len;
    int64_t bytes = 0;
    for (int64_t k = 0; k < n; k++) {
        int64_t i = vx_range_at(r, (uint64_t)k);
        if (i < 0 || i >= len) {
            vx_str rs = vx_range_str(r);
            vx_fail(line, "range %.*s is out of range for a string of length %" PRId64, (int)rs.len, rs.ptr, len);
        }
        bytes += offs[i + 1] - offs[i];
    }
    char *buf = vx_alloc_atomic((size_t)bytes + 1);
    int64_t at = 0;
    for (int64_t k = 0; k < n; k++) {
        int64_t i = vx_range_at(r, (uint64_t)k);
        memcpy(buf + at, s.ptr + offs[i], (size_t)(offs[i + 1] - offs[i]));
        at += offs[i + 1] - offs[i];
    }
    buf[at] = 0;
    return (vx_str){buf, at};
}

// --- Bytes ---

// Bytes are a list of uint8_t elements, allocated without pointers so the
// collector never scans them.

// Returns new Bytes holding a copy of the [n] bytes at [items].
static vx_list *vx_bytes_new(int64_t n, const void *items) { return vx_list_make(1, n, 0, items, 0); }

// Returns [n] zero bytes.
static vx_list *vx_bytes_count(int64_t n, int line) {
    if (n < 0) vx_fail(line, "Bytes(count:) needs a count of 0 or more, found %" PRId64, n);
    vx_list *b = vx_list_make(1, 0, n, NULL, 0);
    memset(b->data, 0, (size_t)n);
    b->len = n;
    return b;
}

// Returns [v] as a byte, stopping the program when it is outside 0 to 255.
static inline uint8_t vx_byte(int64_t v, int line) {
    if (__builtin_expect((uint64_t)v > 255, 0)) vx_fail(line, "%" PRId64 " is not a byte; a byte is 0 to 255", v);
    return (uint8_t)v;
}

// Returns the ints of [l] as Bytes.
static vx_list *vx_bytes_from_list(vx_list *l, int line) {
    vx_list *b = vx_list_make(1, 0, l->len, NULL, 0);
    for (int64_t i = 0; i < l->len; i++) b->data[i] = (char)vx_byte(((int64_t *)l->data)[i], line);
    b->len = l->len;
    return b;
}

__attribute__((noreturn, cold))
static void vx_bytes_index_fail(vx_list *b, int64_t i, int line) {
    vx_fail(line, "index %" PRId64 " is out of range for Bytes of length %" PRId64, i, b->len);
}

// A pointer to byte [i] of [b], or a stop at [line] when [i] is out of
// range.
#define VX_BYTE(b, i, line) ({ \
    vx_list *vx_byte_b = (b); \
    int64_t vx_byte_i = (i); \
    if (__builtin_expect((uint64_t)vx_byte_i >= (uint64_t)vx_byte_b->len, 0)) vx_bytes_index_fail(vx_byte_b, vx_byte_i, line); \
    (uint8_t *)vx_byte_b->data + vx_byte_i; \
})

static vx_list *vx_bytes_slice_range(vx_list *b, vx_range r, int line) { return vx_slice_range(b, r, line, "Bytes"); }

static bool vx_bytes_eq(vx_list *a, vx_list *b) {
    return a == b || (a->len == b->len && memcmp(a->data, b->data, (size_t)a->len) == 0);
}

// Returns the bytes of [b] as a list of ints.
static vx_list *vx_bytes_list(vx_list *b) {
    vx_list *l = vx_list_make(sizeof(int64_t), 0, b->len, NULL, 0);
    for (int64_t i = 0; i < b->len; i++) ((int64_t *)l->data)[i] = (uint8_t)b->data[i];
    l->len = b->len;
    return l;
}

static const char vx_hex_digits[] = "0123456789abcdef";

// Returns [b] as lowercase hex, two digits a byte.
static vx_str vx_bytes_hex(vx_list *b) {
    char *buf = vx_alloc_atomic((size_t)b->len * 2 + 1);
    for (int64_t i = 0; i < b->len; i++) {
        uint8_t c = (uint8_t)b->data[i];
        buf[2 * i] = vx_hex_digits[c >> 4];
        buf[2 * i + 1] = vx_hex_digits[c & 15];
    }
    buf[2 * b->len] = 0;
    return (vx_str){buf, 2 * b->len};
}

static int vx_hex_value(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

// Returns the bytes the hex digits of [s] spell, or NULL when [s] isn't
// pairs of hex digits.
static vx_list *vx_bytes_from_hex(vx_str s) {
    if (s.len % 2 != 0) return NULL;
    vx_list *b = vx_list_make(1, 0, s.len / 2, NULL, 0);
    for (int64_t i = 0; i < s.len; i += 2) {
        int hi = vx_hex_value(s.ptr[i]), lo = vx_hex_value(s.ptr[i + 1]);
        if (hi < 0 || lo < 0) return NULL;
        b->data[i / 2] = (char)(hi << 4 | lo);
    }
    b->len = s.len / 2;
    return b;
}

// Returns [b] as a string, or a string with a NULL pointer when [b] isn't
// valid UTF-8: overlong forms, surrogates and code points past U+10FFFF
// are rejected.
static vx_str vx_bytes_utf8(vx_list *b) {
    const unsigned char *p = (const unsigned char *)b->data;
    int64_t n = b->len;
    for (int64_t i = 0; i < n;) {
        unsigned c = p[i];
        if (c < 0x80) {
            i++;
            continue;
        }
        int k;
        uint32_t cp, min;
        if (c >= 0xC2 && c < 0xE0) k = 1, cp = c & 0x1F, min = 0x80;
        else if (c >= 0xE0 && c < 0xF0) k = 2, cp = c & 0x0F, min = 0x800;
        else if (c >= 0xF0 && c < 0xF5) k = 3, cp = c & 0x07, min = 0x10000;
        else return (vx_str){NULL, 0};
        if (n - i <= k) return (vx_str){NULL, 0};
        for (int j = 1; j <= k; j++) {
            if ((p[i + j] & 0xC0) != 0x80) return (vx_str){NULL, 0};
            cp = cp << 6 | (p[i + j] & 0x3F);
        }
        if (cp < min || cp > 0x10FFFF || (cp >= 0xD800 && cp < 0xE000)) return (vx_str){NULL, 0};
        i += k + 1;
    }
    return vx_str_new(b->data, n);
}

// Returns [b] as printed: its length and its first 32 bytes in hex, such
// as `Bytes(2 bytes: 48 69)`.
static vx_str vx_bytes_str(vx_list *b) {
    int64_t shown = b->len < 32 ? b->len : 32;
    char *buf = vx_alloc_atomic((size_t)(48 + 3 * shown + 6));
    int n = snprintf(buf, 48, "Bytes(%" PRId64 " byte%s", b->len, b->len == 1 ? "" : "s");
    if (shown > 0) buf[n++] = ':';
    for (int64_t i = 0; i < shown; i++) {
        uint8_t c = (uint8_t)b->data[i];
        buf[n++] = ' ';
        buf[n++] = vx_hex_digits[c >> 4];
        buf[n++] = vx_hex_digits[c & 15];
    }
    if (b->len > shown) {
        memcpy(buf + n, " ...", 4);
        n += 4;
    }
    buf[n++] = ')';
    buf[n] = 0;
    return (vx_str){buf, n};
}

static const char vx_base64_digits[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

// Returns [b] in standard base64, with padding.
static vx_str vx_bytes_base64(vx_list *b) {
    const unsigned char *p = (const unsigned char *)b->data;
    int64_t n = b->len, len = (n + 2) / 3 * 4;
    char *buf = vx_alloc_atomic((size_t)len + 1);
    int64_t o = 0;
    for (int64_t i = 0; i < n; i += 3) {
        uint32_t v = (uint32_t)p[i] << 16 | (i + 1 < n ? (uint32_t)p[i + 1] << 8 : 0) | (i + 2 < n ? p[i + 2] : 0);
        buf[o++] = vx_base64_digits[v >> 18];
        buf[o++] = vx_base64_digits[v >> 12 & 63];
        buf[o++] = i + 1 < n ? vx_base64_digits[v >> 6 & 63] : '=';
        buf[o++] = i + 2 < n ? vx_base64_digits[v & 63] : '=';
    }
    buf[len] = 0;
    return (vx_str){buf, len};
}

// Returns the bytes the standard base64 text [s] holds, or NULL when it
// isn't base64. Padding may be left out.
static vx_list *vx_bytes_from_base64(vx_str s) {
    int64_t n = s.len;
    if (n % 4 == 0 && n > 0 && s.ptr[n - 1] == '=') n -= s.ptr[n - 2] == '=' ? 2 : 1;
    if (n % 4 == 1) return NULL;
    vx_list *b = vx_list_make(1, 0, n * 3 / 4, NULL, 0);
    uint32_t acc = 0;
    int bits = 0;
    int64_t o = 0;
    for (int64_t i = 0; i < n; i++) {
        const char *d = memchr(vx_base64_digits, s.ptr[i], 64);
        if (!d) return NULL;
        acc = acc << 6 | (uint32_t)(d - vx_base64_digits);
        bits += 6;
        if (bits >= 8) {
            bits -= 8;
            b->data[o++] = (char)(acc >> bits);
        }
    }
    b->len = o;
    return b;
}

static vx_list *vx_str_chars(vx_str s) {
    vx_list *l = vx_list_new(sizeof(vx_str), VX_PM_STR, 0, NULL);
    int64_t i = 0;
    while (i < s.len) {
        int64_t j = i + vx_utf8_width(s, i);
        vx_str c = {s.ptr + i, j - i};
        vx_list_push(l, &c);
        i = j;
    }
    return l;
}

// Returns the grapheme clusters of [s].
static vx_list *vx_str_graphemes(vx_str s) {
    vx_list *l = vx_list_new(sizeof(vx_str), VX_PM_STR, 0, NULL);
    for (int64_t i = 0; i < s.len;) {
        int64_t j = vx_grapheme_end(s, i);
        vx_str g = {s.ptr + i, j - i};
        vx_list_push(l, &g);
        i = j;
    }
    return l;
}

// Returns the lines of [s], without their line endings (\n or \r\n). A
// final line ending doesn't start another line.
static vx_list *vx_str_lines(vx_str s) {
    vx_list *l = vx_list_new(sizeof(vx_str), VX_PM_STR, 0, NULL);
    int64_t start = 0;
    while (start < s.len) {
        int64_t end = start;
        while (end < s.len && s.ptr[end] != '\n') end++;
        int64_t n = end - start;
        if (n > 0 && s.ptr[start + n - 1] == '\r') n--;
        vx_str line = {s.ptr + start, n};
        vx_list_push(l, &line);
        start = end + 1;
    }
    return l;
}

// --- Maps ---

// How map keys are hashed and compared.
enum { VX_KEY_INT, VX_KEY_BOOL, VX_KEY_STR, VX_KEY_ENUM, VX_KEY_CUSTOM };

// A hash map that keeps insertion order. Entries sit in one array in the
// order they were added, and an index of slots points into it. Removing an
// entry leaves a hole that the next growth compacts away.
//
// Each entry is a hash, a sequence number (0 for a hole), the key and the
// value. Sequence numbers rise with each insertion, so a loop can find its
// place again after compaction moves the entries.
typedef struct {
    int64_t len;       // live entries
    int64_t used;      // entries in the array, holes included
    int64_t cap;       // capacity of the entry array
    int64_t mask;      // index slots minus one
    uint64_t next_seq;
    uint64_t epoch;    // changes whenever compaction moves entries
    int32_t kind, vsz, ksz, esz, voff;
    int32_t frozen;    // set for the maps of constants
    uint64_t emask;    // the layout of an entry
    int32_t *index;    // -1 empty, -2 removed, otherwise an entry number
    char *entries;
    // For VX_KEY_CUSTOM, the generated functions that hash and compare keys.
    uint64_t (*khash)(const void *);
    bool (*keq)(const void *, const void *);
} vx_map;

#define VX_MAP_KEY 16
#define VX_MAP_EMPTY -1
#define VX_MAP_REMOVED -2

static inline uint64_t vx_mix(uint64_t x) {
    x ^= x >> 30;
    x *= 0xbf58476d1ce4e5b9ULL;
    x ^= x >> 27;
    x *= 0x94d049bb133111ebULL;
    return x ^ (x >> 31);
}

static uint64_t vx_hash_bytes(const char *p, int64_t n) {
    uint64_t h = 0x9e3779b97f4a7c15ULL ^ (uint64_t)n;
    for (; n >= 8; p += 8, n -= 8) {
        uint64_t w;
        memcpy(&w, p, 8);
        h = (h ^ vx_mix(w)) * 0x100000001b3ULL;
    }
    uint64_t w = 0;
    if (n > 0) memcpy(&w, p, (size_t)n);
    return vx_mix(h ^ w);
}

// Combines the hash [h] built so far with the hash [v] of one more value.
static inline uint64_t vx_hash_add(uint64_t h, uint64_t v) {
    return vx_mix(h ^ (v + 0x9e3779b97f4a7c15ULL + (h << 6) + (h >> 2)));
}

// Hashes a float so that values == calls equal hash the same: 0.0 and -0.0.
static inline uint64_t vx_hash_float(double v) {
    if (v == 0) v = 0;
    uint64_t bits;
    memcpy(&bits, &v, 8);
    return vx_mix(bits);
}

static inline uint64_t vx_key_hash(const vx_map *m, const void *k) {
    switch (m->kind) {
    case VX_KEY_CUSTOM: return m->khash(k);
    case VX_KEY_INT: return vx_mix(*(const uint64_t *)k);
    case VX_KEY_BOOL: return vx_mix(*(const bool *)k);
    case VX_KEY_STR: {
        vx_str s = *(const vx_str *)k;
        return vx_hash_bytes(s.ptr, s.len);
    }
    }
    // Enum keys have no values, so the tag identifies them.
    return vx_mix((uint64_t)**(int32_t *const *)k);
}

static inline bool vx_key_eq(const vx_map *m, const void *a, const void *b) {
    switch (m->kind) {
    case VX_KEY_CUSTOM: return m->keq(a, b);
    case VX_KEY_INT: return *(const int64_t *)a == *(const int64_t *)b;
    case VX_KEY_BOOL: return *(const bool *)a == *(const bool *)b;
    case VX_KEY_STR: return vx_str_eq(*(const vx_str *)a, *(const vx_str *)b);
    }
    return **(int32_t *const *)a == **(int32_t *const *)b;
}

#define VX_PM_MAP (VX_FIELD(vx_map, entries, 1) | VX_FIELD(vx_map, index, 1))

// Returns a new map with keys of [ksz] bytes laid out as [kmask], and
// values of [vsz] bytes laid out as [vmask].
static vx_map *vx_map_new(int kind, int ksz, uint64_t kmask, int vsz, uint64_t vmask) {
    vx_map *m = vx_alloc_obj(sizeof(vx_map), VX_PM_MAP);
    m->kind = kind;
    m->ksz = ksz;
    m->vsz = vsz;
    m->voff = VX_MAP_KEY + ((ksz + 7) & ~7);
    m->esz = m->voff + ((vsz + 7) & ~7);
    m->emask = m->esz > 512 ? ~(uint64_t)0 : kmask << (VX_MAP_KEY / 8) | vmask << (m->voff / 8);
    m->next_seq = 1;
    return m;
}

// Returns a new map whose keys [khash] hashes and [keq] compares.
static vx_map *vx_map_new_custom(uint64_t (*khash)(const void *), bool (*keq)(const void *, const void *), int ksz, uint64_t kmask, int vsz, uint64_t vmask) {
    vx_map *m = vx_map_new(VX_KEY_CUSTOM, ksz, kmask, vsz, vmask);
    m->khash = khash;
    m->keq = keq;
    return m;
}

#define VX_MAP_ENTRY(m, i) ((m)->entries + (size_t)(i) * (size_t)(m)->esz)
#define VX_MAP_SEQ(e) (((uint64_t *)(e))[1])

// Returns the slot of [key], or -1 if it is missing.
static int64_t vx_map_slot(vx_map *m, const void *key, uint64_t h) {
    if (!m->index) return -1;
    for (size_t i = h & (size_t)m->mask;; i = (i + 1) & (size_t)m->mask) {
        int32_t e = m->index[i];
        if (e == VX_MAP_EMPTY) return -1;
        if (e >= 0) {
            char *ent = VX_MAP_ENTRY(m, e);
            if (*(uint64_t *)ent == h && vx_key_eq(m, ent + VX_MAP_KEY, key)) return (int64_t)i;
        }
    }
}

// Makes room for one more entry: compacts away holes, and doubles the
// capacity unless compaction alone frees enough.
static void vx_map_grow(vx_map *m) {
    int64_t cap = m->cap == 0 ? 8 : (m->len * 2 > m->cap ? m->cap * 2 : m->cap);
    if (cap > INT32_MAX / 2) vx_fail(0, "map is too big");
    char *entries = cap == m->cap ? m->entries : vx_alloc_t((size_t)(cap * m->esz), m->emask, (size_t)m->esz);
    int64_t n = 0;
    for (int64_t i = 0; i < m->used; i++) {
        char *e = VX_MAP_ENTRY(m, i);
        if (VX_MAP_SEQ(e) == 0) continue;
        if (entries + (size_t)n * (size_t)m->esz != e) memmove(entries + (size_t)n * (size_t)m->esz, e, (size_t)m->esz);
        n++;
    }
    if (n != m->used) m->epoch++;
    if (entries == m->entries) memset(VX_MAP_ENTRY(m, n), 0, (size_t)((m->used - n) * m->esz));
    m->entries = entries;
    vx_remember(m);
    m->used = n;
    m->cap = cap;
    size_t slots = 16;
    while (slots < (size_t)cap * 2) slots *= 2;
    m->index = vx_alloc_atomic(slots * sizeof(int32_t));
    memset(m->index, 0xff, slots * sizeof(int32_t));
    m->mask = (int64_t)slots - 1;
    for (int64_t i = 0; i < n; i++) {
        size_t s = *(uint64_t *)VX_MAP_ENTRY(m, i) & (size_t)m->mask;
        while (m->index[s] != VX_MAP_EMPTY) s = (s + 1) & (size_t)m->mask;
        m->index[s] = (int32_t)i;
    }
    vx_remember(m);
}

// Returns a pointer to the value of [key], or NULL if it is missing.
static inline void *vx_map_find(vx_map *m, const void *key) {
    int64_t s = vx_map_slot(m, key, vx_key_hash(m, key));
    return s < 0 ? NULL : VX_MAP_ENTRY(m, m->index[s]) + m->voff;
}

// Returns a pointer to the value of [key], adding the key with a zero value
// at the end when it is missing.
static void *vx_map_set(vx_map *m, const void *key) {
    uint64_t h = vx_key_hash(m, key);
    int64_t s = vx_map_slot(m, key, h);
    if (s >= 0) {
        // The caller writes the value next, with nothing allocated between.
        vx_remember(m->entries);
        return VX_MAP_ENTRY(m, m->index[s]) + m->voff;
    }
    if (m->used == m->cap) vx_map_grow(m);
    size_t i = h & (size_t)m->mask;
    while (m->index[i] >= 0) i = (i + 1) & (size_t)m->mask;
    char *e = VX_MAP_ENTRY(m, m->used);
    ((uint64_t *)e)[0] = h;
    VX_MAP_SEQ(e) = m->next_seq++;
    memcpy(e + VX_MAP_KEY, key, (size_t)m->ksz);
    m->index[i] = (int32_t)m->used++;
    m->len++;
    vx_remember(m->entries);
    return e + m->voff;
}

// Removes [key], copying its value to [out]. Returns false if it was
// missing.
static bool vx_map_remove(vx_map *m, const void *key, void *out) {
    int64_t s = vx_map_slot(m, key, vx_key_hash(m, key));
    if (s < 0) return false;
    char *e = VX_MAP_ENTRY(m, m->index[s]);
    memcpy(out, e + m->voff, (size_t)m->vsz);
    memset(e, 0, (size_t)m->esz);
    m->index[s] = VX_MAP_REMOVED;
    m->len--;
    return true;
}

// The position of a loop over a map. It visits the entries that existed
// when the loop started, in order, skipping ones removed since.
typedef struct {
    int64_t i;
    uint64_t last, limit, epoch;
} vx_map_iter;

static inline vx_map_iter vx_map_iter_new(vx_map *m) {
    return (vx_map_iter){0, 0, m->next_seq, m->epoch};
}

// Returns the next entry of the loop, or NULL when it is done.
static inline char *vx_map_next(vx_map *m, vx_map_iter *it) {
    if (__builtin_expect(it->epoch != m->epoch, 0)) {
        // Entries moved: continue after the last one visited.
        it->i = 0;
        while (it->i < m->used && VX_MAP_SEQ(VX_MAP_ENTRY(m, it->i)) <= it->last) it->i++;
        it->epoch = m->epoch;
    }
    while (it->i < m->used) {
        char *e = VX_MAP_ENTRY(m, it->i++);
        uint64_t seq = VX_MAP_SEQ(e);
        if (seq == 0) continue;
        if (seq >= it->limit) return NULL;
        it->last = seq;
        return e;
    }
    return NULL;
}

// Returns the keys of [m], or its values when [values] is set, in order.
static vx_list *vx_map_list(vx_map *m, bool values) {
    uint64_t mask = m->esz > 512 ? ~(uint64_t)0 : m->emask >> ((values ? m->voff : VX_MAP_KEY) / 8);
    int64_t esz = values ? m->vsz : m->ksz;
    if (esz <= 512) mask &= VX_ONES(esz);
    vx_list *l = vx_list_new(esz, mask, 0, NULL);
    for (int64_t i = 0; i < m->used; i++) {
        char *e = VX_MAP_ENTRY(m, i);
        if (VX_MAP_SEQ(e) != 0) vx_list_push(l, e + (values ? m->voff : VX_MAP_KEY));
    }
    return l;
}

// Sets every entry of [src] in [m], in insertion order.
static void vx_map_merge(vx_map *m, vx_map *src) {
    for (int64_t i = 0; i < src->used; i++) {
        char *e = VX_MAP_ENTRY(src, i);
        if (VX_MAP_SEQ(e) != 0) memcpy(vx_map_set(m, e + VX_MAP_KEY), e + src->voff, (size_t)src->vsz);
    }
}

static vx_map *vx_map_freeze(vx_map *m) {
    m->frozen = 1;
    return m;
}

// Returns [m], failing at [line] if it is part of a constant.
static inline vx_map *vx_map_mut(vx_map *m, int line) {
    if (__builtin_expect(m->frozen, 0)) vx_fail(line, "can't change a constant map; change a copy made with .copy()");
    return m;
}

static vx_map *vx_map_copy(vx_map *src) {
    vx_map *m = vx_map_new(src->kind, src->ksz, 0, src->vsz, 0);
    m->emask = src->emask;
    m->khash = src->khash;
    m->keq = src->keq;
    vx_map_merge(m, src);
    return m;
}

// --- Sets ---

// A set is a map whose entries have keys and no values.

// Returns a new, empty set that hashes and compares like [s].
static vx_map *vx_set_like(vx_map *s) {
    vx_map *m = vx_map_new(s->kind, s->ksz, 0, 0, 0);
    m->emask = s->emask;
    m->khash = s->khash;
    m->keq = s->keq;
    return m;
}

// Adds every item of [l] to [s], in order, and returns [s].
static vx_map *vx_set_add_list(vx_map *s, vx_list *l) {
    for (int64_t i = 0; i < l->len; i++) vx_map_set(s, (char *)l->data + (size_t)i * (size_t)s->ksz);
    return s;
}

// Returns [s], failing at [line] if it is part of a constant.
static inline vx_map *vx_set_mut(vx_map *s, int line) {
    if (__builtin_expect(s->frozen, 0)) vx_fail(line, "can't change a constant set; change a copy made with .copy()");
    return s;
}

// Adds [x] to [s]. Returns false if it was already there.
static inline bool vx_set_insert(vx_map *s, const void *x) {
    int64_t n = s->len;
    vx_map_set(s, x);
    return s->len != n;
}

// Removes [x] from [s]. Returns false if it wasn't there.
static inline bool vx_set_remove(vx_map *s, const void *x) {
    char none;
    return vx_map_remove(s, x, &none);
}

// Returns the items of [a] that are in [b] when [keep] is set, or that
// aren't when it is clear, in the order of [a].
static vx_map *vx_set_filter(vx_map *a, vx_map *b, bool keep) {
    vx_map *m = vx_set_like(a);
    for (int64_t i = 0; i < a->used; i++) {
        char *e = VX_MAP_ENTRY(a, i);
        if (VX_MAP_SEQ(e) != 0 && (vx_map_find(b, e + VX_MAP_KEY) != NULL) == keep) vx_map_set(m, e + VX_MAP_KEY);
    }
    return m;
}

static vx_map *vx_set_union(vx_map *a, vx_map *b) {
    vx_map *m = vx_map_copy(a);
    vx_map_merge(m, b);
    return m;
}

// Reports whether every item of [a] is in [b].
static bool vx_set_subset(vx_map *a, vx_map *b) {
    if (a->len > b->len) return false;
    for (int64_t i = 0; i < a->used; i++) {
        char *e = VX_MAP_ENTRY(a, i);
        if (VX_MAP_SEQ(e) != 0 && !vx_map_find(b, e + VX_MAP_KEY)) return false;
    }
    return true;
}

static inline bool vx_set_eq(vx_map *a, vx_map *b) {
    return a == b || (a->len == b->len && vx_set_subset(a, b));
}

// --- Conversions ---

// Returns how a value of a C type called [name] at [p] is shown.
__attribute__((unused))
static vx_str vx_handle_str(const char *name, void *p) {
    char buf[128];
    // Not %p, which Windows prints without the 0x.
    int n = p ? snprintf(buf, sizeof buf, "<%s 0x%" PRIxPTR ">", name, (uintptr_t)p) : snprintf(buf, sizeof buf, "<%s null>", name);
    return vx_str_new(buf, n < (int)sizeof buf ? n : (int)sizeof buf - 1);
}

static vx_str vx_int_str(int64_t v) {
    char buf[24];
    int n = snprintf(buf, sizeof buf, "%" PRId64, v);
    return vx_str_new(buf, n);
}

// Writes finite [v] as [vx_float_str] shows it into [buf], which holds
// 400 bytes, and returns its length.
static int vx_float_fmt(char *buf, double v) {
    int prec = 1;
    for (; prec < 17; prec++) {
        snprintf(buf, 400, "%.*e", prec - 1, v);
        if (strtod(buf, NULL) == v) break;
    }
    snprintf(buf, 400, "%.*e", prec - 1, v);
    int exp10 = atoi(strchr(buf, 'e') + 1);
    int n;
    if (exp10 >= -5 && exp10 < 17) {
        int decimals = prec - 1 - exp10;
        n = snprintf(buf, 400, "%.*f", decimals > 0 ? decimals : 0, v);
    } else {
        n = snprintf(buf, 400, "%.*g", prec, v);
    }
    if (!strpbrk(buf, ".e")) {
        buf[n++] = '.';
        buf[n++] = '0';
        buf[n] = 0;
    }
    return n;
}

// Formats [v] with the fewest digits that read back as the same value,
// always showing a decimal point: 2.5, 10.0, 0.001. Very large or small
// values use an exponent: 1e+20, 1e-07.
static vx_str vx_float_str(double v) {
    if (isnan(v)) return VX_LIT("nan");
    if (isinf(v)) return v > 0 ? VX_LIT("inf") : VX_LIT("-inf");
    char buf[400];
    return vx_str_new(buf, vx_float_fmt(buf, v));
}

static vx_str vx_bool_str(bool v) { return v ? VX_LIT("true") : VX_LIT("false"); }

// Writes [s] in quotes with escapes into [buf], which holds at least
// `2 * s.len + 2` bytes, and returns the length written.
static int64_t vx_quote_into(char *buf, vx_str s) {
    int64_t n = 0;
    buf[n++] = '"';
    for (int64_t i = 0; i < s.len; i++) {
        char c = s.ptr[i];
        switch (c) {
        case '"': buf[n++] = '\\'; buf[n++] = '"'; break;
        case '\\': buf[n++] = '\\'; buf[n++] = '\\'; break;
        case '\n': buf[n++] = '\\'; buf[n++] = 'n'; break;
        case '\t': buf[n++] = '\\'; buf[n++] = 't'; break;
        case '\r': buf[n++] = '\\'; buf[n++] = 'r'; break;
        default: buf[n++] = c;
        }
    }
    buf[n++] = '"';
    return n;
}

// Returns [s] in quotes with escapes, as shown inside lists and structs.
static vx_str vx_str_quote(vx_str s) {
    char *buf = vx_alloc_atomic((size_t)s.len * 2 + 3);
    return (vx_str){buf, vx_quote_into(buf, s)};
}

// --- Showing values ---
//
// A value is shown by writing it, and everything inside it, into one
// growing buffer that becomes a string at the end, so showing takes time in
// proportion to the text. The text holds no pointers, so it is allocated
// atomic; the [vx_buf] itself lives on the stack, where the collector finds
// its pointer.

// Text being built up for a shown value.
typedef struct {
    char *ptr;
    int64_t len, cap;
} vx_buf;

// Returns where [n] more bytes go in [b], growing it if needed.
static char *vx_buf_reserve(vx_buf *b, int64_t n) {
    if (b->len + n > b->cap) {
        int64_t cap = b->cap ? b->cap * 2 : 64;
        while (cap < b->len + n) cap *= 2;
        char *p = vx_alloc_atomic((size_t)cap + 1);
        if (b->len > 0) memcpy(p, b->ptr, (size_t)b->len);
        b->ptr = p;
        b->cap = cap;
    }
    return b->ptr + b->len;
}

static void vx_buf_add(vx_buf *b, vx_str s) {
    if (s.len == 0) return;
    memcpy(vx_buf_reserve(b, s.len), s.ptr, (size_t)s.len);
    b->len += s.len;
}

static void vx_buf_int(vx_buf *b, int64_t v) {
    char *p = vx_buf_reserve(b, 24);
    b->len += snprintf(p, 24, "%" PRId64, v);
}

static void vx_buf_float(vx_buf *b, double v) {
    if (isnan(v) || isinf(v)) {
        vx_buf_add(b, vx_float_str(v));
        return;
    }
    char buf[400];
    vx_buf_add(b, (vx_str){buf, vx_float_fmt(buf, v)});
}

static void vx_buf_quote(vx_buf *b, vx_str s) {
    b->len += vx_quote_into(vx_buf_reserve(b, s.len * 2 + 2), s);
}

// Returns the text of [b] as a string.
static vx_str vx_buf_done(vx_buf *b) {
    if (b->len == 0) return VX_LIT("");
    b->ptr[b->len] = 0;
    return (vx_str){b->ptr, b->len};
}

// Copies [s] into a NUL-terminated buffer, trimming surrounding spaces, and
// stores the trimmed length in [len].
static char *vx_cstr_trim(vx_str s, int64_t *len) {
    int64_t a = 0, b = s.len;
    while (a < b && (s.ptr[a] == ' ' || s.ptr[a] == '\t' || s.ptr[a] == '\n' || s.ptr[a] == '\r')) a++;
    while (b > a && (s.ptr[b - 1] == ' ' || s.ptr[b - 1] == '\t' || s.ptr[b - 1] == '\n' || s.ptr[b - 1] == '\r')) b--;
    char *buf = vx_alloc_atomic((size_t)(b - a) + 1);
    memcpy(buf, s.ptr + a, (size_t)(b - a));
    buf[b - a] = '\0';
    *len = b - a;
    return buf;
}

static void vx_check_radix(int64_t radix, int line) {
    if (radix < 2 || radix > 36) vx_fail(line, "radix %" PRId64 " is outside 2 to 36", radix);
}

// The value of digit [c] in bases up to 36, or 36 if it isn't one.
static int vx_digit(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'z') return c - 'a' + 10;
    if (c >= 'A' && c <= 'Z') return c - 'A' + 10;
    return 36;
}

// Parses [s], with surrounding spaces and an optional sign, as an int in
// [radix]. Returns 0 on success, 1 if it isn't a number and 2 if it doesn't
// fit in an int.
static int vx_parse_int(vx_str s, int64_t radix, int64_t *out) {
    int64_t a = 0, b = s.len;
    while (a < b && (s.ptr[a] == ' ' || s.ptr[a] == '\t' || s.ptr[a] == '\n' || s.ptr[a] == '\r')) a++;
    while (b > a && (s.ptr[b - 1] == ' ' || s.ptr[b - 1] == '\t' || s.ptr[b - 1] == '\n' || s.ptr[b - 1] == '\r')) b--;
    bool neg = a < b && s.ptr[a] == '-';
    if (a < b && (s.ptr[a] == '-' || s.ptr[a] == '+')) a++;
    if (a == b) return 1;
    // Accumulates the negative value, which reaches INT64_MIN.
    int64_t v = 0;
    bool big = false;
    for (int64_t i = a; i < b; i++) {
        int d = vx_digit(s.ptr[i]);
        if (d >= radix) return 1;
        if (!big && (__builtin_mul_overflow(v, radix, &v) || __builtin_sub_overflow(v, d, &v))) big = true;
    }
    if (big || (!neg && v == INT64_MIN)) return 2;
    *out = neg ? v : -v;
    return 0;
}

static int64_t vx_str_to_int(vx_str s, int line) {
    int64_t v;
    switch (vx_parse_int(s, 10, &v)) {
    case 1: vx_fail(line, "can't convert \"%.*s\" to int", (int)s.len, s.ptr);
    case 2: {
        int64_t n;
        vx_fail(line, "can't convert \"%s\" to int: the number is too big", vx_cstr_trim(s, &n));
    }
    }
    return v;
}

static bool vx_int_parse(vx_str s, int64_t radix, int line, int64_t *out) {
    vx_check_radix(radix, line);
    *out = 0;
    return vx_parse_int(s, radix, out) == 0;
}

// Parses [s], with surrounding spaces, as a decimal float into [out];
// unlike strtod, hex, nan and inf aren't accepted, nor values too big for
// a float.
static bool vx_parse_float(vx_str s, double *out) {
    int64_t n;
    char *c = vx_cstr_trim(s, &n), *end;
    bool digit = false;
    for (int64_t i = 0; i < n; i++) {
        if (c[i] >= '0' && c[i] <= '9') digit = true;
        else if (!strchr(".eE+-", c[i]) || c[i] == 0) return false;
    }
    if (!digit) return false;
    double v = strtod(c, &end);
    if (end != c + n || isinf(v)) return false;
    *out = v;
    return true;
}

static double vx_str_to_float(vx_str s, int line) {
    double v;
    if (!vx_parse_float(s, &v)) vx_fail(line, "can't convert \"%.*s\" to float", (int)s.len, s.ptr);
    return v;
}

static bool vx_float_parse(vx_str s, double *out) {
    *out = 0;
    return vx_parse_float(s, out);
}

// Returns [v] in [radix], with lowercase letters for digits past 9.
static vx_str vx_int_str_radix(int64_t v, int64_t radix, int line) {
    vx_check_radix(radix, line);
    char tmp[66];
    int n = 0;
    uint64_t u = v < 0 ? 0 - (uint64_t)v : (uint64_t)v;
    do {
        tmp[n++] = "0123456789abcdefghijklmnopqrstuvwxyz"[u % (uint64_t)radix];
        u /= (uint64_t)radix;
    } while (u);
    if (v < 0) tmp[n++] = '-';
    char *buf = vx_alloc_atomic((size_t)n);
    for (int i = 0; i < n; i++) buf[i] = tmp[n - 1 - i];
    return (vx_str){buf, n};
}

// Returns [v] with exactly [digits] digits after the point. A value exactly
// halfway rounds away from zero, as in Dart and JavaScript, where printf
// would round it to even.
static vx_str vx_float_fixed(double v, int64_t digits, int line) {
    if (digits < 0 || digits > 20) vx_fail(line, "toFixed(%" PRId64 "): the digits must be 0 to 20", digits);
    if (isnan(v) || isinf(v)) return vx_float_str(v);
    int d = (int)digits;
    // The exact digits of |v|, after a '-' slot and a '0' slot for a carry.
    int n = snprintf(NULL, 0, "%.*f", d + 40, fabs(v));
    char *buf = vx_alloc_atomic((size_t)n + 3);
    snprintf(buf + 2, (size_t)n + 1, "%.*f", d + 40, fabs(v));
    char *dot = strchr(buf + 2, '.'), *cut = dot + 1 + d;
    bool tie = *cut == '5';
    for (char *p = cut + 1; tie && *p; p++) tie = *p == '0';
    if (!tie) {
        n = snprintf(NULL, 0, "%.*f", d, v);
        char *out = vx_alloc_atomic((size_t)n + 1);
        snprintf(out, (size_t)n + 1, "%.*f", d, v);
        return (vx_str){out, n};
    }
    char *end = d == 0 ? dot : cut;
    buf[1] = '0';
    for (char *p = end - 1;; p--) {
        if (*p == '.') continue;
        if (*p != '9') {
            (*p)++;
            break;
        }
        *p = '0';
    }
    char *start = buf[1] == '0' ? buf + 2 : buf + 1;
    if (signbit(v)) *--start = '-';
    return (vx_str){start, end - start};
}

// --- Format specs ---

// The flags of a format spec, such as `{n:+,}`.
enum { VX_FMT_PLUS = 1, VX_FMT_ALT = 2, VX_FMT_UPPER = 4, VX_FMT_COMMA = 8, VX_FMT_ZERO = 16 };

// Returns the sign, [prefix] and [digits] of a number as one string, with
// thousands separators in the leading digits and zeros after the prefix up
// to [width] when [flags] ask for them.
static vx_str vx_fmt_num(bool neg, const char *prefix, vx_str digits, int flags, int64_t width) {
    int64_t lead = 0;
    while (lead < digits.len && digits.ptr[lead] >= '0' && digits.ptr[lead] <= '9') lead++;
    bool comma = (flags & VX_FMT_COMMA) && lead > 3;
    char sign = neg ? '-' : (flags & VX_FMT_PLUS) ? '+' : 0;
    int64_t plen = (int64_t)strlen(prefix);
    int64_t n = (sign != 0) + plen + digits.len + (comma ? (lead - 1) / 3 : 0);
    int64_t zeros = (flags & VX_FMT_ZERO) && width > n ? width - n : 0;
    char *buf = vx_alloc_atomic((size_t)(n + zeros)), *p = buf;
    if (sign) *p++ = sign;
    memcpy(p, prefix, (size_t)plen);
    p += plen;
    memset(p, '0', (size_t)zeros);
    p += zeros;
    for (int64_t i = 0; i < digits.len; i++) {
        if (comma && i > 0 && i < lead && (lead - i) % 3 == 0) *p++ = ',';
        *p++ = digits.ptr[i];
    }
    return (vx_str){buf, n + zeros};
}

// Formats [v] in [base] for a format spec such as `{n:#x}` or `{n:+05}`.
static vx_str vx_fmt_int(int64_t v, int base, int flags, int64_t width) {
    const char *letters = flags & VX_FMT_UPPER ? "0123456789ABCDEF" : "0123456789abcdef";
    char tmp[64];
    int n = 0;
    uint64_t u = v < 0 ? 0 - (uint64_t)v : (uint64_t)v;
    do {
        tmp[63 - n++] = letters[u % (uint64_t)base];
        u /= (uint64_t)base;
    } while (u);
    const char *prefix = "";
    if (flags & VX_FMT_ALT) prefix = base == 16 ? "0x" : base == 2 ? "0b" : "0o";
    return vx_fmt_num(v < 0, prefix, (vx_str){tmp + 64 - n, n}, flags, width);
}

// Pads [s] with [fill] to [width] characters, keeping it on the side that
// [align] names: '<', '>' or '^', where an odd padding leans right.
static vx_str vx_fmt_pad(vx_str s, int64_t width, char align, const char *fill) {
    int64_t chars = 0;
    for (int64_t i = 0; i < s.len; i++) chars += ((unsigned char)s.ptr[i] & 0xC0) != 0x80;
    if (chars >= width) return s;
    int64_t pad = width - chars, left = align == '>' ? pad : align == '^' ? pad / 2 : 0;
    int64_t flen = (int64_t)strlen(fill);
    char *buf = vx_alloc_atomic((size_t)(s.len + pad * flen)), *p = buf;
    for (int64_t i = 0; i < left; i++, p += flen) memcpy(p, fill, (size_t)flen);
    memcpy(p, s.ptr, (size_t)s.len);
    p += s.len;
    for (int64_t i = left; i < pad; i++, p += flen) memcpy(p, fill, (size_t)flen);
    return (vx_str){buf, s.len + pad * flen};
}

// Formats [v] for a format spec such as `{x:.2}` or `{x:+08,}`, with
// [precision] digits after the point, rounded as by `toFixed`, or as
// printed when it is negative. Nan and infinity pad with spaces.
static vx_str vx_fmt_float(double v, int64_t precision, int flags, int64_t width) {
    if (isnan(v) || isinf(v)) {
        vx_str s = vx_fmt_num(v < 0, "", isnan(v) ? VX_LIT("nan") : VX_LIT("inf"), isnan(v) ? 0 : flags & VX_FMT_PLUS, 0);
        return flags & VX_FMT_ZERO ? vx_fmt_pad(s, width, '>', " ") : s;
    }
    vx_str digits = precision < 0 ? vx_float_str(fabs(v)) : vx_float_fixed(fabs(v), precision, 0);
    return vx_fmt_num(signbit(v), "", digits, flags, width);
}

static int64_t vx_float_to_int(double v, int line) {
    if (isnan(v) || v >= 9223372036854775808.0 || v < -9223372036854775808.0) {
        vx_str f = vx_float_str(v);
        vx_fail(line, "can't convert %.*s to int: it's out of range", (int)f.len, f.ptr);
    }
    return (int64_t)v;
}

// --- Errors ---

// Stops the program after an error passed up out of main.
// [err] is the error itself, which shows where it was thrown if it is
// the last one thrown.
__attribute__((noreturn))
static void vx_uncaught(vx_str message, void *err) {
    vx_test_mark();
    fflush(stdout);
    fprintf(stderr, "error: %.*s\n", (int)message.len, message.ptr);
    if (err && err == vx_thrown) {
        fprintf(stderr, " --> %s:%d\n", vx_files[vx_thrown_line >> 20], vx_thrown_line & 0xFFFFF);
        vx_trace_print(&vx_thrown_trace);
    }
    exit(1);
}

// --- Arithmetic ---

__attribute__((noreturn, cold, noinline))
static void vx_overflow(int64_t a, char op, int64_t b, int line) {
    vx_fail(line, "integer overflow: %" PRId64 " %c %" PRId64 " doesn't fit in an int; use &%c to wrap around", a, op, b, op);
}

// Checked int arithmetic. The overflow test is a single flag check after
// the instruction, and the failure path is kept out of the hot code.
static inline int64_t vx_add(int64_t a, int64_t b, int line) {
    int64_t r;
    if (__builtin_expect(__builtin_add_overflow(a, b, &r), 0)) vx_overflow(a, '+', b, line);
    return r;
}

static inline int64_t vx_sub(int64_t a, int64_t b, int line) {
    int64_t r;
    if (__builtin_expect(__builtin_sub_overflow(a, b, &r), 0)) vx_overflow(a, '-', b, line);
    return r;
}

static inline int64_t vx_mul(int64_t a, int64_t b, int line) {
    // With a constant factor, two comparisons against precomputed limits
    // are cheaper than a full overflow test (which needs smulh on arm64).
    if (__builtin_constant_p(b) && b > 0) {
        if (__builtin_expect(a > INT64_MAX / b || a < INT64_MIN / b, 0)) vx_overflow(a, '*', b, line);
        return a * b;
    }
    if (__builtin_constant_p(a) && a > 0) {
        if (__builtin_expect(b > INT64_MAX / a || b < INT64_MIN / a, 0)) vx_overflow(a, '*', b, line);
        return a * b;
    }
    int64_t r;
    if (__builtin_expect(__builtin_mul_overflow(a, b, &r), 0)) vx_overflow(a, '*', b, line);
    return r;
}

static inline int64_t vx_neg(int64_t a, int line) {
    if (__builtin_expect(a == INT64_MIN, 0)) vx_fail(line, "integer overflow: -(%" PRId64 ") doesn't fit in an int", a);
    return -a;
}

// Wrapping arithmetic for &+, &- and &*.
#define VX_WRAP(a, op, b) ((int64_t)((uint64_t)(a) op (uint64_t)(b)))

static int64_t vx_div(int64_t a, int64_t b, int line) {
    if (b == 0) vx_fail(line, "division by zero");
    if (b == -1) {
        if (a == INT64_MIN) vx_fail(line, "integer overflow: %" PRId64 " / -1 doesn't fit in an int", a);
        return -a;
    }
    return a / b;
}

static int64_t vx_mod(int64_t a, int64_t b, int line) {
    if (b == 0) vx_fail(line, "division by zero in %%");
    if (b == -1) return 0;
    return a % b;
}

// Bits shifted out of the top are lost, as in Swift and Go; `>>` copies the
// sign bit in.
#define VX_SHL(a, b) ((int64_t)((uint64_t)(a) << (b)))

static int64_t vx_shl(int64_t a, int64_t b, int line) {
    if ((uint64_t)b > 63) vx_fail(line, "shift by %" PRId64 ", outside 0 to 63", b);
    return VX_SHL(a, b);
}

static int64_t vx_shr(int64_t a, int64_t b, int line) {
    if ((uint64_t)b > 63) vx_fail(line, "shift by %" PRId64 ", outside 0 to 63", b);
    return a >> b;
}

// --- Input and output ---

#ifdef _WIN32
// Stops the program once standard output is a closed pipe, as SIGPIPE
// does elsewhere, with the status a shell shows for that.
#define VX_CHECK_OUT() do { \
    if (ferror(stdout) && (GetLastError() == ERROR_NO_DATA || GetLastError() == ERROR_BROKEN_PIPE)) _exit(128 + 13); \
} while (0)
#else
#define VX_CHECK_OUT() ((void)0)
#endif

static void vx_write(vx_str s) {
    fwrite(s.ptr, 1, (size_t)s.len, stdout);
    VX_CHECK_OUT();
}

// Prints [n] strings separated by spaces, then a newline if [newline] is set.
static void vx_print(bool newline, int n, const vx_str *parts) {
    for (int i = 0; i < n; i++) {
        if (i > 0) fputc(' ', stdout);
        vx_write(parts[i]);
    }
    if (newline) fputc('\n', stdout);
    VX_CHECK_OUT();
}

// Prints like vx_print, to standard error. Standard output is flushed
// first, so the two keep their order in a terminal.
static void vx_eprint(bool newline, int n, const vx_str *parts) {
    fflush(stdout);
    for (int i = 0; i < n; i++) {
        if (i > 0) fputc(' ', stderr);
        fwrite(parts[i].ptr, 1, (size_t)parts[i].len, stderr);
    }
    if (newline) fputc('\n', stderr);
}

#ifdef _WIN32
static char *vx_utf8_of(const wchar_t *w, int n);

// Reads a line from standard input into [*line], growing it, as getline
// does. A console is read as UTF-16, which is the only way it gives
// every character, and the line converted to UTF-8.
static ssize_t vx_getline(char **line, size_t *cap, FILE *f) {
    HANDLE in = GetStdHandle(STD_INPUT_HANDLE);
    DWORD mode;
    if (f == stdin && GetConsoleMode(in, &mode)) {
        wchar_t buf[1024];
        wchar_t *w = NULL;
        int len = 0;
        DWORD got;
        while (ReadConsoleW(in, buf, 1024, &got, NULL) && got > 0) {
            w = realloc(w, (size_t)(len + (int)got) * sizeof(wchar_t));
            if (!w) vx_out_of_memory();
            memcpy(w + len, buf, got * sizeof(wchar_t));
            len += (int)got;
            if (w[len - 1] == L'\n') break;
        }
        if (len == 0 || w[0] == 26) {  // Ctrl+Z ends the input
            free(w);
            return -1;
        }
        free(*line);
        *line = vx_utf8_of(w, len);
        free(w);
        *cap = strlen(*line) + 1;
        return (ssize_t)strlen(*line);
    }
    size_t n = 0;
    int c;
    while ((c = getc(f)) != EOF) {
        if (n + 2 > *cap) {
            *cap = *cap ? *cap * 2 : 128;
            *line = realloc(*line, *cap);
            if (!*line) vx_out_of_memory();
        }
        (*line)[n++] = (char)c;
        if (c == '\n') break;
    }
    if (n == 0) return -1;
    (*line)[n] = 0;
    return (ssize_t)n;
}
#else
#define vx_getline getline
#endif

// Reads one line from standard input without its line break; "" at the end
// of input.
static vx_str vx_input(vx_str prompt) {
    vx_write(prompt);
    fflush(stdout);
    char *line = NULL;
    size_t cap = 0;
    ssize_t n = vx_getline(&line, &cap, stdin);
    if (n <= 0) {
        free(line);
        return VX_LIT("");
    }
    if (line[n - 1] == '\n') n--;
    if (n > 0 && line[n - 1] == '\r') n--;
    char *buf = vx_alloc_atomic((size_t)n + 1);
    memcpy(buf, line, (size_t)n);
    free(line);
    return (vx_str){buf, (int64_t)n};
}

__attribute__((noreturn))
static void vx_panic(vx_str msg, int line) {
    vx_fail(line, "%.*s", (int)msg.len, msg.ptr);
}

static void vx_assert(bool cond, vx_str msg, int line) {
    if (cond) return;
    if (msg.len == 0) vx_fail(line, "assertion failed");
    vx_fail(line, "assertion failed: %.*s", (int)msg.len, msg.ptr);
}

// Reports a failed `expect([text])` at [line], with [msg] when it isn't
// empty, and the values [left] and [right] of a comparison when [sides].
// The test goes on, so one run shows every failure.
static void vx_expect_fail(int line, const char *text, vx_str msg, bool sides, vx_str left, vx_str right) {
    vx_test_mark();
    printf("  %s:%d: expect(%s)\n", vx_files[line >> 20], line & 0xFFFFF, text);
    if (msg.len > 0) printf("    %.*s\n", (int)msg.len, msg.ptr);
    if (sides) {
        printf("    left:  %.*s\n", (int)left.len, left.ptr);
        printf("    right: %.*s\n", (int)right.len, right.ptr);
    }
    fflush(stdout);
}

// Reports an error passed up out of the running test.
static void vx_test_error(vx_str message) {
    vx_test_mark();
    printf("  error: %.*s\n", (int)message.len, message.ptr);
    fflush(stdout);
}

// --- Files and the system ---
//
// Helpers behind vex:fs and vex:os. Functions that can fail return NULL,
// false or -1 and keep errno for vx_os_error.

static VX_TLS int vx_errno;
static int vx_argc;
static char **vx_argv;

#ifdef _WIN32
// Returns a malloc'd UTF-8 copy of the [n] UTF-16 units at [w], or of the
// whole NUL-terminated string when [n] is -1.
static char *vx_utf8_of(const wchar_t *w, int n) {
    int len = WideCharToMultiByte(CP_UTF8, 0, w, n, NULL, 0, NULL, NULL);
    char *s = malloc((size_t)len + 1);
    if (!s) vx_out_of_memory();
    WideCharToMultiByte(CP_UTF8, 0, w, n, s, len, NULL, NULL);
    s[len] = 0;
    return s;
}

// Returns a malloc'd UTF-16 copy of the UTF-8 string [s].
static wchar_t *vx_utf16_of(const char *s) {
    int len = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
    wchar_t *w = malloc((size_t)len * sizeof(wchar_t));
    if (!w) vx_out_of_memory();
    MultiByteToWideChar(CP_UTF8, 0, s, -1, w, len);
    return w;
}

static UINT vx_console_cp;

static void vx_console_restore(void) { SetConsoleOutputCP(vx_console_cp); }

// Makes a program on Windows work in UTF-8 as it does elsewhere: its
// arguments, the file names it passes to the C library and the text it
// shows in a console. Its output is written byte for byte, so a newline
// stays one byte.
static void vx_windows_init(void) {
    setlocale(LC_CTYPE, ".UTF8");
    vx_console_cp = GetConsoleOutputCP();
    if (vx_console_cp != CP_UTF8 && SetConsoleOutputCP(CP_UTF8)) atexit(vx_console_restore);
    _setmode(_fileno(stdout), _O_BINARY);
    _setmode(_fileno(stderr), _O_BINARY);
    int argc;
    wchar_t **wargv = CommandLineToArgvW(GetCommandLineW(), &argc);
    if (!wargv) return;
    char **argv = malloc(((size_t)argc + 1) * sizeof(char *));
    if (!argv) vx_out_of_memory();
    for (int i = 0; i < argc; i++) argv[i] = vx_utf8_of(wargv[i], -1);
    argv[argc] = NULL;
    LocalFree(wargv);
    vx_argc = argc;
    vx_argv = argv;
}
#endif

static const char *vx_os_error(void) { return strerror(vx_errno); }

// Writes [text] to standard output when [fd] is 1, or standard error.
static void vx_os_write(int64_t fd, vx_str text) {
    if (fd == 1) {
        vx_write(text);
        return;
    }
    fflush(stdout);
    fwrite(text.ptr, 1, (size_t)text.len, stderr);
}

static void vx_os_flush(int64_t fd) { fflush(fd == 1 ? stdout : stderr); }

// Returns the bytes of the file at [path], or a string with a null pointer
// on failure.
static vx_str vx_fs_read(const char *path) {
    FILE *f = fopen(path, "rb");
    if (!f) { vx_errno = errno; return (vx_str){NULL, 0}; }
    size_t cap = 4096, len = 0;
    char *buf = vx_alloc_atomic(cap);
    size_t n;
    while ((n = fread(buf + len, 1, cap - len - 1, f)) > 0) {
        len += n;
        if (cap - len < 2) {
            buf = vx_realloc(buf, cap * 2, 1);
            cap *= 2;
        }
    }
    bool failed = ferror(f);
    if (failed) vx_errno = errno;
    fclose(f);
    if (failed) return (vx_str){NULL, 0};
    buf[len] = 0;
    return (vx_str){buf, (int64_t)len};
}

static bool vx_fs_write(const char *path, vx_str text, bool append) {
    FILE *f = fopen(path, append ? "ab" : "wb");
    if (!f) { vx_errno = errno; return false; }
    size_t n = (size_t)text.len;
    bool ok = n == 0 || fwrite(text.ptr, 1, n, f) == n;
    if (!ok) vx_errno = errno;
    if (fclose(f) != 0 && ok) { vx_errno = errno; ok = false; }
    return ok;
}

// Returns the contents of the file at [path] as Bytes, or NULL on failure.
static vx_list *vx_fs_read_bytes(const char *path) {
    vx_str s = vx_fs_read(path);
    if (!s.ptr) return NULL;
    vx_list *b = vx_alloc_obj(sizeof(vx_list), VX_PM_LIST);
    *b = (vx_list){s.len, s.len + 1, 1, (char *)s.ptr};
    vx_remember(b);
    return b;
}

static bool vx_fs_write_bytes(const char *path, vx_list *data, bool append) {
    return vx_fs_write(path, (vx_str){data->data, data->len}, append);
}

static bool vx_fs_exists(const char *path) {
    struct stat st;
    return stat(path, &st) == 0;
}

static bool vx_fs_is_dir(const char *path) {
    struct stat st;
    return stat(path, &st) == 0 && S_ISDIR(st.st_mode);
}

static int64_t vx_fs_size(const char *path) {
    struct stat st;
    if (stat(path, &st) != 0) { vx_errno = errno; return -1; }
    return (int64_t)st.st_size;
}

static bool vx_fs_remove(const char *path) {
    if (remove(path) != 0) { vx_errno = errno; return false; }
    return true;
}

#ifdef _WIN32
// Creates the folder [p]; a drive, such as C:, is there already.
static int vx_mkdir(const char *p) {
    if (isalpha((unsigned char)p[0]) && p[1] == ':' && !p[2]) return 0;
    return _mkdir(p);
}
#define VX_IS_SEP(c) ((c) == '/' || (c) == '\\')
#else
#define vx_mkdir(p) mkdir(p, 0777)
#define VX_IS_SEP(c) ((c) == '/')
#endif

// Creates the folder [path] and any missing parents.
static bool vx_fs_make_dir(const char *path) {
    size_t n = strlen(path);
    char *p = vx_alloc_atomic(n + 1);
    memcpy(p, path, n + 1);
    for (size_t i = 1; i <= n; i++) {
        if (!VX_IS_SEP(p[i]) && p[i] != 0) continue;
        char saved = p[i];
        p[i] = 0;
        if (vx_mkdir(p) != 0 && errno != EEXIST) { vx_errno = errno; return false; }
        p[i] = saved;
    }
    if (!vx_fs_is_dir(path)) { vx_errno = ENOTDIR; return false; }
    return true;
}

static int vx_fs_cmp(const void *a, const void *b) {
    return strcmp(*(char *const *)a, *(char *const *)b);
}

#ifdef _WIN32
// A folder being read with FindFirstFileW, since the C library's opendir
// doesn't take UTF-8 names.
typedef struct {
    HANDLE h;
    WIN32_FIND_DATAW found;
    bool fresh; // [found] holds an entry not yet read
    char *name;
} vx_dir;

static vx_dir *vx_opendir(const char *path) {
    size_t n = strlen(path);
    char *pattern = malloc(n + 3);
    if (!pattern) vx_out_of_memory();
    memcpy(pattern, path, n);
    memcpy(pattern + n, n > 0 && VX_IS_SEP(path[n - 1]) ? "*" : "\\*", 3);
    wchar_t *w = vx_utf16_of(pattern);
    free(pattern);
    vx_dir *d = calloc(1, sizeof(vx_dir));
    if (!d) vx_out_of_memory();
    d->h = FindFirstFileW(w, &d->found);
    free(w);
    if (d->h == INVALID_HANDLE_VALUE) {
        DWORD e = GetLastError();
        errno = e == ERROR_FILE_NOT_FOUND || e == ERROR_PATH_NOT_FOUND ? ENOENT
              : e == ERROR_DIRECTORY ? ENOTDIR : e == ERROR_ACCESS_DENIED ? EACCES : EIO;
        free(d);
        return NULL;
    }
    d->fresh = true;
    return d;
}

// Returns the name of the next entry of [d], or NULL after the last.
static const char *vx_readdir(vx_dir *d) {
    if (!d->fresh && !FindNextFileW(d->h, &d->found)) return NULL;
    d->fresh = false;
    free(d->name);
    d->name = vx_utf8_of(d->found.cFileName, -1);
    return d->name;
}

static void vx_closedir(vx_dir *d) {
    FindClose(d->h);
    free(d->name);
    free(d);
}
#else
typedef DIR vx_dir;
#define vx_opendir opendir
#define vx_closedir closedir

static const char *vx_readdir(vx_dir *d) {
    struct dirent *e = readdir(d);
    return e ? e->d_name : NULL;
}
#endif

static int64_t vx_fs_list_open(const char *path) {
    vx_dir *d = vx_opendir(path);
    if (!d) { vx_errno = errno; return -1; }
    int64_t cap = 16, n = 0;
    char **names = vx_alloc((size_t)cap * sizeof(char *));
    const char *name;
    while ((name = vx_readdir(d)) != NULL) {
        if (strcmp(name, ".") == 0 || strcmp(name, "..") == 0) continue;
        if (n == cap) {
            names = vx_realloc(names, (size_t)cap * 2 * sizeof(char *), sizeof(char *));
            cap *= 2;
        }
        size_t len = strlen(name);
        names[n] = vx_alloc_atomic(len + 1);
        vx_remember(names);
        memcpy(names[n], name, len + 1);
        n++;
    }
    vx_closedir(d);
    qsort(names, (size_t)n, sizeof(char *), vx_fs_cmp);
    vx_fs_entries = names;
    vx_fs_entry_count = n;
    return n;
}

static const char *vx_fs_list_name(int64_t i) {
    return i >= 0 && i < vx_fs_entry_count ? vx_fs_entries[i] : NULL;
}

// JSON is read in two steps: vx_json_scan checks the text and records it
// as a flat list of events, which Vex code walks to build its values. An
// event points into the text; strings and numbers are decoded when read.
enum { VX_J_NULL, VX_J_TRUE, VX_J_FALSE, VX_J_INT, VX_J_FLOAT, VX_J_STR, VX_J_LIST, VX_J_OBJ, VX_J_END };

typedef struct { int8_t kind; int64_t start, end; } vx_json_event;

static VX_TLS vx_json_event *vx_json_events;
static VX_TLS int64_t vx_json_len, vx_json_cap;
static VX_TLS char vx_json_err[160];
static VX_TLS int64_t vx_json_err_line, vx_json_err_col;

static void vx_json_add(int8_t kind, int64_t start, int64_t end) {
    if (vx_json_len == vx_json_cap) {
        vx_json_cap = vx_json_cap ? vx_json_cap * 2 : 256;
        vx_json_events = realloc(vx_json_events, (size_t)vx_json_cap * sizeof(vx_json_event));
        if (!vx_json_events) { fputs("error: out of memory\n", stderr); exit(1); }
    }
    vx_json_events[vx_json_len++] = (vx_json_event){kind, start, end};
}

// Reports a scan error at byte [at] of [s], with its line and column.
static bool vx_json_fail(const char *s, int64_t at, const char *what) {
    int64_t line = 1, col = 1;
    for (int64_t i = 0; i < at; i++) {
        if (s[i] == '\n') { line++; col = 1; } else if (((unsigned char)s[i] & 0xC0) != 0x80) col++;
    }
    snprintf(vx_json_err, sizeof vx_json_err, "%s", what);
    vx_json_err_line = line;
    vx_json_err_col = col;
    return false;
}

static void vx_json_space(const char *s, int64_t n, int64_t *i) {
    while (*i < n && (s[*i] == ' ' || s[*i] == '\t' || s[*i] == '\n' || s[*i] == '\r')) (*i)++;
}

static bool vx_json_string(const char *s, int64_t n, int64_t *i) {
    int64_t start = ++*i;
    while (*i < n && s[*i] != '"') {
        unsigned char c = (unsigned char)s[*i];
        if (c < 0x20) return vx_json_fail(s, *i, "control character in a string");
        if (c == '\\') {
            (*i)++;
            if (*i >= n) break;
            switch (s[*i]) {
            case '"': case '\\': case '/': case 'b': case 'f': case 'n': case 'r': case 't': break;
            case 'u':
                for (int k = 1; k <= 4; k++) {
                    if (*i + k >= n || !isxdigit((unsigned char)s[*i + k])) return vx_json_fail(s, *i, "bad \\u escape");
                }
                *i += 4;
                break;
            default: return vx_json_fail(s, *i, "bad escape in a string");
            }
        }
        (*i)++;
    }
    if (*i >= n) return vx_json_fail(s, start - 1, "unclosed string");
    vx_json_add(VX_J_STR, start, *i);
    (*i)++;
    return true;
}

static bool vx_json_number(const char *s, int64_t n, int64_t *i) {
    int64_t start = *i;
    bool frac = false;
    if (s[*i] == '-') (*i)++;
    if (*i >= n || !isdigit((unsigned char)s[*i])) return vx_json_fail(s, start, "bad number");
    if (s[*i] == '0') (*i)++;
    else while (*i < n && isdigit((unsigned char)s[*i])) (*i)++;
    if (*i < n && s[*i] == '.') {
        frac = true;
        (*i)++;
        if (*i >= n || !isdigit((unsigned char)s[*i])) return vx_json_fail(s, start, "bad number");
        while (*i < n && isdigit((unsigned char)s[*i])) (*i)++;
    }
    if (*i < n && (s[*i] == 'e' || s[*i] == 'E')) {
        frac = true;
        (*i)++;
        if (*i < n && (s[*i] == '+' || s[*i] == '-')) (*i)++;
        if (*i >= n || !isdigit((unsigned char)s[*i])) return vx_json_fail(s, start, "bad number");
        while (*i < n && isdigit((unsigned char)s[*i])) (*i)++;
    }
    if (!frac) {
        errno = 0;
        char buf[32];
        int64_t len = *i - start;
        if (len < (int64_t)sizeof buf) {
            memcpy(buf, s + start, (size_t)len);
            buf[len] = 0;
            (void)strtoll(buf, NULL, 10);
        }
        // Whole numbers too big for an int are kept as floats.
        frac = len >= (int64_t)sizeof buf || errno == ERANGE;
    }
    vx_json_add(frac ? VX_J_FLOAT : VX_J_INT, start, *i);
    return true;
}

static bool vx_json_value(const char *s, int64_t n, int64_t *i, int depth) {
    vx_json_space(s, n, i);
    if (*i >= n) return vx_json_fail(s, *i, "expected a value, found the end");
    if (depth > 512) return vx_json_fail(s, *i, "nested too deeply");
    char c = s[*i];
    if (c == '{' || c == '[') {
        bool obj = c == '{';
        char close = obj ? '}' : ']';
        vx_json_add(obj ? VX_J_OBJ : VX_J_LIST, *i, *i + 1);
        (*i)++;
        vx_json_space(s, n, i);
        if (*i < n && s[*i] == close) {
            vx_json_add(VX_J_END, *i, *i + 1);
            (*i)++;
            return true;
        }
        for (;;) {
            if (obj) {
                vx_json_space(s, n, i);
                if (*i >= n || s[*i] != '"') return vx_json_fail(s, *i, "expected a key in quotes");
                if (!vx_json_string(s, n, i)) return false;
                vx_json_space(s, n, i);
                if (*i >= n || s[*i] != ':') return vx_json_fail(s, *i, "expected ':' after the key");
                (*i)++;
            }
            if (!vx_json_value(s, n, i, depth + 1)) return false;
            vx_json_space(s, n, i);
            if (*i < n && s[*i] == ',') { (*i)++; continue; }
            if (*i < n && s[*i] == close) break;
            return vx_json_fail(s, *i, obj ? "expected ',' or '}'" : "expected ',' or ']'");
        }
        vx_json_add(VX_J_END, *i, *i + 1);
        (*i)++;
        return true;
    }
    if (c == '"') return vx_json_string(s, n, i);
    if (c == '-' || isdigit((unsigned char)c)) return vx_json_number(s, n, i);
    static const struct { const char *word; int8_t kind; } words[] = {{"null", VX_J_NULL}, {"true", VX_J_TRUE}, {"false", VX_J_FALSE}};
    for (int k = 0; k < 3; k++) {
        size_t len = strlen(words[k].word);
        if ((size_t)(n - *i) >= len && memcmp(s + *i, words[k].word, len) == 0) {
            vx_json_add(words[k].kind, *i, *i + (int64_t)len);
            *i += (int64_t)len;
            return true;
        }
    }
    return vx_json_fail(s, *i, "unexpected character");
}

// Checks [text] as JSON and records its events. Returns how many there
// are, or -1 with the reason in vx_json_error.
static int64_t vx_json_scan(vx_str text) {
    vx_json_src = text.ptr;
    vx_json_len = 0;
    int64_t i = 0;
    if (!vx_json_value(text.ptr, text.len, &i, 0)) return -1;
    vx_json_space(text.ptr, text.len, &i);
    if (i < text.len) {
        vx_json_fail(text.ptr, i, "unexpected text after the value");
        return -1;
    }
    return vx_json_len;
}

static vx_str vx_json_error(void) { return vx_str_from_c(vx_json_err); }

static int64_t vx_json_error_line(void) { return vx_json_err_line; }

static int64_t vx_json_error_column(void) { return vx_json_err_col; }

// A step from the outermost JSON value to the one being read: a field
// name, or an item index when [key] has no pointer.
typedef struct vx_jpath { const struct vx_jpath *up; vx_str key; int64_t index; } vx_jpath;

// Returns [p] written as in `users[2].age`; empty for the outermost value.
static vx_str vx_json_path(const vx_jpath *p) {
    if (!p) return VX_LIT("");
    vx_str up = vx_json_path(p->up);
    char tail[32];
    vx_str seg;
    if (p->key.ptr) {
        seg = p->key;
    } else {
        int n = snprintf(tail, sizeof tail, "[%lld]", (long long)p->index);
        seg = (vx_str){tail, n};
    }
    bool dot = p->key.ptr && up.len > 0;
    int64_t len = up.len + dot + seg.len;
    char *buf = vx_alloc_atomic((size_t)len + 1);
    memcpy(buf, up.ptr, (size_t)up.len);
    if (dot) buf[up.len] = '.';
    memcpy(buf + up.len + dot, seg.ptr, (size_t)seg.len);
    buf[len] = 0;
    return (vx_str){buf, len};
}

// Returns the path [inner], found inside the value at [outer].
static vx_str vx_json_join(vx_str outer, vx_str inner) {
    if (outer.len == 0) return inner;
    if (inner.len == 0) return outer;
    bool dot = inner.ptr[0] != '[';
    int64_t len = outer.len + dot + inner.len;
    char *buf = vx_alloc_atomic((size_t)len + 1);
    memcpy(buf, outer.ptr, (size_t)outer.len);
    if (dot) buf[outer.len] = '.';
    memcpy(buf + outer.len + dot, inner.ptr, (size_t)inner.len);
    buf[len] = 0;
    return (vx_str){buf, len};
}

// The kinds of JSON value, in the order of the cases of `Json`.
static const char *const vx_json_kinds[] = {"null", "a bool", "an int", "a float", "a string", "a list", "an object"};

// Returns the reason a value of kind [tag] doesn't fit where [want] is
// expected.
static vx_str vx_json_want(const char *want, int32_t tag) {
    char buf[160];
    snprintf(buf, sizeof buf, "expected %s, found %s", want, tag >= 0 && tag < 7 ? vx_json_kinds[tag] : "a value");
    return vx_str_from_c(buf);
}

// Returns the reason [name] doesn't name a case of the enum [type].
static vx_str vx_json_unknown_case(vx_str name, const char *type) {
    size_t n = (size_t)name.len + strlen(type) + 32;
    char *buf = vx_alloc_atomic(n);
    int len = snprintf(buf, n, "unknown case '%.*s' of %s", (int)name.len, name.ptr, type);
    return (vx_str){buf, len};
}

static int64_t vx_json_kind(int64_t i) { return vx_json_events[i].kind; }

static int64_t vx_json_int(int64_t i) {
    return strtoll(vx_json_src + vx_json_events[i].start, NULL, 10);
}

static double vx_json_float(int64_t i) {
    return strtod(vx_json_src + vx_json_events[i].start, NULL);
}

// Writes code point [cp] to [out] as UTF-8, returning the bytes written.
static int vx_json_utf8(char *out, uint32_t cp) {
    if (cp < 0x80) { out[0] = (char)cp; return 1; }
    if (cp < 0x800) { out[0] = (char)(0xC0 | cp >> 6); out[1] = (char)(0x80 | (cp & 0x3F)); return 2; }
    if (cp < 0x10000) {
        out[0] = (char)(0xE0 | cp >> 12); out[1] = (char)(0x80 | (cp >> 6 & 0x3F)); out[2] = (char)(0x80 | (cp & 0x3F));
        return 3;
    }
    out[0] = (char)(0xF0 | cp >> 18); out[1] = (char)(0x80 | (cp >> 12 & 0x3F));
    out[2] = (char)(0x80 | (cp >> 6 & 0x3F)); out[3] = (char)(0x80 | (cp & 0x3F));
    return 4;
}

// Returns the string of event [i], with its escapes decoded.
static vx_str vx_json_text(int64_t i) {
    const char *s = vx_json_src + vx_json_events[i].start;
    int64_t n = vx_json_events[i].end - vx_json_events[i].start;
    if (!memchr(s, '\\', (size_t)n)) return vx_str_new(s, n);
    char *buf = vx_alloc_atomic((size_t)n + 1);
    int64_t len = 0;
    for (int64_t k = 0; k < n; k++) {
        if (s[k] != '\\') { buf[len++] = s[k]; continue; }
        char e = s[++k];
        switch (e) {
        case 'b': buf[len++] = '\b'; break;
        case 'f': buf[len++] = '\f'; break;
        case 'n': buf[len++] = '\n'; break;
        case 'r': buf[len++] = '\r'; break;
        case 't': buf[len++] = '\t'; break;
        case 'u': {
            uint32_t cp = (uint32_t)strtoul((char[5]){s[k + 1], s[k + 2], s[k + 3], s[k + 4], 0}, NULL, 16);
            k += 4;
            if (cp >= 0xD800 && cp < 0xDC00 && k + 6 < n && s[k + 1] == '\\' && s[k + 2] == 'u') {
                uint32_t lo = (uint32_t)strtoul((char[5]){s[k + 3], s[k + 4], s[k + 5], s[k + 6], 0}, NULL, 16);
                if (lo >= 0xDC00 && lo < 0xE000) {
                    cp = 0x10000 + ((cp - 0xD800) << 10) + (lo - 0xDC00);
                    k += 6;
                }
            }
            if (cp >= 0xD800 && cp < 0xE000) cp = 0xFFFD;
            len += vx_json_utf8(buf + len, cp);
            break;
        }
        default: buf[len++] = e;
        }
    }
    buf[len] = 0;
    return (vx_str){buf, len};
}

// Returns [s] as a JSON string literal, quotes included.
static vx_str vx_json_quote(vx_str s) {
    int64_t extra = 2;
    for (int64_t i = 0; i < s.len; i++) {
        unsigned char c = (unsigned char)s.ptr[i];
        if (c == '"' || c == '\\' || c == '\n' || c == '\r' || c == '\t') extra++;
        else if (c < 0x20) extra += 5;
    }
    char *buf = vx_alloc_atomic((size_t)(s.len + extra) + 1);
    int64_t len = 0;
    buf[len++] = '"';
    for (int64_t i = 0; i < s.len; i++) {
        unsigned char c = (unsigned char)s.ptr[i];
        switch (c) {
        case '"': buf[len++] = '\\'; buf[len++] = '"'; break;
        case '\\': buf[len++] = '\\'; buf[len++] = '\\'; break;
        case '\n': buf[len++] = '\\'; buf[len++] = 'n'; break;
        case '\r': buf[len++] = '\\'; buf[len++] = 'r'; break;
        case '\t': buf[len++] = '\\'; buf[len++] = 't'; break;
        default:
            if (c < 0x20) len += snprintf(buf + len, 7, "\\u%04x", c);
            else buf[len++] = (char)c;
        }
    }
    buf[len++] = '"';
    buf[len] = 0;
    return (vx_str){buf, len};
}

// Returns [v] written as a JSON number, or "null" when it isn't finite.
static vx_str vx_json_number_str(double v) {
    if (!isfinite(v)) return VX_LIT("null");
    char buf[32];
    int n = 0;
    for (int p = 15; p <= 17; p++) {
        n = snprintf(buf, sizeof buf, "%.*g", p, v);
        if (strtod(buf, NULL) == v) break;
    }
    if (!strpbrk(buf, ".eEn")) { buf[n++] = '.'; buf[n++] = '0'; buf[n] = 0; }
    return vx_str_new(buf, n);
}

static int64_t vx_os_argc(void) { return vx_argc; }

static const char *vx_os_arg(int64_t i) { return i >= 0 && i < vx_argc ? vx_argv[i] : NULL; }

__attribute__((noreturn))
static void vx_os_exit(int64_t code) {
    fflush(stdout);
    exit((int)code);
}

#ifdef _WIN32
// Returns a copy of the malloc'd string [s] on the collected heap, and
// frees [s].
static const char *vx_collected(char *s) {
    size_t n = strlen(s);
    char *out = vx_alloc_atomic(n + 1);
    memcpy(out, s, n + 1);
    free(s);
    return out;
}

static const char *vx_os_cwd(void) {
    wchar_t *w = _wgetcwd(NULL, 0);
    if (!w) { vx_errno = errno; return NULL; }
    const char *out = vx_collected(vx_utf8_of(w, -1));
    free(w);
    return out;
}

// Returns the environment variable [name], or NULL when it isn't set.
static const char *vx_os_get_env(const char *name) {
    wchar_t *wname = vx_utf16_of(name);
    DWORD n = GetEnvironmentVariableW(wname, NULL, 0);
    if (n == 0) {
        bool empty = GetLastError() != ERROR_ENVVAR_NOT_FOUND;
        free(wname);
        return empty ? "" : NULL;
    }
    wchar_t *w = malloc(n * sizeof(wchar_t));
    if (!w) vx_out_of_memory();
    GetEnvironmentVariableW(wname, w, n);
    free(wname);
    const char *out = vx_collected(vx_utf8_of(w, -1));
    free(w);
    return out;
}

// Sets the environment variable [name] for this program, its C library
// and the programs it starts.
static void vx_os_set_env(const char *name, const char *value) {
    wchar_t *wname = vx_utf16_of(name), *wvalue = vx_utf16_of(value);
    _wputenv_s(wname, wvalue);
    // The C library removes a variable set to "", which Windows keeps.
    if (!*value) SetEnvironmentVariableW(wname, L"");
    free(wname);
    free(wvalue);
}

// Returns the exit status of a finished child.
static int64_t vx_os_status(int status) { return status; }
#else
static const char *vx_os_cwd(void) {
    char buf[4096];
    if (!getcwd(buf, sizeof buf)) { vx_errno = errno; return NULL; }
    size_t n = strlen(buf);
    char *out = vx_alloc_atomic(n + 1);
    memcpy(out, buf, n + 1);
    return out;
}

// Returns the environment variable [name], or NULL when it isn't set.
static const char *vx_os_get_env(const char *name) { return getenv(name); }

static void vx_os_set_env(const char *name, const char *value) { setenv(name, value, 1); }

// Returns the exit status of a finished child, or 128 + the signal that
// stopped it.
static int64_t vx_os_status(int status) {
    if (WIFEXITED(status)) return WEXITSTATUS(status);
    if (WIFSIGNALED(status)) return 128 + WTERMSIG(status);
    return -1;
}
#endif

// Runs [cmd] with the shell; returns its exit status, or -1 if it couldn't
// start.
static int64_t vx_os_run(const char *cmd) {
    fflush(stdout);
    int status = system(cmd);
    if (status == -1) { vx_errno = errno; return -1; }
    return vx_os_status(status);
}

static int64_t vx_os_last_status;

// Runs [cmd] with the shell and returns what it printed; its exit status
// is left in vx_os_last_status.
static const char *vx_os_output(const char *cmd) {
    fflush(stdout);
    FILE *p = popen(cmd, "r");
    if (!p) { vx_errno = errno; return NULL; }
    size_t cap = 4096, len = 0, n;
    char *buf = vx_alloc_atomic(cap);
    while ((n = fread(buf + len, 1, cap - len - 1, p)) > 0) {
        len += n;
        if (cap - len < 2) {
            buf = vx_realloc(buf, cap * 2, 1);
            cap *= 2;
        }
    }
    buf[len] = 0;
    int status = pclose(p);
    vx_os_last_status = status == -1 ? -1 : vx_os_status(status);
    return buf;
}

static int64_t vx_os_status_of_last(void) { return vx_os_last_status; }

// --- Regular expressions ---
//
// The engine behind vex:regex. A pattern is parsed into a tree, compiled
// to a program, and run by a backtracking matcher that remembers which
// (instruction, position) pairs already failed, so matching takes time
// proportional to the program's size times the text's length. Matches
// prefer the leftmost start, then the first alternative, as in Perl.

enum { VX_RN_CHAR, VX_RN_ANY, VX_RN_CLASS, VX_RN_BOL, VX_RN_EOL, VX_RN_WORDB, VX_RN_NWORDB,
       VX_RN_GROUP, VX_RN_CAT, VX_RN_ALT, VX_RN_REPEAT, VX_RN_EMPTY };

enum { VX_RE_CHAR, VX_RE_ANY, VX_RE_CLASS, VX_RE_BOL, VX_RE_EOL, VX_RE_WORDB, VX_RE_NWORDB,
       VX_RE_SPLIT, VX_RE_JMP, VX_RE_SAVE, VX_RE_MATCH };

// The most a counted repeat may ask for, and the longest program.
#define VX_RE_MAX_REPEAT 1000
#define VX_RE_MAX_PROG 100000

typedef struct {
    int kind;
    int a, b;          // children; for a class, its first range and count
    int min, max;      // repeat counts, max -1 for no limit; a group's number, -1 if it captures nothing
    bool greedy, neg;
    uint32_t c;
} vx_re_node;

typedef struct {
    uint8_t op;
    bool neg;
    int32_t x, y;      // jump targets; a class's first range and count; a save's slot
    uint32_t c;
} vx_re_inst;

typedef struct {
    vx_re_inst *code;
    int n, cap;
    uint32_t *ranges;  // pairs of lowest and highest code point
    int nranges;
    int groups;        // capturing groups, not counting the whole match
    char **names;      // the name of each group from 1, or NULL
    bool icase;
} vx_re_prog;

typedef struct {
    const char *p;
    int64_t n, i;
    vx_re_node *nodes;
    int nn, ncap;
    uint32_t *ranges;
    int nr, rcap;
    int groups;
    char **names;
    int names_cap;
    bool icase;
    bool failed;
    char err[200];
} vx_re_parser;

static VX_TLS char vx_re_err[256];

// Returns the character index of byte [i] of the pattern, for messages.
static int64_t vx_re_column(vx_re_parser *ps, int64_t i) {
    vx_str s = {ps->p, ps->n};
    int64_t col = 0;
    for (int64_t b = 0; b < i && b < ps->n; b += vx_utf8_width(s, b)) col++;
    return col;
}

static int vx_re_fail(vx_re_parser *ps, int64_t at, const char *fmt, ...) {
    if (ps->failed) return -1;
    ps->failed = true;
    char msg[160];
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(msg, sizeof msg, fmt, ap);
    va_end(ap);
    snprintf(ps->err, sizeof ps->err, "%s at character %" PRId64, msg, vx_re_column(ps, at));
    return -1;
}

static int vx_re_node_new(vx_re_parser *ps, int kind) {
    if (ps->nn == ps->ncap) {
        ps->ncap = ps->ncap ? ps->ncap * 2 : 32;
        ps->nodes = realloc(ps->nodes, (size_t)ps->ncap * sizeof(vx_re_node));
        if (!ps->nodes) abort();
    }
    ps->nodes[ps->nn] = (vx_re_node){.kind = kind, .a = -1, .b = -1, .min = -1, .max = -1, .greedy = true};
    return ps->nn++;
}

static int vx_re_pair(vx_re_parser *ps, int kind, int a, int b) {
    if (a < 0) return b;
    if (b < 0) return a;
    int n = vx_re_node_new(ps, kind);
    ps->nodes[n].a = a;
    ps->nodes[n].b = b;
    return n;
}

static void vx_re_range(vx_re_parser *ps, uint32_t lo, uint32_t hi) {
    if (ps->nr + 2 > ps->rcap) {
        ps->rcap = ps->rcap ? ps->rcap * 2 : 32;
        ps->ranges = realloc(ps->ranges, (size_t)ps->rcap * sizeof(uint32_t));
        if (!ps->ranges) abort();
    }
    ps->ranges[ps->nr++] = lo;
    ps->ranges[ps->nr++] = hi;
}

// Returns the code point at byte [*i] of the pattern and moves past it.
static uint32_t vx_re_decode(const char *p, int64_t n, int64_t *i) {
    vx_str s = {p, n};
    int64_t w = vx_utf8_width(s, *i);
    unsigned char c = (unsigned char)p[*i];
    uint32_t cp = c;
    if (w == 2) cp = c & 0x1F;
    else if (w == 3) cp = c & 0x0F;
    else if (w == 4) cp = c & 0x07;
    for (int64_t k = 1; k < w; k++) cp = (cp << 6) | ((unsigned char)p[*i + k] & 0x3F);
    *i += w;
    return cp;
}

// Adds the ranges of the class escape [c] (d, w or s, in either case),
// negated for the capital letters inside a bracket class.
static void vx_re_class_escape(vx_re_parser *ps, char c) {
    switch (c | 0x20) {
    case 'd':
        vx_re_range(ps, '0', '9');
        break;
    case 'w':
        vx_re_range(ps, '0', '9');
        vx_re_range(ps, 'A', 'Z');
        vx_re_range(ps, '_', '_');
        vx_re_range(ps, 'a', 'z');
        break;
    case 's':
        vx_re_range(ps, '\t', '\r');
        vx_re_range(ps, ' ', ' ');
        break;
    }
}

// Adds the complement of the ranges from [start] on, which are sorted.
static void vx_re_complement(vx_re_parser *ps, int start) {
    int end = ps->nr;
    uint32_t next = 0;
    for (int k = start; k < end; k += 2) {
        if (ps->ranges[k] > next) vx_re_range(ps, next, ps->ranges[k] - 1);
        next = ps->ranges[k + 1] + 1;
    }
    vx_re_range(ps, next, 0x10FFFF);
    memmove(ps->ranges + start, ps->ranges + end, (size_t)(ps->nr - end) * sizeof(uint32_t));
    ps->nr -= end - start;
}

// Reads the escape after a backslash at [*i] that stands for one
// character, or returns false for an unknown one.
static bool vx_re_char_escape(vx_re_parser *ps, uint32_t *out) {
    char c = ps->p[ps->i];
    static const char map[][2] = {{'n', '\n'}, {'t', '\t'}, {'r', '\r'}, {'f', '\f'}, {'v', '\v'}, {'0', 0}};
    for (size_t k = 0; k < sizeof map / sizeof map[0]; k++) {
        if (map[k][0] == c) {
            *out = (uint32_t)map[k][1];
            ps->i++;
            return true;
        }
    }
    if ((unsigned char)c < 0x80 && !isalnum((unsigned char)c)) {
        *out = (uint32_t)c;
        ps->i++;
        return true;
    }
    return false;
}

// Parses a bracket class; [*i] is just past the [.
static int vx_re_parse_class(vx_re_parser *ps) {
    int64_t open = ps->i - 1;
    bool neg = false;
    if (ps->i < ps->n && ps->p[ps->i] == '^') {
        neg = true;
        ps->i++;
    }
    int start = ps->nr;
    bool first = true;
    for (;;) {
        if (ps->i >= ps->n) return vx_re_fail(ps, open, "missing ] to close the class");
        char c = ps->p[ps->i];
        if (c == ']' && !first) {
            ps->i++;
            break;
        }
        first = false;
        uint32_t lo;
        if (c == '\\') {
            ps->i++;
            if (ps->i >= ps->n) return vx_re_fail(ps, ps->i - 1, "the pattern ends after \\");
            char e = ps->p[ps->i];
            if (strchr("dwsDWS", e)) {
                ps->i++;
                if (e >= 'a') {
                    vx_re_class_escape(ps, e);
                } else {
                    int sub = ps->nr;
                    vx_re_class_escape(ps, e);
                    vx_re_complement(ps, sub);
                }
                continue;
            }
            if (!vx_re_char_escape(ps, &lo)) return vx_re_fail(ps, ps->i - 1, "unknown escape \\%c", e);
        } else {
            lo = vx_re_decode(ps->p, ps->n, &ps->i);
        }
        uint32_t hi = lo;
        if (ps->i + 1 < ps->n && ps->p[ps->i] == '-' && ps->p[ps->i + 1] != ']') {
            int64_t dash = ps->i;
            ps->i++;
            if (ps->p[ps->i] == '\\') {
                ps->i++;
                if (ps->i >= ps->n || !vx_re_char_escape(ps, &hi)) return vx_re_fail(ps, dash, "a range must end in a single character");
            } else {
                hi = vx_re_decode(ps->p, ps->n, &ps->i);
            }
            if (hi < lo) return vx_re_fail(ps, dash, "the range ends before it starts");
        }
        vx_re_range(ps, lo, hi);
        if (ps->icase) {
            uint32_t a = lo < 'A' ? 'A' : lo, b = hi > 'Z' ? 'Z' : hi;
            if (a <= b) vx_re_range(ps, a + 32, b + 32);
            a = lo < 'a' ? 'a' : lo, b = hi > 'z' ? 'z' : hi;
            if (a <= b) vx_re_range(ps, a - 32, b - 32);
        }
    }
    // Sort and merge the ranges, so the class can be complemented and
    // searched in order.
    int count = (ps->nr - start) / 2;
    uint32_t *r = ps->ranges + start;
    for (int x = 1; x < count; x++) {
        uint32_t lo = r[2 * x], hi = r[2 * x + 1];
        int y = x - 1;
        while (y >= 0 && r[2 * y] > lo) {
            r[2 * y + 2] = r[2 * y];
            r[2 * y + 3] = r[2 * y + 1];
            y--;
        }
        r[2 * y + 2] = lo;
        r[2 * y + 3] = hi;
    }
    int m = 0;
    for (int x = 0; x < count; x++) {
        if (m > 0 && r[2 * x] <= r[2 * m - 1] + 1) {
            if (r[2 * x + 1] > r[2 * m - 1]) r[2 * m - 1] = r[2 * x + 1];
        } else {
            r[2 * m] = r[2 * x];
            r[2 * m + 1] = r[2 * x + 1];
            m++;
        }
    }
    ps->nr = start + 2 * m;
    if (neg) vx_re_complement(ps, start);
    int node = vx_re_node_new(ps, VX_RN_CLASS);
    ps->nodes[node].a = start;
    ps->nodes[node].b = (ps->nr - start) / 2;
    return node;
}

static int vx_re_parse_alt(vx_re_parser *ps, int depth);

// Reads a whole number at [*i] for a repeat count; -1 when there is none.
static int vx_re_count(vx_re_parser *ps) {
    if (ps->i >= ps->n || !isdigit((unsigned char)ps->p[ps->i])) return -1;
    int v = 0;
    while (ps->i < ps->n && isdigit((unsigned char)ps->p[ps->i])) {
        if (v <= VX_RE_MAX_REPEAT) v = v * 10 + (ps->p[ps->i] - '0');
        ps->i++;
    }
    return v;
}

static int vx_re_parse_atom(vx_re_parser *ps, int depth) {
    int64_t at = ps->i;
    char c = ps->p[ps->i];
    switch (c) {
    case '(': {
        ps->i++;
        int group = -1;
        if (ps->i < ps->n && ps->p[ps->i] == '?') {
            if (ps->i + 1 < ps->n && ps->p[ps->i + 1] == ':') {
                ps->i += 2;
            } else if (ps->i + 1 < ps->n && ps->p[ps->i + 1] == '<') {
                ps->i += 2;
                int64_t name = ps->i;
                while (ps->i < ps->n && (isalnum((unsigned char)ps->p[ps->i]) || ps->p[ps->i] == '_')) ps->i++;
                if (ps->i == name || isdigit((unsigned char)ps->p[name])) return vx_re_fail(ps, name, "a group name must start with a letter or _");
                if (ps->i >= ps->n || ps->p[ps->i] != '>') return vx_re_fail(ps, ps->i, "missing > after the group name");
                size_t len = (size_t)(ps->i - name);
                for (int g = 0; g < ps->groups; g++) {
                    if (ps->names[g] && strlen(ps->names[g]) == len && memcmp(ps->names[g], ps->p + name, len) == 0) {
                        return vx_re_fail(ps, name, "the group name '%.*s' is used twice", (int)len, ps->p + name);
                    }
                }
                group = ps->groups++;
                if (ps->groups > ps->names_cap) {
                    ps->names_cap = ps->names_cap ? ps->names_cap * 2 : 8;
                    ps->names = realloc(ps->names, (size_t)ps->names_cap * sizeof(char *));
                    if (!ps->names) abort();
                }
                ps->names[group] = strndup(ps->p + name, len);
                ps->i++;
            } else {
                return vx_re_fail(ps, ps->i, "unknown group kind; use (?:...) or (?<name>...)");
            }
        } else {
            group = ps->groups++;
            if (ps->groups > ps->names_cap) {
                ps->names_cap = ps->names_cap ? ps->names_cap * 2 : 8;
                ps->names = realloc(ps->names, (size_t)ps->names_cap * sizeof(char *));
                if (!ps->names) abort();
            }
            ps->names[group] = NULL;
        }
        int inner = vx_re_parse_alt(ps, depth + 1);
        if (ps->failed) return -1;
        if (ps->i >= ps->n || ps->p[ps->i] != ')') return vx_re_fail(ps, at, "missing ) to close the group");
        ps->i++;
        int node = vx_re_node_new(ps, VX_RN_GROUP);
        ps->nodes[node].a = inner;
        ps->nodes[node].min = group < 0 ? -1 : group + 1;
        return node;
    }
    case ')':
        return vx_re_fail(ps, at, "unmatched )");
    case '[':
        ps->i++;
        return vx_re_parse_class(ps);
    case '.':
        ps->i++;
        return vx_re_node_new(ps, VX_RN_ANY);
    case '^':
        ps->i++;
        return vx_re_node_new(ps, VX_RN_BOL);
    case '$':
        ps->i++;
        return vx_re_node_new(ps, VX_RN_EOL);
    case '*': case '+': case '?':
        return vx_re_fail(ps, at, "nothing to repeat before %c", c);
    case '{':
        return vx_re_fail(ps, at, "nothing to repeat before {; write \\{ for a brace");
    case '\\': {
        ps->i++;
        if (ps->i >= ps->n) return vx_re_fail(ps, at, "the pattern ends after \\");
        char e = ps->p[ps->i];
        if (e == 'b' || e == 'B') {
            ps->i++;
            return vx_re_node_new(ps, e == 'b' ? VX_RN_WORDB : VX_RN_NWORDB);
        }
        if (strchr("dwsDWS", e)) {
            ps->i++;
            int start = ps->nr;
            vx_re_class_escape(ps, e);
            if (e < 'a') vx_re_complement(ps, start);
            int node = vx_re_node_new(ps, VX_RN_CLASS);
            ps->nodes[node].a = start;
            ps->nodes[node].b = (ps->nr - start) / 2;
            return node;
        }
        uint32_t cp;
        if (!vx_re_char_escape(ps, &cp)) return vx_re_fail(ps, at, "unknown escape \\%c", e);
        int node = vx_re_node_new(ps, VX_RN_CHAR);
        ps->nodes[node].c = cp;
        return node;
    }
    default: {
        int node = vx_re_node_new(ps, VX_RN_CHAR);
        ps->nodes[node].c = vx_re_decode(ps->p, ps->n, &ps->i);
        return node;
    }
    }
}

// Parses an atom and the repeat after it, if any.
static int vx_re_parse_repeat(vx_re_parser *ps, int depth) {
    int atom = vx_re_parse_atom(ps, depth);
    if (ps->failed || ps->i >= ps->n) return atom;
    int64_t at = ps->i;
    char c = ps->p[ps->i];
    int min, max;
    if (c == '*') min = 0, max = -1;
    else if (c == '+') min = 1, max = -1;
    else if (c == '?') min = 0, max = 1;
    else if (c == '{') {
        ps->i++;
        min = vx_re_count(ps);
        if (min < 0) return vx_re_fail(ps, at, "expected a count after {; write \\{ for a brace");
        max = min;
        if (ps->i < ps->n && ps->p[ps->i] == ',') {
            ps->i++;
            max = vx_re_count(ps);
        }
        if (ps->i >= ps->n || ps->p[ps->i] != '}') return vx_re_fail(ps, at, "missing } to close the repeat count");
        if (min > VX_RE_MAX_REPEAT || max > VX_RE_MAX_REPEAT) return vx_re_fail(ps, at, "repeat counts can be at most %d", VX_RE_MAX_REPEAT);
        if (max >= 0 && max < min) return vx_re_fail(ps, at, "the repeat count {%d,%d} has its largest first", min, max);
    } else {
        return atom;
    }
    ps->i++;
    int kind = ps->nodes[atom].kind;
    if (kind == VX_RN_BOL || kind == VX_RN_EOL || kind == VX_RN_WORDB || kind == VX_RN_NWORDB) {
        return vx_re_fail(ps, at, "an anchor can't be repeated");
    }
    bool greedy = true;
    if (ps->i < ps->n && ps->p[ps->i] == '?') {
        greedy = false;
        ps->i++;
    }
    if (ps->i < ps->n && strchr("*+?{", ps->p[ps->i])) {
        return vx_re_fail(ps, ps->i, "a repeat can't follow another; put the first in (?:...)");
    }
    int node = vx_re_node_new(ps, VX_RN_REPEAT);
    ps->nodes[node].a = atom;
    ps->nodes[node].min = min;
    ps->nodes[node].max = max;
    ps->nodes[node].greedy = greedy;
    return node;
}

static int vx_re_parse_cat(vx_re_parser *ps, int depth) {
    int left = -1;
    while (!ps->failed && ps->i < ps->n && ps->p[ps->i] != '|' && !(ps->p[ps->i] == ')' && depth > 0)) {
        left = vx_re_pair(ps, VX_RN_CAT, left, vx_re_parse_repeat(ps, depth));
    }
    return left < 0 ? vx_re_node_new(ps, VX_RN_EMPTY) : left;
}

static int vx_re_parse_alt(vx_re_parser *ps, int depth) {
    if (depth > 200) return vx_re_fail(ps, ps->i, "groups are nested too deeply");
    int left = vx_re_parse_cat(ps, depth);
    while (!ps->failed && ps->i < ps->n && ps->p[ps->i] == '|') {
        ps->i++;
        int right = vx_re_parse_cat(ps, depth);
        int n = vx_re_node_new(ps, VX_RN_ALT);
        ps->nodes[n].a = left;
        ps->nodes[n].b = right;
        left = n;
    }
    return left;
}

static int vx_re_emit(vx_re_prog *p, vx_re_inst in) {
    if (p->n == p->cap) {
        p->cap = p->cap ? p->cap * 2 : 64;
        p->code = realloc(p->code, (size_t)p->cap * sizeof(vx_re_inst));
        if (!p->code) abort();
    }
    p->code[p->n] = in;
    return p->n++;
}

// Writes the program for node [k]; false when it grows too long.
static bool vx_re_gen(vx_re_parser *ps, vx_re_prog *p, int k) {
    if (p->n > VX_RE_MAX_PROG) return false;
    vx_re_node *nd = &ps->nodes[k];
    switch (nd->kind) {
    case VX_RN_CHAR:
        vx_re_emit(p, (vx_re_inst){.op = VX_RE_CHAR, .c = ps->icase && nd->c >= 'A' && nd->c <= 'Z' ? nd->c + 32 : nd->c});
        return true;
    case VX_RN_ANY: vx_re_emit(p, (vx_re_inst){.op = VX_RE_ANY}); return true;
    case VX_RN_CLASS: vx_re_emit(p, (vx_re_inst){.op = VX_RE_CLASS, .x = nd->a, .y = nd->b}); return true;
    case VX_RN_BOL: vx_re_emit(p, (vx_re_inst){.op = VX_RE_BOL}); return true;
    case VX_RN_EOL: vx_re_emit(p, (vx_re_inst){.op = VX_RE_EOL}); return true;
    case VX_RN_WORDB: vx_re_emit(p, (vx_re_inst){.op = VX_RE_WORDB}); return true;
    case VX_RN_NWORDB: vx_re_emit(p, (vx_re_inst){.op = VX_RE_NWORDB}); return true;
    case VX_RN_EMPTY: return true;
    case VX_RN_GROUP:
        if (nd->min >= 0) vx_re_emit(p, (vx_re_inst){.op = VX_RE_SAVE, .x = 2 * nd->min});
        if (!vx_re_gen(ps, p, nd->a)) return false;
        if (nd->min >= 0) vx_re_emit(p, (vx_re_inst){.op = VX_RE_SAVE, .x = 2 * nd->min + 1});
        return true;
    case VX_RN_CAT: case VX_RN_ALT: {
        // Chains nest to the left; they are walked in a loop, so a long
        // pattern doesn't recurse deeply.
        int kind = nd->kind, count = 0;
        for (int at = k; ps->nodes[at].kind == kind; at = ps->nodes[at].a) count++;
        int *parts = malloc((size_t)(count + 1) * sizeof(int));
        if (!parts) abort();
        int at = k;
        for (int x = count; x > 0; x--) {
            parts[x] = ps->nodes[at].b;
            at = ps->nodes[at].a;
        }
        parts[0] = at;
        bool ok = true;
        if (kind == VX_RN_CAT) {
            for (int x = 0; x <= count && ok; x++) ok = vx_re_gen(ps, p, parts[x]);
        } else {
            int *jumps = malloc((size_t)count * sizeof(int) + 1);
            if (!jumps) abort();
            for (int x = 0; x < count && ok; x++) {
                int split = vx_re_emit(p, (vx_re_inst){.op = VX_RE_SPLIT});
                p->code[split].x = p->n;
                ok = vx_re_gen(ps, p, parts[x]);
                jumps[x] = vx_re_emit(p, (vx_re_inst){.op = VX_RE_JMP});
                p->code[split].y = p->n;
            }
            if (ok) ok = vx_re_gen(ps, p, parts[count]);
            if (ok) {
                for (int x = 0; x < count; x++) p->code[jumps[x]].x = p->n;
            }
            free(jumps);
        }
        free(parts);
        return ok;
    }
    case VX_RN_REPEAT: {
        int min = nd->min, max = nd->max, child = nd->a;
        bool greedy = nd->greedy;
        for (int r = 0; r < min; r++) {
            if (!vx_re_gen(ps, p, child)) return false;
        }
        if (max < 0) {
            int loop = vx_re_emit(p, (vx_re_inst){.op = VX_RE_SPLIT});
            if (!vx_re_gen(ps, p, child)) return false;
            vx_re_emit(p, (vx_re_inst){.op = VX_RE_JMP, .x = loop});
            p->code[loop].x = greedy ? loop + 1 : p->n;
            p->code[loop].y = greedy ? p->n : loop + 1;
            return true;
        }
        int optional = max - min;
        int *splits = malloc((size_t)(optional > 0 ? optional : 1) * sizeof(int));
        if (!splits) abort();
        for (int r = 0; r < optional; r++) {
            splits[r] = vx_re_emit(p, (vx_re_inst){.op = VX_RE_SPLIT});
            if (!vx_re_gen(ps, p, child)) {
                free(splits);
                return false;
            }
        }
        // Each optional copy may end the repeat.
        for (int r = 0; r < optional; r++) {
            p->code[splits[r]].x = greedy ? splits[r] + 1 : p->n;
            p->code[splits[r]].y = greedy ? p->n : splits[r] + 1;
        }
        free(splits);
        return true;
    }
    }
    return true;
}

static void vx_re_prog_free(vx_re_prog *p) {
    if (!p) return;
    for (int g = 0; g < p->groups; g++) free(p->names[g]);
    free(p->names);
    free(p->code);
    free(p->ranges);
    free(p);
}

// Compiles [pattern], or returns NULL with the reason in vx_re_err.
static vx_re_prog *vx_re_build(vx_str pattern) {
    vx_re_parser ps = {.p = pattern.ptr, .n = pattern.len};
    if (ps.n >= 4 && memcmp(ps.p, "(?i)", 4) == 0) {
        ps.icase = true;
        ps.i = 4;
    }
    int root = vx_re_parse_alt(&ps, 0);
    if (!ps.failed && ps.i < ps.n) vx_re_fail(&ps, ps.i, "unmatched )");
    vx_re_prog *p = calloc(1, sizeof(vx_re_prog));
    if (!p) abort();
    p->groups = ps.groups;
    p->names = ps.names;
    p->icase = ps.icase;
    if (!ps.failed && !vx_re_gen(&ps, p, root)) {
        snprintf(ps.err, sizeof ps.err, "the pattern is too large; repeat counts multiply its size");
        ps.failed = true;
    }
    if (ps.failed) {
        snprintf(vx_re_err, sizeof vx_re_err, "%s", ps.err);
        free(ps.nodes);
        free(ps.ranges);
        vx_re_prog_free(p);
        return NULL;
    }
    vx_re_emit(p, (vx_re_inst){.op = VX_RE_MATCH});
    p->ranges = ps.ranges;
    p->nranges = ps.nr;
    free(ps.nodes);
    return p;
}

// Recently used programs, by pattern, so a Regex needn't own C memory.
#define VX_RE_CACHE 16
typedef struct {
    char *pattern;
    int64_t len;
    vx_re_prog *prog;
    uint64_t used;
} vx_re_entry;
static VX_TLS vx_re_entry vx_re_cache[VX_RE_CACHE];
static VX_TLS uint64_t vx_re_clock;

// Returns the program for [pattern], compiling it if it isn't cached.
static vx_re_prog *vx_re_get(vx_str pattern) {
    vx_re_entry *slot = &vx_re_cache[0];
    for (int k = 0; k < VX_RE_CACHE; k++) {
        vx_re_entry *e = &vx_re_cache[k];
        if (e->prog && e->len == pattern.len && memcmp(e->pattern, pattern.ptr, (size_t)pattern.len) == 0) {
            e->used = ++vx_re_clock;
            return e->prog;
        }
        if (!e->prog || e->used < slot->used) slot = e;
    }
    vx_re_prog *p = vx_re_build(pattern);
    if (!p) return NULL;
    free(slot->pattern);
    vx_re_prog_free(slot->prog);
    slot->pattern = malloc((size_t)pattern.len + 1);
    if (!slot->pattern) abort();
    memcpy(slot->pattern, pattern.ptr, (size_t)pattern.len);
    slot->len = pattern.len;
    slot->prog = p;
    slot->used = ++vx_re_clock;
    return p;
}

// Compiles [pattern] and returns its number of groups, or -1 with the
// reason in vx_re_error.
static int64_t vx_re_compile(vx_str pattern) {
    vx_re_prog *p = vx_re_get(pattern);
    return p ? p->groups : -1;
}

static vx_str vx_re_error(void) { return vx_str_from_c(vx_re_err); }

// Returns the name of group [g] of [pattern], counting from 1, or a
// string with a null pointer for a group without one.
static vx_str vx_re_name(vx_str pattern, int64_t g) {
    vx_re_prog *p = vx_re_get(pattern);
    if (!p || g < 1 || g > p->groups || !p->names[g - 1]) return (vx_str){NULL, 0};
    return vx_str_from_c(p->names[g - 1]);
}

// Which (instruction, position) pairs the current search has tried: a
// row of bits for each text position, valid when its generation is the
// current one, so a new search clears only the rows it reaches.
static VX_TLS uint8_t *vx_re_bits;
static VX_TLS uint32_t *vx_re_gens;
static VX_TLS size_t vx_re_bits_cap, vx_re_gens_cap, vx_re_row_bytes;
static VX_TLS uint32_t vx_re_gen_now;

// The jobs left to try: a position to try an instruction at, or a slot
// to restore when backtracking past the save that set it.
typedef struct { int32_t pc, slot; int64_t pos; } vx_re_job;
static VX_TLS vx_re_job *vx_re_stack;
static VX_TLS size_t vx_re_stack_cap;

// The slots of the last match: two byte offsets per group, -1 when unset.
static VX_TLS int64_t *vx_re_caps;
static VX_TLS size_t vx_re_caps_cap;

// Prepares the memory of tried pairs for a search of [rows] positions
// with a program of [prog_len] instructions.
static void vx_re_memo(size_t rows, int prog_len) {
    size_t row_bytes = ((size_t)prog_len + 7) / 8;
    size_t need = rows * row_bytes;
    // A buffer left large by a long text is given back.
    if (need > vx_re_bits_cap || (vx_re_bits_cap > ((size_t)64 << 20) && need < vx_re_bits_cap / 4)) {
        free(vx_re_bits);
        vx_re_bits = malloc(need);
        if (!vx_re_bits) abort();
        vx_re_bits_cap = need;
    }
    if (row_bytes != vx_re_row_bytes || rows > vx_re_gens_cap) {
        if (rows > vx_re_gens_cap) {
            free(vx_re_gens);
            vx_re_gens = malloc(rows * sizeof(uint32_t));
            if (!vx_re_gens) abort();
            vx_re_gens_cap = rows;
        }
        memset(vx_re_gens, 0, vx_re_gens_cap * sizeof(uint32_t));
        vx_re_row_bytes = row_bytes;
        vx_re_gen_now = 0;
    }
    if (++vx_re_gen_now == 0) {
        memset(vx_re_gens, 0, vx_re_gens_cap * sizeof(uint32_t));
        vx_re_gen_now = 1;
    }
}

// Marks instruction [pc] at [pos] as tried, and reports whether it was.
static inline bool vx_re_seen(int32_t pc, int64_t pos) {
    uint8_t *row = vx_re_bits + (size_t)pos * vx_re_row_bytes;
    if (vx_re_gens[pos] != vx_re_gen_now) {
        memset(row, 0, vx_re_row_bytes);
        vx_re_gens[pos] = vx_re_gen_now;
    }
    uint8_t bit = (uint8_t)(1u << (pc & 7));
    if (row[pc >> 3] & bit) return true;
    row[pc >> 3] |= bit;
    return false;
}

static inline void vx_re_push(int32_t pc, int32_t slot, int64_t pos, size_t *len) {
    if (*len == vx_re_stack_cap) {
        vx_re_stack_cap = vx_re_stack_cap ? vx_re_stack_cap * 2 : 256;
        vx_re_stack = realloc(vx_re_stack, vx_re_stack_cap * sizeof(vx_re_job));
        if (!vx_re_stack) abort();
    }
    vx_re_stack[(*len)++] = (vx_re_job){pc, slot, pos};
}

static inline bool vx_re_word(const char *s, int64_t n, int64_t i) {
    if (i < 0 || i >= n) return false;
    unsigned char c = (unsigned char)s[i];
    return isalnum(c) || c == '_';
}

static bool vx_re_in_class(const vx_re_prog *p, const vx_re_inst *in, uint32_t cp) {
    const uint32_t *r = p->ranges + in->x;
    for (int k = 0; k < in->y; k++) {
        if (cp < r[2 * k]) return false;
        if (cp <= r[2 * k + 1]) return true;
    }
    return false;
}

// Tries to match [p] starting at byte [start] of [text]. With [whole],
// the match must also reach the end of the text.
static bool vx_re_run(const vx_re_prog *p, vx_str text, int64_t start, bool whole) {
    const char *s = text.ptr;
    int64_t n = text.len;
    size_t len = 0;
    vx_re_push(0, -1, start, &len);
    while (len > 0) {
        vx_re_job job = vx_re_stack[--len];
        if (job.slot >= 0) {
            vx_re_caps[job.slot] = job.pos;
            continue;
        }
        int32_t pc = job.pc;
        int64_t pos = job.pos;
        for (;;) {
            if (vx_re_seen(pc, pos)) break;
            const vx_re_inst *in = &p->code[pc];
            bool ok = false;
            switch (in->op) {
            case VX_RE_CHAR: case VX_RE_ANY: case VX_RE_CLASS: {
                if (pos >= n) break;
                int64_t at = pos;
                uint32_t cp = vx_re_decode(s, n, &at);
                if (in->op == VX_RE_ANY) ok = cp != '\n';
                else if (in->op == VX_RE_CHAR) ok = (p->icase && cp >= 'A' && cp <= 'Z' ? cp + 32 : cp) == in->c;
                else ok = vx_re_in_class(p, in, cp);
                if (ok) {
                    pos = at;
                    pc++;
                }
                break;
            }
            case VX_RE_BOL: ok = pos == 0; pc++; break;
            case VX_RE_EOL: ok = pos == n; pc++; break;
            case VX_RE_WORDB: ok = vx_re_word(s, n, pos - 1) != vx_re_word(s, n, pos); pc++; break;
            case VX_RE_NWORDB: ok = vx_re_word(s, n, pos - 1) == vx_re_word(s, n, pos); pc++; break;
            case VX_RE_JMP: ok = true; pc = in->x; break;
            case VX_RE_SPLIT:
                vx_re_push(in->y, -1, pos, &len);
                ok = true;
                pc = in->x;
                break;
            case VX_RE_SAVE:
                vx_re_push(0, in->x, vx_re_caps[in->x], &len);
                vx_re_caps[in->x] = pos;
                ok = true;
                pc++;
                break;
            case VX_RE_MATCH:
                if (whole && pos != n) break;
                vx_re_caps[0] = start;
                vx_re_caps[1] = pos;
                return true;
            }
            if (!ok) break;
        }
    }
    return false;
}

// The last byte offset turned into a character index, so the next one in
// the same text counts on from there.
static VX_TLS const char *vx_re_index_text;
static VX_TLS int64_t vx_re_index_byte, vx_re_index_char;

// Returns the character index of byte [b] of [text].
static int64_t vx_re_chars(vx_str text, int64_t b) {
    int64_t i = 0, c = 0;
    if (text.ptr == vx_re_index_text && b >= vx_re_index_byte) {
        i = vx_re_index_byte;
        c = vx_re_index_char;
    }
    for (; i < b && i < text.len; i += vx_utf8_width(text, i)) c++;
    vx_re_index_text = text.ptr;
    vx_re_index_byte = i;
    vx_re_index_char = c;
    return c;
}

// Forgets the character index kept for vx_re_chars, before a new text.
static void vx_re_reset(void) { vx_re_index_text = NULL; }

// Searches [text] for [pattern] from byte [from], keeping the groups of
// the match for vx_re_cap. With [whole], the match must cover the text
// from [from] to the end.
static bool vx_re_exec(vx_str pattern, vx_str text, int64_t from, bool whole) {
    vx_re_prog *p = vx_re_get(pattern);
    if (!p || from < 0 || from > text.len) return false;
    size_t slots = 2 * ((size_t)p->groups + 1);
    if (slots > vx_re_caps_cap) {
        vx_re_caps = realloc(vx_re_caps, slots * sizeof(int64_t));
        if (!vx_re_caps) abort();
        vx_re_caps_cap = slots;
    }
    for (size_t k = 0; k < slots; k++) vx_re_caps[k] = -1;
    if (from == 0) vx_re_reset();
    vx_re_memo((size_t)text.len + 1, p->n);
    bool anchored = p->code[0].op == VX_RE_BOL;
    for (int64_t at = from; at <= text.len; at += at < text.len ? vx_utf8_width(text, at) : 1) {
        if (vx_re_run(p, text, at, whole)) return true;
        if (whole || anchored) break;
    }
    return false;
}

// Returns slot [k] of the last match: the start of group k/2 for even k,
// its end for odd k, as a byte offset, or -1 when it took no part.
static int64_t vx_re_cap(int64_t k) {
    return k >= 0 && (size_t)k < vx_re_caps_cap ? vx_re_caps[k] : -1;
}

// Returns bytes [a] to [b] of [text].
static vx_str vx_re_slice(vx_str text, int64_t a, int64_t b) {
    if (a < 0 || b > text.len || b < a) return VX_LIT("");
    return vx_str_new(text.ptr + a, b - a);
}

// Returns the byte after the character at byte [b] of [text], or -1 at
// the end.
static int64_t vx_re_next(vx_str text, int64_t b) {
    return b < text.len ? b + vx_utf8_width(text, b) : -1;
}

// Returns the length of [text] in bytes.
static int64_t vx_re_bytes(vx_str text) { return text.len; }

// --- Async ---

// Switches from the running stack to [to], saving the callee-saved
// registers on the running stack and its stack pointer in [save]. A task
// starts in vx_task_boot, with the task in a callee-saved register.
void vx_ctx_switch(void **save, void *to);
void vx_task_main(vx_task *t);

#if defined(__aarch64__)
__asm__(
    ".text\n"
    ".p2align 2\n"
    ".globl " VX_SYM("vx_ctx_switch") "\n"
    VX_SYM("vx_ctx_switch") ":\n"
    "    sub sp, sp, #160\n"
    "    stp x19, x20, [sp, #0]\n"
    "    stp x21, x22, [sp, #16]\n"
    "    stp x23, x24, [sp, #32]\n"
    "    stp x25, x26, [sp, #48]\n"
    "    stp x27, x28, [sp, #64]\n"
    "    stp x29, x30, [sp, #80]\n"
    "    stp d8, d9, [sp, #96]\n"
    "    stp d10, d11, [sp, #112]\n"
    "    stp d12, d13, [sp, #128]\n"
    "    stp d14, d15, [sp, #144]\n"
    "    mov x2, sp\n"
    "    str x2, [x0]\n"
    "    mov sp, x1\n"
    "    ldp x19, x20, [sp, #0]\n"
    "    ldp x21, x22, [sp, #16]\n"
    "    ldp x23, x24, [sp, #32]\n"
    "    ldp x25, x26, [sp, #48]\n"
    "    ldp x27, x28, [sp, #64]\n"
    "    ldp x29, x30, [sp, #80]\n"
    "    ldp d8, d9, [sp, #96]\n"
    "    ldp d10, d11, [sp, #112]\n"
    "    ldp d12, d13, [sp, #128]\n"
    "    ldp d14, d15, [sp, #144]\n"
    "    add sp, sp, #160\n"
    "    ret\n"
    ".p2align 2\n"
    VX_SYM("vx_task_boot") ":\n"
    "    mov x0, x19\n"
    "    bl " VX_SYM("vx_task_main") "\n"
    "    brk #0\n");
#define VX_FRAME 160
#define VX_FRAME_TASK 0   // x19
#define VX_FRAME_RET 88   // x30
#elif defined(__x86_64__) && defined(_WIN32)
// The Windows x64 convention also keeps rdi, rsi and xmm6 to xmm15, and
// the thread information block holds the bounds of the running stack:
// its base, its limit and where its memory starts.
__asm__(
    ".text\n"
    ".globl " VX_SYM("vx_ctx_switch") "\n"
    VX_SYM("vx_ctx_switch") ":\n"
    "    pushq %rbp\n"
    "    pushq %rbx\n"
    "    pushq %rdi\n"
    "    pushq %rsi\n"
    "    pushq %r12\n"
    "    pushq %r13\n"
    "    pushq %r14\n"
    "    pushq %r15\n"
    "    pushq %gs:0x08\n"
    "    pushq %gs:0x10\n"
    "    pushq %gs:0x1478\n"
    "    subq $160, %rsp\n"
    "    movaps %xmm6, 0(%rsp)\n"
    "    movaps %xmm7, 16(%rsp)\n"
    "    movaps %xmm8, 32(%rsp)\n"
    "    movaps %xmm9, 48(%rsp)\n"
    "    movaps %xmm10, 64(%rsp)\n"
    "    movaps %xmm11, 80(%rsp)\n"
    "    movaps %xmm12, 96(%rsp)\n"
    "    movaps %xmm13, 112(%rsp)\n"
    "    movaps %xmm14, 128(%rsp)\n"
    "    movaps %xmm15, 144(%rsp)\n"
    "    movq %rsp, (%rcx)\n"
    "    movq %rdx, %rsp\n"
    "    movaps 0(%rsp), %xmm6\n"
    "    movaps 16(%rsp), %xmm7\n"
    "    movaps 32(%rsp), %xmm8\n"
    "    movaps 48(%rsp), %xmm9\n"
    "    movaps 64(%rsp), %xmm10\n"
    "    movaps 80(%rsp), %xmm11\n"
    "    movaps 96(%rsp), %xmm12\n"
    "    movaps 112(%rsp), %xmm13\n"
    "    movaps 128(%rsp), %xmm14\n"
    "    movaps 144(%rsp), %xmm15\n"
    "    addq $160, %rsp\n"
    "    popq %gs:0x1478\n"
    "    popq %gs:0x10\n"
    "    popq %gs:0x08\n"
    "    popq %r15\n"
    "    popq %r14\n"
    "    popq %r13\n"
    "    popq %r12\n"
    "    popq %rsi\n"
    "    popq %rdi\n"
    "    popq %rbx\n"
    "    popq %rbp\n"
    "    ret\n"
    VX_SYM("vx_task_boot") ":\n"
    "    movq %r12, %rcx\n"
    "    subq $32, %rsp\n"   // the space a callee may use for its arguments
    "    call " VX_SYM("vx_task_main") "\n"
    "    ud2\n");
#define VX_FRAME 256      // ten xmm registers, the stack bounds, eight registers and the return address
#define VX_FRAME_TIB 160  // where the stack's memory starts, its limit and its base
#define VX_FRAME_TASK 208 // r12
#define VX_FRAME_RET 248
#elif defined(__x86_64__)
__asm__(
    ".text\n"
    ".globl " VX_SYM("vx_ctx_switch") "\n"
    VX_SYM("vx_ctx_switch") ":\n"
    "    pushq %rbp\n"
    "    pushq %rbx\n"
    "    pushq %r12\n"
    "    pushq %r13\n"
    "    pushq %r14\n"
    "    pushq %r15\n"
    "    movq %rsp, (%rdi)\n"
    "    movq %rsi, %rsp\n"
    "    popq %r15\n"
    "    popq %r14\n"
    "    popq %r13\n"
    "    popq %r12\n"
    "    popq %rbx\n"
    "    popq %rbp\n"
    "    ret\n"
    VX_SYM("vx_task_boot") ":\n"
    "    movq %r12, %rdi\n"
    "    call " VX_SYM("vx_task_main") "\n"
    "    ud2\n");
#define VX_FRAME 56       // six registers and the return address
#define VX_FRAME_TASK 24  // r12
#define VX_FRAME_RET 48
#else
#error "async functions need an arm64 or x86-64 CPU"
#endif

void vx_task_boot(void);

#ifndef __has_feature
#define __has_feature(x) 0
#endif
#if defined(__SANITIZE_ADDRESS__) || __has_feature(address_sanitizer)
#define VX_ASAN 1
// Tasks switch stacks, which fake stacks don't follow, and the collector
// owns the heap, so leak reports would only list its memory.
const char *__asan_default_options(void) { return "detect_stack_use_after_return=0:detect_leaks=0"; }
void __sanitizer_start_switch_fiber(void **fake_stack_save, const void *bottom, size_t size);
void __sanitizer_finish_switch_fiber(void *fake_stack_save, const void **bottom_old, size_t *size_old);
void __asan_unpoison_memory_region(const volatile void *addr, size_t size);
#endif

#define VX_TASK_STACK ((size_t)256 << 10)

#ifdef _WIN32
// Returns a new task stack of [size] bytes, starting with VX_STACK_GUARD,
// or NULL.
static char *vx_stack_new(size_t size) {
    char *p = VirtualAlloc(NULL, size, MEM_RESERVE | MEM_COMMIT, PAGE_READWRITE);
    DWORD old;
    if (p) VirtualProtect(p + VX_STACK_GUARD - VX_GUARD_PAGE, VX_GUARD_PAGE, PAGE_READWRITE | PAGE_GUARD, &old);
    return p;
}

static void vx_stack_free(char *p, size_t size) {
    (void)size;
    VirtualFree(p, 0, MEM_RELEASE);
}
#else
// Returns a new task stack of [size] bytes, starting with a guard page,
// or NULL.
static char *vx_stack_new(size_t size) {
    char *p = mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANON, -1, 0);
    if (p == MAP_FAILED) return NULL;
    mprotect(p, VX_STACK_GUARD, PROT_NONE);
    return p;
}

static void vx_stack_free(char *p, size_t size) { munmap(p, size); }
#endif

// Stacks of finished tasks, kept for new ones.
static VX_TLS char *vx_spare_stacks[64];
static VX_TLS int vx_spare_len;

// Switches from the running task to [to], which runs until it switches
// back. [ending] is set when the running task has finished.
static void vx_switch(vx_task *to, bool ending) {
    vx_task *from = vx_cur;
    vx_cur = to;
#ifdef VX_TRACES
    from->frames = vx_top;
    vx_top = to->frames;
#endif
    vx_stack_base = to->top;
#ifdef VX_ASAN
    __sanitizer_start_switch_fiber(ending ? NULL : &from->asan_fake, to->asan_bottom, to->asan_size);
#else
    (void)ending;
#endif
    vx_ctx_switch(&from->sp, to->sp);
#ifdef VX_ASAN
    __sanitizer_finish_switch_fiber(from->asan_fake, NULL, NULL);
#endif
}

static void vx_task_free(vx_task *t) {
    for (size_t i = 0; i < vx_tasks_len; i++) {
        if (vx_tasks[i] == t) {
            vx_tasks[i] = vx_tasks[--vx_tasks_len];
            break;
        }
    }
    // Its children now belong to its own parent.
    for (size_t i = 0; i < vx_tasks_len; i++) {
        if (vx_tasks[i]->parent == t) vx_tasks[i]->parent = t->parent;
    }
    if (vx_spare_len < 64) {
#ifdef VX_ASAN
        // Frames that ended without returning leave their redzones behind.
        __asan_unpoison_memory_region(t->asan_bottom, t->asan_size);
#endif
        vx_spare_stacks[vx_spare_len++] = t->stack;
    } else {
        vx_stack_free(t->stack, t->size);
    }
    free(t);
}

// Runs task [t] until it waits or finishes, then returns.
static void vx_resume(vx_task *t) {
    vx_task *self = vx_cur;
    t->caller = self;
    vx_switch(t, false);
    vx_cur = self;
    vx_stack_base = self->top;
    if (t->done) vx_task_free(t);
}

// Returns from the running task to the one that resumed it, until it is
// resumed again.
static void vx_suspend(void) {
    vx_switch(vx_cur->caller, false);
}

__attribute__((noreturn)) static void vx_task_end(vx_task *t);

void vx_task_main(vx_task *t) {
#ifdef VX_ASAN
    const void *bottom;
    size_t size;
    __sanitizer_finish_switch_fiber(NULL, &bottom, &size);
    // The first task started tells the bounds of the program's own stack.
    if (vx_main_task.asan_size == 0 && t->caller == &vx_main_task) {
        vx_main_task.asan_bottom = bottom;
        vx_main_task.asan_size = size;
    }
#endif
    t->entry(t->arg);
    vx_task_end(t);
}

static void vx_future_own(struct vx_future *f, vx_task *t);

// Returns a task that will run [entry] with [arg] when first resumed.
static vx_task *vx_task_make(void (*entry)(void *), void *arg) {
    vx_task *t = calloc(1, sizeof(vx_task));
    if (!t) vx_out_of_memory();
    t->size = VX_TASK_STACK;
    if (vx_spare_len > 0) {
        t->stack = vx_spare_stacks[--vx_spare_len];
    } else {
        t->stack = vx_stack_new(t->size);
        if (!t->stack) vx_out_of_memory();
    }
    t->top = (char *)(((uintptr_t)(t->stack + t->size)) & ~(uintptr_t)15);
    t->asan_bottom = t->stack + VX_STACK_GUARD;
    t->asan_size = t->size - VX_STACK_GUARD;
    t->entry = entry;
    t->arg = arg;
#if defined(__x86_64__)
    char *sp = t->top - 16 - VX_FRAME;   // 8 mod 16, so vx_task_boot calls with an aligned stack
#else
    char *sp = t->top - VX_FRAME;
#endif
    memset(sp, 0, VX_FRAME);
    *(void **)(sp + VX_FRAME_TASK) = t;
    *(void **)(sp + VX_FRAME_RET) = (void *)vx_task_boot;
#ifdef VX_FRAME_TIB
    void **tib = (void **)(sp + VX_FRAME_TIB);
    tib[0] = t->stack;
    tib[1] = t->stack + VX_STACK_GUARD;
    tib[2] = t->stack + t->size;
#endif
    t->sp = sp;
    return t;
}

// Starts a task that runs [entry] with [arg] and settles [f], which may be
// NULL, and runs it until it first waits. It is a child of the running
// task.
static void vx_go_f(void (*entry)(void *), void *arg, struct vx_future *f) {
    vx_task *t = vx_task_make(entry, arg);
    t->parent = vx_cur;
#ifdef VX_TRACES
    if (vx_top) {
        t->origin = vx_top->name;
        t->origin_line = vx_top->line;
    }
#endif
    t->future = f;
    if (f) vx_future_own(f, t);
    if (vx_tasks_len == vx_tasks_cap) vx_tasks = vx_grow(vx_tasks, &vx_tasks_cap, sizeof(vx_task *));
    vx_tasks[vx_tasks_len++] = t;
    vx_resume(t);
}

static void vx_go(void (*entry)(void *), void *arg) { vx_go_f(entry, arg, NULL); }

// A future: the result of async work, filled in once it is done. The
// value of [size] bytes follows the header.
typedef struct vx_future {
    int32_t state;            // 0 while pending, 1 when done, 2 when it failed, 3 when cancelled
    vx_task *waiters;
    vx_task *task;            // the task that settles it, while it runs
    void *err;
    int64_t size;
    _Alignas(16) char value[];
} vx_future;

static void vx_future_own(struct vx_future *f, vx_task *t) { f->task = t; }

// Returns a pending future of a value of [size] bytes laid out as [mask].
static vx_future *vx_future_new(int64_t size, uint64_t mask) {
    size_t n = sizeof(vx_future) + (size_t)size;
    vx_future *f = vx_alloc_obj(n, n > 512 ? ~(uint64_t)0 : VX_FIELD(vx_future, err, 1) | VX_SH(mask, offsetof(vx_future, value)));
    f->size = size;
    return f;
}

// Tasks ready to run, in order.
static VX_TLS vx_task *vx_ready_head, *vx_ready_tail;

static void vx_ready(vx_task *t) {
    t->next = NULL;
    if (vx_ready_tail) {
        vx_ready_tail->next = t;
    } else {
        vx_ready_head = t;
    }
    vx_ready_tail = t;
}

static void vx_future_settle(vx_future *f, int32_t state) {
    if (f->state != 0) return;
    f->state = state;
    vx_task *w = f->waiters;
    f->waiters = NULL;
    // Waiters run in the order they started waiting.
    vx_task *rev = NULL;
    while (w) {
        vx_task *n = w->next;
        w->next = rev;
        rev = w;
        w = n;
    }
    while (rev) {
        vx_task *n = rev->next;
        rev->waiting = NULL;
        vx_ready(rev);
        rev = n;
    }
}

// Completes [f] with the [f->size] bytes at [value].
static void vx_future_done(vx_future *f, const void *value) {
    if (f->state != 0) return;
    if (f->size > 0) memcpy(f->value, value, (size_t)f->size);
    vx_remember(f);
    vx_future_settle(f, 1);
}

static void vx_future_fail(vx_future *f, void *err) {
    if (f->state != 0) return;
    f->err = err;
    vx_remember(f);
    vx_future_settle(f, 2);
}

static void vx_remote_remove(vx_future *f);
static void vx_timer_remove(vx_future *f);

// Cancels task [t] and the tasks it started: its future is cancelled now,
// and the task stops at its next await. A waiting task wakes to stop.
static void vx_task_cancel(vx_task *t) {
    if (t->cancelled || t->done) return;
    t->cancelled = true;
    if (t->future) vx_future_settle(t->future, 3);
    if (t->waiting) {
        vx_task **p = &t->waiting->waiters;
        while (*p && *p != t) p = &(*p)->next;
        if (*p) *p = t->next;
        t->waiting = NULL;
        vx_ready(t);
    } else if (t->parked) {
        *t->parked = NULL;
        t->parked = NULL;
        vx_ready(t);
    }
    for (size_t i = 0; i < vx_tasks_len; i++) {
        if (vx_tasks[i]->parent == t) vx_task_cancel(vx_tasks[i]);
    }
}

// Cancels [f]: the task settling it stops, and so does every task waiting
// for it. Settled futures don't change.
static void vx_cancel(vx_future *f) {
    if (f->state != 0) return;
    if (f->task) {
        vx_task_cancel(f->task);
        return;
    }
    vx_remote_remove(f);
    vx_timer_remove(f);
    vx_future_settle(f, 3);
}

// Finishes the running task [t], which never runs again.
__attribute__((noreturn))
static void vx_task_end(vx_task *t) {
    if (t->future) t->future->task = NULL;
    t->done = true;
    vx_switch(t->caller, true);
    __builtin_unreachable();
}

// Stops the running task, abandoning its stack, after cancelling the
// tasks it started.
__attribute__((noreturn))
static void vx_stop(void) {
    vx_task *t = vx_cur;
    t->cancelled = false;
    vx_task_cancel(t);
    vx_task_end(t);
}

// Waits until [f] is settled in any way, letting other tasks run
// meanwhile. A cancelled task stops here.
static void vx_wait_settled(vx_future *f) {
    for (;;) {
        if (vx_cur->cancelled) vx_stop();
        if (f->state != 0) return;
        if (vx_cur == &vx_main_task) {
            fflush(stdout);
            fputs("error: await outside a task\n", stderr);
            exit(1);
        }
        vx_cur->waiting = f;
        vx_cur->next = f->waiters;
        f->waiters = vx_cur;
        vx_suspend();
    }
}

// Waits until [f] is settled, letting other tasks run meanwhile, and
// returns its error, or NULL when it is done. A cancelled task stops here,
// and so does one whose future was cancelled.
static void *vx_await(vx_future *f) {
    vx_wait_settled(f);
    if (f->state == 3) {
        if (vx_cur == &vx_main_task) {
            fflush(stdout);
            fputs("error: awaited a cancelled task\n", stderr);
            exit(1);
        }
        vx_stop();
    }
    return f->state == 2 ? f->err : NULL;
}

// Timers waiting to complete their futures: a min-heap on when each is
// due, with those due together in the order they were set.
typedef struct {
    int64_t at;
    uint64_t seq;
    vx_future *future;
} vx_timer;

static VX_TLS vx_timer *vx_timers;
static VX_TLS size_t vx_timers_len, vx_timers_cap;
static VX_TLS uint64_t vx_timer_seq;

static bool vx_timer_before(const vx_timer *a, const vx_timer *b) {
    return a->at < b->at || (a->at == b->at && a->seq < b->seq);
}

static void vx_timer_push(vx_timer t) {
    if (vx_timers_len == vx_timers_cap) vx_timers = vx_grow(vx_timers, &vx_timers_cap, sizeof(vx_timer));
    size_t i = vx_timers_len++;
    while (i > 0 && vx_timer_before(&t, &vx_timers[(i - 1) / 2])) {
        vx_timers[i] = vx_timers[(i - 1) / 2];
        i = (i - 1) / 2;
    }
    vx_timers[i] = t;
}

// Takes the timer of [f] off the heap, if it has one.
static void vx_timer_remove(vx_future *f) {
    for (size_t i = 0; i < vx_timers_len; i++) {
        if (vx_timers[i].future != f) continue;
        size_t n = vx_timers_len;
        vx_timer *old = malloc(n * sizeof(vx_timer));
        if (!old) vx_out_of_memory();
        memcpy(old, vx_timers, n * sizeof(vx_timer));
        vx_timers_len = 0;
        for (size_t j = 0; j < n; j++) {
            if (j != i) vx_timer_push(old[j]);
        }
        free(old);
        return;
    }
}

static vx_timer vx_timer_pop(void) {
    vx_timer top = vx_timers[0], last = vx_timers[--vx_timers_len];
    size_t i = 0, n = vx_timers_len;
    for (;;) {
        size_t c = 2 * i + 1;
        if (c >= n) break;
        if (c + 1 < n && vx_timer_before(&vx_timers[c + 1], &vx_timers[c])) c++;
        if (!vx_timer_before(&vx_timers[c], &last)) break;
        vx_timers[i] = vx_timers[c];
        i = c;
    }
    if (n > 0) vx_timers[i] = last;
    return top;
}

// Futures waiting for a message from another isolate. They are kept
// alive until it arrives.
static VX_TLS vx_future **vx_remote;
static VX_TLS size_t vx_remote_len, vx_remote_cap;

static void vx_remote_add(vx_future *f) {
    if (vx_remote_len == vx_remote_cap) vx_remote = vx_grow(vx_remote, &vx_remote_cap, sizeof(vx_future *));
    vx_remote[vx_remote_len++] = f;
}

static void vx_remote_remove(vx_future *f) {
    for (size_t i = 0; i < vx_remote_len; i++) {
        if (vx_remote[i] == f) {
            vx_remote[i] = vx_remote[--vx_remote_len];
            return;
        }
    }
}

// --- Generators ---

// The iterator of a generator. Its body runs as a task from the first
// call of next, and pauses at each yield. Unlike other tasks, a paused
// generator isn't a root: its stack is scanned only while the iterator
// is reachable, and the collector frees it once the iterator isn't.
typedef struct vx_gen {
    const void *vx_t;         // its descriptor, as an Iterator<T>
    void *src;                // the Iterable that made it, which holds the arguments
    void (*entry)(void *);
    vx_task *task;            // NULL before the first next and after the body ends
    void *out;                // where yield stores the value, while next runs
    size_t slot;              // its place in vx_gens, while it has a task
    int line;                 // the generator function's, for errors
    bool done, running, scanned;
} vx_gen;

static VX_TLS vx_gen **vx_gens;
static VX_TLS size_t vx_gens_len, vx_gens_cap;

// Stacks cost far more than the iterators that hold them, so a collection
// also runs once the generators with stacks have doubled since the last.
static VX_TLS size_t vx_gens_next = 64;

static vx_gen *vx_gen_new(void *src, void (*entry)(void *), const void *desc, int line) {
    vx_gen *g = vx_alloc_obj(sizeof(vx_gen), VX_FIELD(vx_gen, src, 1) | VX_FIELD(vx_gen, out, 1));
    g->vx_t = desc;
    g->src = src;
    g->entry = entry;
    g->line = line;
    return g;
}

static void vx_gen_forget(vx_gen *g) {
    vx_gen *last = vx_gens[--vx_gens_len];
    vx_gens[g->slot] = last;
    last->slot = g->slot;
    g->task = NULL;
}

static void vx_gen_main(void *p) {
    vx_gen *g = p;
    g->entry(g);
    g->done = true;
    vx_gen_forget(g);
}

// Runs the body of [g] until it yields a value into [out], returning true,
// or ends, returning false.
static bool vx_gen_next(vx_gen *g, void *out) {
    if (g->done) return false;
    if (g->running) vx_fail(g->line, "a generator can't ask itself for its next value");
    if (!g->task) {
        if (vx_gens_len >= vx_gens_next) {
            vx_collect();
            vx_gens_next = vx_gens_len * 2 > 64 ? vx_gens_len * 2 : 64;
        }
        g->task = vx_task_make(vx_gen_main, g);
        g->task->gen = true;
        if (vx_gens_len == vx_gens_cap) vx_gens = vx_grow(vx_gens, &vx_gens_cap, sizeof(vx_gen *));
        g->slot = vx_gens_len;
        vx_gens[vx_gens_len++] = g;
    }
    g->out = out;
    g->running = true;
    vx_resume(g->task);
    g->running = false;
    return !g->done;
}

// Gives the generator's caller the value that its body stored in out.
static void vx_gen_yield(void) { vx_suspend(); }

static bool vx_is_marked(const void *p) {
    char *start;
    size_t size;
    bool marked, atomic;
    return vx_object_at((uintptr_t)p, &start, &size, &marked, &atomic) && marked;
}

// Scans the stacks of the paused generators whose iterators are marked,
// and what they reach, until no more are found.
static void vx_gens_mark(void) {
    for (bool more = true; more;) {
        more = false;
        for (size_t i = 0; i < vx_gens_len; i++) {
            vx_gen *g = vx_gens[i];
            if (g->scanned || !vx_is_marked(g)) continue;
            g->scanned = more = true;
            if (g->task != vx_cur) vx_mark_range(g->task->sp, g->task->top);
            vx_mark_gray();
        }
    }
}

// Frees the stacks of the generators whose iterators are garbage.
static void vx_gens_sweep(void) {
    for (size_t i = vx_gens_len; i-- > 0;) {
        vx_gen *g = vx_gens[i];
        if (g->scanned) {
            g->scanned = false;
            continue;
        }
        vx_task *t = g->task;
        vx_gen_forget(g);
        vx_task_free(t);
    }
}

static void vx_async_mark(void) {
    for (size_t i = 0; i < vx_timers_len; i++) vx_mark_range((const char *)&vx_timers[i].future, (const char *)(&vx_timers[i].future + 1));
    if (vx_remote) vx_mark_range((const char *)vx_remote, (const char *)(vx_remote + vx_remote_len));
}

// Returns a future that completes after [ms] milliseconds.
static vx_future *vx_sleep(int64_t ms) {
    vx_future *f = vx_future_new(0, 0);
    // A wait too long to count in nanoseconds waits as long as it can.
    int64_t now = vx_time_ns(), at;
    if (ms <= 0) at = now;
    else if (ms > (INT64_MAX - now) / 1000000) at = INT64_MAX;
    else at = now + ms * 1000000;
    vx_timer_push((vx_timer){at, vx_timer_seq++, f});
    return f;
}

static bool vx_drain(void);

// Runs every task that is ready, and completes the timers that are due,
// until nothing more can run now. Returns the milliseconds until the next
// timer, or -1 when nothing is pending. Platform code with its own run
// loop calls it as vex_poll.
static int64_t vx_poll(void) {
    if (vx_cur != &vx_main_task) return 0;
    for (;;) {
        vx_drain();
        int64_t now = vx_time_ns();
        while (vx_timers_len > 0 && vx_timers[0].at <= now) vx_future_settle(vx_timer_pop().future, 1);
        if (!vx_ready_head) break;
        vx_task *t = vx_ready_head;
        vx_ready_head = t->next;
        if (!vx_ready_head) vx_ready_tail = NULL;
        vx_resume(t);
    }
    // While only other isolates can wake it, ask to be polled again soon.
    if (vx_timers_len == 0) return vx_remote_len > 0 ? 10 : -1;
    int64_t wait = vx_timers[0].at - vx_time_ns();
    return wait <= 0 ? 0 : wait / 1000000 + (wait % 1000000 != 0);
}

int64_t vex_poll(void) { return vx_poll(); }

static void vx_idle(void);

// Runs tasks and timers until nothing is pending. Once no task is alive,
// timers left behind, such as those of cancelled tasks, can wake nothing.
static void vx_loop(void) {
    while (vx_poll() >= 0 && (vx_tasks_len > 0 || vx_remote_len > 0)) vx_idle();
}

// Runs tasks and timers until [f] is settled, for `run` in code that isn't
// async, and returns its error, or NULL.
static void *vx_run(vx_future *f, int line) {
    if (vx_cur != &vx_main_task) vx_fail(line, "run can't be used inside async code; use await");
    while (f->state == 0) {
        if (vx_poll() < 0 && f->state == 0) vx_fail(line, "run: the future can never complete, since nothing is left to run");
        if (f->state == 0) vx_idle();
    }
    if (f->state == 3) vx_fail(line, "run: the future was cancelled");
    return f->state == 2 ? f->err : NULL;
}

// --- Streams ---
//
// A stream hands values to one reader in the same isolate. A controller
// adds them whenever it likes. A stream function runs as a task, started
// by the first read, that pauses at each yield until its value is taken.

typedef struct {
    vx_list *queue;          // values not read yet
    bool closed;             // set once no more values will come
    void (*entry)(void *);   // the body of a stream function, or NULL
    void *arg;
    bool started;            // set once the body's task was started
    struct vx_future *done;  // settled when the body's task ends
    vx_task *reader;         // the task waiting for a value
    vx_task *producer;       // the body's task, while it waits at a yield
} vx_stream;

// Returns a stream of values of [esz] bytes laid out as [mask].
static vx_stream *vx_stream_new(int64_t esz, uint64_t mask) {
    vx_stream *s = vx_alloc_obj(sizeof(vx_stream), VX_FIELD(vx_stream, queue, 1) | VX_FIELD(vx_stream, arg, 1) | VX_FIELD(vx_stream, done, 1));
    s->queue = vx_list_new(esz, mask, 0, NULL);
    vx_remember(s);
    return s;
}

// Makes [s] the stream of a stream function, whose body [entry] runs with
// [arg] once the stream is first read.
static void vx_stream_start(vx_stream *s, void (*entry)(void *), void *arg) {
    s->entry = entry;
    s->arg = arg;
    s->done = vx_future_new(0, 0);
    vx_remember(s);
}

// Keeps the running task in [slot] until another wakes it.
static void vx_stream_park(vx_task **slot) {
    *slot = vx_cur;
    vx_cur->parked = slot;
    vx_suspend();
}

// Wakes the task kept in [slot], if any.
static void vx_stream_wake(vx_task **slot) {
    vx_task *t = *slot;
    if (!t) return;
    *slot = NULL;
    t->parked = NULL;
    vx_ready(t);
}

// Adds [value] for the reader of [s], the stream of a controller.
static void vx_stream_add(vx_stream *s, const void *value, int line) {
    if (s->closed) vx_fail(line, "add() on a closed StreamController");
    vx_list_push(s->queue, value);
    vx_stream_wake(&s->reader);
}

// Ends [s] once the values already added are read.
static void vx_stream_close(vx_stream *s) {
    s->closed = true;
    vx_stream_wake(&s->reader);
}

// Gives [value] from the body of a stream function, and waits until the
// reader has taken it.
static void vx_stream_yield(vx_stream *s, const void *value) {
    vx_list_push(s->queue, value);
    vx_stream_wake(&s->reader);
    while (s->queue->len > 0) {
        if (vx_cur->cancelled) vx_stop();
        vx_stream_park(&s->producer);
    }
    if (vx_cur->cancelled) vx_stop();
}

// Takes the next value of [s] into [out], waiting for it. Returns false
// once the stream has ended.
static bool vx_stream_next(vx_stream *s, void *out, int line) {
    for (;;) {
        if (vx_cur->cancelled) vx_stop();
        if (s->queue->len > 0) {
            int64_t esz = s->queue->esz;
            memcpy(out, s->queue->data, (size_t)esz);
            memmove(s->queue->data, s->queue->data + esz, (size_t)((s->queue->len - 1) * esz));
            s->queue->len--;
            vx_stream_wake(&s->producer);
            return true;
        }
        if (s->closed) return false;
        if (s->entry && !s->started) {
            s->started = true;
            vx_go_f(s->entry, s->arg, s->done);
            continue;
        }
        if (vx_cur == &vx_main_task) {
            fflush(stdout);
            fputs("error: for await outside a task\n", stderr);
            exit(1);
        }
        if (s->reader) vx_fail(line, "two loops are reading the same stream; a stream has one reader");
        vx_stream_park(&s->reader);
    }
}

// Stops reading [s] when a for await loop ends: a stream function that
// is still running is cancelled.
static void vx_stream_stop(vx_stream *s) {
    if (!s->entry || s->closed) return;
    s->closed = true;
    s->started = true;
    vx_cancel(s->done);
}

// The work of [vx_first] for one of its futures.
typedef struct {
    vx_future *out;
    vx_future *in;
} vx_first_arg;

static void vx_first_task(void *p) {
    vx_first_arg *a = p;
    vx_wait_settled(a->in);
    vx_future_settle(a->out, 1);
}

// Returns a future that is done once [x] or [y] is settled in any way.
static vx_future *vx_first(vx_future *x, vx_future *y) {
    vx_future *out = vx_future_new(0, 0);
    vx_future *in[2] = {x, y};
    for (int i = 0; i < 2 && out->state == 0; i++) {
        vx_first_arg *a = vx_alloc_obj(sizeof(vx_first_arg), VX_ONES(sizeof(vx_first_arg)));
        *a = (vx_first_arg){out, in[i]};
        vx_remember(a);
        vx_go(vx_first_task, a);
    }
    return out;
}

// The work of `all`: waits for each future in turn and collects their
// values, failing at the first error.
typedef struct {
    vx_future *result;
    vx_list *futures;
    int64_t esz;
    uint64_t mask;
} vx_all_arg;

static void vx_all_task(void *p) {
    vx_all_arg *a = p;
    vx_list *values = a->esz > 0 ? vx_list_new(a->esz, a->mask, 0, NULL) : NULL;
    for (int64_t i = 0; i < a->futures->len; i++) {
        vx_future *f = ((vx_future **)a->futures->data)[i];
        void *err = vx_await(f);
        if (err) {
            vx_future_fail(a->result, err);
            return;
        }
        if (values) vx_list_push(values, f->value);
    }
    vx_future_done(a->result, &values);
}

static vx_future *vx_all(vx_list *futures, int64_t esz, uint64_t mask) {
    vx_all_arg *a = vx_alloc_obj(sizeof(vx_all_arg), VX_FIELD(vx_all_arg, result, 1) | VX_FIELD(vx_all_arg, futures, 1));
    *a = (vx_all_arg){vx_future_new(esz > 0 ? (int64_t)sizeof(vx_list *) : 0, 1), futures, esz, mask};
    vx_remember(a);
    vx_go_f(vx_all_task, a, a->result);
    return a->result;
}

// The work of `any` for one of its futures: settles the shared result with
// that future's outcome, unless another settled it first.
typedef struct {
    vx_future *result;
    vx_future *future;
} vx_any_arg;

static void vx_any_task(void *p) {
    vx_any_arg *a = p;
    void *err = vx_await(a->future);
    if (a->result->state != 0) return;
    if (err) {
        vx_future_fail(a->result, err);
    } else {
        vx_future_done(a->result, a->future->value);
    }
}

static vx_future *vx_any(vx_list *futures, int64_t esz, uint64_t mask, int line) {
    // With no futures, nothing could ever settle the result.
    if (futures->len == 0) vx_fail(line, "any() needs at least one future");
    vx_future *result = vx_future_new(esz, mask);
    for (int64_t i = 0; i < futures->len && result->state == 0; i++) {
        vx_any_arg *a = vx_alloc_obj(sizeof(vx_any_arg), VX_ONES(sizeof(vx_any_arg)));
        *a = (vx_any_arg){result, ((vx_future **)futures->data)[i]};
        vx_remember(a);
        vx_go(vx_any_task, a);
    }
    return result;
}

// --- Threads ---
//
// The locks and condition variables of isolates and channels: pthreads,
// or their Win32 counterparts.

#ifdef _WIN32
typedef SRWLOCK vx_mutex;
typedef CONDITION_VARIABLE vx_cond;
#define vx_mutex_init(m) InitializeSRWLock(m)
#define vx_mutex_destroy(m) ((void)(m))
#define vx_mutex_lock(m) AcquireSRWLockExclusive(m)
#define vx_mutex_unlock(m) ReleaseSRWLockExclusive(m)
#define vx_cond_init(c) InitializeConditionVariable(c)
#define vx_cond_destroy(c) ((void)(c))
#define vx_cond_wait(c, m) SleepConditionVariableSRW(c, m, INFINITE, 0)
#define vx_cond_signal(c) WakeConditionVariable(c)
#define vx_cond_broadcast(c) WakeAllConditionVariable(c)

// Waits on [c] for at most [ns] nanoseconds, up to a day.
static void vx_cond_wait_ns(vx_cond *c, vx_mutex *m, int64_t ns) {
    SleepConditionVariableSRW(c, m, (DWORD)((ns + 999999) / 1000000), 0);
}
#else
typedef pthread_mutex_t vx_mutex;
typedef pthread_cond_t vx_cond;
#define vx_mutex_init(m) pthread_mutex_init(m, NULL)
#define vx_mutex_destroy(m) pthread_mutex_destroy(m)
#define vx_mutex_lock(m) pthread_mutex_lock(m)
#define vx_mutex_unlock(m) pthread_mutex_unlock(m)
#define vx_cond_init(c) pthread_cond_init(c, NULL)
#define vx_cond_destroy(c) pthread_cond_destroy(c)
#define vx_cond_wait(c, m) pthread_cond_wait(c, m)
#define vx_cond_signal(c) pthread_cond_signal(c)
#define vx_cond_broadcast(c) pthread_cond_broadcast(c)

// Waits on [c] for at most [ns] nanoseconds, up to a day.
static void vx_cond_wait_ns(vx_cond *c, vx_mutex *m, int64_t ns) {
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    ns += (int64_t)ts.tv_nsec;
    ts.tv_sec += (time_t)(ns / 1000000000);
    ts.tv_nsec = (long)(ns % 1000000000);
    pthread_cond_timedwait(c, m, &ts);
}
#endif

// --- Isolates ---
//
// An isolate is a thread with its own heap, collector and event loop, so
// none of them need locks. Values pass between isolates as messages: the
// sender encodes them into bytes, and the receiver decodes them into its
// own heap.

// A message being encoded. Objects sent before in the same message are
// written as back references, so sharing and cycles survive the copy.
typedef struct {
    char *buf;
    size_t len, cap;
    struct { const void *p; int64_t id; } *seen;
    size_t seen_len, seen_cap;
} vx_enc;

static void vx_enc_put(vx_enc *e, const void *p, size_t n) {
    if (e->len + n > e->cap) {
        e->cap = e->cap * 2 > e->len + n ? e->cap * 2 : e->len + n + 64;
        e->buf = realloc(e->buf, e->cap);
        if (!e->buf) vx_out_of_memory();
    }
    memcpy(e->buf + e->len, p, n);
    e->len += n;
}

static void vx_enc_i64(vx_enc *e, int64_t v) { vx_enc_put(e, &v, 8); }

static void vx_enc_str(vx_enc *e, vx_str s) {
    vx_enc_i64(e, s.len);
    vx_enc_put(e, s.ptr, (size_t)s.len);
}

// Writes a reference to object [p] and returns true when it was sent
// before; otherwise writes 0, remembers it, and returns false, so the
// caller writes its contents.
static bool vx_enc_ref(vx_enc *e, const void *p) {
    if ((e->seen_len + 1) * 2 > e->seen_cap) {
        size_t old_cap = e->seen_cap;
        void *old = e->seen;
        e->seen_cap = old_cap ? old_cap * 2 : 64;
        e->seen = calloc(e->seen_cap, sizeof(*e->seen));
        if (!e->seen) vx_out_of_memory();
        for (size_t i = 0; i < old_cap; i++) {
            const void *q = ((__typeof__(e->seen))old)[i].p;
            if (!q) continue;
            size_t j = (size_t)vx_mix((uint64_t)(uintptr_t)q) & (e->seen_cap - 1);
            while (e->seen[j].p) j = (j + 1) & (e->seen_cap - 1);
            e->seen[j] = ((__typeof__(e->seen))old)[i];
        }
        free(old);
    }
    size_t j = (size_t)vx_mix((uint64_t)(uintptr_t)p) & (e->seen_cap - 1);
    for (; e->seen[j].p; j = (j + 1) & (e->seen_cap - 1)) {
        if (e->seen[j].p == p) {
            vx_enc_i64(e, e->seen[j].id);
            return true;
        }
    }
    e->seen[j].p = p;
    e->seen[j].id = (int64_t)++e->seen_len;
    vx_enc_i64(e, 0);
    return false;
}

// Ends encoding and returns the message, which the caller frees.
static char *vx_enc_done(vx_enc *e) {
    free(e->seen);
    return e->buf ? e->buf : calloc(1, 1);
}

// A message being decoded. [objs] lists the objects made so far, for back
// references; it is in the collected heap, reachable from the stack.
typedef struct {
    const char *p;
    void **objs;
    int64_t len, cap;
} vx_dec;

static void vx_dec_get(vx_dec *d, void *out, size_t n) {
    memcpy(out, d->p, n);
    d->p += n;
}

static int64_t vx_dec_i64(vx_dec *d) {
    int64_t v;
    vx_dec_get(d, &v, 8);
    return v;
}

static vx_str vx_dec_str(vx_dec *d) {
    int64_t n = vx_dec_i64(d);
    vx_str s = vx_str_new(d->p, n);
    d->p += n;
    return s;
}

// Returns the object a back reference names, or NULL when a new object
// follows.
static void *vx_dec_ref(vx_dec *d) {
    int64_t id = vx_dec_i64(d);
    return id ? d->objs[id - 1] : NULL;
}

// Records the new object [o], before its contents are decoded.
static void vx_dec_obj(vx_dec *d, void *o) {
    if (d->len == d->cap) {
        int64_t cap = d->cap ? d->cap * 2 : 16;
        void **objs = vx_alloc((size_t)cap * sizeof(void *));
        if (d->len) memcpy(objs, d->objs, (size_t)d->len * sizeof(void *));
        d->objs = objs;
        d->cap = cap;
    }
    d->objs[d->len++] = o;
    vx_remember(d->objs);
}

// A message to an isolate: what completes future [f]. [fill] decodes
// [buf] into the future's value, or into its error when [failed]; with no
// [buf] the value is all zero, which is nil for an optional.
typedef struct vx_msg {
    struct vx_msg *next;
    vx_future *f;
    char *buf;
    void (*fill)(const char *, void *);
    bool failed;
} vx_msg;

typedef struct {
    vx_mutex mu;
    vx_cond cv;
    vx_msg *head, *tail;
} vx_inbox;

// The inbox of the running isolate.
static VX_TLS vx_inbox *vx_self;

static vx_inbox *vx_inbox_new(void) {
    vx_inbox *in = calloc(1, sizeof(vx_inbox));
    if (!in) vx_out_of_memory();
    vx_mutex_init(&in->mu);
    vx_cond_init(&in->cv);
    return in;
}

static void vx_inbox_init(void) { vx_self = vx_inbox_new(); }

static void vx_post(vx_inbox *to, vx_future *f, char *buf, void (*fill)(const char *, void *), bool failed) {
    vx_msg *m = malloc(sizeof(vx_msg));
    if (!m) vx_out_of_memory();
    *m = (vx_msg){NULL, f, buf, fill, failed};
    vx_mutex_lock(&to->mu);
    if (to->tail) {
        to->tail->next = m;
    } else {
        to->head = m;
    }
    to->tail = m;
    vx_cond_signal(&to->cv);
    vx_mutex_unlock(&to->mu);
}

// Completes the futures of the messages that have arrived. Returns
// whether there were any.
static bool vx_drain(void) {
    if (!vx_self) return false;
    vx_mutex_lock(&vx_self->mu);
    vx_msg *m = vx_self->head;
    vx_self->head = vx_self->tail = NULL;
    vx_mutex_unlock(&vx_self->mu);
    bool any = m != NULL;
    while (m) {
        vx_msg *next = m->next;
        if (m->failed) {
            void *err = NULL;
            m->fill(m->buf, &err);
            vx_future_fail(m->f, err);
        } else {
            if (m->buf && m->fill) {
                m->fill(m->buf, m->f->value);
            } else if (m->f->size > 0) {
                memset(m->f->value, 0, (size_t)m->f->size);
            }
            vx_future_settle(m->f, 1);
        }
        vx_remote_remove(m->f);
        free(m->buf);
        free(m);
        m = next;
    }
    return any;
}

// Waits until the next timer is due or a message arrives, after vx_poll
// found nothing to run.
static void vx_idle(void) {
    vx_mutex_lock(&vx_self->mu);
    if (!vx_self->head) {
        if (vx_timers_len > 0) {
            int64_t wait = vx_timers[0].at - vx_time_ns();
            // Very long waits wake once a day, which keeps the deadline
            // arithmetic in range.
            if (wait > 86400 * (int64_t)1000000000) wait = 86400 * (int64_t)1000000000;
            if (wait > 0) vx_cond_wait_ns(&vx_self->cv, &vx_self->mu, wait);
        } else if (vx_remote_len > 0) {
            vx_cond_wait(&vx_self->cv, &vx_self->mu);
        }
    }
    vx_mutex_unlock(&vx_self->mu);
}

// Frees the heap of the running thread when its isolate ends.
static void vx_heap_free(void) {
    for (size_t i = 0; i < vx_pages_len; i++) {
        vx_page_memory_free(vx_pages[i]->start);
        free(vx_pages[i]);
    }
    for (size_t i = 0; i < vx_larges_len; i++) free(vx_larges[i]);
    free(vx_pages);
    free(vx_page_table);
    free(vx_larges);
    free(vx_gray);
    free(vx_kept);
    free(vx_tasks);
    free(vx_timers);
    free(vx_remote);
    free(vx_repr_set);
    if (vx_h.mixed_len > 0) free(vx_h.mixed);
    for (int i = 0; i < vx_spare_len; i++) vx_stack_free(vx_spare_stacks[i], VX_TASK_STACK);
}

#define VX_ISOLATE_STACK ((size_t)8 << 20)

// What a new isolate runs: [entry] decodes its work from [buf] and does
// it, completing future [f] of isolate [parent] when there is one.
typedef void (*vx_isolate_entry)(vx_dec *, vx_inbox *, vx_future *);

typedef struct {
    vx_isolate_entry entry;
    char *buf;
    vx_inbox *parent;
    vx_future *f;
} vx_start;

__attribute__((noinline))
static void vx_isolate_body(vx_start *s) {
    vx_dec d = {s->buf, NULL, 0, 0};
    s->entry(&d, s->parent, s->f);
    vx_loop();
}

static void *vx_isolate_main(void *p) {
    vx_start s = *(vx_start *)p;
    free(p);
    char base;
    vx_gc_thread_init(&base);
#ifdef _WIN32
    vx_stack_reserve();
#else
    vx_stack_limit = VX_ISOLATE_STACK;
    stack_t old_alt;
    void *alt = vx_signal_stack(&old_alt);
#endif
    vx_self = vx_inbox_new();
    vx_isolate_body(&s);
    free(s.buf);
    vx_heap_free();
    vx_mutex_destroy(&vx_self->mu);
    vx_cond_destroy(&vx_self->cv);
    free(vx_self);
#ifndef _WIN32
    if (alt) {
        // Sanitizers free the stack they installed when the thread ends,
        // so theirs goes back first.
        sigaltstack(&old_alt, NULL);
        free(alt);
    }
#endif
    return NULL;
}

#ifdef _WIN32
static unsigned __stdcall vx_isolate_thread(void *p) {
    vx_isolate_main(p);
    return 0;
}
#endif

static void vx_isolate_start(vx_isolate_entry entry, vx_enc *e, vx_future *f) {
    vx_start *s = malloc(sizeof(vx_start));
    if (!s) vx_out_of_memory();
    *s = (vx_start){entry, vx_enc_done(e), vx_self, f};
#ifdef _WIN32
    HANDLE t = (HANDLE)_beginthreadex(NULL, (unsigned)VX_ISOLATE_STACK, vx_isolate_thread, s, STACK_SIZE_PARAM_IS_A_RESERVATION, NULL);
    bool started = t != NULL;
    if (t) CloseHandle(t);
#else
    pthread_attr_t attr;
    pthread_attr_init(&attr);
    pthread_attr_setstacksize(&attr, VX_ISOLATE_STACK);
    pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED);
    pthread_t t;
    bool started = pthread_create(&t, &attr, vx_isolate_main, s) == 0;
    pthread_attr_destroy(&attr);
#endif
    if (!started) {
        fflush(stdout);
        fputs("error: can't start another isolate: the system has no threads left\n", stderr);
        exit(1);
    }
}

// Starts an isolate for isolate.run, and returns the future of its result.
static vx_future *vx_isolate_run(vx_isolate_entry entry, vx_enc *e, int64_t size, uint64_t mask) {
    vx_future *f = vx_future_new(size, mask);
    vx_remote_add(f);
    vx_isolate_start(entry, e, f);
    return f;
}

// --- Channels ---
//
// A channel is shared by every isolate that has it, so it lives outside
// the collected heaps. Messages wait in it encoded.

typedef struct vx_queued {
    struct vx_queued *next;
    char *buf;
} vx_queued;

typedef struct vx_waiter {
    struct vx_waiter *next;
    vx_inbox *to;
    vx_future *f;
    void (*fill)(const char *, void *);
} vx_waiter;

typedef struct {
    vx_mutex mu;
    vx_cond cv;
    vx_queued *head, *tail;
    vx_waiter *waiters, *waiters_tail;
    bool closed;
} vx_chan;

static vx_chan *vx_chan_new(void) {
    vx_chan *c = calloc(1, sizeof(vx_chan));
    if (!c) vx_out_of_memory();
    vx_mutex_init(&c->mu);
    vx_cond_init(&c->cv);
    return c;
}

static void vx_chan_send(vx_chan *c, vx_enc *e, int line) {
    char *buf = vx_enc_done(e);
    vx_mutex_lock(&c->mu);
    if (c->closed) {
        vx_mutex_unlock(&c->mu);
        free(buf);
        vx_fail(line, "send on a closed channel");
    }
    vx_waiter *w = c->waiters;
    if (w) {
        c->waiters = w->next;
        if (!c->waiters) c->waiters_tail = NULL;
        vx_post(w->to, w->f, buf, w->fill, false);
        free(w);
    } else {
        vx_queued *q = malloc(sizeof(vx_queued));
        if (!q) vx_out_of_memory();
        *q = (vx_queued){NULL, buf};
        if (c->tail) {
            c->tail->next = q;
        } else {
            c->head = q;
        }
        c->tail = q;
        vx_cond_signal(&c->cv);
    }
    vx_mutex_unlock(&c->mu);
}

// Takes the oldest waiting message; the channel is locked.
static char *vx_chan_take(vx_chan *c) {
    vx_queued *q = c->head;
    c->head = q->next;
    if (!c->head) c->tail = NULL;
    char *buf = q->buf;
    free(q);
    return buf;
}

// Returns a future of the next message as an optional of [size] bytes,
// which [fill] decodes; it is nil once the channel is closed and empty.
static vx_future *vx_chan_receive(vx_chan *c, void (*fill)(const char *, void *), int64_t size, uint64_t mask) {
    vx_future *f = vx_future_new(size, mask);
    vx_mutex_lock(&c->mu);
    if (c->head) {
        char *buf = vx_chan_take(c);
        vx_mutex_unlock(&c->mu);
        fill(buf, f->value);
        free(buf);
        vx_future_settle(f, 1);
    } else if (c->closed) {
        vx_mutex_unlock(&c->mu);
        vx_future_settle(f, 1);
    } else {
        vx_waiter *w = malloc(sizeof(vx_waiter));
        if (!w) vx_out_of_memory();
        *w = (vx_waiter){NULL, vx_self, f, fill};
        if (c->waiters_tail) {
            c->waiters_tail->next = w;
        } else {
            c->waiters = w;
        }
        c->waiters_tail = w;
        vx_remote_add(f);
        vx_mutex_unlock(&c->mu);
    }
    return f;
}

// Blocks the thread until a message arrives, and returns it, or NULL once
// the channel is closed and empty.
static char *vx_chan_receive_sync(vx_chan *c) {
    vx_mutex_lock(&c->mu);
    while (!c->head && !c->closed) vx_cond_wait(&c->cv, &c->mu);
    char *buf = c->head ? vx_chan_take(c) : NULL;
    vx_mutex_unlock(&c->mu);
    return buf;
}

static void vx_chan_close(vx_chan *c) {
    vx_mutex_lock(&c->mu);
    if (!c->closed) {
        c->closed = true;
        for (vx_waiter *w = c->waiters, *next; w; w = next) {
            next = w->next;
            vx_post(w->to, w->f, NULL, NULL, false);
            free(w);
        }
        c->waiters = c->waiters_tail = NULL;
        vx_cond_broadcast(&c->cv);
    }
    vx_mutex_unlock(&c->mu);
}

static int64_t vx_cores(void) {
#ifdef _WIN32
    long n = (long)GetActiveProcessorCount(ALL_PROCESSOR_GROUPS);
#else
    long n = sysconf(_SC_NPROCESSORS_ONLN);
#endif
    return n > 0 ? n : 1;
}

#ifdef _WIN32
// Runs test [i] of [fns] in a child process, which shares [*marked], and
// returns its exit status, or -1 when it can't start.
static int64_t vx_test_child(int i, HANDLE marked) {
    char spec[64];
    snprintf(spec, sizeof spec, "%d:%llu", i, (unsigned long long)(uintptr_t)marked);
    SetEnvironmentVariableA("VEX_TEST_CHILD", spec);
    STARTUPINFOW si = {.cb = sizeof si, .dwFlags = STARTF_USESTDHANDLES};
    si.hStdInput = GetStdHandle(STD_INPUT_HANDLE);
    si.hStdOutput = GetStdHandle(STD_OUTPUT_HANDLE);
    si.hStdError = GetStdHandle(STD_ERROR_HANDLE);
    PROCESS_INFORMATION pi;
    wchar_t exe[32768];
    DWORD len = GetModuleFileNameW(NULL, exe, 32768);
    bool started = len > 0 && len < 32768 && CreateProcessW(exe, GetCommandLineW(), NULL, NULL, TRUE, 0, NULL, NULL, &si, &pi);
    SetEnvironmentVariableA("VEX_TEST_CHILD", NULL);
    if (!started) return -1;
    DWORD code = 1;
    WaitForSingleObject(pi.hProcess, INFINITE);
    GetExitCodeProcess(pi.hProcess, &code);
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    return code;
}
#endif

// Runs the [n] tests [fns], named [names], whose names contain the text
// given as the program's first argument, and prints a summary. Returns the
// exit status: 1 when a test failed.
static int vx_test_run(int n, const char *const names[], void (*const fns[])(void)) {
    const char *filter = vx_argc > 1 ? vx_argv[1] : "";
#ifdef _WIN32
    const char *child = getenv("VEX_TEST_CHILD");
    if (child) {
        int i = atoi(child);
        HANDLE h = (HANDLE)(uintptr_t)strtoull(strchr(child, ':') + 1, NULL, 10);
        vx_test_marked = MapViewOfFile(h, FILE_MAP_ALL_ACCESS, 0, 0, sizeof(int));
        if (!vx_test_marked || i < 0 || i >= n) return 1;
        vx_test_name = names[i];
        fns[i]();
        vx_loop();
        fflush(stdout);
        _exit(*vx_test_marked ? 1 : 0);
    }
    SECURITY_ATTRIBUTES inherit = {.nLength = sizeof inherit, .bInheritHandle = TRUE};
    HANDLE shared = CreateFileMappingW(INVALID_HANDLE_VALUE, &inherit, PAGE_READWRITE, 0, sizeof(int), NULL);
    vx_test_marked = shared ? MapViewOfFile(shared, FILE_MAP_ALL_ACCESS, 0, 0, sizeof(int)) : NULL;
    if (!vx_test_marked) vx_out_of_memory();
#else
    vx_test_marked = mmap(NULL, sizeof(int), PROT_READ | PROT_WRITE, MAP_SHARED | MAP_ANON, -1, 0);
    if (vx_test_marked == MAP_FAILED) vx_out_of_memory();
#endif
    int ran = 0, failed = 0;
    for (int i = 0; i < n; i++) {
        if (!strstr(names[i], filter)) continue;
        ran++;
        *vx_test_marked = 0;
        fflush(stdout);
        fflush(stderr);
#ifdef _WIN32
        int64_t code = vx_test_child(i, shared);
        if (code < 0) {
            fprintf(stderr, "error: can't start the test: Windows error %lu\n", GetLastError());
            return 1;
        }
        if (code == 0 && !*vx_test_marked) {
            printf("\u2713 %s\n", names[i]);
            continue;
        }
        failed++;
        if (!*vx_test_marked) {
            printf("\u2717 %s\n", names[i]);
            if (code >= 0xC0000000) {
                printf("  crashed with exception 0x%08llX\n", (unsigned long long)code);
            } else {
                printf("  stopped with exit status %lld\n", (long long)code);
            }
        }
#else
        pid_t pid = fork();
        if (pid < 0) {
            perror("fork");
            return 1;
        }
        if (pid == 0) {
            vx_test_name = names[i];
            fns[i]();
            vx_loop();
            fflush(stdout);
            _exit(*vx_test_marked ? 1 : 0);
        }
        int status = 0;
        while (waitpid(pid, &status, 0) < 0 && errno == EINTR) {}
        if (WIFEXITED(status) && WEXITSTATUS(status) == 0 && !*vx_test_marked) {
            printf("\u2713 %s\n", names[i]);
            continue;
        }
        failed++;
        if (!*vx_test_marked) {
            printf("\u2717 %s\n", names[i]);
            if (WIFSIGNALED(status)) {
                printf("  crashed with signal %d\n", WTERMSIG(status));
            } else {
                printf("  stopped with exit status %d\n", WEXITSTATUS(status));
            }
        }
#endif
    }
    if (ran == 0) {
        printf(*filter ? "no tests match '%s'\n" : "no tests\n", filter);
        return *filter ? 1 : 0;
    }
    printf("\n%d %s: %d passed, %d failed\n", ran, ran == 1 ? "test" : "tests", ran - failed, failed);
    return failed > 0;
}
