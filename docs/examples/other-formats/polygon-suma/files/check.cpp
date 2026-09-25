// A checker with testlib's interface (argv = input, output, answer; exit
// code 0 = accepted, 1 = wrong answer), written without testlib.h to keep
// the example small. Real Polygon checkers include testlib.h, which the
// package carries in files/.
#include <cstdio>
int main(int argc, char **argv) {
  if (argc < 4) return 3;
  FILE *out = fopen(argv[2], "r"), *ans = fopen(argv[3], "r");
  long long a = 0, b = 1;
  if (!out || !ans || fscanf(out, "%lld", &a) != 1 || fscanf(ans, "%lld", &b) != 1 || a != b) {
    fprintf(stderr, "wrong answer\n");
    return 1;
  }
  fprintf(stderr, "ok\n");
  return 0;
}
