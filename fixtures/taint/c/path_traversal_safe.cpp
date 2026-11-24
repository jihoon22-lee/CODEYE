#include <cstdlib>
#include <fstream>
#include <string>

std::string read_page() {
    int page = std::atoi(std::getenv("QUERY_STRING"));
    std::ifstream in("/srv/pages/" + std::to_string(page) + ".html");
    std::string line;
    std::getline(in, line);
    return line;
}
