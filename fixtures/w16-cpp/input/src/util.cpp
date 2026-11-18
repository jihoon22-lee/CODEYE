// String helpers of the W16 sample service.
#include <string>
#include <utility>

namespace w16 {

std::size_t joined_size(std::string a) {
  std::string b = std::move(a);
  return a.size() + b.size();
}

std::size_t kept_size(std::string a) {
  std::string b = std::move(a);
  return a.size() + b.size(); // NOLINT(bugprone-use-after-move,clang-analyzer-cplusplus.Move)
}

int first_count() {
  int *counts = new int[4];
  counts[0] = 1;
  int first = counts[0];
  // cppcheck-suppress mismatchAllocDealloc
  delete counts;
  return first;
}

} // namespace w16
