#!/bin/sh
# Records demo.gif. Starts a throwaway SSH server on loopback so the session
# tabs show real shells, seeds a database with plausible infrastructure, then
# drives the UI with VHS.
#
#   ./hack/demo.sh          (needs vhs: brew install vhs)
set -e
cd "$(dirname "$0")/.."

PORT=42222
WORK=/tmp/omassh-demo
rm -rf "$WORK"; mkdir -p "$WORK"

ssh-keygen -t ed25519 -N "" -C demo-host   -f "$WORK/hostkey" -q
ssh-keygen -t ed25519 -N "" -C demo-client -f "$WORK/id_demo" -q

go build -o "$WORK/omassh" ./cmd/omassh
go build -o "$WORK/demoserver" ./hack/demoserver

"$WORK/demoserver" -hostkey "$WORK/hostkey" -addr "127.0.0.1:$PORT" &
SERVER=$!
trap 'kill $SERVER 2>/dev/null || true' EXIT

# Wait for it to accept before seeding hosts that point at it.
i=0
while [ $i -lt 50 ]; do
  nc -z 127.0.0.1 $PORT 2>/dev/null && break
  i=$((i+1)); sleep 0.1
done

go run ./hack/seed "$WORK/omassh.db" "$PORT" "$WORK/id_demo"
ssh-keyscan -p $PORT -t ed25519 127.0.0.1 > "$WORK/known_hosts" 2>/dev/null

vhs demo.tape
echo "wrote demo.gif"
