#include <cassert>

#include "ring.h"

int main() {
  Ring ring(2);
  assert(ring.push(1));
  assert(ring.push(2));
  assert(!ring.push(3));
  int v = 0;
  assert(ring.pop(&v) && v == 1);
  return 0;
}
