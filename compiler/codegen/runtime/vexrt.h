// Runtime library compiled into every Vex program.
//
// Memory is never freed in v0.1; programs are expected to be short-lived.

#include <errno.h>
#include <inttypes.h>
#include <math.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

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

// --- Allocation ---

static void *vx_alloc(size_t n) {
    void *p = calloc(1, n ? n : 1);
    if (!p) {
        fputs("error: out of memory\n", stderr);
        exit(1);
    }
    return p;
}

static void *vx_realloc(void *p, size_t n) {
    p = realloc(p, n ? n : 1);
    if (!p) {
        fputs("error: out of memory\n", stderr);
        exit(1);
    }
    return p;
}

// --- Strings ---

// Returns a string that owns a copy of [n] bytes at [p].
static vx_str vx_str_new(const char *p, int64_t n) {
    char *buf = vx_alloc((size_t)n + 1);
    memcpy(buf, p, (size_t)n);
    return (vx_str){buf, n};
}

static vx_str vx_str_concat(vx_str a, vx_str b) {
    if (a.len == 0) return b;
    if (b.len == 0) return a;
    char *buf = vx_alloc((size_t)(a.len + b.len) + 1);
    memcpy(buf, a.ptr, (size_t)a.len);
    memcpy(buf + a.len, b.ptr, (size_t)b.len);
    return (vx_str){buf, a.len + b.len};
}

// Joins [n] strings, as used by string interpolation.
static vx_str vx_str_join(int n, const vx_str *parts) {
    int64_t total = 0;
    for (int i = 0; i < n; i++) total += parts[i].len;
    char *buf = vx_alloc((size_t)total + 1);
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
    char *buf = vx_alloc((size_t)s.len + 1);
    for (int64_t i = 0; i < s.len; i++) {
        char c = s.ptr[i];
        buf[i] = (c >= 'a' && c <= 'z') ? (char)(c - 32) : c;
    }
    return (vx_str){buf, s.len};
}

static vx_str vx_str_lower(vx_str s) {
    char *buf = vx_alloc((size_t)s.len + 1);
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
    char *buf = vx_alloc((size_t)s.len * 2 + 3);
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

// Copies [s] into a NUL-terminated buffer, trimming surrounding spaces.
static char *vx_cstr_trim(vx_str s) {
    int64_t a = 0, b = s.len;
    while (a < b && (s.ptr[a] == ' ' || s.ptr[a] == '\t' || s.ptr[a] == '\n' || s.ptr[a] == '\r')) a++;
    while (b > a && (s.ptr[b - 1] == ' ' || s.ptr[b - 1] == '\t' || s.ptr[b - 1] == '\n' || s.ptr[b - 1] == '\r')) b--;
    char *buf = vx_alloc((size_t)(b - a) + 1);
    memcpy(buf, s.ptr + a, (size_t)(b - a));
    return buf;
}

static int64_t vx_str_to_int(vx_str s, int line) {
    char *c = vx_cstr_trim(s), *end;
    char *digits = c + (*c == '-' || *c == '+');
    if (*digits < '0' || *digits > '9') {
        vx_fail(line, "can't convert \"%.*s\" to int", (int)s.len, s.ptr);
    }
    errno = 0;
    long long v = strtoll(c, &end, 10);
    if (*end != 0) vx_fail(line, "can't convert \"%.*s\" to int", (int)s.len, s.ptr);
    if (errno == ERANGE) vx_fail(line, "can't convert \"%s\" to int: the number is too big", c);
    return (int64_t)v;
}

static double vx_str_to_float(vx_str s, int line) {
    char *c = vx_cstr_trim(s), *end;
    if (*c == 0) goto bad;
    double v = strtod(c, &end);
    if (*end != 0) goto bad;
    return v;
bad:
    vx_fail(line, "can't convert \"%.*s\" to float", (int)s.len, s.ptr);
}

static int64_t vx_float_to_int(double v, int line) {
    if (isnan(v) || v >= 9223372036854775808.0 || v < -9223372036854775808.0) {
        vx_fail(line, "can't convert %s to int: it's out of range", vx_float_str(v).ptr);
    }
    return (int64_t)v;
}

// --- Arithmetic ---

static int64_t vx_div(int64_t a, int64_t b, int line) {
    if (b == 0) vx_fail(line, "division by zero");
    if (b == -1) return (int64_t)(0 - (uint64_t)a);
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
    if (n <= 0) return VX_LIT("");
    if (n > 0 && line[n - 1] == '\n') n--;
    if (n > 0 && line[n - 1] == '\r') n--;
    return (vx_str){line, (int64_t)n};
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
