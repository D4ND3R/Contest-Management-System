/* Unbounded recursion. */
#include <stdio.h>
int depth(int n) { volatile char pad[4096]; pad[0] = (char)n; return depth(n + 1) + pad[0]; }
int main(void) { printf("%d\n", depth(0)); return 0; }
