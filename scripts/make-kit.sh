#!/bin/sh
# Build the folder to copy to the Docker host: the installer, the module for
# each CPU asked for, and a SHA256SUMS to check the copy against.
#
#   sh scripts/make-kit.sh [platform ...]     default: linux_amd64 (Unraid, most servers)
#
# Writes kit/ beside the module:
#
#   kit/install.sh
#   kit/<name>-linux_amd64.tar.gz
#   kit/SHA256SUMS
#
# The kit is built beside kit/ and moved into place only when it is whole, so a
# failed build leaves the last good kit as it was.
#
# Then: scp -r kit root@<host>:/tmp/   and on the host: sh /tmp/kit/install.sh <container>
set -eu

HERE=$(cd "$(dirname "$0")/.." && pwd)
KIT=$HERE/kit
NEW=$HERE/kit.new

# The name and module path are read from the files that own them.
NAME=$(sed -n 's/^name: *//p' "$HERE/module.yaml" | head -n 1)
MODULE=$(sed -n 's/^module  *//p' "$HERE/go.mod" | head -n 1)
[ -n "$NAME" ] && [ -n "$MODULE" ] || { echo "FAIL  could not read the name from module.yaml and the module path from go.mod" >&2; exit 1; }

[ $# -gt 0 ] || set -- linux_amd64

rm -rf "$NEW"
trap 'rm -rf "$NEW"' EXIT
mkdir -p "$NEW"
sed "s|@MODULE@|$MODULE|" "$HERE/scripts/install-on-host.sh" > "$NEW/install.sh"
chmod 755 "$NEW/install.sh"

for platform in "$@"; do
	sh "$HERE/scripts/bundle.sh" "$platform" "$NEW" > /dev/null
done

(
	cd "$NEW"
	if command -v sha256sum > /dev/null 2>&1; then
		sha256sum "$NAME"-*.tar.gz > SHA256SUMS
	else
		shasum -a 256 "$NAME"-*.tar.gz > SHA256SUMS
	fi
)

rm -rf "$KIT"
mv "$NEW" "$KIT"

echo "kit: $KIT"
ls -la "$KIT"
