#!/bin/sh
# Pack one platform's release as a .tar.gz for `module add -from`: module.yaml,
# the browser half (ui/) and the one binary, at the root of the archive. The
# browser half comes from `sh scripts/ui-build.sh`.
#
#   sh scripts/bundle.sh [platform [out-dir]]     default linux_amd64 (Unraid, most NAS and servers)
#
# Writes <name>-<platform>.tar.gz into out-dir (default dist-bundles/, which is
# ignored by git). Needs go and tar.
set -e

HERE=$(cd "$(dirname "$0")/.." && pwd)
PLATFORM=${1:-linux_amd64}
OUTDIR=${2:-$HERE/dist-bundles}
NAME=$(sed -n 's/^name: *//p' "$HERE/module.yaml" | head -n 1)
[ -n "$NAME" ] || { echo "FAIL  no name in module.yaml" >&2; exit 1; }
OS=${PLATFORM%_*}
ARCH=${PLATFORM#*_}
EXT=
[ "$OS" = windows ] && EXT=.exe

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

if [ ! -f "$HERE/ui/dist/ui/index.js" ]; then
	echo "FAIL  the browser half is not built. Run: sh scripts/ui-build.sh" >&2
	exit 1
fi
# The browser half is built by a separate step, so a source edited since is not
# in it. Refuse that rather than ship the old page.
if [ -n "$(find "$HERE/ui/src" "$HERE/ui/package.json" -type f -newer "$HERE/ui/dist/ui/index.js" | head -n 1)" ]; then
	echo "FAIL  ui/ has files newer than the built browser half. Run: sh scripts/ui-build.sh" >&2
	exit 1
fi
cp -R "$HERE/ui/dist/." "$STAGE/"
cp "$HERE/module.yaml" "$STAGE/module.yaml"
CGO_ENABLED=0 GOOS=$OS GOARCH=$ARCH go -C "$HERE" build -trimpath -o "$STAGE/${NAME}_${PLATFORM}${EXT}" .

# docker cp extracts as root, and the server runs as an unprivileged user, so
# the files must be readable by others whatever mode the source tree has.
find "$STAGE" -type d -exec chmod 755 {} +
find "$STAGE" -type f -exec chmod 644 {} +
chmod 755 "$STAGE/${NAME}_${PLATFORM}${EXT}"

mkdir -p "$OUTDIR"
OUT=$OUTDIR/$NAME-$PLATFORM.tar.gz
# Named files, not ".": module add refuses an archive entry like "./".
COPYFILE_DISABLE=1 tar --no-xattrs -C "$STAGE" -czf "$OUT" module.yaml ui "${NAME}_${PLATFORM}${EXT}"
echo "wrote $OUT"
tar -tzf "$OUT"
