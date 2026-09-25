/* Reads a TCP port from stdin and tries to reach the host on it, plus a
   public address. Prints CONNECTED on success. */
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <arpa/inet.h>
#include <sys/socket.h>
static void attempt(const char *ip, int port) {
    int s = socket(AF_INET, SOCK_STREAM, 0);
    if (s < 0) { printf("socket failed\n"); return; }
    struct sockaddr_in a; memset(&a, 0, sizeof a);
    a.sin_family = AF_INET; a.sin_port = htons(port);
    inet_pton(AF_INET, ip, &a.sin_addr);
    if (connect(s, (struct sockaddr *)&a, sizeof a) == 0) printf("CONNECTED %s:%d\n", ip, port);
    else printf("blocked %s:%d\n", ip, port);
    close(s);
}
int main(void) {
    int port = 0;
    if (scanf("%d", &port) != 1) return 2;
    attempt("127.0.0.1", port);
    attempt("10.0.0.1", port);
    attempt("1.1.1.1", 53);
    int u = socket(AF_INET, SOCK_DGRAM, 0);
    struct sockaddr_in a; memset(&a, 0, sizeof a);
    a.sin_family = AF_INET; a.sin_port = htons(53); inet_pton(AF_INET, "8.8.8.8", &a.sin_addr);
    if (u >= 0 && sendto(u, "x", 1, 0, (struct sockaddr *)&a, sizeof a) == 1) printf("UDP-SENT\n");
    return 0;
}
