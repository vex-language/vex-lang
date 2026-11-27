// Runtime library compiled into every Vex program.

#include <errno.h>
#include <inttypes.h>
#include <pthread.h>
#include <math.h>
#include <setjmp.h>
#include <signal.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
#include <string.h>
#include <dirent.h>
#include <sys/mman.h>
#include <sys/resource.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

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
// passed or crashed.

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

// Prints a runtime error with the source line and stops the program.
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
    exit(1);
}

// --- Memory ---
//
// A mark-and-sweep garbage collector. Live objects are found conservatively:
// any word on the stack, in a register or inside a live object that points
// into an object keeps that object alive. Everything else is reused.
//
// Small objects live in 64 KB pages, each holding objects of one size class,
// and are handed out from per-class free lists. Larger objects get their own
// allocation.

#define VX_PAGE_SHIFT 16
#define VX_PAGE_SIZE ((size_t)1 << VX_PAGE_SHIFT)
#define VX_SMALL_MAX 2048
#define VX_CLASSES 24

static const uint32_t vx_class_size[VX_CLASSES] = {
    16, 32, 48, 64, 80, 96, 112, 128, 160, 192, 224, 256,
    320, 384, 448, 512, 640, 768, 896, 1024, 1280, 1536, 1792, 2048,
};

// Size class of each multiple of 16 bytes up to VX_SMALL_MAX.
static uint8_t vx_class_of[VX_SMALL_MAX / 16 + 1];

typedef struct {
    char *start;
    uint32_t size;    // object size; 0 while the page is unused
    uint32_t count;   // objects that fit
    uint32_t atomic;  // its objects hold no pointers, so they aren't scanned
    uint32_t live;
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
} vx_large;

typedef struct {
    char *ptr;
    size_t size;
} vx_span;

// The heap of one thread. It is a single thread-local struct, rather than
// one variable per field, so that an allocation looks up thread-local
// storage once.
typedef struct {
    void *free[2][VX_CLASSES]; // free objects by atomic flag and class
    size_t heap_bytes;         // bytes in objects, live or not
    size_t gc_next;            // collect once heap_bytes passes this
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
    // The unused end of the page each class allocates from in order, once
    // its free list is empty.
    char *bump[2][VX_CLASSES], *bump_end[2][VX_CLASSES];
    size_t page_scan;          // pages before this one aren't empty
    const void **rem;          // addresses written since the last collection
    size_t rem_len, rem_cap;
    size_t rem_done;           // entries before this one name distinct objects
    size_t major_next;         // a major collection is due once gc_base passes this
} vx_heap;


static VX_TLS vx_heap vx_h = {.gc_next = 4 << 20, .lo = UINTPTR_MAX};

#define vx_free vx_h.free
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

// Closures handed to C as callback contexts. They stay alive, since C
// may keep them, until C passes the context to vex_release.
static VX_TLS void **vx_kept;

// The globals holding the program's constants, built by the main thread
// before anything else runs and kept alive from then on.
static const char *vx_roots_lo, *vx_roots_hi;
// This isolate's copy of the variables declared outside functions.
static VX_TLS const char *vx_vars_lo, *vx_vars_hi;
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
    void *asan_fake;
    const void *asan_bottom;
    size_t asan_size;
};

static VX_TLS vx_task vx_main_task;
static VX_TLS vx_task *vx_cur;
static VX_TLS vx_task **vx_tasks;          // every task that has started and not finished
static VX_TLS size_t vx_tasks_len, vx_tasks_cap;


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

// Gives the objects of [pg] to class [cls], which allocates them in order.
static void vx_page_format(vx_page *pg, int cls, bool atomic) {
    pg->size = vx_class_size[cls];
    pg->count = (uint32_t)(VX_PAGE_SIZE / pg->size);
    pg->recip = (uint32_t)((((uint64_t)1 << 32) + pg->size - 1) / pg->size);
    pg->atomic = atomic;
    vx_h.bump[atomic][cls] = pg->start;
    vx_h.bump_end[atomic][cls] = pg->start + (size_t)pg->count * pg->size;
}

static void vx_page_new(int cls, bool atomic) {
    for (size_t i = vx_h.page_scan; i < vx_pages_len; i++) {
        if (vx_pages[i]->size == 0) {
            vx_h.page_scan = i + 1;
            vx_page_format(vx_pages[i], cls, atomic);
            return;
        }
    }
    vx_h.page_scan = vx_pages_len + 1;
    vx_page *pg = calloc(1, sizeof(vx_page));
    if (!pg || posix_memalign((void **)&pg->start, VX_PAGE_SIZE, VX_PAGE_SIZE) != 0) vx_out_of_memory();
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
    vx_page_format(pg, cls, atomic);
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

static void vx_gray_push(char *ptr, size_t size) {
    if (vx_gray_len == vx_gray_cap) vx_gray = vx_grow(vx_gray, &vx_gray_cap, sizeof(vx_span));
    vx_gray[vx_gray_len++] = (vx_span){ptr, size};
}

// Reads whole stack frames and objects on purpose, so it isn't checked by
// AddressSanitizer.
//
// The heap's fields are read through [h] once, since each access to the
// thread-local heap otherwise looks it up again.
//
// Objects near each other are usually on the same page, so the page last
// found is kept in [cache] and checked before the page table.
typedef struct {
    uintptr_t n;
    vx_page *pg;
} vx_page_cache;

__attribute__((no_sanitize_address, always_inline))
static inline void vx_scan(vx_heap *h, vx_page_cache *cache, const char *lo, const char *hi) {
    const uintptr_t heap_lo = h->lo, heap_hi = h->hi;
    vx_page *const *table = h->page_table;
    const size_t mask = h->page_table_cap - 1;
    uintptr_t start = ((uintptr_t)lo + sizeof(void *) - 1) & ~(uintptr_t)(sizeof(void *) - 1);
    for (uintptr_t a = start; a + sizeof(void *) <= (uintptr_t)hi; a += sizeof(void *)) {
        uintptr_t p = *(uintptr_t *)a;
        if (p < heap_lo || p >= heap_hi) continue;
        uintptr_t n = p >> VX_PAGE_SHIFT;
        vx_page *pg = NULL;
        if (n == cache->n) {
            pg = cache->pg;
        } else if (table) {
            for (size_t j = vx_page_hash(n, mask + 1);; j = (j + 1) & mask) {
                vx_page *t = table[j];
                if (!t || (uintptr_t)t->start >> VX_PAGE_SHIFT == n) {
                    pg = t;
                    break;
                }
            }
            if (pg) *cache = (vx_page_cache){n, pg};
        }
        if (pg) {
            if (pg->size == 0) continue;
            size_t i = (size_t)(((uint64_t)(p - (uintptr_t)pg->start) * pg->recip) >> 32);
            if (i >= pg->count || pg->marks[i]) continue;
            pg->marks[i] = 1;
            pg->live++;
            if (!pg->atomic) {
                if (h->gray_len == h->gray_cap) h->gray = vx_grow(h->gray, &h->gray_cap, sizeof(vx_span));
                h->gray[h->gray_len++] = (vx_span){pg->start + i * pg->size, pg->size};
            }
            continue;
        }
        vx_large *l = vx_large_of(p);
        if (!l || l->mark) continue;
        l->mark = 1;
        if (!l->atomic) vx_gray_push((char *)(l + 1), l->size);
    }
}

__attribute__((no_sanitize_address))
static void vx_mark_range(const char *lo, const char *hi) {
    vx_page_cache cache = {0, NULL};
    vx_scan(&vx_h, &cache, lo, hi);
}

// Scans the marked objects until none are left.
__attribute__((no_sanitize_address))
static void vx_mark_gray(void) {
    vx_heap *h = &vx_h;
    vx_page_cache cache = {0, NULL};
    while (h->gray_len > 0) {
        vx_span s = h->gray[--h->gray_len];
        vx_scan(h, &cache, s.ptr, s.ptr + s.size);
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
        if (!major && marked && !atomic) vx_gray_push(start, size);
    }
    vx_h.rem_len = vx_h.rem_done = 0;
}

// Marks everything the program can reach directly, and what that reaches.
static void vx_mark_roots(void) {
    vx_mark_stack();
    vx_mark_range((const char *)&vx_fs_entries, (const char *)(&vx_fs_entries + 1));
    if (vx_kept) vx_mark_range((const char *)vx_kept, (const char *)(vx_kept + vx_kept_len));
    if (vx_roots_lo) vx_mark_range(vx_roots_lo, vx_roots_hi);
    if (vx_vars_lo) vx_mark_range(vx_vars_lo, vx_vars_hi);
    // The stacks of the tasks that aren't running, the program's own
    // included, from their saved registers up.
    if (vx_cur != &vx_main_task) vx_mark_range(vx_main_task.sp, vx_main_task.top);
    for (size_t i = 0; i < vx_tasks_len; i++) {
        if (vx_tasks[i] != vx_cur) vx_mark_range(vx_tasks[i]->sp, vx_tasks[i]->top);
    }
    vx_async_mark();
    vx_mark_gray();
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

    // Sweep: rebuild the free lists from the unmarked objects, keeping the
    // marks. A page with nothing live is empty again, and a full one has
    // nothing to add.
    memset(vx_free, 0, sizeof(vx_free));
    memset(vx_h.bump, 0, sizeof(vx_h.bump));
    memset(vx_h.bump_end, 0, sizeof(vx_h.bump_end));
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
        int cls = vx_class_of[pg->size / 16];
        void **list = &vx_free[pg->atomic][cls];
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

static void *vx_alloc_large(size_t n, bool atomic) {
    vx_large *l = calloc(1, sizeof(vx_large) + n);
    if (!l) vx_out_of_memory();
    l->size = n;
    l->atomic = atomic;
    if (vx_larges_len == vx_larges_cap) vx_larges = vx_grow(vx_larges, &vx_larges_cap, sizeof(vx_large *));
    vx_larges[vx_larges_len++] = l;
    vx_bounds((uintptr_t)(l + 1), (uintptr_t)(l + 1) + n);
    return l + 1;
}

static void *vx_alloc_block(size_t n, bool atomic) {
    vx_heap *h = &vx_h;
    if (vx_gc_stress && --h->gc_countdown == 0) {
        h->gc_countdown = vx_gc_stress;
        vx_collect();
    } else if (h->heap_bytes + n > h->gc_next) {
        vx_collect();
    }
    void *p;
    if (n > VX_SMALL_MAX) {
        p = vx_alloc_large(n, atomic);
    } else {
        int cls = vx_class_of[(n + 15) / 16];
        n = vx_class_size[cls];
        void **list = &h->free[atomic][cls];
        if (*list) {
            p = *list;
            *list = *(void **)p;
        } else {
            if (h->bump[atomic][cls] == h->bump_end[atomic][cls]) vx_page_new(cls, atomic);
            p = h->bump[atomic][cls];
            h->bump[atomic][cls] += n;
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

// The common case of an allocation, inlined where it happens: an object of
// up to 128 bytes, from its class's free list or the page the class
// allocates from in order, when no collection is due. The class is
// worked out here rather than looked up, so for a constant size it is a
// constant.
__attribute__((always_inline))
static inline void *vx_alloc_small(size_t n, bool atomic) {
    if (n - 1 < 128 && !vx_gc_stress) {
        vx_heap *h = &vx_h;
        size_t cls = (n - 1) >> 4, size = (cls + 1) << 4;
        if (__builtin_expect(h->heap_bytes + size <= h->gc_next, 1)) {
            void **list = &h->free[atomic][cls];
            void *p = *list;
            if (p) {
                *list = *(void **)p;
            } else if (h->bump[atomic][cls] != h->bump_end[atomic][cls]) {
                p = h->bump[atomic][cls];
                h->bump[atomic][cls] += size;
            }
            if (__builtin_expect(p != NULL, 1)) {
                h->heap_bytes += size;
                uint64_t *q = p;
                q[0] = 0;
                if (!atomic) {
                    q[1] = 0;
                    for (size_t i = 2; i < size / 8; i++) q[i] = 0;
                }
                return p;
            }
        }
    }
    return vx_alloc_block(n ? n : 1, atomic);
}

// Allocates [n] zeroed bytes that may hold pointers.
static inline void *vx_alloc(size_t n) { return vx_alloc_small(n, false); }

// Returns a context for passing closure [f] to C, kept alive until C
// passes it to vex_release.
__attribute__((unused))
static void *vx_keep_fn(vx_fn f) {
    vx_fn *box = vx_alloc(sizeof(vx_fn));
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
static inline void *vx_alloc_atomic(size_t n) { return vx_alloc_small(n, true); }

// Returns a copy of [p] resized to [n] bytes. The old object is left for
// the collector, since other code may still be reading it.
static void *vx_realloc(void *p, size_t n) {
    vx_page *pg = vx_page_of((uintptr_t)p);
    size_t old = pg ? pg->size : ((vx_large *)p - 1)->size;
    bool atomic = pg ? pg->atomic : ((vx_large *)p - 1)->atomic;
    char *q = atomic ? vx_alloc_atomic(n) : vx_alloc(n);
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

static VX_TLS size_t vx_stack_limit;

// Turns a crash just below the stack into a readable error: it means the
// program ran out of stack, almost always through endless recursion.
static void vx_on_fault(int sig, siginfo_t *info, void *ctx) {
    (void)ctx;
    uintptr_t addr = (uintptr_t)info->si_addr, base = (uintptr_t)vx_stack_base;
    bool task_guard = vx_cur && vx_cur->stack && addr >= (uintptr_t)vx_cur->stack && addr < (uintptr_t)vx_cur->stack + (size_t)getpagesize();
    if (task_guard || ((!vx_cur || vx_cur == &vx_main_task) && addr < base && base - addr <= vx_stack_limit + ((size_t)1 << 20))) {
        fflush(stdout);
        static const char msg[] = "error: stack overflow: too many nested function calls, "
                                  "probably a function that calls itself without stopping\n";
        ssize_t n = write(STDERR_FILENO, msg, sizeof(msg) - 1);
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

// Prepares the runtime; [base] is the top of the stack.
static void vx_inbox_init(void);

static void vx_init(void *base) {
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
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec;
}

// Seconds since 1970-01-01 UTC.
static int64_t vx_time_unix(void) { return (int64_t)time(NULL); }

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

static vx_str vx_str_upper(vx_str s) {
    char *buf = vx_alloc_atomic((size_t)s.len + 1);
    for (int64_t i = 0; i < s.len; i++) {
        char c = s.ptr[i];
        buf[i] = (c >= 'a' && c <= 'z') ? (char)(c - 32) : c;
    }
    return (vx_str){buf, s.len};
}

static vx_str vx_str_lower(vx_str s) {
    char *buf = vx_alloc_atomic((size_t)s.len + 1);
    for (int64_t i = 0; i < s.len; i++) {
        char c = s.ptr[i];
        buf[i] = (c >= 'A' && c <= 'Z') ? (char)(c + 32) : c;
    }
    return (vx_str){buf, s.len};
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
// [cap]. The elements of a [flat] list hold no pointers, so the collector
// doesn't scan them.
static vx_list *vx_list_make(int64_t esz, int64_t n, int64_t cap, const void *items, bool flat) {
    vx_list *l = vx_alloc(sizeof(vx_list));
    if (cap < n) cap = n;
    l->esz = esz;
    l->len = n;
    l->cap = cap < 4 ? 4 : cap;
    size_t size = (size_t)(l->cap * esz);
    l->data = flat ? vx_alloc_atomic(size) : vx_alloc(size);
    if (n > 0) memcpy(l->data, items, (size_t)(n * esz));
    vx_remember(l);
    vx_remember(l->data);
    return l;
}

static vx_list *vx_list_new(int64_t esz, int64_t n, const void *items) { return vx_list_make(esz, n, 0, items, false); }

// Like [vx_list_new], for elements that hold no pointers.
static vx_list *vx_list_flat(int64_t esz, int64_t n, const void *items) { return vx_list_make(esz, n, 0, items, true); }

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
        l->data = vx_realloc(l->data, (size_t)(l->cap * l->esz));
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

static vx_list *vx_list_copy(vx_list *l) { return vx_list_make(l->esz, l->len, 0, l->data, vx_is_atomic(l->data)); }

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
    bool atomic = vx_is_atomic(l->data);
    size_t size = (size_t)(n * esz);
    char *a = atomic ? vx_alloc_atomic(size) : vx_alloc(size);
    char *b = atomic ? vx_alloc_atomic(size) : vx_alloc(size);
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
    return vx_list_make(l->esz, to - from, 0, l->data + from * l->esz, vx_is_atomic(l->data));
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
        l->data = vx_realloc(l->data, (size_t)(l->cap * l->esz));
        vx_remember(l);
    }
    if (n > 0) memcpy(l->data + l->len * l->esz, src->data, (size_t)(n * l->esz));
    l->len += n;
    vx_remember(l->data);
}

static vx_list *vx_str_split(vx_str s, vx_str sep, int line) {
    if (sep.len == 0) vx_fail(line, "split separator can't be empty");
    vx_list *l = vx_list_new(sizeof(vx_str), 0, NULL);
    int64_t start = 0;
    for (;;) {
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
            buf = vx_realloc(buf, (size_t)cap + 1);
        }
        memcpy(buf + n, s.ptr + start, (size_t)(at - start));
        n += at - start;
        if (with.len > 0) memcpy(buf + n, with.ptr, (size_t)with.len);
        n += with.len;
        start = at + old.len;
        at = vx_str_find(s, old, start);
    }
    if (n + (s.len - start) > cap) buf = vx_realloc(buf, (size_t)(n + s.len - start) + 1);
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

static vx_list *vx_str_chars(vx_str s) {
    vx_list *l = vx_list_new(sizeof(vx_str), 0, NULL);
    int64_t i = 0;
    while (i < s.len) {
        int64_t j = i + vx_utf8_width(s, i);
        vx_str c = {s.ptr + i, j - i};
        vx_list_push(l, &c);
        i = j;
    }
    return l;
}

// Returns the lines of [s], without their line endings (\n or \r\n). A
// final line ending doesn't start another line.
static vx_list *vx_str_lines(vx_str s) {
    vx_list *l = vx_list_new(sizeof(vx_str), 0, NULL);
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

static vx_map *vx_map_new(int kind, int ksz, int vsz) {
    vx_map *m = vx_alloc(sizeof(vx_map));
    m->kind = kind;
    m->ksz = ksz;
    m->vsz = vsz;
    m->voff = VX_MAP_KEY + ((ksz + 7) & ~7);
    m->esz = m->voff + ((vsz + 7) & ~7);
    m->next_seq = 1;
    return m;
}

// Returns a new map whose keys [khash] hashes and [keq] compares.
static vx_map *vx_map_new_custom(uint64_t (*khash)(const void *), bool (*keq)(const void *, const void *), int ksz, int vsz) {
    vx_map *m = vx_map_new(VX_KEY_CUSTOM, ksz, vsz);
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
    char *entries = cap == m->cap ? m->entries : vx_alloc((size_t)(cap * m->esz));
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
    vx_list *l = vx_list_new(values ? m->vsz : m->ksz, 0, NULL);
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
    vx_map *m = vx_map_new(src->kind, src->ksz, src->vsz);
    m->khash = src->khash;
    m->keq = src->keq;
    vx_map_merge(m, src);
    return m;
}

// --- Conversions ---

// Returns how a value of a C type called [name] at [p] is shown.
__attribute__((unused))
static vx_str vx_handle_str(const char *name, void *p) {
    char buf[128];
    int n = snprintf(buf, sizeof buf, p ? "<%s %p>" : "<%s null>", name, p);
    return vx_str_new(buf, n < (int)sizeof buf ? n : (int)sizeof buf - 1);
}

static vx_str vx_int_str(int64_t v) {
    char buf[24];
    int n = snprintf(buf, sizeof buf, "%" PRId64, v);
    return vx_str_new(buf, n);
}

// Formats [v] with the fewest digits that read back as the same value,
// always showing a decimal point: 2.5, 10.0, 0.001. Very large or small
// values use an exponent: 1e+20, 1e-07.
static vx_str vx_float_str(double v) {
    if (isnan(v)) return VX_LIT("nan");
    if (isinf(v)) return v > 0 ? VX_LIT("inf") : VX_LIT("-inf");
    char buf[400];
    int prec = 1;
    for (; prec < 17; prec++) {
        snprintf(buf, sizeof buf, "%.*e", prec - 1, v);
        if (strtod(buf, NULL) == v) break;
    }
    snprintf(buf, sizeof buf, "%.*e", prec - 1, v);
    int exp10 = atoi(strchr(buf, 'e') + 1);
    int n;
    if (exp10 >= -5 && exp10 < 17) {
        int decimals = prec - 1 - exp10;
        n = snprintf(buf, sizeof buf, "%.*f", decimals > 0 ? decimals : 0, v);
    } else {
        n = snprintf(buf, sizeof buf, "%.*g", prec, v);
    }
    if (!strpbrk(buf, ".e")) {
        buf[n++] = '.';
        buf[n++] = '0';
        buf[n] = 0;
    }
    return vx_str_new(buf, n);
}

static vx_str vx_bool_str(bool v) { return v ? VX_LIT("true") : VX_LIT("false"); }

// Returns [s] in quotes with escapes, as shown inside lists and structs.
static vx_str vx_str_quote(vx_str s) {
    char *buf = vx_alloc_atomic((size_t)s.len * 2 + 3);
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
    return (vx_str){buf, n};
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

static int64_t vx_float_to_int(double v, int line) {
    if (isnan(v) || v >= 9223372036854775808.0 || v < -9223372036854775808.0) {
        vx_str f = vx_float_str(v);
        vx_fail(line, "can't convert %.*s to int: it's out of range", (int)f.len, f.ptr);
    }
    return (int64_t)v;
}

// --- Errors ---

// Stops the program after an error passed up out of main.
__attribute__((noreturn))
static void vx_uncaught(vx_str message) {
    vx_test_mark();
    fflush(stdout);
    fprintf(stderr, "error: %.*s\n", (int)message.len, message.ptr);
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

static void vx_write(vx_str s) { fwrite(s.ptr, 1, (size_t)s.len, stdout); }

// Prints [n] strings separated by spaces, then a newline if [newline] is set.
static void vx_print(bool newline, int n, const vx_str *parts) {
    for (int i = 0; i < n; i++) {
        if (i > 0) fputc(' ', stdout);
        vx_write(parts[i]);
    }
    if (newline) fputc('\n', stdout);
}

// Reads one line from standard input without its line break; "" at the end
// of input.
static vx_str vx_input(vx_str prompt) {
    vx_write(prompt);
    fflush(stdout);
    char *line = NULL;
    size_t cap = 0;
    ssize_t n = getline(&line, &cap, stdin);
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

static const char *vx_os_error(void) { return strerror(vx_errno); }

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
            buf = vx_realloc(buf, cap * 2);
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

// Creates the folder [path] and any missing parents.
static bool vx_fs_make_dir(const char *path) {
    size_t n = strlen(path);
    char *p = vx_alloc_atomic(n + 1);
    memcpy(p, path, n + 1);
    for (size_t i = 1; i <= n; i++) {
        if (p[i] != '/' && p[i] != 0) continue;
        char saved = p[i];
        p[i] = 0;
        if (mkdir(p, 0777) != 0 && errno != EEXIST) { vx_errno = errno; return false; }
        p[i] = saved;
    }
    if (!vx_fs_is_dir(path)) { vx_errno = ENOTDIR; return false; }
    return true;
}

static int vx_fs_cmp(const void *a, const void *b) {
    return strcmp(*(char *const *)a, *(char *const *)b);
}

static int64_t vx_fs_list_open(const char *path) {
    DIR *d = opendir(path);
    if (!d) { vx_errno = errno; return -1; }
    int64_t cap = 16, n = 0;
    char **names = vx_alloc((size_t)cap * sizeof(char *));
    struct dirent *e;
    while ((e = readdir(d)) != NULL) {
        if (strcmp(e->d_name, ".") == 0 || strcmp(e->d_name, "..") == 0) continue;
        if (n == cap) {
            names = vx_realloc(names, (size_t)cap * 2 * sizeof(char *));
            cap *= 2;
        }
        size_t len = strlen(e->d_name);
        names[n] = vx_alloc_atomic(len + 1);
        vx_remember(names);
        memcpy(names[n], e->d_name, len + 1);
        n++;
    }
    closedir(d);
    qsort(names, (size_t)n, sizeof(char *), vx_fs_cmp);
    vx_fs_entries = names;
    vx_fs_entry_count = n;
    return n;
}

static const char *vx_fs_list_name(int64_t i) {
    return i >= 0 && i < vx_fs_entry_count ? vx_fs_entries[i] : NULL;
}

static int64_t vx_os_argc(void) { return vx_argc; }

static const char *vx_os_arg(int64_t i) { return i >= 0 && i < vx_argc ? vx_argv[i] : NULL; }

__attribute__((noreturn))
static void vx_os_exit(int64_t code) {
    fflush(stdout);
    exit((int)code);
}

static const char *vx_os_cwd(void) {
    char buf[4096];
    if (!getcwd(buf, sizeof buf)) { vx_errno = errno; return NULL; }
    size_t n = strlen(buf);
    char *out = vx_alloc_atomic(n + 1);
    memcpy(out, buf, n + 1);
    return out;
}

static void vx_os_set_env(const char *name, const char *value) { setenv(name, value, 1); }

// Returns the exit status of a finished child, or 128 + the signal that
// stopped it.
static int64_t vx_os_status(int status) {
    if (WIFEXITED(status)) return WEXITSTATUS(status);
    if (WIFSIGNALED(status)) return 128 + WTERMSIG(status);
    return -1;
}

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
            buf = vx_realloc(buf, cap * 2);
            cap *= 2;
        }
    }
    buf[len] = 0;
    int status = pclose(p);
    vx_os_last_status = status == -1 ? -1 : vx_os_status(status);
    return buf;
}

static int64_t vx_os_status_of_last(void) { return vx_os_last_status; }

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

// Stacks of finished tasks, kept for new ones.
static VX_TLS char *vx_spare_stacks[64];
static VX_TLS int vx_spare_len;

// Switches from the running task to [to], which runs until it switches
// back. [ending] is set when the running task has finished.
static void vx_switch(vx_task *to, bool ending) {
    vx_task *from = vx_cur;
    vx_cur = to;
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
        munmap(t->stack, t->size);
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

// Starts a task that runs [entry] with [arg] and settles [f], which may be
// NULL, and runs it until it first waits. It is a child of the running
// task.
static void vx_go_f(void (*entry)(void *), void *arg, struct vx_future *f) {
    vx_task *t = calloc(1, sizeof(vx_task));
    if (!t) vx_out_of_memory();
    t->parent = vx_cur;
    t->future = f;
    if (f) vx_future_own(f, t);
    t->size = VX_TASK_STACK;
    if (vx_spare_len > 0) {
        t->stack = vx_spare_stacks[--vx_spare_len];
    } else {
        t->stack = mmap(NULL, t->size, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANON, -1, 0);
        if (t->stack == MAP_FAILED) vx_out_of_memory();
        mprotect(t->stack, (size_t)getpagesize(), PROT_NONE);
    }
    t->top = (char *)(((uintptr_t)(t->stack + t->size)) & ~(uintptr_t)15);
    t->asan_bottom = t->stack + getpagesize();
    t->asan_size = t->size - (size_t)getpagesize();
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
    t->sp = sp;
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

static vx_future *vx_future_new(int64_t size) {
    vx_future *f = vx_alloc(sizeof(vx_future) + (size_t)size);
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

static void vx_async_mark(void) {
    if (vx_timers) vx_mark_range((const char *)vx_timers, (const char *)(vx_timers + vx_timers_len));
    if (vx_remote) vx_mark_range((const char *)vx_remote, (const char *)(vx_remote + vx_remote_len));
}

// Returns a future that completes after [ms] milliseconds.
static vx_future *vx_sleep(int64_t ms) {
    vx_future *f = vx_future_new(0);
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

static vx_stream *vx_stream_new(int64_t esz) {
    vx_stream *s = vx_alloc(sizeof(vx_stream));
    s->queue = vx_list_new(esz, 0, NULL);
    vx_remember(s);
    return s;
}

// Makes [s] the stream of a stream function, whose body [entry] runs with
// [arg] once the stream is first read.
static void vx_stream_start(vx_stream *s, void (*entry)(void *), void *arg) {
    s->entry = entry;
    s->arg = arg;
    s->done = vx_future_new(0);
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
    vx_future *out = vx_future_new(0);
    vx_future *in[2] = {x, y};
    for (int i = 0; i < 2 && out->state == 0; i++) {
        vx_first_arg *a = vx_alloc(sizeof(vx_first_arg));
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
} vx_all_arg;

static void vx_all_task(void *p) {
    vx_all_arg *a = p;
    vx_list *values = a->esz > 0 ? vx_list_new(a->esz, 0, NULL) : NULL;
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

static vx_future *vx_all(vx_list *futures, int64_t esz) {
    vx_all_arg *a = vx_alloc(sizeof(vx_all_arg));
    *a = (vx_all_arg){vx_future_new(esz > 0 ? (int64_t)sizeof(vx_list *) : 0), futures, esz};
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

static vx_future *vx_any(vx_list *futures, int64_t esz, int line) {
    // With no futures, nothing could ever settle the result.
    if (futures->len == 0) vx_fail(line, "any() needs at least one future");
    vx_future *result = vx_future_new(esz);
    for (int64_t i = 0; i < futures->len && result->state == 0; i++) {
        vx_any_arg *a = vx_alloc(sizeof(vx_any_arg));
        *a = (vx_any_arg){result, ((vx_future **)futures->data)[i]};
        vx_remember(a);
        vx_go(vx_any_task, a);
    }
    return result;
}

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
    pthread_mutex_t mu;
    pthread_cond_t cv;
    vx_msg *head, *tail;
} vx_inbox;

// The inbox of the running isolate.
static VX_TLS vx_inbox *vx_self;

static vx_inbox *vx_inbox_new(void) {
    vx_inbox *in = calloc(1, sizeof(vx_inbox));
    if (!in) vx_out_of_memory();
    pthread_mutex_init(&in->mu, NULL);
    pthread_cond_init(&in->cv, NULL);
    return in;
}

static void vx_inbox_init(void) { vx_self = vx_inbox_new(); }

static void vx_post(vx_inbox *to, vx_future *f, char *buf, void (*fill)(const char *, void *), bool failed) {
    vx_msg *m = malloc(sizeof(vx_msg));
    if (!m) vx_out_of_memory();
    *m = (vx_msg){NULL, f, buf, fill, failed};
    pthread_mutex_lock(&to->mu);
    if (to->tail) {
        to->tail->next = m;
    } else {
        to->head = m;
    }
    to->tail = m;
    pthread_cond_signal(&to->cv);
    pthread_mutex_unlock(&to->mu);
}

// Completes the futures of the messages that have arrived. Returns
// whether there were any.
static bool vx_drain(void) {
    if (!vx_self) return false;
    pthread_mutex_lock(&vx_self->mu);
    vx_msg *m = vx_self->head;
    vx_self->head = vx_self->tail = NULL;
    pthread_mutex_unlock(&vx_self->mu);
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
    pthread_mutex_lock(&vx_self->mu);
    if (!vx_self->head) {
        if (vx_timers_len > 0) {
            int64_t wait = vx_timers[0].at - vx_time_ns();
            // Very long waits wake once a day, which keeps the deadline
            // arithmetic in range.
            if (wait > 86400 * (int64_t)1000000000) wait = 86400 * (int64_t)1000000000;
            if (wait > 0) {
                struct timespec ts;
                clock_gettime(CLOCK_REALTIME, &ts);
                int64_t ns = (int64_t)ts.tv_nsec + wait;
                ts.tv_sec += (time_t)(ns / 1000000000);
                ts.tv_nsec = (long)(ns % 1000000000);
                pthread_cond_timedwait(&vx_self->cv, &vx_self->mu, &ts);
            }
        } else if (vx_remote_len > 0) {
            pthread_cond_wait(&vx_self->cv, &vx_self->mu);
        }
    }
    pthread_mutex_unlock(&vx_self->mu);
}

// Frees the heap of the running thread when its isolate ends.
static void vx_heap_free(void) {
    for (size_t i = 0; i < vx_pages_len; i++) {
        free(vx_pages[i]->start);
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
    for (int i = 0; i < vx_spare_len; i++) munmap(vx_spare_stacks[i], VX_TASK_STACK);
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
    vx_stack_limit = VX_ISOLATE_STACK;
    stack_t old_alt;
    void *alt = vx_signal_stack(&old_alt);
    vx_self = vx_inbox_new();
    vx_isolate_body(&s);
    free(s.buf);
    vx_heap_free();
    pthread_mutex_destroy(&vx_self->mu);
    pthread_cond_destroy(&vx_self->cv);
    free(vx_self);
    if (alt) {
        // Sanitizers free the stack they installed when the thread ends,
        // so theirs goes back first.
        sigaltstack(&old_alt, NULL);
        free(alt);
    }
    return NULL;
}

static void vx_isolate_start(vx_isolate_entry entry, vx_enc *e, vx_future *f) {
    vx_start *s = malloc(sizeof(vx_start));
    if (!s) vx_out_of_memory();
    *s = (vx_start){entry, vx_enc_done(e), vx_self, f};
    pthread_attr_t attr;
    pthread_attr_init(&attr);
    pthread_attr_setstacksize(&attr, VX_ISOLATE_STACK);
    pthread_attr_setdetachstate(&attr, PTHREAD_CREATE_DETACHED);
    pthread_t t;
    if (pthread_create(&t, &attr, vx_isolate_main, s) != 0) {
        fflush(stdout);
        fputs("error: can't start another isolate: the system has no threads left\n", stderr);
        exit(1);
    }
    pthread_attr_destroy(&attr);
}

// Starts an isolate for isolate.run, and returns the future of its result.
static vx_future *vx_isolate_run(vx_isolate_entry entry, vx_enc *e, int64_t size) {
    vx_future *f = vx_future_new(size);
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
    pthread_mutex_t mu;
    pthread_cond_t cv;
    vx_queued *head, *tail;
    vx_waiter *waiters, *waiters_tail;
    bool closed;
} vx_chan;

static vx_chan *vx_chan_new(void) {
    vx_chan *c = calloc(1, sizeof(vx_chan));
    if (!c) vx_out_of_memory();
    pthread_mutex_init(&c->mu, NULL);
    pthread_cond_init(&c->cv, NULL);
    return c;
}

static void vx_chan_send(vx_chan *c, vx_enc *e, int line) {
    char *buf = vx_enc_done(e);
    pthread_mutex_lock(&c->mu);
    if (c->closed) {
        pthread_mutex_unlock(&c->mu);
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
        pthread_cond_signal(&c->cv);
    }
    pthread_mutex_unlock(&c->mu);
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
static vx_future *vx_chan_receive(vx_chan *c, void (*fill)(const char *, void *), int64_t size) {
    vx_future *f = vx_future_new(size);
    pthread_mutex_lock(&c->mu);
    if (c->head) {
        char *buf = vx_chan_take(c);
        pthread_mutex_unlock(&c->mu);
        fill(buf, f->value);
        free(buf);
        vx_future_settle(f, 1);
    } else if (c->closed) {
        pthread_mutex_unlock(&c->mu);
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
        pthread_mutex_unlock(&c->mu);
    }
    return f;
}

// Blocks the thread until a message arrives, and returns it, or NULL once
// the channel is closed and empty.
static char *vx_chan_receive_sync(vx_chan *c) {
    pthread_mutex_lock(&c->mu);
    while (!c->head && !c->closed) pthread_cond_wait(&c->cv, &c->mu);
    char *buf = c->head ? vx_chan_take(c) : NULL;
    pthread_mutex_unlock(&c->mu);
    return buf;
}

static void vx_chan_close(vx_chan *c) {
    pthread_mutex_lock(&c->mu);
    if (!c->closed) {
        c->closed = true;
        for (vx_waiter *w = c->waiters, *next; w; w = next) {
            next = w->next;
            vx_post(w->to, w->f, NULL, NULL, false);
            free(w);
        }
        c->waiters = c->waiters_tail = NULL;
        pthread_cond_broadcast(&c->cv);
    }
    pthread_mutex_unlock(&c->mu);
}

static int64_t vx_cores(void) {
    long n = sysconf(_SC_NPROCESSORS_ONLN);
    return n > 0 ? n : 1;
}

// Runs the [n] tests [fns], named [names], whose names contain the text
// given as the program's first argument, and prints a summary. Returns the
// exit status: 1 when a test failed.
static int vx_test_run(int n, const char *const names[], void (*const fns[])(void)) {
    const char *filter = vx_argc > 1 ? vx_argv[1] : "";
    vx_test_marked = mmap(NULL, sizeof(int), PROT_READ | PROT_WRITE, MAP_SHARED | MAP_ANON, -1, 0);
    if (vx_test_marked == MAP_FAILED) vx_out_of_memory();
    int ran = 0, failed = 0;
    for (int i = 0; i < n; i++) {
        if (!strstr(names[i], filter)) continue;
        ran++;
        *vx_test_marked = 0;
        fflush(stdout);
        fflush(stderr);
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
    }
    if (ran == 0) {
        printf(*filter ? "no tests match '%s'\n" : "no tests\n", filter);
        return *filter ? 1 : 0;
    }
    printf("\n%d %s: %d passed, %d failed\n", ran, ran == 1 ? "test" : "tests", ran - failed, failed);
    return failed > 0;
}
