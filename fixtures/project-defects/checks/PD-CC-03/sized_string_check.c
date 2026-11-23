#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "parson.h"

/* Copies `len` bytes into a heap block of exactly that size (no terminator)
 * and hands it to json_value_init_string_with_len. Returns 1 when a value
 * was created. */
static int accepts(const char *bytes, size_t len)
{
    char *block = (char *)malloc(len);
    JSON_Value *value = NULL;
    int created = 0;

    if (block == NULL)
    {
        fprintf(stderr, "out of memory\n");
        exit(2);
    }
    memcpy(block, bytes, len);
    value = json_value_init_string_with_len(block, len);
    created = value != NULL;
    if (value != NULL)
    {
        json_value_free(value);
    }
    free(block);
    return created;
}

int main(void)
{
    static const char four[] = "ab\xf0\x9f\x98\x80";
    static const char three[] = "ab\xe2\x82\xac";
    static const char two[] = "ab\xc2\xa9";
    int failures = 0;
    size_t n = 0;

    /* lengths that stop inside a multi-byte sequence must be refused */
    for (n = 3; n < 6; n++)
    {
        failures += accepts(four, n);
    }
    for (n = 3; n < 5; n++)
    {
        failures += accepts(three, n);
    }
    failures += accepts(two, 3);

    /* complete sequences filling the whole block must be accepted */
    failures += !accepts(four, 6);
    failures += !accepts(three, 5);
    failures += !accepts(two, 4);

    if (failures)
    {
        printf("sized string check: %d unexpected results\n", failures);
        return 1;
    }
    printf("sized string check ok\n");
    return 0;
}
