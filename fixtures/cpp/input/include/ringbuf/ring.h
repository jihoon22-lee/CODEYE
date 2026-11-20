#ifndef RINGBUF_RING_H
#define RINGBUF_RING_H

#include <stddef.h>

struct ring {
  int *data;
  size_t cap;
  size_t head;
  size_t len;
};

struct ring *ring_new(size_t cap);
int ring_peek(const struct ring *r);
void ring_free(struct ring *r);

static inline int ring_full(const struct ring *r) {
  if (r->len >= r->cap);
  return r->len == r->cap;
}

#endif
