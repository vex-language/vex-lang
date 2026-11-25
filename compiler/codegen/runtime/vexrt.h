// Runtime library compiled into every Vex program.

#include <errno.h>
#include <inttypes.h>
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
#include <sys/resource.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

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

// --- Errors ---

// Prints a runtime error with the source line and stops the program.
__attribute__((noreturn, format(printf, 2, 3)))
static void vx_fail(int line, const char *fmt, ...) {
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
    uint8_t marks[VX_PAGE_SIZE / 16];
} vx_page;

// A large object's header. Its size keeps objects 16-byte aligned.
typedef struct {
    size_t size;
    uint32_t atomic;
    uint32_t mark;
} vx_large;

static vx_page **vx_pages;          // every page
static size_t vx_pages_len, vx_pages_cap;
static vx_page **vx_page_table;     // open-addressing map from page number to page
static size_t vx_page_table_cap;
static void *vx_free[2][VX_CLASSES]; // free objects by atomic flag and class
static vx_large **vx_larges;        // every large object; sorted during a collection
static size_t vx_larges_len, vx_larges_cap;

static uintptr_t vx_lo = UINTPTR_MAX, vx_hi; // bounds of all objects
static size_t vx_heap_bytes;        // bytes in objects, live or not
static size_t vx_gc_next = 4 << 20; // collect once vx_heap_bytes passes this
static char *vx_stack_base;

// The entries of the folder read by the last vx_fs_list_open, sorted. The
// collector marks them, since only the stack is scanned otherwise.
static char **vx_fs_entries;
static int64_t vx_fs_entry_count;

// Closures handed to C as callback contexts. They stay alive, since C
// may keep them, until C passes the context to vex_release.
static void **vx_kept;
static size_t vx_kept_len, vx_kept_cap;

typedef struct {
    char *ptr;
    size_t size;
} vx_span;

static vx_span *vx_gray;            // objects marked but not yet scanned
static size_t vx_gray_len, vx_gray_cap;

static size_t vx_gc_stress;         // VEX_GC_STRESS=N: collect every N allocations
static size_t vx_gc_countdown;
static size_t vx_gc_count, vx_gc_total, vx_gc_peak;

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

// Threads every object of [pg] onto its class's free list.
static void vx_page_format(vx_page *pg, int cls, bool atomic) {
    pg->size = vx_class_size[cls];
    pg->count = (uint32_t)(VX_PAGE_SIZE / pg->size);
    pg->atomic = atomic;
    void **list = &vx_free[atomic][cls];
    for (uint32_t i = pg->count; i-- > 0;) {
        void **obj = (void **)(pg->start + (size_t)i * pg->size);
        *obj = *list;
        *list = obj;
    }
}

static void vx_page_new(int cls, bool atomic) {
    for (size_t i = 0; i < vx_pages_len; i++) {
        if (vx_pages[i]->size == 0) {
            vx_page_format(vx_pages[i], cls, atomic);
            return;
        }
    }
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

static void vx_gray_push(char *ptr, size_t size) {
    if (vx_gray_len == vx_gray_cap) vx_gray = vx_grow(vx_gray, &vx_gray_cap, sizeof(vx_span));
    vx_gray[vx_gray_len++] = (vx_span){ptr, size};
}

// Reads whole stack frames and objects on purpose, so it isn't checked by
// AddressSanitizer.
__attribute__((no_sanitize_address))
static void vx_mark_range(const char *lo, const char *hi) {
    uintptr_t start = ((uintptr_t)lo + sizeof(void *) - 1) & ~(uintptr_t)(sizeof(void *) - 1);
    for (uintptr_t a = start; a + sizeof(void *) <= (uintptr_t)hi; a += sizeof(void *)) {
        uintptr_t p = *(uintptr_t *)a;
        if (p < vx_lo || p >= vx_hi) continue;
        vx_page *pg = vx_page_of(p);
        if (pg) {
            if (pg->size == 0) continue;
            size_t i = (p - (uintptr_t)pg->start) / pg->size;
            if (i >= pg->count || pg->marks[i]) continue;
            pg->marks[i] = 1;
            pg->live++;
            if (!pg->atomic) vx_gray_push(pg->start + i * pg->size, pg->size);
            continue;
        }
        vx_large *l = vx_large_of(p);
        if (!l || l->mark) continue;
        l->mark = 1;
        if (!l->atomic) vx_gray_push((char *)(l + 1), l->size);
    }
}

__attribute__((noinline, no_sanitize_address))
static void vx_mark_stack(void) {
    // Spill callee-saved registers onto the stack so they are scanned too.
    jmp_buf regs;
    setjmp(regs);
    char here;
    const char *lo = (const char *)&regs < &here ? (const char *)&regs : &here;
    vx_mark_range(lo, vx_stack_base);
}

static void vx_collect(void) {
    if (!vx_stack_base) return;
    vx_gc_count++;
    if (vx_larges_len > 1) qsort(vx_larges, vx_larges_len, sizeof(vx_large *), vx_large_cmp);

    vx_mark_stack();
    vx_mark_range((const char *)&vx_fs_entries, (const char *)(&vx_fs_entries + 1));
    if (vx_kept) vx_mark_range((const char *)vx_kept, (const char *)(vx_kept + vx_kept_len));
    while (vx_gray_len > 0) {
        vx_span s = vx_gray[--vx_gray_len];
        vx_mark_range(s.ptr, s.ptr + s.size);
    }

    // Sweep: rebuild the free lists from the unmarked objects.
    memset(vx_free, 0, sizeof(vx_free));
    vx_heap_bytes = 0;
    for (size_t p = 0; p < vx_pages_len; p++) {
        vx_page *pg = vx_pages[p];
        if (pg->size == 0) continue;
        if (pg->live == 0) {
            pg->size = 0;
            continue;
        }
        int cls = vx_class_of[pg->size / 16];
        void **list = &vx_free[pg->atomic][cls];
        for (uint32_t i = pg->count; i-- > 0;) {
            if (pg->marks[i]) continue;
            void **obj = (void **)(pg->start + (size_t)i * pg->size);
            *obj = *list;
            *list = obj;
        }
        vx_heap_bytes += (size_t)pg->live * pg->size;
        memset(pg->marks, 0, pg->count);
        pg->live = 0;
    }
    size_t kept = 0;
    for (size_t i = 0; i < vx_larges_len; i++) {
        vx_large *l = vx_larges[i];
        if (l->mark) {
            l->mark = 0;
            vx_heap_bytes += l->size;
            vx_larges[kept++] = l;
        } else {
            free(l);
        }
    }
    vx_larges_len = kept;
    vx_gc_next = vx_heap_bytes * 2 > (size_t)(4 << 20) ? vx_heap_bytes * 2 : (size_t)(4 << 20);
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
    if (vx_gc_stress && --vx_gc_countdown == 0) {
        vx_gc_countdown = vx_gc_stress;
        vx_collect();
    } else if (vx_heap_bytes + n > vx_gc_next) {
        vx_collect();
    }
    void *p;
    if (n > VX_SMALL_MAX) {
        p = vx_alloc_large(n, atomic);
    } else {
        int cls = vx_class_of[(n + 15) / 16];
        n = vx_class_size[cls];
        void **list = &vx_free[atomic][cls];
        if (!*list) vx_page_new(cls, atomic);
        p = *list;
        *list = *(void **)p;
        if (atomic) *(void **)p = NULL;
        else memset(p, 0, n);
    }
    vx_heap_bytes += n;
    vx_gc_total += n;
    if (vx_heap_bytes > vx_gc_peak) vx_gc_peak = vx_heap_bytes;
    return p;
}

// Allocates [n] zeroed bytes that may hold pointers.
static void *vx_alloc(size_t n) { return vx_alloc_block(n ? n : 1, false); }

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
static void *vx_alloc_atomic(size_t n) { return vx_alloc_block(n ? n : 1, true); }

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
    fprintf(stderr, "gc: %zu collections, %zu KB allocated, %zu KB peak heap, %zu KB live\n",
        vx_gc_count, vx_gc_total / 1024, vx_gc_peak / 1024, vx_heap_bytes / 1024);
}

// Starts the collector; [base] is the top of the stack to scan.
static void vx_gc_init(void *base) {
    vx_stack_base = base;
    for (int c = 0, i = 0; i <= VX_SMALL_MAX / 16; i++) {
        while (vx_class_size[c] < (uint32_t)i * 16) c++;
        vx_class_of[i] = (uint8_t)c;
    }
    const char *stress = getenv("VEX_GC_STRESS");
    if (stress) vx_gc_stress = vx_gc_countdown = (size_t)strtoull(stress, NULL, 10);
    const char *stats = getenv("VEX_GC_STATS");
    if (stats && *stats && strcmp(stats, "0") != 0) atexit(vx_gc_report);
}

// --- Printing ---

// The structs being printed, so one that refers back to itself prints as
// Name(...) instead of recursing forever. An open-addressing set: entries
// are added and removed in stack order while printing nests.
static const void **vx_repr_set;
static size_t vx_repr_cap, vx_repr_count;

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

static size_t vx_stack_limit;
static char vx_signal_stack[64 * 1024];

// Turns a crash just below the stack into a readable error: it means the
// program ran out of stack, almost always through endless recursion.
static void vx_on_fault(int sig, siginfo_t *info, void *ctx) {
    (void)ctx;
    uintptr_t addr = (uintptr_t)info->si_addr, base = (uintptr_t)vx_stack_base;
    if (addr < base && base - addr <= vx_stack_limit + ((size_t)1 << 20)) {
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

static void vx_catch_stack_overflow(void) {
    struct rlimit rl;
    vx_stack_limit = getrlimit(RLIMIT_STACK, &rl) == 0 && rl.rlim_cur != RLIM_INFINITY
        ? (size_t)rl.rlim_cur : (size_t)1 << 30;
    stack_t ss = {.ss_sp = vx_signal_stack, .ss_size = sizeof(vx_signal_stack)};
    if (sigaltstack(&ss, NULL) != 0) return;
    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_sigaction = vx_on_fault;
    sa.sa_flags = SA_SIGINFO | SA_ONSTACK;
    sigemptyset(&sa.sa_mask);
    sigaction(SIGSEGV, &sa, NULL);
    sigaction(SIGBUS, &sa, NULL);
}

// Prepares the runtime; [base] is the top of the stack.
static void vx_init(void *base) {
    vx_gc_init(base);
    vx_catch_stack_overflow();
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

// Returns the number of characters (code points) in [s].
static int64_t vx_str_len(vx_str s) {
    int64_t n = 0;
    for (int64_t i = 0; i < s.len; i++) {
        if (((unsigned char)s.ptr[i] & 0xC0) != 0x80) n++;
    }
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

static vx_list *vx_list_new(int64_t esz, int64_t n, const void *items) {
    vx_list *l = vx_alloc(sizeof(vx_list));
    l->esz = esz;
    l->len = n;
    l->cap = n < 4 ? 4 : n;
    l->data = vx_alloc((size_t)(l->cap * esz));
    if (n > 0) memcpy(l->data, items, (size_t)(n * esz));
    return l;
}

// Returns a pointer to element [i], stopping the program if it is out of range.
static void *vx_list_at(vx_list *l, int64_t i, int line) {
    if (i < 0 || i >= l->len) {
        if (l->len == 0) vx_fail(line, "index %" PRId64 " is out of range: the list is empty", i);
        vx_fail(line, "index %" PRId64 " is out of range for a list of length %" PRId64, i, l->len);
    }
    return l->data + i * l->esz;
}

static void vx_list_push(vx_list *l, const void *item) {
    if (l->len == l->cap) {
        l->cap *= 2;
        l->data = vx_realloc(l->data, (size_t)(l->cap * l->esz));
    }
    memcpy(l->data + l->len * l->esz, item, (size_t)l->esz);
    l->len++;
}

// Removes the last element and returns a pointer to it, valid until the
// next push.
static void *vx_list_pop(vx_list *l, int line) {
    if (l->len == 0) vx_fail(line, "can't pop from an empty list");
    l->len--;
    return l->data + l->len * l->esz;
}

// Adds the elements of [src], which have the same size, to the end of [l].
static void vx_list_append(vx_list *l, vx_list *src) {
    int64_t n = src->len;
    if (l->len + n > l->cap) {
        while (l->len + n > l->cap) l->cap *= 2;
        l->data = vx_realloc(l->data, (size_t)(l->cap * l->esz));
    }
    if (n > 0) memcpy(l->data + l->len * l->esz, src->data, (size_t)(n * l->esz));
    l->len += n;
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
    for (; b < s.len; b++) {
        if (((unsigned char)s.ptr[b] & 0xC0) != 0x80) {
            if (i == 0) return b;
            i--;
        }
    }
    return b;
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
        int64_t j = i + 1;
        while (j < s.len && ((unsigned char)s.ptr[j] & 0xC0) == 0x80) j++;
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
enum { VX_KEY_INT, VX_KEY_BOOL, VX_KEY_STR, VX_KEY_ENUM };

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
    int32_t *index;    // -1 empty, -2 removed, otherwise an entry number
    char *entries;
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

static inline uint64_t vx_key_hash(int kind, const void *k) {
    switch (kind) {
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

static inline bool vx_key_eq(int kind, const void *a, const void *b) {
    switch (kind) {
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
            if (*(uint64_t *)ent == h && vx_key_eq(m->kind, ent + VX_MAP_KEY, key)) return (int64_t)i;
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
}

// Returns a pointer to the value of [key], or NULL if it is missing.
static inline void *vx_map_find(vx_map *m, const void *key) {
    int64_t s = vx_map_slot(m, key, vx_key_hash(m->kind, key));
    return s < 0 ? NULL : VX_MAP_ENTRY(m, m->index[s]) + m->voff;
}

// Returns a pointer to the value of [key], adding the key with a zero value
// at the end when it is missing.
static void *vx_map_set(vx_map *m, const void *key) {
    uint64_t h = vx_key_hash(m->kind, key);
    int64_t s = vx_map_slot(m, key, h);
    if (s >= 0) return VX_MAP_ENTRY(m, m->index[s]) + m->voff;
    if (m->used == m->cap) vx_map_grow(m);
    size_t i = h & (size_t)m->mask;
    while (m->index[i] >= 0) i = (i + 1) & (size_t)m->mask;
    char *e = VX_MAP_ENTRY(m, m->used);
    ((uint64_t *)e)[0] = h;
    VX_MAP_SEQ(e) = m->next_seq++;
    memcpy(e + VX_MAP_KEY, key, (size_t)m->ksz);
    m->index[i] = (int32_t)m->used++;
    m->len++;
    return e + m->voff;
}

// Removes [key], copying its value to [out]. Returns false if it was
// missing.
static bool vx_map_remove(vx_map *m, const void *key, void *out) {
    int64_t s = vx_map_slot(m, key, vx_key_hash(m->kind, key));
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

static int64_t vx_str_to_int(vx_str s, int line) {
    int64_t n;
    char *c = vx_cstr_trim(s, &n), *end;
    char *digits = c + (*c == '-' || *c == '+');
    if (*digits < '0' || *digits > '9') {
        vx_fail(line, "can't convert \"%.*s\" to int", (int)s.len, s.ptr);
    }
    errno = 0;
    long long v = strtoll(c, &end, 10);
    // Checking against the length also catches a NUL inside the text.
    if (end != c + n) vx_fail(line, "can't convert \"%.*s\" to int", (int)s.len, s.ptr);
    if (errno == ERANGE) vx_fail(line, "can't convert \"%s\" to int: the number is too big", c);
    return (int64_t)v;
}

static double vx_str_to_float(vx_str s, int line) {
    int64_t n;
    char *c = vx_cstr_trim(s, &n), *end;
    // Only decimal numbers: strtod would also take hex, nan and inf.
    bool digit = false;
    for (int64_t i = 0; i < n; i++) {
        if (c[i] >= '0' && c[i] <= '9') digit = true;
        else if (!strchr(".eE+-", c[i]) || c[i] == 0) goto bad;
    }
    if (!digit) goto bad;
    double v = strtod(c, &end);
    if (end != c + n || isinf(v)) goto bad;
    return v;
bad:
    vx_fail(line, "can't convert \"%.*s\" to float", (int)s.len, s.ptr);
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

// --- Files and the system ---
//
// Helpers behind vex:fs and vex:os. Functions that can fail return NULL,
// false or -1 and keep errno for vx_os_error.

static int vx_errno;
static int vx_argc;
static char **vx_argv;

static const char *vx_os_error(void) { return strerror(vx_errno); }

static const char *vx_fs_read(const char *path) {
    FILE *f = fopen(path, "rb");
    if (!f) { vx_errno = errno; return NULL; }
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
    if (failed) return NULL;
    buf[len] = 0;
    return buf;
}

static bool vx_fs_write(const char *path, const char *text, bool append) {
    FILE *f = fopen(path, append ? "ab" : "wb");
    if (!f) { vx_errno = errno; return false; }
    size_t n = strlen(text);
    bool ok = fwrite(text, 1, n, f) == n;
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
