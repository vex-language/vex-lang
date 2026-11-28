// The parts of vex:crypto that come from the operating system's own
// cryptography library: CommonCrypto, CryptoKit (through cryptokit.swift)
// and Security on Apple systems, CNG on Windows, and OpenSSL 3's libcrypto
// elsewhere. Each function returns 0 on success, -1 when it fails or the
// input is rejected, and -2 when the system has no provider yet. Kept apart from the generated C so system headers
// never meet program names.

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#if defined(__APPLE__) && !defined(VX_CRYPTO_OPENSSL)
#define VXC_APPLE 1
#elif defined(_WIN32)
#define VXC_WINDOWS 1
#else
#define VXC_OPENSSL 1
#endif

#if defined(__APPLE__)
#include <stdlib.h>
#elif defined(__linux__)
#include <errno.h>
#include <sys/random.h>
#include <unistd.h>
#elif defined(_WIN32)
#include <limits.h>
#include <windows.h>
#include <bcrypt.h>
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
#elif defined(_WIN32)
    while (n > 0) {
        ULONG k = n > 0x40000000 ? 0x40000000 : (ULONG)n;
        if (!BCRYPT_SUCCESS(BCryptGenRandom(NULL, out, k, BCRYPT_USE_SYSTEM_PREFERRED_RNG))) return -1;
        out += k;
        n -= k;
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

// AES-GCM and HKDF from CryptoKit, in cryptokit.swift.
int vxc_ck_gcm_seal(const uint8_t *key, const uint8_t *iv, long ivlen, const uint8_t *aad, long aadlen,
                    const uint8_t *in, long n, uint8_t *out, uint8_t *tag);
int vxc_ck_gcm_open(const uint8_t *key, const uint8_t *iv, long ivlen, const uint8_t *aad, long aadlen,
                    const uint8_t *in, long n, const uint8_t *tag, uint8_t *out);
int vxc_ck_hkdf_sha256(const uint8_t *ikm, long ikmlen, const uint8_t *salt, long saltlen,
                       const uint8_t *info, long infolen, uint8_t *out, long n);

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

int vxc_hkdf_sha256(const uint8_t *ikm, size_t ikmlen, const uint8_t *salt, size_t saltlen,
                    const uint8_t *info, size_t infolen, uint8_t *out, size_t n) {
    return vxc_ck_hkdf_sha256(ikm, (long)ikmlen, salt, (long)saltlen, info, (long)infolen, out, (long)n);
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
#include <openssl/kdf.h>
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

int vxc_hkdf_sha256(const uint8_t *ikm, size_t ikmlen, const uint8_t *salt, size_t saltlen,
                    const uint8_t *info, size_t infolen, uint8_t *out, size_t n) {
    EVP_KDF *kdf = EVP_KDF_fetch(NULL, "HKDF", NULL);
    EVP_KDF_CTX *ctx = kdf ? EVP_KDF_CTX_new(kdf) : NULL;
    EVP_KDF_free(kdf);
    if (!ctx) return -1;
    OSSL_PARAM params[5], *p = params;
    *p++ = OSSL_PARAM_construct_utf8_string(OSSL_KDF_PARAM_DIGEST, "SHA256", 0);
    *p++ = OSSL_PARAM_construct_octet_string(OSSL_KDF_PARAM_KEY, (void *)VXC_PTR(ikm, ikmlen), ikmlen);
    if (saltlen) *p++ = OSSL_PARAM_construct_octet_string(OSSL_KDF_PARAM_SALT, (void *)salt, saltlen);
    if (infolen) *p++ = OSSL_PARAM_construct_octet_string(OSSL_KDF_PARAM_INFO, (void *)info, infolen);
    *p = OSSL_PARAM_construct_end();
    int ok = EVP_KDF_derive(ctx, out, n, params) == 1;
    EVP_KDF_CTX_free(ctx);
    return ok ? 0 : -1;
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

#elif VXC_WINDOWS

#ifndef BCRYPT_HKDF_ALGORITHM
#define BCRYPT_HKDF_ALGORITHM L"HKDF"
#define BCRYPT_HKDF_HASH_ALGORITHM L"HkdfHashAlgorithm"
#define BCRYPT_HKDF_SALT_AND_FINALIZE L"HkdfSaltAndFinalize"
#define BCRYPT_HKDF_PRK_AND_FINALIZE L"HkdfPrkAndFinalize"
#endif
#ifndef KDF_HKDF_INFO
#define KDF_HKDF_INFO 0x14
#endif

// The CNG algorithms used here, each opened once and kept.
enum { VXC_SHA256, VXC_SHA1, VXC_MD5, VXC_HMAC, VXC_HKDF, VXC_GCM, VXC_ECDSA, VXC_ALGS };

static BCRYPT_ALG_HANDLE vxc_algs[VXC_ALGS];

// Returns the provider of the algorithm [which], opening it the first
// time, or NULL when the system has none.
static BCRYPT_ALG_HANDLE vxc_alg(int which) {
    BCRYPT_ALG_HANDLE h = InterlockedCompareExchangePointer(&vxc_algs[which], NULL, NULL);
    if (h) return h;
    static const LPCWSTR names[] = {BCRYPT_SHA256_ALGORITHM, BCRYPT_SHA1_ALGORITHM, BCRYPT_MD5_ALGORITHM,
                                    BCRYPT_SHA256_ALGORITHM, BCRYPT_HKDF_ALGORITHM, BCRYPT_AES_ALGORITHM,
                                    BCRYPT_ECDSA_P256_ALGORITHM};
    ULONG flags = which == VXC_HMAC ? BCRYPT_ALG_HANDLE_HMAC_FLAG : 0;
    if (!BCRYPT_SUCCESS(BCryptOpenAlgorithmProvider(&h, names[which], NULL, flags))) return NULL;
    if (which == VXC_GCM &&
        !BCRYPT_SUCCESS(BCryptSetProperty(h, BCRYPT_CHAINING_MODE, (PUCHAR)BCRYPT_CHAIN_MODE_GCM,
                                          sizeof(BCRYPT_CHAIN_MODE_GCM), 0))) {
        BCryptCloseAlgorithmProvider(h, 0);
        return NULL;
    }
    // Another thread may have opened it first; keep that one.
    BCRYPT_ALG_HANDLE was = InterlockedCompareExchangePointer(&vxc_algs[which], h, NULL);
    if (was) {
        BCryptCloseAlgorithmProvider(h, 0);
        return was;
    }
    return h;
}

// CNG lengths are 32-bit, so longer input is fed in pieces.
#define VXC_CHUNK ((ULONG)1 << 30)

// Writes the digest, or the HMAC under [key] when [key] isn't NULL, of the
// [n] bytes at [data] to the [outlen] bytes at [out].
static int vxc_hash(int which, const uint8_t *key, size_t keylen, const uint8_t *data, size_t n, uint8_t *out,
                    ULONG outlen) {
    BCRYPT_ALG_HANDLE alg = vxc_alg(which);
    if (!alg || keylen > ULONG_MAX) return -1;
    // An empty HMAC key is the same as a zero byte, since keys are padded
    // with zeros, and CNG refuses an empty one.
    static const uint8_t zero[1];
    if (key && keylen == 0) {
        key = zero;
        keylen = 1;
    }
    BCRYPT_HASH_HANDLE h;
    if (!BCRYPT_SUCCESS(BCryptCreateHash(alg, &h, NULL, 0, (PUCHAR)key, (ULONG)keylen, 0))) return -1;
    bool ok = true;
    for (size_t done = 0; ok && done < n; done += VXC_CHUNK) {
        ULONG k = n - done < VXC_CHUNK ? (ULONG)(n - done) : VXC_CHUNK;
        ok = BCRYPT_SUCCESS(BCryptHashData(h, (PUCHAR)data + done, k, 0));
    }
    ok = ok && BCRYPT_SUCCESS(BCryptFinishHash(h, out, outlen, 0));
    BCryptDestroyHash(h);
    return ok ? 0 : -1;
}

int vxc_sha256(const uint8_t *data, size_t n, uint8_t out[32]) { return vxc_hash(VXC_SHA256, NULL, 0, data, n, out, 32); }
int vxc_sha1(const uint8_t *data, size_t n, uint8_t out[20]) { return vxc_hash(VXC_SHA1, NULL, 0, data, n, out, 20); }
int vxc_md5(const uint8_t *data, size_t n, uint8_t out[16]) { return vxc_hash(VXC_MD5, NULL, 0, data, n, out, 16); }

int vxc_hmac_sha256(const uint8_t *key, size_t keylen, const uint8_t *data, size_t n, uint8_t out[32]) {
    return vxc_hash(VXC_HMAC, VXC_PTR(key, keylen), keylen, data, n, out, 32);
}

int vxc_pbkdf2_sha256(const uint8_t *pw, size_t pwlen, const uint8_t *salt, size_t saltlen,
                      uint32_t rounds, uint8_t *out, size_t outlen) {
    BCRYPT_ALG_HANDLE alg = vxc_alg(VXC_HMAC);
    if (!alg || pwlen > ULONG_MAX || saltlen > ULONG_MAX || outlen > ULONG_MAX) return -1;
    // The password is the HMAC key, so an empty one is a zero byte too.
    static const uint8_t zero[1];
    if (pwlen == 0) {
        pw = zero;
        pwlen = 1;
    }
    NTSTATUS st = BCryptDeriveKeyPBKDF2(alg, (PUCHAR)pw, (ULONG)pwlen, (PUCHAR)VXC_PTR(salt, saltlen), (ULONG)saltlen,
                                        rounds, out, (ULONG)outlen, 0);
    return BCRYPT_SUCCESS(st) ? 0 : -1;
}

int vxc_hkdf_sha256(const uint8_t *ikm, size_t ikmlen, const uint8_t *salt, size_t saltlen,
                    const uint8_t *info, size_t infolen, uint8_t *out, size_t n) {
    BCRYPT_ALG_HANDLE alg = vxc_alg(VXC_HKDF);
    if (!alg) return -2;
    if (ikmlen > ULONG_MAX || saltlen > ULONG_MAX || infolen > ULONG_MAX || n > ULONG_MAX) return -1;
    // RFC 5869 reads a missing salt as 32 zero bytes, which CNG needs
    // spelled out.
    static const uint8_t zeros[32];
    if (saltlen == 0) {
        salt = zeros;
        saltlen = sizeof zeros;
    }
    // CNG refuses an empty input key, so then the extract step is done
    // here with HMAC and CNG is handed the result.
    uint8_t prk[32];
    bool expand_only = ikmlen == 0;
    if (expand_only) {
        if (vxc_hmac_sha256(salt, saltlen, NULL, 0, prk) != 0) return -1;
        ikm = prk;
        ikmlen = sizeof prk;
    }
    BCRYPT_KEY_HANDLE key;
    NTSTATUS st = BCryptGenerateSymmetricKey(alg, &key, NULL, 0, (PUCHAR)ikm, (ULONG)ikmlen, 0);
    vxc_wipe(prk, sizeof prk);
    if (!BCRYPT_SUCCESS(st)) return -1;
    bool ok = BCRYPT_SUCCESS(BCryptSetProperty(key, BCRYPT_HKDF_HASH_ALGORITHM, (PUCHAR)BCRYPT_SHA256_ALGORITHM,
                                               sizeof(BCRYPT_SHA256_ALGORITHM), 0));
    if (ok && expand_only) {
        ok = BCRYPT_SUCCESS(BCryptSetProperty(key, BCRYPT_HKDF_PRK_AND_FINALIZE, NULL, 0, 0));
    } else if (ok) {
        ok = BCRYPT_SUCCESS(BCryptSetProperty(key, BCRYPT_HKDF_SALT_AND_FINALIZE, (PUCHAR)salt, (ULONG)saltlen, 0));
    }
    BCryptBuffer param = {(ULONG)infolen, KDF_HKDF_INFO, (PVOID)info};
    BCryptBufferDesc params = {BCRYPTBUFFER_VERSION, 1, &param};
    ULONG got = 0;
    ok = ok && BCRYPT_SUCCESS(BCryptKeyDerivation(key, infolen ? &params : NULL, out, (ULONG)n, &got, 0)) && got == n;
    BCryptDestroyKey(key);
    return ok ? 0 : -1;
}

static int vxc_gcm(bool seal, const uint8_t key[32], const uint8_t *iv, size_t ivlen, const uint8_t *aad, size_t aadlen,
                   const uint8_t *in, size_t n, uint8_t *out, uint8_t tag[16]) {
    BCRYPT_ALG_HANDLE alg = vxc_alg(VXC_GCM);
    if (!alg) return -1;
    // CNG takes only 96-bit nonces, the size NIST recommends.
    if (ivlen != 12) return -2;
    if (aadlen > ULONG_MAX) return -1;
    BCRYPT_KEY_HANDLE k;
    if (!BCRYPT_SUCCESS(BCryptGenerateSymmetricKey(alg, &k, NULL, 0, (PUCHAR)key, 32, 0))) return -1;
    uint8_t mac[16] = {0}, counter[16] = {0}, spare[1];
    BCRYPT_AUTHENTICATED_CIPHER_MODE_INFO info;
    BCRYPT_INIT_AUTH_MODE_INFO(info);
    info.pbNonce = (PUCHAR)iv;
    info.cbNonce = 12;
    info.pbAuthData = (PUCHAR)VXC_PTR(aad, aadlen);
    info.cbAuthData = (ULONG)aadlen;
    info.pbTag = tag;
    info.cbTag = 16;
    info.pbMacContext = mac;
    info.cbMacContext = sizeof mac;
    bool ok = true;
    size_t done = 0;
    do {
        ULONG len = n - done < VXC_CHUNK ? (ULONG)(n - done) : VXC_CHUNK;
        bool last = done + len == n;
        if (last) {
            info.dwFlags &= ~BCRYPT_AUTH_MODE_CHAIN_CALLS_FLAG;
        } else {
            info.dwFlags |= BCRYPT_AUTH_MODE_CHAIN_CALLS_FLAG;
        }
        ULONG wrote = 0;
        PUCHAR src = (PUCHAR)VXC_PTR(in + done, len), dst = len ? out + done : spare;
        NTSTATUS st = seal ? BCryptEncrypt(k, src, len, &info, counter, sizeof counter, dst, len, &wrote, 0)
                           : BCryptDecrypt(k, src, len, &info, counter, sizeof counter, dst, len, &wrote, 0);
        ok = BCRYPT_SUCCESS(st) && wrote == len;
        done += len;
    } while (ok && done < n);
    BCryptDestroyKey(k);
    vxc_wipe(mac, sizeof mac);
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
// when [priv] isn't NULL, the private scalar [priv], or NULL when CNG
// refuses them.
static BCRYPT_KEY_HANDLE vxc_p256_key(const uint8_t pub[65], const uint8_t *priv) {
    BCRYPT_ALG_HANDLE alg = vxc_alg(VXC_ECDSA);
    if (!alg || pub[0] != 4) return NULL;
    struct {
        BCRYPT_ECCKEY_BLOB head;
        uint8_t key[96];
    } blob = {{priv ? BCRYPT_ECDSA_PRIVATE_P256_MAGIC : BCRYPT_ECDSA_PUBLIC_P256_MAGIC, 32}, {0}};
    memcpy(blob.key, pub + 1, 64);
    if (priv) memcpy(blob.key + 64, priv, 32);
    BCRYPT_KEY_HANDLE key = NULL;
    NTSTATUS st = BCryptImportKeyPair(alg, NULL, priv ? BCRYPT_ECCPRIVATE_BLOB : BCRYPT_ECCPUBLIC_BLOB, &key,
                                      (PUCHAR)&blob, sizeof blob.head + (priv ? 96 : 64), 0);
    vxc_wipe(&blob, sizeof blob);
    return BCRYPT_SUCCESS(st) ? key : NULL;
}

int vxc_p256_check(const uint8_t pub[65]) {
    BCRYPT_KEY_HANDLE key = vxc_p256_key(pub, NULL);
    if (!key) return -1;
    BCryptDestroyKey(key);
    return 0;
}

// Copies the DER INTEGER at [*p] to the 32 bytes at [out], right-aligned,
// moving [*p] past it. Returns false when it doesn't fit in 32 bytes. The
// encoding has been checked already.
static bool vxc_der_scalar(const uint8_t **p, uint8_t out[32]) {
    size_t n = (*p)[1];
    const uint8_t *c = *p + 2;
    *p = c + n;
    while (n > 0 && *c == 0) {
        c++;
        n--;
    }
    if (n > 32) return false;
    memset(out, 0, 32);
    memcpy(out + 32 - n, c, n);
    return true;
}

int vxc_p256_verify(const uint8_t pub[65], const uint8_t *msg, size_t n, const uint8_t *sig, size_t siglen) {
    // CNG takes r||s; the DER form was checked before this is called.
    uint8_t rs[64], digest[32];
    const uint8_t *p = sig + 2;
    if (siglen < 8 || !vxc_der_scalar(&p, rs) || !vxc_der_scalar(&p, rs + 32)) return -1;
    if (vxc_sha256(msg, n, digest) != 0) return -1;
    BCRYPT_KEY_HANDLE key = vxc_p256_key(pub, NULL);
    if (!key) return -1;
    NTSTATUS st = BCryptVerifySignature(key, NULL, digest, 32, rs, 64, 0);
    BCryptDestroyKey(key);
    return BCRYPT_SUCCESS(st) ? 0 : -1;
}

int vxc_p256_generate(uint8_t out[97]) {
    BCRYPT_ALG_HANDLE alg = vxc_alg(VXC_ECDSA);
    BCRYPT_KEY_HANDLE key;
    if (!alg || !BCRYPT_SUCCESS(BCryptGenerateKeyPair(alg, &key, 256, 0))) return -1;
    struct {
        BCRYPT_ECCKEY_BLOB head;
        uint8_t key[96];
    } blob;
    ULONG len = 0;
    bool ok = BCRYPT_SUCCESS(BCryptFinalizeKeyPair(key, 0)) &&
              BCRYPT_SUCCESS(BCryptExportKey(key, NULL, BCRYPT_ECCPRIVATE_BLOB, (PUCHAR)&blob, sizeof blob, &len, 0)) &&
              len == sizeof blob && blob.head.cbKey == 32;
    BCryptDestroyKey(key);
    if (ok) {
        out[0] = 4;
        memcpy(out + 1, blob.key, 96);
    }
    vxc_wipe(&blob, sizeof blob);
    return ok ? 0 : -1;
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

// Appends the 32-byte big-endian [v] to [out] as a DER INTEGER, returning
// the bytes written.
static size_t vxc_der_put(uint8_t *out, const uint8_t v[32]) {
    size_t skip = 0;
    while (skip < 31 && v[skip] == 0) skip++;
    size_t n = 32 - skip;
    bool pad = v[skip] & 0x80;
    out[0] = 0x02;
    out[1] = (uint8_t)(n + pad);
    if (pad) out[2] = 0;
    memcpy(out + 2 + pad, v + skip, n);
    return 2 + pad + n;
}

int vxc_p256_sign(const uint8_t priv[97], const uint8_t *msg, size_t n, uint8_t *sig, size_t *siglen) {
    uint8_t digest[32], rs[64];
    if (vxc_sha256(msg, n, digest) != 0) return -1;
    BCRYPT_KEY_HANDLE key = vxc_p256_key(priv, priv + 65);
    if (!key) return -1;
    ULONG len = 0;
    NTSTATUS st = BCryptSignHash(key, NULL, digest, 32, rs, 64, &len, 0);
    BCryptDestroyKey(key);
    if (!BCRYPT_SUCCESS(st) || len != 64) return -1;
    size_t body = vxc_der_put(sig + 2, rs);
    body += vxc_der_put(sig + 2 + body, rs + 32);
    sig[0] = 0x30;
    sig[1] = (uint8_t)body;
    *siglen = body + 2;
    return 0;
}

#endif
