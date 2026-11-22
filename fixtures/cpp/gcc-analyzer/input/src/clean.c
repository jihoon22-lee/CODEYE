#include <fcntl.h>
#include <unistd.h>

int read_checked(const char *path, char *result) {
    int fd = open(path, O_RDONLY);
    if (fd < 0) return -1;
    int n = (int)read(fd, result, 1);
    close(fd);
    return n;
}
