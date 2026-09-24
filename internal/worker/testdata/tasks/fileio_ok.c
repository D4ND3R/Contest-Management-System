#include <stdio.h>
int main(void) { long a, b; FILE *in = fopen("input.txt", "r"), *out = fopen("output.txt", "w");
  if (!in || !out || fscanf(in, "%ld %ld", &a, &b) != 2) return 1; fprintf(out, "%ld\n", a + b); fclose(out); return 0; }
