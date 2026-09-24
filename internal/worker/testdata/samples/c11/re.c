#include <stdio.h>
int main(void) { volatile int z = 0; long long a, b; if (scanf("%lld %lld", &a, &b) != 2) return 1; printf("%lld\n", a / z); return 0; }
