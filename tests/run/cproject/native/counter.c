#include <stdbool.h>
#include <stdint.h>
#include "config.h"

static int64_t count = START;

int64_t counter_next(void) {
    count += STEP;
    return count;
}

bool counter_past(int64_t limit) { return count > limit; }

double average(double a, double b) { return (a + b) / 2; }
