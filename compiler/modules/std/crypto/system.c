// The parts of vex:crypto that come from the operating system's own
// cryptography library: CommonCrypto, CryptoKit (through cryptokit.swift)
// and Security on Apple systems, and
// OpenSSL 3's libcrypto elsewhere. Each function returns 0 on success,
// -1 when it fails or the input is rejected, and -2 when the system has
// no provider yet. Kept apart from the generated C so system headers
// never meet program names.

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#if defined(__APPLE__) && !defined(VX_CRYPTO_OPENSSL)
#define VXC_APPLE 1
#elif !defined(_WIN32)
#define VXC_OPENSSL 1
#endif

#if defined(__APPLE__)
#include <stdlib.h>
#elif defined(__linux__)
#include <errno.h>
#include <sys/random.h>
#include <unistd.h>
#endif

// Fills [out] with [n] bytes from the operating system's secure random
// source.
int vxc_random(uint8_t *out, size_t n) {
#if defined(__APPLE__)
    arc4random_buf(out, n);
    return 0;
#elif defined(__linux__)
    while (n > 0) {
        ssize_t got = getrandom(out, n, 0);
        if (got < 0) {
            if (errno == EINTR) continue;
            return -1;
        }
        out += got;
        n -= (size_t)got;
    }
    return 0;
#else
    (void)out;
    (void)n;
    return -2;
#endif
}

// A byte to point at for empty input, since some providers reject NULL.
static const uint8_t vxc_empty[1];

#define VXC_PTR(p, n) ((n) ? (const void *)(p) : (const void *)vxc_empty)

// Zeroes the [n] bytes at [p] in a way the compiler can't leave out.
__attribute__((unused)) static void vxc_wipe(void *p, size_t n) {
    volatile uint8_t *q = p;
    while (n--) *q++ = 0;
}

#if VXC_APPLE

#include <CommonCrypto/CommonCryptor.h>
#include <CommonCrypto/CommonDigest.h>
#include <CommonCrypto/CommonHMAC.h>
#include <CommonCrypto/CommonKeyDerivation.h>
#include <Security/Security.h>

// AES-GCM from CryptoKit, in cryptokit.swift.
int vxc_ck_gcm_seal(const uint8_t *key, const uint8_t *iv, long ivlen, const uint8_t *aad, long aadlen,
                    const uint8_t *in, long n, uint8_t *out, uint8_t *tag);
int vxc_ck_gcm_open(const uint8_t *key, const uint8_t *iv, long ivlen, const uint8_t *aad, long aadlen,
                    const uint8_t *in, long n, const uint8_t *tag, uint8_t *out);

// CommonDigest takes 32-bit lengths, so longer input is fed in pieces.
#define VXC_CHUNK ((size_t)1 << 30)

#define VXC_DIGEST(NAME, CTX, INIT, UPDATE, FINAL)                         \
    int NAME(const uint8_t *data, size_t n, uint8_t *out) {                \
        CTX ctx;                                                           \
        INIT(&ctx);                                                        \
        do {                                                               \
            size_t k = n < VXC_CHUNK ? n : VXC_CHUNK;                      \
            UPDATE(&ctx, VXC_PTR(data, k), (CC_LONG)k);                    \
            data += k;                                                     \
            n -= k;                                                        \
        } while (n > 0);                                                   \
        FINAL(out, &ctx);                                                  \
        vxc_wipe(&ctx, sizeof ctx);                         \
        return 0;                                                          \
    }

VXC_DIGEST(vxc_sha256, CC_SHA256_CTX, CC_SHA256_Init, CC_SHA256_Update, CC_SHA256_Final)
VXC_DIGEST(vxc_sha1, CC_SHA1_CTX, CC_SHA1_Init, CC_SHA1_Update, CC_SHA1_Final)
VXC_DIGEST(vxc_md5, CC_MD5_CTX, CC_MD5_Init, CC_MD5_Update, CC_MD5_Final)

int vxc_hmac_sha256(const uint8_t *key, size_t keylen, const uint8_t *data, size_t n, uint8_t out[32]) {
    CCHmacContext ctx;
    CCHmacInit(&ctx, kCCHmacAlgSHA256, VXC_PTR(key, keylen), keylen);
    CCHmacUpdate(&ctx, VXC_PTR(data, n), n);
    CCHmacFinal(&ctx, out);
    vxc_wipe(&ctx, sizeof ctx);
    return 0;
}

int vxc_pbkdf2_sha256(const uint8_t *pw, size_t pwlen, const uint8_t *salt, size_t saltlen,
                      uint32_t rounds, uint8_t *out, size_t outlen) {
    int r = CCKeyDerivationPBKDF(kCCPBKDF2, VXC_PTR(pw, pwlen), pwlen, VXC_PTR(salt, saltlen), saltlen,
                                 kCCPRFHmacAlgSHA256, rounds, out, outlen);
    return r == kCCSuccess ? 0 : -1;
}

int vxc_aes_gcm_seal(const uint8_t key[32], const uint8_t *iv, size_t ivlen, const uint8_t *aad, size_t aadlen,
                     const uint8_t *in, size_t n, uint8_t *out, uint8_t tag[16]) {
    return vxc_ck_gcm_seal(key, iv, (long)ivlen, aad, (long)aadlen, in, (long)n, out, tag);
}

int vxc_aes_gcm_open(const uint8_t key[32], const uint8_t *iv, size_t ivlen, const uint8_t *aad, size_t aadlen,
                     const uint8_t *in, size_t n, const uint8_t tag[16], uint8_t *out) {
    return vxc_ck_gcm_open(key, iv, (long)ivlen, aad, (long)aadlen, in, (long)n, tag, out);
}

// Returns the P-256 key held in [data] (a public key as 04||X||Y, or a
// private one as 04||X||Y||D), or NULL when it isn't one.
static SecKeyRef vxc_p256_key(const uint8_t *data, size_t n, bool private) {
    CFDataRef bytes = CFDataCreate(NULL, data, (CFIndex)n);
    const void *keys[] = {kSecAttrKeyType, kSecAttrKeyClass, kSecAttrKeySizeInBits};
    int bits = 256;
    CFNumberRef size = CFNumberCreate(NULL, kCFNumberIntType, &bits);
    const void *values[] = {kSecAttrKeyTypeECSECPrimeRandom, private ? kSecAttrKeyClassPrivate : kSecAttrKeyClassPublic, size};
    CFDictionaryRef attrs = CFDictionaryCreate(NULL, keys, values, 3, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    SecKeyRef key = SecKeyCreateWithData(bytes, attrs, NULL);
    CFRelease(attrs);
    CFRelease(size);
    CFRelease(bytes);
    return key;
}

int vxc_p256_check(const uint8_t pub[65]) {
    SecKeyRef key = vxc_p256_key(pub, 65, false);
    if (!key) return -1;
    CFRelease(key);
    return 0;
}

int vxc_p256_verify(const uint8_t pub[65], const uint8_t *msg, size_t n, const uint8_t *sig, size_t siglen) {
    SecKeyRef key = vxc_p256_key(pub, 65, false);
    if (!key) return -1;
    CFDataRef m = CFDataCreate(NULL, VXC_PTR(msg, n), (CFIndex)n);
    CFDataRef s = CFDataCreate(NULL, VXC_PTR(sig, siglen), (CFIndex)siglen);
    Boolean ok = SecKeyVerifySignature(key, kSecKeyAlgorithmECDSASignatureMessageX962SHA256, m, s, NULL);
    CFRelease(s);
    CFRelease(m);
    CFRelease(key);
    return ok ? 0 : -1;
}

int vxc_p256_generate(uint8_t out[97]) {
    const void *keys[] = {kSecAttrKeyType, kSecAttrKeySizeInBits};
    int bits = 256;
    CFNumberRef size = CFNumberCreate(NULL, kCFNumberIntType, &bits);
    const void *values[] = {kSecAttrKeyTypeECSECPrimeRandom, size};
    CFDictionaryRef attrs = CFDictionaryCreate(NULL, keys, values, 2, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    SecKeyRef key = SecKeyCreateRandomKey(attrs, NULL);
    CFRelease(attrs);
    CFRelease(size);
    if (!key) return -1;
    CFDataRef data = SecKeyCopyExternalRepresentation(key, NULL);
    CFRelease(key);
    if (!data) return -1;
    int r = -1;
    if (CFDataGetLength(data) == 97) {
        memcpy(out, CFDataGetBytePtr(data), 97);
        r = 0;
    }
    CFRelease(data);
    return r;
}

int vxc_p256_sign(const uint8_t priv[97], const uint8_t *msg, size_t n, uint8_t *sig, size_t *siglen);

int vxc_p256_check_private(const uint8_t priv[97]) {
    // The scalar must belong to the public point: a signature it makes
    // must check out under that point.
    static const uint8_t msg[] = "pairwise check";
    uint8_t sig[72];
    size_t len = 0;
    if (vxc_p256_sign(priv, msg, sizeof msg, sig, &len) != 0) return -1;
    return vxc_p256_verify(priv, msg, sizeof msg, sig, len);
}

int vxc_p256_sign(const uint8_t priv[97], const uint8_t *msg, size_t n, uint8_t *sig, size_t *siglen) {
    SecKeyRef key = vxc_p256_key(priv, 97, true);
    if (!key) return -1;
    CFDataRef m = CFDataCreate(NULL, VXC_PTR(msg, n), (CFIndex)n);
    CFDataRef s = SecKeyCreateSignature(key, kSecKeyAlgorithmECDSASignatureMessageX962SHA256, m, NULL);
    CFRelease(m);
    CFRelease(key);
    if (!s) return -1;
    int r = -1;
    CFIndex len = CFDataGetLength(s);
    if (len > 0 && len <= 72) {
        memcpy(sig, CFDataGetBytePtr(s), (size_t)len);
        *siglen = (size_t)len;
        r = 0;
    }
    CFRelease(s);
    return r;
}

#elif VXC_OPENSSL

#include <limits.h>
#include <openssl/bn.h>
#include <openssl/core_names.h>
#include <openssl/crypto.h>
#include <openssl/evp.h>
#include <openssl/hmac.h>
#include <openssl/param_build.h>

static int vxc_digest(const EVP_MD *md, const uint8_t *data, size_t n, uint8_t *out) {
    if (!md) return -1;
    return EVP_Digest(VXC_PTR(data, n), n, out, NULL, md, NULL) == 1 ? 0 : -1;
}

int vxc_sha256(const uint8_t *data, size_t n, uint8_t out[32]) { return vxc_digest(EVP_sha256(), data, n, out); }
int vxc_sha1(const uint8_t *data, size_t n, uint8_t out[20]) { return vxc_digest(EVP_sha1(), data, n, out); }
int vxc_md5(const uint8_t *data, size_t n, uint8_t out[16]) { return vxc_digest(EVP_md5(), data, n, out); }

int vxc_hmac_sha256(const uint8_t *key, size_t keylen, const uint8_t *data, size_t n, uint8_t out[32]) {
    if (keylen > INT_MAX) return -1;
    unsigned len = 0;
    if (!HMAC(EVP_sha256(), VXC_PTR(key, keylen), (int)keylen, VXC_PTR(data, n), n, out, &len)) return -1;
    return len == 32 ? 0 : -1;
}

int vxc_pbkdf2_sha256(const uint8_t *pw, size_t pwlen, const uint8_t *salt, size_t saltlen,
                      uint32_t rounds, uint8_t *out, size_t outlen) {
    if (pwlen > INT_MAX || saltlen > INT_MAX || outlen > INT_MAX || rounds > INT_MAX) return -1;
    int r = PKCS5_PBKDF2_HMAC(VXC_PTR(pw, pwlen), (int)pwlen, VXC_PTR(salt, saltlen), (int)saltlen,
                              (int)rounds, EVP_sha256(), (int)outlen, out);
    return r == 1 ? 0 : -1;
}

// EVP lengths are ints, so longer input is fed in pieces.
#define VXC_CHUNK ((size_t)1 << 30)

static int vxc_gcm(bool seal, const uint8_t key[32], const uint8_t *iv, size_t ivlen, const uint8_t *aad, size_t aadlen,
                   const uint8_t *in, size_t n, uint8_t *out, uint8_t tag[16]) {
    if (ivlen == 0 || ivlen > INT_MAX) return -1;
    EVP_CIPHER_CTX *ctx = EVP_CIPHER_CTX_new();
    if (!ctx) return -1;
    int ok = EVP_CipherInit_ex(ctx, EVP_aes_256_gcm(), NULL, NULL, NULL, seal) == 1 &&
             EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_SET_IVLEN, (int)ivlen, NULL) == 1 &&
             EVP_CipherInit_ex(ctx, NULL, NULL, key, iv, seal) == 1;
    int len;
    for (size_t done = 0; ok && done < aadlen; done += VXC_CHUNK) {
        size_t k = aadlen - done < VXC_CHUNK ? aadlen - done : VXC_CHUNK;
        ok = EVP_CipherUpdate(ctx, NULL, &len, aad + done, (int)k) == 1;
    }
    for (size_t done = 0; ok && done < n; done += VXC_CHUNK) {
        size_t k = n - done < VXC_CHUNK ? n - done : VXC_CHUNK;
        ok = EVP_CipherUpdate(ctx, out + done, &len, in + done, (int)k) == 1 && (size_t)len == k;
    }
    if (ok && !seal) ok = EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_SET_TAG, 16, (void *)tag) == 1;
    uint8_t spare[16];
    ok = ok && EVP_CipherFinal_ex(ctx, spare, &len) == 1;
    if (ok && seal) ok = EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_GET_TAG, 16, tag) == 1;
    EVP_CIPHER_CTX_free(ctx);
    return ok ? 0 : -1;
}

int vxc_aes_gcm_seal(const uint8_t key[32], const uint8_t *iv, size_t ivlen, const uint8_t *aad, size_t aadlen,
                     const uint8_t *in, size_t n, uint8_t *out, uint8_t tag[16]) {
    return vxc_gcm(true, key, iv, ivlen, aad, aadlen, in, n, out, tag);
}

int vxc_aes_gcm_open(const uint8_t key[32], const uint8_t *iv, size_t ivlen, const uint8_t *aad, size_t aadlen,
                     const uint8_t *in, size_t n, const uint8_t tag[16], uint8_t *out) {
    uint8_t t[16];
    memcpy(t, tag, 16);
    return vxc_gcm(false, key, iv, ivlen, aad, aadlen, in, n, out, t);
}

// Returns the P-256 key made of the public point [pub] (04||X||Y) and,
// when [priv] isn't NULL, the private scalar [priv], or NULL when they
// don't form a valid key.
static EVP_PKEY *vxc_p256_key(const uint8_t pub[65], const uint8_t *priv) {
    OSSL_PARAM_BLD *bld = OSSL_PARAM_BLD_new();
    BIGNUM *d = priv ? BN_secure_new() : NULL;
    OSSL_PARAM *params = NULL;
    EVP_PKEY_CTX *ctx = NULL;
    EVP_PKEY *key = NULL;
    bool ok = bld && (!priv || (d && BN_bin2bn(priv, 32, d))) &&
              OSSL_PARAM_BLD_push_utf8_string(bld, OSSL_PKEY_PARAM_GROUP_NAME, "prime256v1", 0) &&
              OSSL_PARAM_BLD_push_octet_string(bld, OSSL_PKEY_PARAM_PUB_KEY, pub, 65) &&
              (!priv || OSSL_PARAM_BLD_push_BN(bld, OSSL_PKEY_PARAM_PRIV_KEY, d));
    if (ok) params = OSSL_PARAM_BLD_to_param(bld);
    if (params) ctx = EVP_PKEY_CTX_new_from_name(NULL, "EC", NULL);
    if (ctx && EVP_PKEY_fromdata_init(ctx) == 1) {
        EVP_PKEY_fromdata(ctx, &key, priv ? EVP_PKEY_KEYPAIR : EVP_PKEY_PUBLIC_KEY, params);
    }
    EVP_PKEY_CTX_free(ctx);
    if (key) {
        // Checks that the point is on the curve, and for a private key
        // that it belongs to the scalar.
        ctx = EVP_PKEY_CTX_new_from_pkey(NULL, key, NULL);
        int valid = ctx && (priv ? EVP_PKEY_pairwise_check(ctx) : EVP_PKEY_public_check(ctx)) == 1;
        EVP_PKEY_CTX_free(ctx);
        if (!valid) {
            EVP_PKEY_free(key);
            key = NULL;
        }
    }
    OSSL_PARAM_free(params);
    OSSL_PARAM_BLD_free(bld);
    BN_clear_free(d);
    return key;
}

int vxc_p256_check(const uint8_t pub[65]) {
    EVP_PKEY *key = vxc_p256_key(pub, NULL);
    if (!key) return -1;
    EVP_PKEY_free(key);
    return 0;
}

int vxc_p256_verify(const uint8_t pub[65], const uint8_t *msg, size_t n, const uint8_t *sig, size_t siglen) {
    EVP_PKEY *key = vxc_p256_key(pub, NULL);
    if (!key) return -1;
    EVP_MD_CTX *ctx = EVP_MD_CTX_new();
    int ok = ctx && EVP_DigestVerifyInit(ctx, NULL, EVP_sha256(), NULL, key) == 1 &&
             EVP_DigestVerify(ctx, VXC_PTR(sig, siglen), siglen, VXC_PTR(msg, n), n) == 1;
    EVP_MD_CTX_free(ctx);
    EVP_PKEY_free(key);
    return ok ? 0 : -1;
}

int vxc_p256_generate(uint8_t out[97]) {
    EVP_PKEY *key = EVP_PKEY_Q_keygen(NULL, NULL, "EC", "P-256");
    if (!key) return -1;
    BIGNUM *d = NULL;
    size_t len = 0;
    int ok = EVP_PKEY_get_octet_string_param(key, OSSL_PKEY_PARAM_PUB_KEY, out, 65, &len) == 1 && len == 65 &&
             out[0] == 4 && EVP_PKEY_get_bn_param(key, OSSL_PKEY_PARAM_PRIV_KEY, &d) == 1 &&
             BN_bn2binpad(d, out + 65, 32) == 32;
    BN_clear_free(d);
    EVP_PKEY_free(key);
    if (!ok) OPENSSL_cleanse(out, 97);
    return ok ? 0 : -1;
}

int vxc_p256_check_private(const uint8_t priv[97]) {
    EVP_PKEY *key = vxc_p256_key(priv, priv + 65);
    if (!key) return -1;
    EVP_PKEY_free(key);
    return 0;
}

int vxc_p256_sign(const uint8_t priv[97], const uint8_t *msg, size_t n, uint8_t *sig, size_t *siglen) {
    EVP_PKEY *key = vxc_p256_key(priv, priv + 65);
    if (!key) return -1;
    EVP_MD_CTX *ctx = EVP_MD_CTX_new();
    size_t len = 72;
    int ok = ctx && EVP_DigestSignInit(ctx, NULL, EVP_sha256(), NULL, key) == 1 &&
             EVP_DigestSign(ctx, sig, &len, VXC_PTR(msg, n), n) == 1;
    EVP_MD_CTX_free(ctx);
    EVP_PKEY_free(key);
    if (ok) *siglen = len;
    return ok ? 0 : -1;
}

#else

int vxc_sha256(const uint8_t *data, size_t n, uint8_t *out) { return -2; }
int vxc_sha1(const uint8_t *data, size_t n, uint8_t *out) { return -2; }
int vxc_md5(const uint8_t *data, size_t n, uint8_t *out) { return -2; }
int vxc_hmac_sha256(const uint8_t *key, size_t keylen, const uint8_t *data, size_t n, uint8_t *out) { return -2; }
int vxc_pbkdf2_sha256(const uint8_t *pw, size_t pwlen, const uint8_t *salt, size_t saltlen,
                      uint32_t rounds, uint8_t *out, size_t outlen) { return -2; }
int vxc_aes_gcm_seal(const uint8_t *key, const uint8_t *iv, size_t ivlen, const uint8_t *aad, size_t aadlen,
                     const uint8_t *in, size_t n, uint8_t *out, uint8_t *tag) { return -2; }
int vxc_aes_gcm_open(const uint8_t *key, const uint8_t *iv, size_t ivlen, const uint8_t *aad, size_t aadlen,
                     const uint8_t *in, size_t n, const uint8_t *tag, uint8_t *out) { return -2; }
int vxc_p256_check(const uint8_t *pub) { return -2; }
int vxc_p256_verify(const uint8_t *pub, const uint8_t *msg, size_t n, const uint8_t *sig, size_t siglen) { return -2; }
int vxc_p256_generate(uint8_t *out) { return -2; }
int vxc_p256_check_private(const uint8_t *priv) { return -2; }
int vxc_p256_sign(const uint8_t *priv, const uint8_t *msg, size_t n, uint8_t *sig, size_t *siglen) { return -2; }

#endif
