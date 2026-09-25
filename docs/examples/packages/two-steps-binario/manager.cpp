// TwoSteps manager: argv[1] = "0" (encode: input -> message) or "1"
// (decode: message -> answer).
#include <cstdio>
#include <cstring>
#include <string>
std::string encode(long long n);
long long decode(const std::string &msg);
int main(int argc, char **argv) {
  if (argc < 2) return 1;
  if (!strcmp(argv[1], "0")) { long long n; if (scanf("%lld", &n) != 1) return 1; printf("%s\n", encode(n).c_str()); }
  else { char buf[256]; if (scanf("%255s", buf) != 1) return 1; printf("%lld\n", decode(buf)); }
  return 0;
}
