#!/usr/bin/env bash
# Checks what GoReleaser wrote to DIST (default dist/): one tarball per
# architecture, matching checksums, and in each tarball everything the
# installer and `cmsctl upgrade` rely on. Used by CI on every push (with a
# snapshot release) so the release configuration cannot rot between tags.
set -euo pipefail
DIST=${1:-dist}
cd "$DIST"
sha256sum --check --strict checksums.txt
need=(cms cmsctl LICENSE NOTICE README.md config/cms.example.yaml config/languages/cpp17.yaml
      migrations/0001_schema.sql web/templates/aws/layout.html deploy/systemd/cms.target
      deploy/systemd/cms-worker.service scripts/install.sh scripts/install-isolate.sh scripts/verify-host.sh
      docs/en/deployment.md docs/es/despliegue.md)
for arch in amd64 arm64; do
  tgz=$(ls cms_*_linux_"$arch".tar.gz)
  top=${tgz%.tar.gz}
  list=$(tar -tzf "$tgz")
  for f in "${need[@]}"; do
    grep -qx "$top/$f" <<<"$list" || { echo "$tgz: missing $f" >&2; exit 1; }
  done
  echo "$tgz: ${#need[@]} required files present"
done
# The amd64 binary runs and knows its version.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tar -xzf "$(ls cms_*_linux_amd64.tar.gz)" -C "$tmp"
for bin in "$tmp"/cms_*/cms; do "$bin" version; done
