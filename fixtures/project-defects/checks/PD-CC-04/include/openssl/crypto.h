/* Stand-in for the generated <openssl/crypto.h>: only what randfile.c uses. */
#ifndef PD_CHECK_STUB_CRYPTO_H
#define PD_CHECK_STUB_CRYPTO_H
#include <stddef.h>
size_t OPENSSL_strlcpy(char *dst, const char *src, size_t siz);
void OPENSSL_cleanse(void *ptr, size_t len);
#endif
