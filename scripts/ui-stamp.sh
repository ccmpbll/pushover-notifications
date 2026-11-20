#!/bin/sh
# Print a checksum of what the browser half is built from: the page, the typed
# client, the route description, the build and lint configuration (they sit
# beside the sources, outside src/), the lockfiles and the pinned library
# commit. ui-steps.sh writes it into .ui-stamp when it builds, and bundle.sh
# compares, so a bundle is refused when the page was not built from things as
# they are now. It is the content, not the file times, so a `git pull` that
# changes nothing here is fine.
#
# The stamp is kept at the repository root and not in ui/dist, because the
# marketplace publisher packs everything in ui/dist into the release.
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
find ui client openapi.json .core-version \
	-type f -not -path '*/node_modules/*' -not -path '*/dist/*' |
	LC_ALL=C sort | xargs cat | cksum
