#include <stddef.h>
#include <stdint.h>
#include <string.h>

static char buf[64];

const char *shout(const char *s) {
    size_t i = 0;
    for (; s[i] && i < sizeof buf - 2; i++) {
        buf[i] = (s[i] >= 'a' && s[i] <= 'z') ? s[i] - 32 : s[i];
    }
    buf[i] = '!';
    buf[i + 1] = 0;
    return buf;
}

const char *find_word(const char *s, int64_t n) {
    return n == 1 ? s : NULL;
}

void log_line(const char *s) { (void)s; }
