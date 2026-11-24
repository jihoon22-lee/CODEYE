#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define ROOT "/srv/files/"

int main(void) {
    char path[PATH_MAX];
    char resolved[PATH_MAX];
    snprintf(path, sizeof(path), ROOT "%s", getenv("PATH_INFO"));
    if (realpath(path, resolved) == NULL) {
        return 1;
    }
    if (strncmp(resolved, ROOT, strlen(ROOT)) != 0) {
        return 1;
    }
    FILE *f = fopen(resolved, "r");
    if (f != NULL) {
        fclose(f);
    }
    FILE *log = fopen("/var/log/app.log", "a");
    if (log != NULL) {
        fclose(log);
    }
    return 0;
}
