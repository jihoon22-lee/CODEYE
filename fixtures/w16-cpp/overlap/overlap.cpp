// W16 engine overlap measurement (docs/41 §7): one construct per function,
// each a defect class that cppcheck, clang-tidy or the Clang Static Analyzer
// reports. Read by cpp.TestRealEngineOverlap.
#include <cassert>
#include <cstdlib>
#include <cstring>
#include <memory>
#include <string>
#include <vector>
#include <algorithm>
#include <utility>

int g_counter;

void assert_side_effect(int x) { assert(x++ > 0); }

void memset_zero(char *buf) { memset(buf, 0xff, 0); }

void memset_swapped(char *buf) { memset(buf, sizeof(buf), 0); }

void sizeof_pointer(char *buf) { memset(buf, 0, sizeof(buf)); }

std::size_t use_after_move(std::string a) {
  std::string b = std::move(a);
  return a.size() + b.size();
}

int semi(int a) {
  if (a == 1);
  return a;
}

void ignored_return(std::vector<int> &v) { std::unique(v.begin(), v.end()); }

void realloc_misuse(char *p, std::size_t n) {
  p = (char *)realloc(p, n);
  free(p);
}

int shift_too_far(void) { int one = 1; return one << 40; }

void mismatch_new_delete(void) {
  int *p = new int[3];
  delete p;
}

void unique_ptr_array(void) { std::unique_ptr<int> p(new int[3]); }

int *return_local(void) {
  int x = 1;
  return &x;
}

int array_oob(void) {
  int a[3] = {0, 1, 2};
  return a[5];
}

int div_zero(int a) {
  int z = 0;
  return a / z;
}

struct Self {
  int *data;
  Self &operator=(const Self &o) {
    delete data;
    data = new int(*o.data);
    return *this;
  }
};

const char *dangling_cstr(void) {
  std::string s = "x";
  return s.c_str();
}

void empty_call(std::vector<int> &v) { v.empty(); }

void string_compare(const char *a, const char *b, int *out) {
  if (strcmp(a, b))
    *out = 1;
}

double integer_division(int a, int b) { return a / b * 1.0; }

void float_loop(void) {
  for (float f = 0.0f; f < 1.0f; f += 0.1f) {
    g_counter++;
  }
}

char *strlen_alloc(const char *s) {
  char *r = (char *)malloc(strlen(s + 1));
  if (r) strcpy(r, s);
  return r;
}

void bad_erase(std::vector<int> &v) { v.erase(std::remove(v.begin(), v.end(), 1)); }

int unsafe_gets(char *buf) { return buf != nullptr; }

int redundant_cond(int a) {
  if (a > 0) {
    if (a > 0) return 1;
  }
  return 0;
}
