/* Communication manager (CMS protocol): argv = u2m_0 m2u_0 [u2m_1 m2u_1];
   stdin = testcase ("q" then q pairs). Pairs go to the processes in turn. */
#include <stdio.h>
#include <string.h>
int main(int argc, char **argv) {
  int np = (argc - 1) / 2;
  FILE *to[4], *from[4];
  for (int i = 0; i < np; i++) {
    to[i] = fopen(argv[2 + 2 * i], "w");   /* open m2u first: unblocks the process's read */
    from[i] = fopen(argv[1 + 2 * i], "r");
    if (!to[i] || !from[i]) { puts("0.0"); fputs("manager: cannot open fifos\n", stderr); return 0; }
  }
  int q, ok = 1;
  if (scanf("%d", &q) != 1) return 1;
  for (int k = 0; k < q; k++) {
    long long a, b, r;
    if (scanf("%lld %lld", &a, &b) != 2) return 1;
    int i = k % np;
    fprintf(to[i], "%lld %lld\n", a, b); fflush(to[i]);
    if (fscanf(from[i], "%lld", &r) != 1 || r != a + b) { ok = 0; break; }
  }
  for (int i = 0; i < np; i++) { fprintf(to[i], "0 0 end\n"); fflush(to[i]); }
  if (ok) { puts("1.0"); fputs("translate:success\n", stderr); }
  else { puts("0.0"); fputs("translate:wrong\n", stderr); }
  return 0;
}
