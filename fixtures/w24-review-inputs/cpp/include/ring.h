#pragma once
#include <cstddef>

class Ring {
 public:
  explicit Ring(std::size_t capacity);
  ~Ring();
  bool push(int value);
  bool pop(int* out);
  std::size_t size() const { return count_; }

 private:
  int* data_;
  std::size_t capacity_;
  std::size_t head_ = 0;
  std::size_t count_ = 0;
};
