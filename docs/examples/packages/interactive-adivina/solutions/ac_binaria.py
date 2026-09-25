import sys
lo, hi = 1, 10**9
while lo < hi:
    mid = (lo + hi) // 2
    print("?", mid, flush=True)
    r = sys.stdin.readline().strip()
    if r == "=":
        lo = hi = mid
        break
    if r == "<":
        hi = mid - 1
    else:
        lo = mid + 1
print("!", lo, flush=True)
