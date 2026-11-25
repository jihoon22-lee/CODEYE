#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#define ROOT "/srv/files/"

void serve(int sock) {
    char name[128];
    char path[256];
    ssize_t n = recv(sock, name, sizeof(name) - 1, 0);
    if (n <= 0) {
        return;
    }
    name[n] = '\0';
    strcpy(path, ROOT);
    strcat(path, name);
    int fd = open(path, O_RDONLY); /* expect: codeye.c.path-traversal */
    if (fd >= 0) {
        close(fd);
    }
}

int main(void) {
    char path[256];
    snprintf(path, sizeof(path), ROOT "%s", getenv("PATH_INFO"));
    FILE *f = fopen(path, "r"); /* expect: codeye.c.path-traversal */
    if (f != NULL) {
        fclose(f);
    }
    return 0;
}
