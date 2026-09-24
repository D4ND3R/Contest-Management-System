/* A checker speaking the testlib protocol (argv = input output answer;
   exit 0 = OK, 1 = WA, 7 = partial with "points"). */
#include <stdio.h>
int main(int argc, char **argv) {
  long long want, got;
  FILE *o = fopen(argv[2], "r"), *a = fopen(argv[3], "r");
  if (!a || fscanf(a, "%lld", &want) != 1) { fprintf(stderr, "FAIL bad answer\n"); return 3; }
  if (!o || fscanf(o, "%lld", &got) != 1) { fprintf(stderr, "wrong answer no number\n"); return 1; }
  if (got == want) { fprintf(stderr, "ok answer is %lld\n", got); return 0; }
  if (got == want + 1 || got == want - 1) { fprintf(stderr, "points 0.25 close enough\n"); return 7; }
  fprintf(stderr, "wrong answer expected %lld, found %lld\n", want, got);
  return 1;
}
