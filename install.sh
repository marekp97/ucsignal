#!/bin/sh
# Installs the latest ucsignal release to /usr/local/bin.
set -eu

base="https://github.com/marekp97/ucsignal/releases/latest/download"
file="ucsignal-macos.tar.gz"
dir="/usr/local/bin"

if [ "$(uname -s)" != "Darwin" ]; then
	echo "ucsignal runs only on macOS." >&2
	exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
if ! curl -fsSL "$base/$file" -o "$tmp/$file" || ! curl -fsSL "$base/$file.sha256" -o "$tmp/$file.sha256"; then
	echo "Download failed. Check your connection or get it from https://github.com/marekp97/ucsignal/releases" >&2
	exit 1
fi
if ! (cd "$tmp" && shasum -a 256 -c "$file.sha256" >/dev/null 2>&1); then
	echo "The download does not match its checksum, so nothing was installed. Try again." >&2
	exit 1
fi
tar -xzf "$tmp/$file" -C "$tmp"

if [ -w "$dir" ]; then
	mv "$tmp/ucsignal" "$dir/ucsignal"
else
	echo "Installing to $dir, which needs your Mac password."
	sudo mkdir -p "$dir"
	sudo mv "$tmp/ucsignal" "$dir/ucsignal"
fi

echo "Installed $("$dir/ucsignal" --version). Run: ucsignal"
