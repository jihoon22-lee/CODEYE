#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

static int run(const char *cmd) {
    return system(cmd); /* expect: codeye.c.command-injection */
}

void handle_ping(int sock) {
    char host[128];
    char cmd[256];
    ssize_t n = recv(sock, host, sizeof(host) - 1, 0);
    if (n <= 0) {
        return;
    }
    host[n] = '\0';
    snprintf(cmd, sizeof(cmd), "ping -c 1 %s", host);
    run(cmd);
}

void handle_script(int sock) {
    char script[256];
    ssize_t n = recv(sock, script, sizeof(script) - 1, 0);
    if (n <= 0) {
        return;
    }
    script[n] = '\0';
    execl("/bin/sh", "sh", "-c", script, (char *)NULL); /* expect: codeye.c.command-injection */
}

int main(void) {
    const char *query = getenv("QUERY_STRING");
    FILE *p = popen(query, "r"); /* expect: codeye.c.command-injection */
    if (p != NULL) {
        pclose(p);
    }
    return 0;
}
