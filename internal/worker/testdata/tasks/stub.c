/* Stub linked with the contestant's add(): argv = m2u u2m [index]. */
#include <stdio.h>
long long add(long long a, long long b);
int main(int argc, char **argv) {
  FILE *in = fopen(argv[1], "r"), *out = fopen(argv[2], "w");
  if (!in || !out) return 1;
  long long a, b; char tail[8];
  while (fscanf(in, "%lld %lld", &a, &b) == 2) {
    if (a == 0 && b == 0 && fscanf(in, "%7s", tail) == 1) break;
    fprintf(out, "%lld\n", add(a, b)); fflush(out);
  }
  return 0;
}
