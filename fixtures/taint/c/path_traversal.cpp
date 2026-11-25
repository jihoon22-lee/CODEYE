#include <cstdlib>
#include <cstring>
#include <fstream>
#include <string>
#include <sys/socket.h>

std::string read_page() {
    std::string name = std::getenv("PATH_INFO");
    std::ifstream in("/srv/pages/" + name); // expect: codeye.c.path-traversal
    std::string line;
    std::getline(in, line);
    return line;
}

void save_upload(int sock) {
    char name[256] = "/srv/uploads/";
    size_t used = std::strlen(name);
    ssize_t n = recv(sock, (char *)(name + used), sizeof(name) - used - 1, 0);
    if (n <= 0) {
        return;
    }
    name[used + n] = '\0';
    std::ofstream out;
    out.open(name); // expect: codeye.c.path-traversal
}
