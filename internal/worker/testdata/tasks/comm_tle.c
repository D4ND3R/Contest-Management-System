long long add(long long a, long long b) { volatile long long x = 0; for (;;) x++; return a + b; }
