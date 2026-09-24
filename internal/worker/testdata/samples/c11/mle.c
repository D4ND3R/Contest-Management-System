#include <stdio.h>
#include <stdlib.h>
int main(void) {
    long long a, b;
    if (scanf("%lld %lld", &a, &b) != 2) return 1;
    size_t n = 1UL << 27;
    long long *p = malloc(n * sizeof *p);
    if (!p) return 1;
    for (size_t i = 0; i < n; i++) p[i] = (long long)i * a + b;
    long long s = 0;
    for (size_t i = 0; i < n; i += 4096) s += p[i];
    printf("%lld\n", s);
    return 0;
}
