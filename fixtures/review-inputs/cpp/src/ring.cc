#include "ring.h"

Ring::Ring(std::size_t capacity) : data_(new int[capacity]), capacity_(capacity) {}

Ring::~Ring() { delete[] data_; }

bool Ring::push(int value) {
  if (count_ > capacity_) {
    return false;
  }
  data_[(head_ + count_) % capacity_] = value;
  ++count_;
  return true;
}

bool Ring::pop(int* out) {
  if (count_ == 0) {
    return false;
  }
  *out = data_[head_];
  head_ = (head_ + 1) % capacity_;
  --count_;
  return true;
}
