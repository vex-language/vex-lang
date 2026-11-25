#import <Foundation/Foundation.h>

void *str_make(const char *s) {
    return (__bridge_retained void *)[NSMutableString stringWithUTF8String:s];
}
void str_append(void *h, const char *s) {
    [(__bridge NSMutableString *)h appendString:[NSString stringWithUTF8String:s]];
}
const char *str_upper(void *h) {
    return [[(__bridge NSMutableString *)h uppercaseString] UTF8String];
}
long long str_len(void *h) { return [(__bridge NSMutableString *)h length]; }
void str_free(void *h) { CFRelease(h); }
