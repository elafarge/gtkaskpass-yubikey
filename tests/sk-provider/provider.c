/* SPDX-License-Identifier: Apache-2.0
 * TEST ONLY: software-backed FIDO ABI provider; never installed with askpass.
 * Keys and PIN are disposable fixtures, with no USB/hardware access.
 * ABI layout follows OpenSSH sk-api.h version 0x000a0000.
 */
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <openssl/evp.h>
#include <openssl/sha.h>

struct sk_option;
struct sk_resident_key;
struct sk_enroll_response {
    uint8_t flags;
    uint8_t *public_key;
    size_t public_key_len;
    uint8_t *key_handle;
    size_t key_handle_len;
    uint8_t *signature;
    size_t signature_len;
    uint8_t *attestation_cert;
    size_t attestation_cert_len;
    uint8_t *authdata;
    size_t authdata_len;
};
struct sk_sign_response {
    uint8_t flags;
    uint32_t counter;
    uint8_t *sig_r;
    size_t sig_r_len;
    uint8_t *sig_s;
    size_t sig_s_len;
};

uint32_t sk_api_version(void) { return 0x000a0000; }

int sk_enroll(uint32_t alg, const uint8_t *challenge, size_t challenge_len,
    const char *application, uint8_t flags, const char *pin,
    struct sk_option **options, struct sk_enroll_response **out)
{
    (void)challenge; (void)challenge_len; (void)application; (void)pin; (void)options;
    *out = NULL;
    if (alg != 1) return -2;
    EVP_PKEY *key = EVP_PKEY_Q_keygen(NULL, NULL, "ED25519");
    struct sk_enroll_response *r = calloc(1, sizeof(*r));
    if (key == NULL || r == NULL) { EVP_PKEY_free(key); free(r); return -1; }
    r->flags = flags;
    r->public_key_len = r->key_handle_len = 32;
    r->public_key = malloc(32);
    r->key_handle = malloc(32);
    r->signature = calloc(1, 1);
    if (r->public_key == NULL || r->key_handle == NULL || r->signature == NULL ||
        EVP_PKEY_get_raw_public_key(key, r->public_key, &r->public_key_len) != 1 ||
        EVP_PKEY_get_raw_private_key(key, r->key_handle, &r->key_handle_len) != 1) {
        free(r->public_key); free(r->key_handle); free(r->signature); free(r);
        EVP_PKEY_free(key); return -1;
    }
    EVP_PKEY_free(key);
    *out = r;
    return 0;
}

int sk_sign(uint32_t alg, const uint8_t *data, size_t data_len,
    const char *application, const uint8_t *key_handle, size_t key_handle_len,
    uint8_t flags, const char *pin, struct sk_option **options,
    struct sk_sign_response **out)
{
    (void)options;
    *out = NULL;
    if (alg != 1 || key_handle_len != 32) return -2;
    /* OpenSSH must really ask for a PIN; only this synthetic answer succeeds. */
    if (pin == NULL || strcmp(pin, "integration-pin") != 0) return -3;
    uint8_t message[69];
    if (SHA256((const unsigned char *)application, strlen(application), message) == NULL ||
        SHA256(data, data_len, message + 37) == NULL) return -1;
    message[32] = flags;
    memset(message + 33, 0, 4); /* counter */
    EVP_PKEY *key = EVP_PKEY_new_raw_private_key(EVP_PKEY_ED25519, NULL, key_handle, key_handle_len);
    EVP_MD_CTX *ctx = EVP_MD_CTX_new();
    struct sk_sign_response *r = calloc(1, sizeof(*r));
    int result = -1;
    if (key == NULL || ctx == NULL || r == NULL) goto done;
    r->flags = flags;
    r->sig_r_len = 64;
    r->sig_r = malloc(64);
    if (r->sig_r == NULL || EVP_DigestSignInit(ctx, NULL, NULL, NULL, key) != 1 ||
        EVP_DigestSign(ctx, r->sig_r, &r->sig_r_len, message, sizeof(message)) != 1) goto done;
    *out = r;
    r = NULL;
    result = 0;
done:
    if (r != NULL) { free(r->sig_r); free(r); }
    EVP_MD_CTX_free(ctx);
    EVP_PKEY_free(key);
    return result;
}

int sk_load_resident_keys(const char *pin, struct sk_option **options,
    struct sk_resident_key ***keys, size_t *count)
{
    (void)pin; (void)options; (void)keys; (void)count;
    return -2;
}
