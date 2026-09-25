#include <string>
#include <cstdio>
std::string encode(long long n) { std::string s; do { s += char('0' + n % 2); n /= 2; } while (n); return s; }
long long decode(const std::string &m) { long long n = 0; for (int i = (int)m.size() - 1; i >= 0; i--) n = n * 2 + (m[i] - '0'); return n; }
