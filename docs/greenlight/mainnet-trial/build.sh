#!/usr/bin/env bash
# build.sh — reproducible-ish build of the Alby Hub HTTP binary (Greenlight backend)
# Usage: build.sh <alby-hub source checkout> <output dir>
# Needs: git, Go >= the version in go.mod, a C compiler (CGO is required by
# the LDK bindings that ship in the same binary), Node >= 22.12 and yarn 1.x.
# Works on Linux (amd64/arm64) and macOS. On Windows use WSL2 (Ubuntu) and run
# this script inside it; native Windows builds are not covered by this kit.
set -euo pipefail
umask 022
SRC=$(cd "${1:?source dir}" && pwd); OUT=${2:?output dir}
mkdir -p "$OUT"; OUT=$(cd "$OUT" && pwd)
cd "$SRC"
[ -z "$(git status --porcelain --untracked-files=no)" ] || { echo "source tree has uncommitted changes; refusing" >&2; exit 1; }
COMMIT=$(git rev-parse HEAD); BRANCH=$(git rev-parse --abbrev-ref HEAD)
OS=$(go env GOOS); ARCH=$(go env GOARCH)
echo "building $BRANCH @ $COMMIT for $OS/$ARCH"
node -e 'const [a,b]=process.versions.node.split(".").map(Number); if(a<22||(a===22&&b<12)){console.error("need Node >= 22.12, have "+process.versions.node);process.exit(1)}'
( cd frontend && yarn install --frozen-lockfile --network-timeout 3000000 && yarn build:http )
export CGO_ENABLED=1
go build -trimpath -ldflags="-X 'github.com/getAlby/hub/version.Tag=greenlight-trial-${COMMIT:0:8}'" -o "$OUT/albyhub" ./cmd/http
# the LDK shared library is linked even when the Greenlight backend is used
LDKDIR=$(go list -m -f '{{.Dir}}' github.com/getAlby/ldk-node-go)
case "$OS/$ARCH" in
  linux/amd64) cp "$LDKDIR/ldk_node/x86_64-unknown-linux-gnu/libldk_node.so" "$OUT/";;
  linux/arm64) cp "$LDKDIR/ldk_node/aarch64-unknown-linux-gnu/libldk_node.so" "$OUT/";;
  darwin/*)    find "$LDKDIR/ldk_node" -name 'libldk_node.dylib' -path "*darwin*" -exec cp {} "$OUT/" \; -quit;;
esac
( cd frontend && tar -cf - dist ) | (cd "$OUT" && tar -xf -)   # copy of the embedded UI, for reference
cat > "$OUT/BUILDINFO.txt" <<INFO
branch=$BRANCH
commit=$COMMIT
goos=$OS goarch=$ARCH
go=$(go version | awk '{print $3}')
node=$(node --version)
built_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)
INFO
SHA=sha256sum; command -v sha256sum >/dev/null || SHA="shasum -a 256"
( cd "$OUT" && ls -p | grep -v / | grep -vx SHA256SUMS | sort | xargs $SHA > SHA256SUMS )
cat "$OUT/SHA256SUMS"
