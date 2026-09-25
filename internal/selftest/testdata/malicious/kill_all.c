/* Tries to kill every process it can see (including the worker). */
#include <signal.h>
#include <stdio.h>
int main(void) {
    kill(1, SIGKILL);
    kill(-1, SIGKILL);
    puts("survived");
    return 0;
}
