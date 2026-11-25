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
#include <string.h>
#include <sys/resource.h>
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

// The source file name used in runtime error messages.
static const char *vx_file = "main.vx";

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
    fprintf(stderr, "\n --> %s:%d\n", vx_file, line);
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

// Returns a string that owns a copy of [n] bytes at [p].
static vx_str vx_str_new(const char *p, int64_t n) {
    char *buf = vx_alloc_atomic((size_t)n + 1);
    memcpy(buf, p, (size_t)n);
    buf[n] = 0;
    return (vx_str){buf, n};
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

// --- Conversions ---

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
