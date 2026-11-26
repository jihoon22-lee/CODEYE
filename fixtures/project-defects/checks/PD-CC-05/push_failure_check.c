/* Drives fd_array_push into a failed growth and checks what happens to the
 * array it held before. realloc and free are wrapped at link time
 * (-Wl,--wrap=realloc -Wl,--wrap=free) so the growth can be made to fail
 * on demand and the release of the old block can be observed. */
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>

#include "platform/platform.h"

void *__real_realloc(void *ptr, size_t size);
void __real_free(void *ptr);

static int fail_next_realloc;
static void *watched;
static int watched_released;

void *__wrap_realloc(void *ptr, size_t size)
{
    if (fail_next_realloc) {
        fail_next_realloc = 0;
        return NULL;
    }
    return __real_realloc(ptr, size);
}

void __wrap_free(void *ptr)
{
    if (ptr != NULL && ptr == watched)
        watched_released = 1;
    __real_free(ptr);
}

/* reptyr.c provides these to the library files; the driver replaces it. */
void die(const char *msg, ...)
{
    va_list ap;
    va_start(ap, msg);
    vfprintf(stderr, msg, ap);
    va_end(ap);
    fputc('\n', stderr);
    exit(2);
}

void debug(const char *msg, ...)
{
    (void)msg;
}

void error(const char *msg, ...)
{
    (void)msg;
}

int main(void)
{
    struct fd_array fda = {0};

    if (fd_array_push(&fda, 3) != 0 || fd_array_push(&fda, 4) != 0) {
        puts("setup pushes failed");
        return 2;
    }
    watched = fda.fds;

    fail_next_realloc = 1;
    int rc = fd_array_push(&fda, 5);
    printf("third push returned %d, array %p, capacity %d\n", rc, (void *)fda.fds, fda.allocated);
    if (rc == 0) {
        puts("growth was expected to fail");
        return 2;
    }
    if (!watched_released) {
        puts("FD ARRAY BLOCK NOT RELEASED after failed growth");
        __real_free(watched);
        return 1;
    }
    puts("old block released after failed growth");
    return 0;
}
