#!/usr/bin/env bash
set -euo pipefail
image="${MFR_TEST_IMAGE:-mfr-local:test}"
platform_args=()
if [ -n "${MFR_TEST_PLATFORM:-}" ]; then platform_args=(--platform "$MFR_TEST_PLATFORM"); fi
artifacts=$(mktemp -d)
trap 'rm -rf "$artifacts"' EXIT
docker build "${platform_args[@]}" --target final -t "$image" .
docker build "${platform_args[@]}" --target test-artifacts --output "type=local,dest=$artifacts" .
chmod 755 "$artifacts/integration.test"
docker run "${platform_args[@]}" --rm --user 10001:10001 --read-only --cap-drop ALL --security-opt no-new-privileges \
  --tmpfs /tmp:rw,uid=10001,gid=10001,mode=1777 \
  --tmpfs /state:rw,uid=0,gid=10001,mode=0770 --shm-size 256m \
  -e MFR_TEST_STATE_DIR=/state -e MFR_TEST_NO_SANDBOX=true -e MFR_BROWSER_PATH=/usr/bin/chromium \
  --mount "type=bind,src=$artifacts/integration.test,dst=/integration.test,readonly" \
  --entrypoint /usr/bin/tini "$image" -- /integration.test -test.v -test.timeout=5m -test.run="${MFR_CONTAINER_TEST_FILTER:-.}"
