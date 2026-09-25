# Python stub for the Communication tests: argv = m2u u2m.
import sys
import sol
fin = open(sys.argv[1], "r")
fout = open(sys.argv[2], "w")
for line in fin:
    a, b = line.split()[:2]
    if a == "0" and b == "0":
        break
    fout.write(str(sol.add(int(a), int(b))) + "\n")
    fout.flush()
