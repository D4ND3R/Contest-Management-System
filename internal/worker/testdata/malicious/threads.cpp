// Spawns many busy threads. Without a process allowance thread creation
// fails (std::system_error -> abort); with one, CPU time is accounted over
// all threads and the time limit is hit.
#include <atomic>
#include <thread>
#include <vector>
std::atomic<long> counter{0};
int main() {
    std::vector<std::thread> ts;
    for (int i = 0; i < 16; i++) ts.emplace_back([] { for (;;) counter++; });
    for (auto &t : ts) t.join();
}
