/* Calibration benchmark: a fixed amount of integer work with scattered
   memory accesses (like contest solutions), about half a second on a
   current core. Its CPU time is compared across cores and machines. */
#include <stdint.h>
#include <stdio.h>

#define N (1 << 20)
static uint32_t a[N];

int main(void) {
	uint64_t x = 88172645463325252ULL, s = 0;
	for (int r = 0; r < 140; r++) {
		for (uint32_t i = 0; i < N; i++) {
			x ^= x << 13;
			x ^= x >> 7;
			x ^= x << 17;
			a[i] += (uint32_t)x;
			s += a[(i * 2654435761u) & (N - 1)];
		}
	}
	printf("%llu\n", (unsigned long long)s);
	return 0;
}
