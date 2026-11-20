// Engine overlap measurement (docs/ANALYZERS.md#엔진-중복): one construct per function,
// each a defect class that cppcheck, clang-tidy or the Clang Static Analyzer
// reports, then variants of the constructs both engines reported on the same
// line. Read by cpp.TestRealEngineOverlap; not part of the analyzed fixture.
#include <cassert>
#include <cstdlib>
#include <cstring>
#include <memory>
#include <string>
#include <vector>
#include <algorithm>
#include <utility>
#include <system_error>

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

// Variants of the same-line pairs above: does one engine report what the
// other misses?
struct Iter { int i; Iter &operator++() { ++i; return *this; } bool operator!=(const Iter &o) const { return i != o.i; } };
void assert_side_effect_class(Iter it, Iter end) { assert(++it != end); }

void memset_char_zero(char *buf, std::size_t n) { memset(buf, '0', n); }

void unused_make_unique(void) { std::make_unique<int>(1); }
void unused_calloc(void) { calloc(1, 1); }

static int shift_by(int n) { return 1 << n; }
int shift_through_call(void) { return shift_by(40); }

static int *make_array(void) { return new int[3]; }
void mismatch_through_call(void) { int *p = make_array(); delete p; }

int *g_escape;
void stack_escape_global(void) { int x = 1; g_escape = &x; }

static int at(const int *a, int i) { return a[i]; }
int array_oob_through_call(void) { int a[3] = {0, 1, 2}; return at(a, 5); }

static int divide(int a, int b) { return a / b; }
int div_zero_through_call(void) { return divide(1, 0); }

struct Shallow {
  int *data;
  int size;
  Shallow &operator=(const Shallow &o) {
    size = o.size;
    data = new int[size];
    return *this;
  }
};

int inner_pointer_reassign(void) {
  std::string s = "x";
  const char *p = s.c_str();
  s = "a much longer string that reallocates";
  return *p;
}

void empty_free_function(std::vector<int> &v) { std::empty(v); }

void memset_out_of_range(char *buf, std::size_t n) { memset(buf, 300, n); }
void memset_char_zero_literal(char buf[8]) { memset(buf, '0', 8); }

std::error_code make_error(void);
void unused_error_code(void) { make_error(); }

int shift_after_loop(void) { int n = 36; for (int i = 0; i < 4; ++i) n++; return 1 << n; }

void stack_escape_out_param(int **out) { int x = 1; *out = &x; }

int div_zero_after_loop(void) { int z = 3; while (z) z--; return 1 / z; }

struct Owner {
  std::unique_ptr<int> p;
  Owner &operator=(const Owner &o) {
    p.reset(new int(*o.p));
    return *this;
  }
};

int inner_pointer_clear(void) {
  std::string s = "x";
  const char *p = s.data();
  s.clear();
  return *p;
}

struct Bag { bool empty() const; void clear(); };
void empty_custom(Bag &b) { b.empty(); }

int branch_clone(int a) { int x; if (a) x = 1; else x = 1; return x; }

struct Holder { int d; int *p; char *buf; };
int div_zero_field(void) { Holder h{0, nullptr, nullptr}; Holder *q = &h; return 10 / q->d; }
static int ident(int x) { return x; }
static int zero_value(void) { return ident(0); }
int div_zero_two_calls(void) { return 5 / zero_value(); }
int null_field(void) { Holder h{1, nullptr, nullptr}; Holder *q = &h; return *q->p; }
static int *null_source(void) { return ident(0) ? &g_counter : nullptr; }
int null_two_calls(void) { return *null_source(); }
void leak_field(void) { Holder h{0, nullptr, nullptr}; h.buf = (char *)malloc(3); }
void leak_early_return(int c) { char *p = (char *)malloc(4); if (c) return; free(p); }
static void release(char *p) { free(p); }
void double_free_through_call(void) { char *p = (char *)malloc(2); release(p); free(p); }
struct Ref { int *p; };
Ref stack_escape_struct(void) { int x = 1; Ref r{&x}; return r; }
int shift_negative(int a) { int n = ident(-1); return a << n; }
void assert_call(std::vector<int> &v) { assert(v.erase(v.begin()) != v.end()); }
void memset_sizeof_swapped(int *a) { memset(a, sizeof(int) * 4, 0); }
struct Plain { int v; };
int use_after_move_plain(void) { Plain a{1}; Plain b = std::move(a); return a.v + b.v; }
std::size_t move_in_branch(std::string a, bool c) { std::string b; if (c) b = std::move(a); return a.size() + b.size(); }
int moved_unique_ptr(std::unique_ptr<int> p) { std::unique_ptr<int> q = std::move(p); return *p + *q; }
