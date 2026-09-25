#include <stdio.h>
#include <stdlib.h>
int main(void) {
  size_t n = 1UL << 27;
  long long *p = malloc(n * sizeof *p);
  if (!p) return 3;
  for (size_t i = 0; i < n; i++) p[i] = (long long)i;
  volatile long long s = 0;
  for (size_t i = 0; i < n; i += 4096) s += p[i];
  printf("! %lld\n", s);
  fflush(stdout);
  return 0;
}
