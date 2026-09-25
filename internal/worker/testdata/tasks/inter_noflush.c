/* Forgets to flush: both sides wait forever (wall-clock limit). */
#include <stdio.h>
int main(void) {
  char r[4];
  printf("? 500000000\n");
  if (scanf("%3s", r) != 1) return 0;
  printf("! 1\n");
  return 0;
}
