
// --- Cryptography ---
//
// The runtime side of vex:crypto, added to programs that import it.
// Primitives come from Monocypher and from the system's provider, reached
// through the vxc_ functions; nothing here is a primitive of its own.
// Functions return NULL when they fail, with the reason in
// [vx_crypto_err]. Secrets held only for a moment are wiped.

#include "monocypher.h"
#include "monocypher-ed25519.h"

int vxc_random(uint8_t *out, size_t n);
int vxc_sha256(const uint8_t *data, size_t n, uint8_t out[32]);
int vxc_sha1(const uint8_t *data, size_t n, uint8_t out[20]);
int vxc_md5(const uint8_t *data, size_t n, uint8_t out[16]);
int vxc_hmac_sha256(const uint8_t *key, size_t keylen, const uint8_t *data, size_t n, uint8_t out[32]);
int vxc_hkdf_sha256(const uint8_t *ikm, size_t ikmlen, const uint8_t *salt, size_t saltlen,
                    const uint8_t *info, size_t infolen, uint8_t *out, size_t n);
int vxc_pbkdf2_sha256(const uint8_t *pw, size_t pwlen, const uint8_t *salt, size_t saltlen,
                      uint32_t rounds, uint8_t *out, size_t outlen);
int vxc_aes_gcm_seal(const uint8_t key[32], const uint8_t *iv, size_t ivlen, const uint8_t *aad, size_t aadlen,
                     const uint8_t *in, size_t n, uint8_t *out, uint8_t tag[16]);
int vxc_aes_gcm_open(const uint8_t key[32], const uint8_t *iv, size_t ivlen, const uint8_t *aad, size_t aadlen,
                     const uint8_t *in, size_t n, const uint8_t tag[16], uint8_t *out);
int vxc_p256_check(const uint8_t pub[65]);
int vxc_p256_verify(const uint8_t pub[65], const uint8_t *msg, size_t n, const uint8_t *sig, size_t siglen);
int vxc_p256_generate(uint8_t out[97]);
int vxc_p256_check_private(const uint8_t priv[97]);
int vxc_p256_sign(const uint8_t priv[97], const uint8_t *msg, size_t n, uint8_t *sig, size_t *siglen);

static VX_TLS const char *vx_crypto_err = "";

#define VX_CB(l) ((uint8_t *)(l)->data)
#define VX_CN(l) ((size_t)(l)->len)

// Returns [n] zero bytes.
static vx_list *vx_crypto_new(size_t n) {
    vx_list *b = vx_list_make(1, 0, (int64_t)n, NULL, true);
    memset(b->data, 0, n);
    b->len = (int64_t)n;
    return b;
}

// Returns NULL, recording [why].
static vx_list *vx_crypto_fail(const char *why) {
    vx_crypto_err = why;
    return NULL;
}

// Returns [out] when the provider's result [rc] is success. Otherwise
// wipes [out] and returns NULL with the reason recorded.
static vx_list *vx_crypto_result(int rc, vx_list *out) {
    if (rc == 0) return out;
    crypto_wipe(out->data, VX_CN(out));
    return vx_crypto_fail(rc == -2 ? "not available on this system yet" : "the system's cryptography library failed");
}

static vx_str vx_crypto_error(void) { return vx_str_from_c(vx_crypto_err); }

static vx_list *vx_crypto_random(int64_t n) {
    if (n < 0) return vx_crypto_fail("needs a count of 0 or more");
    vx_list *out = vx_crypto_new((size_t)n);
    return vx_crypto_result(n ? vxc_random(VX_CB(out), (size_t)n) : 0, out);
}

static bool vx_crypto_equal(vx_list *a, vx_list *b) {
    if (a->len != b->len) return false;
    volatile uint8_t diff = 0;
    for (int64_t i = 0; i < a->len; i++) diff |= VX_CB(a)[i] ^ VX_CB(b)[i];
    return diff == 0;
}

static vx_list *vx_crypto_sha256(vx_list *d) {
    vx_list *out = vx_crypto_new(32);
    return vx_crypto_result(vxc_sha256(VX_CB(d), VX_CN(d), VX_CB(out)), out);
}

static vx_list *vx_crypto_sha1(vx_list *d) {
    vx_list *out = vx_crypto_new(20);
    return vx_crypto_result(vxc_sha1(VX_CB(d), VX_CN(d), VX_CB(out)), out);
}

static vx_list *vx_crypto_md5(vx_list *d) {
    vx_list *out = vx_crypto_new(16);
    return vx_crypto_result(vxc_md5(VX_CB(d), VX_CN(d), VX_CB(out)), out);
}

static vx_list *vx_crypto_sha512(vx_list *d) {
    vx_list *out = vx_crypto_new(64);
    crypto_sha512(VX_CB(out), VX_CB(d), VX_CN(d));
    return out;
}

static vx_list *vx_crypto_blake2b(vx_list *d, vx_list *key, int64_t size) {
    if (size < 1 || size > 64) return vx_crypto_fail("needs a size from 1 to 64 bytes");
    if (key->len > 64) return vx_crypto_fail("needs a key of at most 64 bytes");
    vx_list *out = vx_crypto_new((size_t)size);
    crypto_blake2b_keyed(VX_CB(out), (size_t)size, VX_CB(key), VX_CN(key), VX_CB(d), VX_CN(d));
    return out;
}

static vx_list *vx_crypto_hmac_sha256(vx_list *key, vx_list *d) {
    vx_list *out = vx_crypto_new(32);
    return vx_crypto_result(vxc_hmac_sha256(VX_CB(key), VX_CN(key), VX_CB(d), VX_CN(d), VX_CB(out)), out);
}

static vx_list *vx_crypto_hmac_sha512(vx_list *key, vx_list *d) {
    vx_list *out = vx_crypto_new(64);
    crypto_sha512_hmac(VX_CB(out), VX_CB(key), VX_CN(key), VX_CB(d), VX_CN(d));
    return out;
}

static vx_list *vx_crypto_hkdf_sha256(vx_list *ikm, vx_list *salt, vx_list *info, int64_t size) {
    if (size < 1 || size > 255 * 32) return vx_crypto_fail("needs a size from 1 to 8160 bytes");
    vx_list *out = vx_crypto_new((size_t)size);
    return vx_crypto_result(vxc_hkdf_sha256(VX_CB(ikm), VX_CN(ikm), VX_CB(salt), VX_CN(salt), VX_CB(info), VX_CN(info),
                                            VX_CB(out), (size_t)size), out);
}

static vx_list *vx_crypto_hkdf_sha512(vx_list *ikm, vx_list *salt, vx_list *info, int64_t size) {
    if (size < 1 || size > 255 * 64) return vx_crypto_fail("needs a size from 1 to 16320 bytes");
    vx_list *out = vx_crypto_new((size_t)size);
    crypto_sha512_hkdf(VX_CB(out), (size_t)size, VX_CB(ikm), VX_CN(ikm), VX_CB(salt), VX_CN(salt), VX_CB(info), VX_CN(info));
    return out;
}

static vx_list *vx_crypto_pbkdf2_sha256(vx_list *pw, vx_list *salt, int64_t rounds, int64_t size) {
    if (rounds < 1 || rounds > UINT32_MAX) return vx_crypto_fail("needs from 1 to 4294967295 iterations");
    if (size < 1 || size > 1 << 20) return vx_crypto_fail("needs a size from 1 byte to 1 MiB");
    vx_list *out = vx_crypto_new((size_t)size);
    return vx_crypto_result(vxc_pbkdf2_sha256(VX_CB(pw), VX_CN(pw), VX_CB(salt), VX_CN(salt), (uint32_t)rounds, VX_CB(out), (size_t)size), out);
}

// Argon2id (RFC 9106), with an optional secret [key] and associated data
// [ad], which may be empty.
static vx_list *vx_crypto_argon2id(vx_list *pw, vx_list *salt, vx_list *key, vx_list *ad, int64_t size, int64_t kib,
                                   int64_t passes, int64_t lanes) {
    if (size < 4 || size > 1 << 20) return vx_crypto_fail("needs a size from 4 bytes to 1 MiB");
    if (salt->len < 8 || salt->len > UINT32_MAX) return vx_crypto_fail("needs a salt of at least 8 bytes");
    if (lanes < 1 || lanes > 0xFFFFFF) return vx_crypto_fail("needs from 1 to 16777215 lanes");
    if (kib < 8 * lanes || kib > UINT32_MAX) return vx_crypto_fail("needs at least 8 KiB of memory for each lane");
    if (passes < 1 || passes > UINT32_MAX) return vx_crypto_fail("needs at least 1 iteration");
    if (pw->len > UINT32_MAX || key->len > UINT32_MAX || ad->len > UINT32_MAX) return vx_crypto_fail("needs shorter input");
    void *work = malloc((size_t)kib * 1024);
    if (!work) return vx_crypto_fail("couldn't get the memory it was asked to use");
    crypto_argon2_config config = {CRYPTO_ARGON2_ID, (uint32_t)kib, (uint32_t)passes, (uint32_t)lanes};
    crypto_argon2_inputs inputs = {VX_CB(pw), VX_CB(salt), (uint32_t)pw->len, (uint32_t)salt->len};
    vx_list *out = vx_crypto_new((size_t)size);
    crypto_argon2_extras extras = {VX_CB(key), VX_CB(ad), (uint32_t)key->len, (uint32_t)ad->len};
    crypto_argon2(VX_CB(out), (uint32_t)size, work, config, inputs, extras);
    free(work);
    return out;
}

// Encrypts [msg] with ChaCha20-Poly1305: XChaCha20 for a 24-byte [nonce]
// and the IETF version (RFC 8439) for a 12-byte one. Returns the
// ciphertext followed by the 16-byte tag.
static vx_list *vx_crypto_chacha_seal(vx_list *key, vx_list *nonce, vx_list *ad, vx_list *msg) {
    if (key->len != 32 || (nonce->len != 24 && nonce->len != 12)) return vx_crypto_fail("needs a 32-byte key and a 12 or 24-byte nonce");
    vx_list *out = vx_crypto_new(VX_CN(msg) + 16);
    crypto_aead_ctx ctx;
    if (nonce->len == 24) {
        crypto_aead_init_x(&ctx, VX_CB(key), VX_CB(nonce));
    } else {
        crypto_aead_init_ietf(&ctx, VX_CB(key), VX_CB(nonce));
    }
    crypto_aead_write(&ctx, VX_CB(out), VX_CB(out) + VX_CN(msg), VX_CB(ad), VX_CN(ad), VX_CB(msg), VX_CN(msg));
    crypto_wipe(&ctx, sizeof ctx);
    return out;
}

// Decrypts [box], a ciphertext followed by its tag, as sealed by
// [vx_crypto_chacha_seal]. Returns NULL when the tag doesn't match.
static vx_list *vx_crypto_chacha_open(vx_list *key, vx_list *nonce, vx_list *ad, vx_list *box) {
    if (key->len != 32 || (nonce->len != 24 && nonce->len != 12)) return vx_crypto_fail("needs a 32-byte key and a 12 or 24-byte nonce");
    if (box->len < 16) return vx_crypto_fail("the data is too short to be sealed");
    size_t n = VX_CN(box) - 16;
    vx_list *out = vx_crypto_new(n);
    crypto_aead_ctx ctx;
    if (nonce->len == 24) {
        crypto_aead_init_x(&ctx, VX_CB(key), VX_CB(nonce));
    } else {
        crypto_aead_init_ietf(&ctx, VX_CB(key), VX_CB(nonce));
    }
    int rc = crypto_aead_read(&ctx, VX_CB(out), VX_CB(box) + n, VX_CB(ad), VX_CN(ad), VX_CB(box), n);
    crypto_wipe(&ctx, sizeof ctx);
    return rc == 0 ? out : vx_crypto_fail("the data was changed or the key is wrong");
}

static vx_list *vx_crypto_aes_gcm_seal(vx_list *key, vx_list *nonce, vx_list *ad, vx_list *msg) {
    if (key->len != 32 || nonce->len < 1) return vx_crypto_fail("needs a 32-byte key and a nonce");
    vx_list *out = vx_crypto_new(VX_CN(msg) + 16);
    int rc = vxc_aes_gcm_seal(VX_CB(key), VX_CB(nonce), VX_CN(nonce), VX_CB(ad), VX_CN(ad), VX_CB(msg), VX_CN(msg),
                              VX_CB(out), VX_CB(out) + VX_CN(msg));
    return vx_crypto_result(rc, out);
}

static vx_list *vx_crypto_aes_gcm_open(vx_list *key, vx_list *nonce, vx_list *ad, vx_list *box) {
    if (key->len != 32 || nonce->len < 1) return vx_crypto_fail("needs a 32-byte key and a nonce");
    if (box->len < 16) return vx_crypto_fail("the data is too short to be sealed");
    size_t n = VX_CN(box) - 16;
    vx_list *out = vx_crypto_new(n);
    int rc = vxc_aes_gcm_open(VX_CB(key), VX_CB(nonce), VX_CN(nonce), VX_CB(ad), VX_CN(ad), VX_CB(box), n,
                              VX_CB(box) + n, VX_CB(out));
    if (rc == -1) {
        crypto_wipe(out->data, n);
        return vx_crypto_fail("the data was changed or the key is wrong");
    }
    return vx_crypto_result(rc, out);
}

// Returns the 64-byte Ed25519 secret key (the seed, then the public key)
// made from a 32-byte [seed].
static vx_list *vx_crypto_ed25519_key(vx_list *seed) {
    if (seed->len != 32) return vx_crypto_fail("needs a 32-byte seed");
    uint8_t s[32], pk[32];
    memcpy(s, VX_CB(seed), 32);  // wiped by Monocypher
    vx_list *out = vx_crypto_new(64);
    crypto_ed25519_key_pair(VX_CB(out), pk, s);
    return out;
}

static vx_list *vx_crypto_ed25519_sign(vx_list *sk, vx_list *msg) {
    if (sk->len != 64) return vx_crypto_fail("needs a 64-byte secret key");
    vx_list *out = vx_crypto_new(64);
    crypto_ed25519_sign(VX_CB(out), VX_CB(sk), VX_CB(msg), VX_CN(msg));
    return out;
}

// Reports whether the 32 bytes [p] encode a point with x = 0 and the sign
// bit of x set, which RFC 8032 (5.1.3) says must not decode: y = 1 or
// y = p - 1 with the top bit set.
static bool vx_crypto_negative_zero(const uint8_t *p) {
    static const uint8_t one[32] = {1, [31] = 0x80};
    static const uint8_t minus_one[32] = {
        0xec, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
        0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff};
    return memcmp(p, one, 32) == 0 || memcmp(p, minus_one, 32) == 0;
}

static bool vx_crypto_ed25519_verify(vx_list *pk, vx_list *msg, vx_list *sig) {
    if (pk->len != 32 || sig->len != 64) return false;
    if (vx_crypto_negative_zero(VX_CB(sig)) || vx_crypto_negative_zero(VX_CB(pk))) return false;
    return crypto_ed25519_check(VX_CB(sig), VX_CB(pk), VX_CB(msg), VX_CN(msg)) == 0;
}

static vx_list *vx_crypto_x25519_public(vx_list *sk) {
    if (sk->len != 32) return vx_crypto_fail("needs a 32-byte secret key");
    vx_list *out = vx_crypto_new(32);
    crypto_x25519_public_key(VX_CB(out), VX_CB(sk));
    return out;
}

// Returns the raw X25519 shared secret of [sk] and [pk], or NULL when it
// is all zeros, as it is for a public key of small order.
static vx_list *vx_crypto_x25519(vx_list *sk, vx_list *pk) {
    if (sk->len != 32 || pk->len != 32) return vx_crypto_fail("needs 32-byte keys");
    vx_list *out = vx_crypto_new(32);
    crypto_x25519(VX_CB(out), VX_CB(sk), VX_CB(pk));
    uint8_t zero[32] = {0};
    if (crypto_verify32(VX_CB(out), zero) == 0) return vx_crypto_fail("the public key is weak and gives no shared secret");
    return out;
}

static bool vx_crypto_p256_check(vx_list *pub) {
    return pub->len == 65 && vxc_p256_check(VX_CB(pub)) == 0;
}

static bool vx_crypto_p256_check_private(vx_list *priv) {
    return priv->len == 97 && vxc_p256_check_private(VX_CB(priv)) == 0;
}

// Reads a DER INTEGER at [*p], before [end], moving [*p] past it.
// Returns false unless it is in DER's one form: a short length, from 1 to
// 33 bytes, positive and without a needless leading zero.
static bool vx_crypto_der_int(const uint8_t **p, const uint8_t *end) {
    if (end - *p < 2 || (*p)[0] != 0x02) return false;
    size_t n = (*p)[1];
    const uint8_t *c = *p + 2;
    if (n < 1 || n > 33 || (size_t)(end - c) < n) return false;
    if (c[0] & 0x80) return false;
    if (n > 1 && c[0] == 0 && !(c[1] & 0x80)) return false;
    *p = c + n;
    return true;
}

// Reports whether [sig] is a P-256 ECDSA signature in strict DER, as
// SEQUENCE { INTEGER r, INTEGER s }, since some providers accept other
// encodings of the same signature.
static bool vx_crypto_der_sig(const uint8_t *sig, size_t n) {
    if (n < 8 || n > 72 || sig[0] != 0x30 || sig[1] != n - 2) return false;
    const uint8_t *p = sig + 2, *end = sig + n;
    return vx_crypto_der_int(&p, end) && vx_crypto_der_int(&p, end) && p == end;
}

static bool vx_crypto_p256_verify(vx_list *pub, vx_list *msg, vx_list *sig) {
    if (pub->len != 65 || !vx_crypto_der_sig(VX_CB(sig), VX_CN(sig))) return false;
    return vxc_p256_verify(VX_CB(pub), VX_CB(msg), VX_CN(msg), VX_CB(sig), VX_CN(sig)) == 0;
}

static vx_list *vx_crypto_p256_generate(void) {
    vx_list *out = vx_crypto_new(97);
    return vx_crypto_result(vxc_p256_generate(VX_CB(out)), out);
}

static vx_list *vx_crypto_p256_sign(vx_list *priv, vx_list *msg) {
    if (priv->len != 97) return vx_crypto_fail("needs a 97-byte private key");
    uint8_t sig[72];
    size_t len = 0;
    int rc = vxc_p256_sign(VX_CB(priv), VX_CB(msg), VX_CN(msg), sig, &len);
    vx_list *out = vx_crypto_new(rc == 0 ? len : 0);
    if (rc == 0) memcpy(VX_CB(out), sig, len);
    return vx_crypto_result(rc, out);
}
