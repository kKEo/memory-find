#!/bin/sh
# Builds memo-tray.app: one universal binary (arm64 + x86_64), the bundle
# around it, ad-hoc signed, zipped with a SHA-256 file next to it.
#
#   tray/scripts/build-app.sh [version]      (macOS with Xcode command-line tools)
#
# Output: tray/dist/memo-tray.app and tray/dist/memo-tray_<version>_darwin_universal.zip.
# The app is not notarized: on first open macOS asks you to allow it
# (System Settings > Privacy & Security > Open Anyway).
set -eu
cd "$(dirname "$0")/.."

VERSION=${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
# CFBundleShortVersionString wants plain numbers: v1.5.0-3-gabc -> 1.5.0.
SHORT=$(printf '%s' "$VERSION" | sed -E 's/^v//; s/-.*$//')
case "$SHORT" in
[0-9]*.[0-9]*.[0-9]*) ;;
*) SHORT=0.0.0 ;;
esac

OUT=dist
APP=$OUT/memo-tray.app
rm -rf "$OUT"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

export CGO_ENABLED=1 GOOS=darwin MACOSX_DEPLOYMENT_TARGET=12.0
export CGO_CFLAGS="-mmacosx-version-min=12.0" CGO_LDFLAGS="-mmacosx-version-min=12.0"
for arch in arm64 amd64; do
	GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/memo-tray-$arch" ./cmd/memo-tray
done
lipo -create -output "$APP/Contents/MacOS/memo-tray" "$OUT/memo-tray-arm64" "$OUT/memo-tray-amd64"
rm "$OUT/memo-tray-arm64" "$OUT/memo-tray-amd64"

sed "s/@SHORT_VERSION@/$SHORT/g" packaging/Info.plist >"$APP/Contents/Info.plist"
plutil -lint -s "$APP/Contents/Info.plist"

GOOS= GOARCH= go run ./scripts/iconset "$OUT/AppIcon.iconset"
iconutil -c icns "$OUT/AppIcon.iconset" -o "$APP/Contents/Resources/AppIcon.icns"
rm -rf "$OUT/AppIcon.iconset"

# Licences: memo-tray's own, then every module linked into the binary.
cp ../LICENSE "$APP/Contents/Resources/LICENSE"
notices=$APP/Contents/Resources/THIRD_PARTY_NOTICES
: >"$notices"
go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Dir}}{{end}}{{end}}' ./cmd/memo-tray | sort -u |
	while read -r path dir; do
		[ "$path" = github.com/kKEo/memory-find ] && continue # covered by LICENSE
		for f in LICENSE LICENSE.txt COPYING NOTICE; do
			if [ -f "$dir/$f" ]; then
				printf '==== %s (%s)\n\n' "$path" "$f" >>"$notices"
				cat "$dir/$f" >>"$notices"
				printf '\n' >>"$notices"
			fi
		done
	done

codesign --force --sign - --timestamp=none "$APP"
codesign --verify --strict "$APP"

ZIP="memo-tray_${VERSION#v}_darwin_universal.zip"
(cd "$OUT" && ditto -c -k --keepParent memo-tray.app "$ZIP" && shasum -a 256 "$ZIP" >"$ZIP.sha256")
echo "$OUT/$ZIP"
