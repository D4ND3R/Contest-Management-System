#include <stdio.h>
int main(void) { volatile int* p = 0; *p = 1; printf("! 1\n"); return 0; }
