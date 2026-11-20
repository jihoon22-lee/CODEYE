/* Fixed-size ring buffer of the ringbuf sample service. */
#include <stdlib.h>

#include "ringbuf/ring.h"

struct ring *ring_new(size_t cap) {
  struct ring *r = malloc(sizeof *r);
  if (r == 0)
    return 0;
  r->data = malloc(cap * sizeof *r->data);
  if (r->data == 0)
    return 0;
  r->cap = cap;
  r->head = 0;
  r->len = 0;
  return r;
}

int ring_peek(const struct ring *r) {
  int *slot = 0;
  if (r->len > 0)
    slot = &r->data[r->head];
  return *slot;
}

void ring_free(struct ring *r) {
  free(r->data);
  free(r);
}
