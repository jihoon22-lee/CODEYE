// Settings parser. W16_VERSION comes from config.h, which the CMake
// configure step generates into the build directory.
#include <string>

#include "config.h"

namespace w16 {

std::string version() { return W16_VERSION; }

int widths_total() {
  int widths[4];
  for (int i = 0; i <= 4; ++i)
    widths[i] = i * 2;
  return widths[0] + widths[3];
}

} // namespace w16
