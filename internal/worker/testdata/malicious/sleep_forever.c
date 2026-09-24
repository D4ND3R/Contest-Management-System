/* Sleeps without using CPU: only the wall-clock limit stops it. */
#include <unistd.h>
int main(void) { for (;;) sleep(1000); }
