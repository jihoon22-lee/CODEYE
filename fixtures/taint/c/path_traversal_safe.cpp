#include <cstdlib>
#include <cstring>
#include <fstream>
#include <string>
#include <sys/socket.h>

std::string read_page() {
    int page = std::atoi(std::getenv("QUERY_STRING"));
    std::ifstream in("/srv/pages/" + std::to_string(page) + ".html");
    std::string line;
    std::getline(in, line);
    return line;
}

void save_log(int sock) {
    char line[256];
    ssize_t n = recv(sock, line, sizeof(line) - 1, 0);
    if (n <= 0) {
        return;
    }
    line[n] = '\0';
    std::ofstream out;
    out.open("/var/log/service.log");
    out << line;
}

void save_datagram(int sock) {
    char line[256] = "entry: ";
    size_t used = std::strlen(line);
    ssize_t n = recvfrom(sock, line + used, sizeof(line) - used - 1, 0, nullptr, nullptr);
    if (n <= 0) {
        return;
    }
    line[used + n] = '\0';
    std::fstream out;
    out.open("/var/log/datagram.log", std::ios::app);
    out << line;
}
