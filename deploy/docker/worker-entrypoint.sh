#!/bin/sh
# Prepares a delegated cgroup v2 subtree for isolate inside a privileged
# container (cgroupns=private), then execs the worker.
#
# cgroup v2 forbids processes in a cgroup whose controllers are delegated
# ("no internal processes"), so every process is first moved to a leaf.
set -eu
CG=/sys/fs/cgroup
if [ -f "$CG/cgroup.controllers" ]; then
  mkdir -p "$CG/init"
  for p in $(cat "$CG/cgroup.procs"); do echo "$p" > "$CG/init/cgroup.procs" 2>/dev/null || true; done
  for c in cpu cpuset memory pids; do echo "+$c" > "$CG/cgroup.subtree_control" 2>/dev/null || true; done
  mkdir -p "$CG/isolate"
  for c in cpu cpuset memory pids; do echo "+$c" > "$CG/isolate/cgroup.subtree_control" 2>/dev/null || true; done
else
  echo "worker-entrypoint: cgroup v2 not available; running isolate without --cg" >&2
  export CMS_ISOLATE_CG=false
fi
exec "$@"
