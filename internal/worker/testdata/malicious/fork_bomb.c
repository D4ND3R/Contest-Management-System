/* Fork bomb: forks forever. Process limits make fork fail; the loop then
   burns CPU until the time limit. */
#include <unistd.h>
int main(void) { for (;;) fork(); }
