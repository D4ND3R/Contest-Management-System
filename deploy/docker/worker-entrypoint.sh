#!/bin/sh
# Worker container entrypoint (runs as root): prepares a delegated cgroup v2
# subtree for isolate, then runs the worker as the unprivileged cms user,
# as systemd does (isolate is setuid root and does the privileged work).
#
# Without --privileged Docker mounts /sys/fs/cgroup read-only; with
# CAP_SYS_ADMIN, no AppArmor confinement and a private cgroup namespace
# (deploy/docker/compose.yml) it can be remounted writable. cgroup v2
# forbids processes in a cgroup whose controllers are delegated ("no
# internal processes"), so every process is first moved to a leaf.
set -eu
CG=/sys/fs/cgroup
if [ -f "$CG/cgroup.controllers" ]; then
  if ! mkdir -p "$CG/init" 2>/dev/null; then
    mount -o remount,rw "$CG" 2>/dev/null || true
    if ! mkdir -p "$CG/init" 2>/dev/null; then
      echo "worker-entrypoint: $CG is read-only: run the container with cap_add SYS_ADMIN, security_opt apparmor:unconfined and cgroup: private (see deploy/docker/compose.yml)" >&2
      exit 1
    fi
  fi
  while read -r p; do echo "$p" > "$CG/init/cgroup.procs" 2>/dev/null || true; done < "$CG/cgroup.procs"
  for c in cpu cpuset memory pids; do echo "+$c" > "$CG/cgroup.subtree_control" 2>/dev/null || true; done
  mkdir -p "$CG/isolate"
  for c in cpu cpuset memory pids; do echo "+$c" > "$CG/isolate/cgroup.subtree_control" 2>/dev/null || true; done
else
  echo "worker-entrypoint: cgroup v2 not available; running isolate without --cg" >&2
  export CMS_ISOLATE_CG=false
fi
if [ "$(id -u)" = 0 ] && id cms >/dev/null 2>&1; then
  exec setpriv --reuid=cms --regid=cms --init-groups "$@"
fi
exec "$@"
