#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
BUILD_DIR="$(mktemp -d "$ROOT_DIR/_build-fpk.XXXXXX")"
trap 'rm -rf "$BUILD_DIR"' EXIT
mkdir -p dist "$BUILD_DIR/package" "$BUILD_DIR/app/bin"
cp -a _pkg/fnssh/app/. "$BUILD_DIR/app/"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$BUILD_DIR/app/bin/fnssh" .
chmod 755 "$BUILD_DIR/app/bin/fnssh"
cp -a _pkg/fnssh/cmd _pkg/fnssh/config _pkg/fnssh/ICON.PNG _pkg/fnssh/ICON_256.PNG "$BUILD_DIR/package/"
chmod 755 "$BUILD_DIR/package/cmd/"*
mkdir -p "$BUILD_DIR/package/wizard"
tar -czf "$BUILD_DIR/package/app.tgz" -C "$BUILD_DIR/app" .
# Convert the source manifest's multiline string notation to FPK format.
sed 's/"""//g' _pkg/fnssh/manifest > "$BUILD_DIR/package/manifest"
printf 'checksum              = %s\n' "$(md5sum "$BUILD_DIR/package/app.tgz" | cut -d' ' -f1)" >> "$BUILD_DIR/package/manifest"
tar -czf dist/fnssh.fpk -C "$BUILD_DIR/package" app.tgz cmd config ICON.PNG ICON_256.PNG manifest wizard
cp "$BUILD_DIR/app/bin/fnssh" dist/fnssh-linux-amd64
(cd dist && sha256sum fnssh.fpk fnssh-linux-amd64 > SHA256SUMS)
echo 'Built dist/fnssh.fpk and dist/fnssh-linux-amd64'
