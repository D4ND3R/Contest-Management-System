#!/usr/bin/env bash
# Builds and installs isolate (https://github.com/ioi/isolate) with cgroup v2
# support on a systemd host: binary in /usr/local/bin, the cgroup keeper
# service (isolate.service), and a configuration for CMS workers.
#
#   sudo scripts/install-isolate.sh [version]
set -euo pipefail
VERSION=${1:-v2.7}
SRC=$(mktemp -d)
trap 'rm -rf "$SRC"' EXIT

if command -v apt-get >/dev/null; then
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    build-essential git libcap-dev libseccomp-dev libsystemd-dev pkg-config >/dev/null
fi
git clone -q --depth 1 --branch "$VERSION" https://github.com/ioi/isolate.git "$SRC"
make -C "$SRC" -s isolate isolate-check-environment isolate-cg-keeper default.cf systemd/isolate.service
make -C "$SRC" -s install

# Fixed uid/gid range instead of /etc/subuid, one box per id; cgroup root
# provided by isolate-cg-keeper.
cat > /usr/local/etc/isolate <<CONF
box_root = /var/local/lib/isolate
lock_root = /run/isolate/locks
cg_root = auto:/run/isolate/cgroup
first_uid = 60000
first_gid = 60000
num_boxes = 1000
CONF
mkdir -p /var/local/lib/isolate
if command -v systemctl >/dev/null && [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable --now isolate.service
fi
isolate --version | head -1
