#!/bin/sh
set -eu
image=${1:?image is required}
user=$(docker image inspect --format '{{.Config.User}}' "$image")
[ "$user" = '10001:10001' ] || { echo 'runtime image must be non-root' >&2; exit 1; }
for binary in jobman-dashboard jobman-log-broker; do
  docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges \
    --entrypoint "$binary" "$image" version | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["os"] == "linux" and d["formatVersion"] == 1'
done
if docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges "$image"; then
  echo 'unconfigured service unexpectedly started successfully' >&2
  exit 1
fi
docker run --rm --network none --read-only --entrypoint /bin/sh "$image" -c \
  'test -s /usr/share/jobman-dashboard/web/index.html && test -s /usr/share/licenses/jobman-dashboard/LICENSE'
echo 'container identity, metadata, assets and fail-closed startup verified'
