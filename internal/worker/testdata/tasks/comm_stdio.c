/* std_io mode: talks to the manager through stdin/stdout, no stub. */
#include <stdio.h>
int main(void) {
  long long a, b; char tail[8];
  while (scanf("%lld %lld", &a, &b) == 2) {
    if (a == 0 && b == 0 && scanf("%7s", tail) == 1) break;
    printf("%lld\n", a + b); fflush(stdout);
  }
  return 0;
}
