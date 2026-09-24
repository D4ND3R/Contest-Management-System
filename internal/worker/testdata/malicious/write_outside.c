/* Tries to create files outside the box; prints ESCAPED <path> on success. */
#include <stdio.h>
static void attempt(const char *p) {
    FILE *f = fopen(p, "w");
    if (f && fputs("pwned\n", f) >= 0 && fclose(f) == 0) printf("ESCAPED %s\n", p);
    else printf("blocked %s\n", p);
}
int main(void) {
    attempt("/cms_pwned");
    attempt("/usr/cms_pwned");
    attempt("/usr/bin/cms_pwned");
    attempt("/etc/cms_pwned");
    attempt("/box/../cms_pwned");
    attempt("../cms_pwned");
    attempt("/proc/sys/kernel/cms_pwned");
    /* The private /tmp and /dev/shm are allowed but must not be the host's. */
    FILE *t = fopen("/tmp/cms_pwned_tmp", "w");
    if (t) { fputs("x", t); fclose(t); }
    t = fopen("/dev/shm/cms_pwned", "w");
    if (t) { fputs("x", t); fclose(t); }
    return 0;
}
