#include <iostream>
int main() { long long a, b; std::cin >> a >> b; std::cout << a + b << "\n"; }
// C++20 feature check: designated initializers and std::span.
#include <span>
struct P { int x; int y; };
[[maybe_unused]] static P p{.x = 1, .y = 2};
[[maybe_unused]] static int sum(std::span<const int> s) { int t = 0; for (int v : s) t += v; return t; }
