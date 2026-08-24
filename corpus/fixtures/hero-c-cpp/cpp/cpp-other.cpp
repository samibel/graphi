// cpp/cpp-other.cpp — second C++ caller; ambiguous `init_cpp` with cpp-caller.cpp.

#include "../shop/core.h"

const char *init_cpp(void) {
  return "init-cpp-other";
}