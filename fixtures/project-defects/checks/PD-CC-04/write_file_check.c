/* Calls the seed-file writer on an existing regular file and compares its
 * reported result with the size of what landed on disk. The random source,
 * the cleanse routine and the string copy are stand-ins defined here. */
#include <openssl/crypto.h>
#include <openssl/rand.h>

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>

int RAND_bytes(unsigned char *buf, int num)
{
    for (int i = 0; i < num; i++)
        buf[i] = (unsigned char)(i * 37 + 11);
    return 1;
}

void OPENSSL_cleanse(void *ptr, size_t len)
{
    memset(ptr, 0, len);
}

size_t OPENSSL_strlcpy(char *dst, const char *src, size_t siz)
{
    size_t n = strlen(src);
    if (siz > 0) {
        size_t c = n < siz - 1 ? n : siz - 1;
        memcpy(dst, src, c);
        dst[c] = '\0';
    }
    return n;
}

int main(void)
{
    const char *home = getenv("HOME");
    char path[4096];
    snprintf(path, sizeof path, "%s/pd_seed.bin", home ? home : ".");

    FILE *f = fopen(path, "wb");
    if (f == NULL) {
        perror("create seed file");
        return 2;
    }
    fclose(f);

    int reported = RAND_write_file(path);
    struct stat sb;
    if (stat(path, &sb) != 0) {
        perror("stat seed file");
        return 2;
    }
    printf("reported %d, file holds %lld bytes\n", reported, (long long)sb.st_size);
    if (reported <= 0 || (long long)reported != (long long)sb.st_size) {
        printf("SEED WRITE MISMATCH: reported %d for %lld bytes on disk\n", reported, (long long)sb.st_size);
        return 1;
    }
    puts("seed write ok");
    return 0;
}
