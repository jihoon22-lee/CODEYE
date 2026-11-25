#include <stdio.h>
#include <stdlib.h>
#include <sys/socket.h>
#include <unistd.h>

void handle_count(int sock) {
    char buf[32];
    char cmd[64];
    ssize_t n = recv(sock, buf, sizeof(buf) - 1, 0);
    if (n <= 0) {
        return;
    }
    buf[n] = '\0';
    long count = strtol(buf, NULL, 10);
    snprintf(cmd, sizeof(cmd), "seq %ld", count);
    system(cmd);
}

void handle_ping(int sock) {
    char host[128];
    ssize_t n = recv(sock, host, sizeof(host) - 1, 0);
    if (n <= 0) {
        return;
    }
    host[n] = '\0';
    /* The host is one argument of a fixed program, not shell text. */
    execlp("ping", "ping", "-c", "1", "--", host, (char *)NULL);
}

int main(void) {
    const char *home = getenv("HOME");
    printf("%s\n", home);
    return system("uptime");
}
