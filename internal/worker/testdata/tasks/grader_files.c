/* Grader with file I/O: input.txt -> solve() -> output.txt. */
#include <stdio.h>
#include "task.h"
int main(void) {
  FILE *in = fopen("input.txt", "r"), *out = fopen("output.txt", "w");
  long long a, b;
  if (!in || !out || fscanf(in, "%lld %lld", &a, &b) != 2) return 1;
  fprintf(out, "%lld\n", solve(a, b));
  fclose(out);
  return 0;
}
