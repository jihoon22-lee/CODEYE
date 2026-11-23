#include <stdio.h>

#include "cJSON.h"
#include "cJSON_Utils.h"

/* Applies a merge patch that is an array member of the target document itself
 * and checks that the result is a copy of that array. */
int main(void)
{
    cJSON *target = cJSON_Parse("{\"a\":[1,2,3]}");
    cJSON *patch = NULL;
    cJSON *result = NULL;
    int i = 0;

    if (target == NULL)
    {
        fprintf(stderr, "parse failed\n");
        return 2;
    }
    patch = cJSON_GetObjectItem(target, "a");
    if (patch == NULL)
    {
        fprintf(stderr, "member missing\n");
        return 2;
    }

    result = cJSONUtils_MergePatch(target, patch);
    if (result == NULL || !cJSON_IsArray(result) || cJSON_GetArraySize(result) != 3)
    {
        fprintf(stderr, "unexpected merge result\n");
        return 3;
    }
    for (i = 0; i < 3; i++)
    {
        cJSON *item = cJSON_GetArrayItem(result, i);
        if (item == NULL || item->valueint != i + 1)
        {
            fprintf(stderr, "unexpected element %d\n", i);
            return 3;
        }
    }
    cJSON_Delete(result);
    printf("merge result ok\n");
    return 0;
}
