#include <stdio.h>
int main(void) {
  long lo = 1, hi = 1000000000;
  char r[4];
  while (lo < hi) {
    long mid = lo + (hi - lo) / 2;
    printf("? %ld\n", mid);
    fflush(stdout);
    if (scanf("%3s", r) != 1) return 0;
    if (r[0] == '=') { lo = hi = mid; break; }
    if (r[0] == '<') hi = mid - 1; else lo = mid + 1;
  }
  printf("! %ld\n", lo);
  fflush(stdout);
  return 0;
}
