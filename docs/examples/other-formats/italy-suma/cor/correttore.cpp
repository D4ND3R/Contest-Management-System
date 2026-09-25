// CMS checker ("correttore"): argv = input, correct output, contestant
// output; prints the outcome (0..1) on stdout and a message on stderr.
#include <cstdio>
int main(int argc, char **argv) {
  if (argc < 4) return 1;
  FILE *c = fopen(argv[2], "r"), *s = fopen(argv[3], "r");
  long long a = 0, b = 1;
  bool ok = c && s && fscanf(c, "%lld", &a) == 1 && fscanf(s, "%lld", &b) == 1 && a == b;
  printf("%s\n", ok ? "1.0" : "0.0");
  fprintf(stderr, "%s\n", ok ? "Correcto" : "Incorrecto");
  return 0;
}
