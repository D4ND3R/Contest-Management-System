#include <stdio.h>
int main(void) { long a, b; if (scanf("%ld %ld", &a, &b) != 2) return 1; printf("%ld\n", a - b); return 0; }
