#!/bin/sh
# Print one fact about this module, read from the file that owns it, so no
# script writes it a second time:
#
#   sh scripts/manifest-field.sh name      module.yaml: name
#   sh scripts/manifest-field.sh version   module.yaml: version
#   sh scripts/manifest-field.sh module    go.mod: the module path
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)

case ${1:-} in
name | version) value=$(sed -n "s/^$1: *//p" "$ROOT/module.yaml" | head -n 1) ;;
module) value=$(sed -n 's/^module  *//p' "$ROOT/go.mod" | head -n 1) ;;
*)
	echo "usage: manifest-field.sh name|version|module" >&2
	exit 2
	;;
esac

[ -n "$value" ] || { echo "FAIL  no $1 found" >&2; exit 1; }
printf '%s\n' "$value"
