#!/bin/bash
# Builds customs-llm-<version>-macos-universal.dmg. Needs macOS (lipo, hdiutil) and Go.
# usage: packaging/macos/package.sh VERSION OUTDIR
set -euo pipefail
version="$1"; out="$2"
root="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$out"
for arch in arm64 amd64; do
  (cd "$root" && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$work/customs-$arch" ./cmd/customs)
done
app="$work/dmg/customs-llm.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
lipo -create -output "$app/Contents/Resources/customs" "$work/customs-arm64" "$work/customs-amd64"
cp "$root/packaging/macos/customs-launcher" "$app/Contents/MacOS/customs-launcher"
chmod +x "$app/Contents/MacOS/customs-launcher" "$app/Contents/Resources/customs"
sed "s/__VERSION__/$version/g" "$root/packaging/macos/Info.plist" > "$app/Contents/Info.plist"
ln -s /Applications "$work/dmg/Applications"
hdiutil create -quiet -volname "customs-llm" -srcfolder "$work/dmg" -format UDZO -ov "$out/customs-llm-$version-macos-universal.dmg"
echo "$out/customs-llm-$version-macos-universal.dmg"
