#!/usr/bin/env bash
#
# Run the smoke tests on Linux, against a real libsecret keyring.
#
# The suite itself needs no Docker and must not grow a dependency on one —
# this is a tool, not a test. What it is for is the half of the credential
# work that cannot be checked on a Mac: security(1) and secret-tool(1) are
# different programs with different opinions, and three bugs lived in the one
# nobody here could run. They were found by running it, and this is how to run
# it again.
#
#   hack/linux-smoke.sh              secret and smoke, against a real keyring
#   hack/linux-smoke.sh -v           the same, saying what ran
#   hack/linux-smoke.sh ./... -v     the whole suite, on Linux
#
# It needs Docker, and nothing else.
set -euo pipefail

cd "$(dirname "$0")/.."
# Everything is passed through to go test. With no package named, both of the
# packages that behave differently here: internal/secret is where secret-tool's
# own contract is pinned — exit codes, and what it does and does not add to a
# secret — and internal/smoke is where that reaches the interface. The three
# bugs this exists for were all in the first of those.
args=("$@")
case "${1:-}" in
  ""|-*) args=("./internal/secret" "./internal/smoke" "$@") ;;
esac
image=omassh-linux-smoke
platform="${OMASSH_SMOKE_PLATFORM:-linux/$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')}"

if ! docker info >/dev/null 2>&1; then
  echo "linux-smoke: no docker daemon — start one, or run the tests on a Linux box" >&2
  exit 1
fi

# Built once and cached by Docker; the layers change only when this changes.
docker build --platform "$platform" -t "$image" -q - >/dev/null <<'DOCKERFILE'
FROM golang:1.26-bookworm
# tmux, because the smoke tests drive the interface through one. libsecret and
# gnome-keyring, because that is the store on Linux. openssh-client, because
# omassh drives the real ssh rather than speaking the protocol itself.
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends \
      tmux openssh-client libsecret-tools gnome-keyring dbus-x11 \
 && rm -rf /var/lib/apt/lists/*
DOCKERFILE

# A keyring has to be running before the tests start, and it needs a session
# bus to be running on. Unlocked with an empty password, which is what a
# throwaway container's keyring is worth.
run='
set -e
eval "$(dbus-launch --sh-syntax)"
printf "\n" | gnome-keyring-daemon --unlock --components=secrets >/dev/null 2>&1 &
sleep 2
eval "$(printf "\n" | gnome-keyring-daemon --start --components=secrets 2>/dev/null)"
export GNOME_KEYRING_CONTROL

if ! printf "probe" | secret-tool store --label=probe service omassh-probe account probe 2>/dev/null; then
  echo "linux-smoke: the keyring is not answering; the password tests would skip" >&2
  exit 1
fi
secret-tool clear service omassh-probe account probe

# The keychain tests are opt-in everywhere else because they write into the
# keyring of whoever runs them. Here the keyring is the container.
export OMASSH_KEYCHAIN_TEST=1
exec go test -count=1 "$@"
'

# The tree read-only, so a test cannot rewrite the working copy; the module and
# build caches in a volume, so a second run does not download Go again.
exec docker run --rm --platform "$platform" \
  -v "$PWD":/src:ro -w /src \
  -v omassh-linux-smoke-cache:/cache \
  -e GOMODCACHE=/cache/mod -e GOCACHE=/cache/build -e GOFLAGS=-mod=mod \
  "$image" bash -c "$run" _ "${args[@]}"
