#!/bin/sh
# Regenerates docs/images/*.png from the a made-up project, so no real work can
# end up in the README.
#
# A test drives the demo popup and saves exactly what it draws; freeze turns
# those into PNGs. No browser, server or port involved.
#
# Needs freeze: brew install charmbracelet/tap/freeze, or
# go install github.com/charmbracelet/freeze@latest
set -eu
cd "$(dirname "$0")/.."

command -v freeze >/dev/null 2>&1 || { echo "needs freeze: brew install charmbracelet/tap/freeze" >&2; exit 1; }
# freeze draws PNGs itself unless rsvg-convert is on the PATH, and its own
# renderer draws an emoji (an emoji) as an empty box. rsvg-convert draws
# with the system's fonts, so those must have the README's font and an emoji
# one: without them the pictures would quietly come out wrong, so stop here.
missing=""
command -v rsvg-convert >/dev/null 2>&1 || missing="$missing rsvg-convert"
if command -v fc-list >/dev/null 2>&1; then
	fc-list | grep -qi "JetBrains Mono" || missing="$missing JetBrains-Mono"
	fc-list | grep -qi "emoji" || missing="$missing an-emoji-font"
fi
if [ -n "$missing" ]; then
	echo "screenshots need:$missing" >&2
	echo "  macOS:  brew install librsvg && brew install --cask font-jetbrains-mono" >&2
	echo "  Debian/Ubuntu:  sudo apt install librsvg2-bin fonts-jetbrains-mono fonts-noto-color-emoji" >&2
	exit 1
fi
# freeze renders at 2x; 1800px wide is still sharp on a Retina README. Only
# wider ones are shrunk: the duration picker is a small popup and stays one.
# sips is macOS's; elsewhere ImageMagick does it, and without either the PNGs
# are left at freeze's size.
if command -v sips >/dev/null 2>&1; then
	shrink() {
		w=$(sips -g pixelWidth "$1" | awk '/pixelWidth/ { print $2 }')
		[ "$w" -le 1800 ] || sips --resampleWidth 1800 "$1" >/dev/null
	}
elif command -v magick >/dev/null 2>&1; then
	shrink() { magick "$1" -resize '1800x>' "$1"; }
else
	shrink() { :; }
fi

screens=$(mktemp -d)
trap 'rm -rf "$screens"' EXIT
SCREENS_DIR="$screens" go test -count=1 -run '^TestWriteDemoScreens$' . >/dev/null

mkdir -p docs/images
for f in "$screens"/*.ansi; do
	name=$(basename "$f" .ansi)
	# On stdin, not --execute "cat …": that runs in a pty and sometimes never
	# returns.
	freeze --language ansi --window \
		--font.family "JetBrains Mono" --font.size 14 --line-height 1.3 \
		--padding 20,24 --margin 0 --border.radius 10 \
		--background "#1e1e2e" --output "docs/images/$name.png" <"$f" >/dev/null
	shrink "docs/images/$name.png"
	echo "docs/images/$name.png"
done
