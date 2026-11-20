#!/bin/sh
# Install (or reinstall) the pushover-notifications module into a Prusa Connect
# Docker container. Run it ON the machine that runs Docker (for example an
# Unraid server), from the folder `scripts/make-kit.sh` built (it is installed
# there as install.sh, beside pushover-notifications-linux_<cpu>.tar.gz and
# SHA256SUMS).
#
#   sh install.sh [-n] [-y] [-c <config path>] <container>
#
#   sh install.sh prusa-connect
#
# It sets nothing up: open the Pushover Notifications page in the app and enter
# your token and user key there (credentials are never given on a command line).
# A reinstall passes no settings, and the ones saved in the app live in the
# host's own records, which `module add` does not write, so this is also how to
# upgrade. After the first upgrade, check on the page that the token is still set.
#
#   <container>   the Connect container's name, as `docker ps` shows it
#   -n            show what would run and stop; nothing is changed
#   -y            do not ask before stopping and starting the container
#   -c <path>     the container's config file, if it is not /config/config.yaml
#
# It checks the copy against SHA256SUMS, copies the module into the container's
# /data, stops the container, runs `module add` in a throwaway container on the
# same volumes (as the image's own user), removes the copy, starts the
# container again (the install is read at start) and shows what the module
# logged. Needs only sh, docker and coreutils.
set -eu

DIR=$(cd "$(dirname "$0")" && pwd)
DRY=0
ASSUME_YES=0
CONFIG=

while getopts "nyc:" opt; do
	case $opt in
	n) DRY=1 ;;
	y) ASSUME_YES=1 ;;
	c) CONFIG=$OPTARG ;;
	*) exit 2 ;;
	esac
done
shift $((OPTIND - 1))

if [ $# -ne 1 ]; then
	awk 'NR == 1 { next } /^#/ { sub(/^# ?/, ""); print; next } { exit }' "$0"
	exit 2
fi

CONTAINER=$1

fail() {
	echo "FAIL  $*" >&2
	exit 1
}

# The container name and config path go into shell lines, so they are plain.
printf '%s\n' "$CONTAINER" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_.-]*$' ||
	fail "the container name is letters, digits, _, . and - only: $CONTAINER"
if [ -n "$CONFIG" ]; then
	printf '%s\n' "$CONFIG" | grep -Eq '^[A-Za-z0-9_./-]+$' ||
		fail "the config path is letters, digits, _, ., / and - only: $CONFIG"
fi

# Which build matches this machine.
case $(uname -m) in
x86_64 | amd64) PLATFORM=linux_amd64 ;;
aarch64 | arm64) PLATFORM=linux_arm64 ;;
*) fail "no build for this CPU: $(uname -m)" ;;
esac
set -- "$DIR"/*-"$PLATFORM".tar.gz
[ $# -eq 1 ] && [ -f "$1" ] || fail "there is not exactly one *-$PLATFORM.tar.gz in $DIR. Copy the whole kit folder, not just install.sh"
BUNDLE=$(basename "$1")

# A copy that was cut short or changed would be installed and run as it is.
if [ -f "$DIR/SHA256SUMS" ]; then
	if command -v sha256sum > /dev/null 2>&1; then
		(cd "$DIR" && grep " $BUNDLE\$" SHA256SUMS | sha256sum -c - > /dev/null 2>&1) ||
			fail "$BUNDLE does not match SHA256SUMS. Copy it again"
	elif command -v shasum > /dev/null 2>&1; then
		(cd "$DIR" && grep " $BUNDLE\$" SHA256SUMS | shasum -a 256 -c - > /dev/null 2>&1) ||
			fail "$BUNDLE does not match SHA256SUMS. Copy it again"
	else
		echo "NOTE  no sha256sum here, so the copy was not checked against SHA256SUMS"
	fi
else
	echo "NOTE  no SHA256SUMS beside the bundle, so the copy was not checked"
fi

# make-kit.sh writes this line from go.mod.
MODULE=@MODULE@
case $MODULE in @*) fail "MODULE was not filled in. Use the install.sh that scripts/make-kit.sh wrote" ;; esac
NAME=$(tar -xzOf "$DIR/$BUNDLE" module.yaml | sed -n 's/^name: *//p' | head -n 1)
VERSION=$(tar -xzOf "$DIR/$BUNDLE" module.yaml | sed -n 's/^version: *//p' | head -n 1)
[ -n "$VERSION" ] || fail "could not read the version from $BUNDLE"

CONFIG_FLAG=
[ -z "$CONFIG" ] || CONFIG_FLAG=" -config $CONFIG"

echo "Container   $CONTAINER"
echo "Module      $MODULE@$VERSION ($PLATFORM)"
echo

# run shows the command and runs it, or only shows it with -n.
run() {
	if [ "$DRY" = 1 ]; then
		echo "  would run: $*"
	else
		"$@"
	fi
}

IMAGE=$(docker inspect -f '{{.Config.Image}}' "$CONTAINER" 2>/dev/null || true)
[ -n "$IMAGE" ] || IMAGE="<the container's image>"

if [ "$DRY" = 0 ]; then
	state=$(docker inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null || true)
	[ "$state" = true ] || fail "$CONTAINER is not a running container. Check the name with: docker ps"

	if [ "$ASSUME_YES" = 0 ]; then
		echo "This STOPS $CONTAINER, installs the module, and starts it again. Printers"
		echo "reconnect afterwards, and a file transfer to a printer that is in progress"
		echo "would fail."
		printf "Continue? [y/N] "
		read -r answer
		case $answer in y | Y | yes) ;; *)
			echo "Stopped. Nothing was changed."
			exit 1
			;;
		esac
	fi
fi

WORK=$(mktemp -d "${TMPDIR:-/tmp}/pushover-install.XXXXXX")
# mktemp makes private files, docker cp keeps the mode, and the server runs as
# another user inside the container, so the copy must be readable by others.
chmod 755 "$WORK"
cp "$DIR/$BUNDLE" "$WORK/$BUNDLE"
chmod 644 "$WORK/$BUNDLE"

# inside runs one command in a throwaway container that shares the Connect
# container's volumes and image (so it is the same user, config and data), with
# no network. It works whether the Connect container is running or stopped.
inside() {
	entry=$1
	shift
	docker run --rm --volumes-from "$CONTAINER" --network none --entrypoint "$entry" "$IMAGE" "$@"
}

COPIED=0
STOPPED=0
cleanup() {
	status=$?
	if [ "$COPIED" = 1 ]; then
		inside rm -f "/data/$BUNDLE" > /dev/null 2>&1 || true
	fi
	if [ "$STOPPED" = 1 ]; then
		echo "Starting $CONTAINER again..."
		docker start "$CONTAINER" > /dev/null 2>&1 || echo "FAIL  could not start $CONTAINER. Start it from the Unraid Docker tab." >&2
	fi
	rm -rf "$WORK"
	exit $status
}
trap cleanup EXIT

echo "1. copying into $CONTAINER:/data..."
# An old copy goes first: docker cp overwrites a file without changing its mode.
if [ "$DRY" = 0 ]; then
	COPIED=1
	docker exec "$CONTAINER" rm -f "/data/$BUNDLE"
fi
run docker cp "$WORK/$BUNDLE" "$CONTAINER:/data/$BUNDLE"

echo "2. stopping $CONTAINER..."
# A running module holds its own files open, and on some file systems (Unraid's
# user shares among them) a file that is open cannot be cleared away, so
# `module add` could not replace an earlier install. A stopped core also has no
# database open while the grants are written.
run docker stop "$CONTAINER"
[ "$DRY" = 1 ] || STOPPED=1

echo "3. installing..."
# No -settings: `module add` rewrites only the declaration, not the records the
# app saves settings in.
# shellcheck disable=SC2086 # CONFIG_FLAG is empty or " -config <path>"
run inside /prusa_connect module add "$MODULE@$VERSION" \
	-from "/data/$BUNDLE" -approve $CONFIG_FLAG

echo "4. removing the copy..."
run inside rm -f "/data/$BUNDLE"
[ "$DRY" = 1 ] || COPIED=0

echo "5. starting $CONTAINER..."
run docker start "$CONTAINER"
[ "$DRY" = 1 ] || STOPPED=0

UNHEALTHY=
echo "6. waiting for it to be healthy..."
if [ "$DRY" = 1 ]; then
	echo "  would wait for the container's health check, up to 90 seconds"
else
	i=0
	health=
	while [ $i -lt 45 ]; do
		health=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$CONTAINER" 2>/dev/null || true)
		if [ "$health" = healthy ] || [ "$health" = none ]; then
			break
		fi
		i=$((i + 1))
		sleep 2
	done
	echo "   health: $health"
	[ "$health" = healthy ] || [ "$health" = none ] || UNHEALTHY=$health
fi

echo "7. what the module logged:"
if [ "$DRY" = 1 ]; then
	echo "  would run: docker logs --since 2m $CONTAINER | grep $NAME"
else
	sleep 3
	docker logs --since 2m "$CONTAINER" 2>&1 | grep -i "$NAME" | tail -n 8 | cut -c1-220 || true
fi

[ -z "$UNHEALTHY" ] || fail "$CONTAINER is $UNHEALTHY, not healthy. Look at: docker logs --tail 50 $CONTAINER"

echo
echo "Done. Open Pushover Notifications in the app's sidebar to set your token and user key."
echo "Watch it with:  docker logs -f $CONTAINER 2>&1 | grep -i pushover"
