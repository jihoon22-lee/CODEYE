#include <cstdlib>
#include <fstream>
#include <string>

std::string read_page() {
    std::string name = std::getenv("PATH_INFO");
    std::ifstream in("/srv/pages/" + name); // expect: codeye.c.path-traversal
    std::string line;
    std::getline(in, line);
    return line;
}
