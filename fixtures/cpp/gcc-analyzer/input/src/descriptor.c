#include <fcntl.h>
#include <unistd.h>

int read_configuration(const char *path, char *result) {
    int fd = open(path, O_RDONLY);
    if (fd < 0) return -1;
    close(fd);
    return (int)read(fd, result, 1);
}
