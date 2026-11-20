#!/bin/sh
# Print a checksum of what the browser half is built from. ui-steps.sh writes it
# into ui/dist when it builds, and bundle.sh compares, so a bundle is refused
# when the page was not built from the sources as they are now. It is the
# content, not the file times, so a `git pull` that changes nothing here is fine.
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
find ui/src ui/package.json openapi.json -type f | LC_ALL=C sort | xargs cat | cksum
