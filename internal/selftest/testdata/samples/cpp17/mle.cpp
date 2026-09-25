#include <vector>
#include <iostream>
int main() { std::vector<long long> v(1u << 27, 1); long long s = 0; for (auto x : v) s += x; std::cout << s << "\n"; }
