#include <cstdio>

#include "ring.h"

int main() {
  Ring ring(4);
  for (int i = 0; i < 8; ++i) {
    if (!ring.push(i)) {
      std::printf("full at %d\n", i);
    }
  }
  int v = 0;
  while (ring.pop(&v)) {
    std::printf("%d\n", v);
  }
  return 0;
}
