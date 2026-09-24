// CMS-protocol checker: argv = input correct contestant. Full score for the
// exact sum, half for an off-by-one answer.
#include <cstdio>
#include <cstdlib>
int main(int argc, char **argv) {
  if (argc != 4) return 1;
  long long want, got;
  FILE *c = fopen(argv[2], "r"), *o = fopen(argv[3], "r");
  if (!c || fscanf(c, "%lld", &want) != 1) return 2;
  if (!o || fscanf(o, "%lld", &got) != 1) { puts("0.0"); fputs("translate:wrong\n", stderr); return 0; }
  if (got == want) { puts("1.0"); fputs("translate:success\n", stderr); }
  else if (llabs(got - want) == 1) { puts("0.5"); fputs("Off by one\n", stderr); }
  else { puts("0.0"); fputs("translate:wrong\n", stderr); }
  return 0;
}
