/* Tries to regain privileges; prints ROOT if anything works. */
#include <stdio.h>
#include <unistd.h>
#include <sys/mount.h>
int main(void) {
    if (setuid(0) == 0) puts("ROOT setuid");
    if (chroot("/") == 0) puts("ROOT chroot");
    if (mount("none", "/box", "tmpfs", 0, NULL) == 0) puts("ROOT mount");
    if (getuid() == 0 || geteuid() == 0) puts("ROOT uid");
    puts("done");
    return 0;
}
