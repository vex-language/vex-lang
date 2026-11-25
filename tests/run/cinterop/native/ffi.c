#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct { int32_t count; char name[32]; } Counter;

Counter *counter_new(const char *name) {
    Counter *c = calloc(1, sizeof *c);
    strncpy(c->name, name, 31);
    return c;
}
void counter_add(Counter *c, int32_t n) { c->count += n; }
int32_t counter_get(Counter *c) { return c->count; }
const char *counter_name(Counter *c) { return c->name; }
Counter *counter_find(int32_t id) { return NULL; }
void counter_free(Counter *c) { free(c); }
uint8_t low_byte(int32_t x) { return (uint8_t)x; }
float halve(float x) { return x / 2; }

void vex_release(void *ctx);
static int64_t (*saved_cb)(void *, int64_t);
static void *saved_ctx;

void each(int32_t n, void (*cb)(void *, int64_t, const char *), void *ctx) {
    for (int32_t i = 0; i < n; i++) cb(ctx, i, i % 2 ? "odd" : "even");
    vex_release(ctx);
}
void store(int64_t (*cb)(void *, int64_t), void *ctx) { saved_cb = cb; saved_ctx = ctx; }
int64_t fire(int64_t x) { return saved_cb(saved_ctx, x); }

int64_t vex_twice(int64_t x);
int32_t vex_greet(const char *name, Counter *c);
int64_t call_back_in(void) { return vex_twice(21); }
int32_t greet_in(Counter *c) { return vex_greet("from C", c); }
