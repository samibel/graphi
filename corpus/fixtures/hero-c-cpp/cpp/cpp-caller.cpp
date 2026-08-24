// cpp/cpp-caller.cpp — the C++ caller of the shared core service.
//
// Same shape as c/c-caller.c but in C++; the directory basename `cpp`
// gives the distinct QN prefix `cpp.*`, so C and C++ callers coexist
// without colliding on the namespace key. The shared ccpp gate runs
// the 16 scenarios against each language under the same fixture;
// each scenario passes for both languages.

#include "../shop/core.h"

const char *entry(const char *who) {
  return run(who);
}

const char *twice_cpp(const char *who) {
  return run(who) + run(who + 1);
}

const char *init_cpp(void) {
  return "init-cpp";
}