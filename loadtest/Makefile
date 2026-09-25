# k6 load tests (see README.md). Run from the repository root with
# "make loadtest", as root (the sandbox needs it).
all:
	cd .. && loadtest/run.sh

smoke:
	cd .. && CONTESTANTS=20 SPECTATORS=20 NORMAL_MIN=1 BURST_MIN=1 loadtest/run.sh

.PHONY: all smoke
