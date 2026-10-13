#!/bin/sh
# Produces ./bin/herdr-file-transfer. herdr runs this as the manifest's [[build]] step
# after `herdr plugin install` clones the repo; you can also run it by hand
# from anywhere.
#
# With Go installed it builds the checked-out source. Without Go it downloads
# the prebuilt binary for this exact version from the GitHub release, and
# installs it only if its SHA-256 matches the release's checksums.txt.
set -eu
cd "$(dirname "$0")/.."

REPO="mrolafsson/herdr-file-transfer"
mkdir -p bin

# HERDR_FILE_TRANSFER_PREBUILT=1 downloads even with Go installed (scripts/test-build.sh
# uses it: Go can be in /usr/bin, where no PATH trick hides it).
if [ "${HERDR_FILE_TRANSFER_PREBUILT:-}" != 1 ] && command -v go >/dev/null 2>&1; then
	echo "herdr-file-transfer: building from source…" >&2
	# go.mod's toolchain line makes an older Go fetch the patched one first.
	exec go build -trimpath -ldflags "-s -w" -o bin/herdr-file-transfer .
fi

# The release that matches this checkout: the manifest's version.
version=$(sed -n 's/^version *= *"\(.*\)"/\1/p' herdr-plugin.toml | head -n 1)
case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) echo "herdr-file-transfer: no prebuilt binary for $(uname -s); install Go and retry" >&2; exit 1 ;;
esac
case "$(uname -m)" in
	arm64 | aarch64) arch=arm64 ;;
	x86_64 | amd64) arch=amd64 ;;
	*) echo "herdr-file-transfer: no prebuilt binary for $(uname -m); install Go and retry" >&2; exit 1 ;;
esac
archive="herdr-file-transfer_${version}_${os}_$arch.tar.gz"
base="https://github.com/$REPO/releases/download/v$version"

echo "herdr-file-transfer: no Go toolchain; downloading v${version} for ${os}/${arch}…" >&2
tmp=$(mktemp -d "${TMPDIR:-/tmp}/herdr-file-transfer.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
# HTTPS only, redirects included (GitHub serves assets from its CDN).
fetch() { curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL "$1" -o "$2"; }
if ! fetch "$base/$archive" "$tmp/$archive" || ! fetch "$base/checksums.txt" "$tmp/checksums.txt"; then
	echo "herdr-file-transfer: download failed ($base). Install Go (https://go.dev/dl) and retry." >&2
	exit 1
fi

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
# macOS has shasum; most Linux systems have sha256sum instead.
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
else
	got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
	echo "herdr-file-transfer: $archive doesn't match the release's checksums.txt; not installing it." >&2
	exit 1
fi

# Extract only the binary, nothing else the archive might hold.
tar -xzf "$tmp/$archive" -C "$tmp" herdr-file-transfer
mv "$tmp/herdr-file-transfer" bin/herdr-file-transfer
chmod +x bin/herdr-file-transfer
