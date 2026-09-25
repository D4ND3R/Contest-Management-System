// Guess the number (testlib conventions, no testlib.h needed).
// Input file: "N Q [slow]": the secret, the query budget and, when slow is
// 1, 1.5 s of interactor CPU time before the game (never charged to the
// contestant). The contestant asks "? x" and reads "<" (the secret is
// smaller), ">" (larger) or "="; it answers "! x".
// Exit codes: 0 accepted, 1 wrong answer, 2 presentation error.
#include <cstdio>
#include <cstring>
#include <ctime>

int main(int argc, char** argv) {
  if (argc < 3) return 3;
  FILE* in = fopen(argv[1], "r");
  if (!in) return 3;
  long n = 0, q = 0;
  int slow = 0;
  if (fscanf(in, "%ld %ld %d", &n, &q, &slow) < 2) return 3;
  if (slow) {
    clock_t s = clock();
    while (clock() - s < CLOCKS_PER_SEC * 3 / 2) {}
  }
  FILE* tout = fopen(argv[2], "w");
  char op[16];
  long x;
  int asked = 0;
  for (;;) {
    if (scanf("%15s", op) != 1) { fprintf(stderr, "unexpected end of file\n"); return 1; }
    if (strcmp(op, "?") != 0 && strcmp(op, "!") != 0) { fprintf(stderr, "invalid query %s\n", op); return 2; }
    if (scanf("%ld", &x) != 1) { fprintf(stderr, "expected a number\n"); return 2; }
    if (op[0] == '!') {
      if (tout) fprintf(tout, "%ld\n", x);
      if (x == n) { fprintf(stderr, "found in %d queries\n", asked); return 0; }
      fprintf(stderr, "wrong answer %ld\n", x);
      return 1;
    }
    if (++asked > q) { fprintf(stderr, "too many queries\n"); return 1; }
    printf("%s\n", x > n ? "<" : (x < n ? ">" : "="));
    fflush(stdout);
  }
}
