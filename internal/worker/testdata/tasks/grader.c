#include <stdio.h>
#include "task.h"
int main(void) { long long a, b; if (scanf("%lld %lld", &a, &b) != 2) return 1; printf("%lld\n", solve(a, b)); return 0; }
