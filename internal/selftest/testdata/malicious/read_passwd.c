/* Tries to read host secrets and prints "LEAK <path>" for each success. */
#include <stdio.h>
#include <string.h>
#include <dirent.h>
static void try_read(const char *p) {
    FILE *f = fopen(p, "r");
    char buf[256];
    if (f && fgets(buf, sizeof buf, f)) printf("LEAK %s %s", p, buf);
    if (f) fclose(f);
}
int main(void) {
    const char *files[] = {"/etc/passwd", "/etc/shadow", "/etc/hostname", "/root/.bashrc",
                           "/root/.ssh/id_rsa", "/var/lib/cms/blobs", "/proc/1/environ", NULL};
    for (int i = 0; files[i]; i++) try_read(files[i]);
    const char *dirs[] = {"/home", "/var/local/lib/isolate", "/etc", "/root", NULL};
    for (int i = 0; dirs[i]; i++) {
        DIR *d = opendir(dirs[i]);
        if (!d) continue;
        struct dirent *e;
        while ((e = readdir(d)))
            if (strcmp(e->d_name, ".") && strcmp(e->d_name, ".."))
                printf("LEAK %s/%s\n", dirs[i], e->d_name);
        closedir(d);
    }
    puts("done");
    return 0;
}
