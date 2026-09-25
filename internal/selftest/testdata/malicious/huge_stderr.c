/* Writes 2 GiB to stderr (discarded, but still bounded). */
#include <stdio.h>
#include <string.h>
int main(void) {
    static char buf[1 << 16];
    memset(buf, 'x', sizeof buf);
    for (long i = 0; i < (2L << 30) / (long)sizeof buf; i++) fwrite(buf, 1, sizeof buf, stderr);
    puts("5");
    return 0;
}
