/* Stand-in for <openssl/rand.h>: the declarations randfile.c needs. */
#ifndef PD_CHECK_STUB_RAND_H
#define PD_CHECK_STUB_RAND_H
#include <stddef.h>
int RAND_bytes(unsigned char *buf, int num);
int RAND_load_file(const char *file, long max_bytes);
int RAND_write_file(const char *file);
const char *RAND_file_name(char *file, size_t num);
#endif
