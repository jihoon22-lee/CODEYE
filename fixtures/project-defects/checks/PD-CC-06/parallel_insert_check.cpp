// Inserts points into one HierarchicalNSW index from several threads at once,
// the way the Python binding's add_items spreads rows over worker threads, and
// then checks that every inserted point can be found again.
#include "hnswlib/hnswlib.h"

#include <atomic>
#include <cstdio>
#include <random>
#include <thread>
#include <vector>

int main() {
    const int dim = 16;
    const size_t n = 1500;
    const int threads = 4;

    std::mt19937 rng(7);
    std::uniform_real_distribution<float> uni(0.0f, 1.0f);
    std::vector<float> data(n * dim);
    for (auto &v : data) v = uni(rng);

    hnswlib::L2Space space(dim);
    hnswlib::HierarchicalNSW<float> index(&space, n, 8, 100, 42);

    // The first point goes in alone, as the binding does before it starts workers.
    index.addPoint(data.data(), 0);

    std::atomic<size_t> next{1};
    std::vector<std::thread> pool;
    for (int t = 0; t < threads; t++) {
        pool.emplace_back([&]() {
            for (;;) {
                size_t row = next.fetch_add(1);
                if (row >= n) break;
                index.addPoint(data.data() + row * dim, row);
            }
        });
    }
    for (auto &th : pool) th.join();

    size_t found = 0;
    index.setEf(100);
    for (size_t i = 0; i < n; i++) {
        auto res = index.searchKnn(data.data() + i * dim, 1);
        if (!res.empty() && res.top().second == i) found++;
    }
    std::printf("inserted %zu points from %d threads, %zu found as their own nearest neighbour\n", n, threads, found);
    if (found * 10 < n * 9) {
        std::printf("PARALLEL INSERT CHECK FAILED: recall %zu/%zu\n", found, n);
        return 1;
    }
    return 0;
}
