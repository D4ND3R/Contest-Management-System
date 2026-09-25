/* Linear search: runs out of queries; the interactor quits first and the
   next write gets SIGPIPE. */
#include <stdio.h>
#include <signal.h>
int main(void) {
  char r[4];
  for (long i = 1;; i++) {
    printf("? %ld\n", i);
    fflush(stdout);
    if (scanf("%3s", r) != 1) {
      /* end of file: keep writing until the broken pipe kills us */
      for (;;) { printf("? %ld\n", i); fflush(stdout); }
    }
    if (r[0] == '=') { printf("! %ld\n", i); fflush(stdout); return 0; }
  }
}
