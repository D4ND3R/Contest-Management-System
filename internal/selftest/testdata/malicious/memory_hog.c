/* Allocates and touches far more memory than allowed. */
#include <stdlib.h>
#include <string.h>
int main(void) {
    for (int i = 0; i < 4096; i++) {
        char *p = malloc(1 << 20);
        if (!p) return 1;
        memset(p, i, 1 << 20);
    }
    return 0;
}
