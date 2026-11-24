#!/bin/sh
set -eu

install_telega() {
    case "$(uname -s)" in
        Linux) os=linux ;;
        OpenBSD) os=openbsd ;;
        Darwin) os=darwin ;;
        *) printf '%s\n' 'Telega builds are available for Linux, macOS and OpenBSD.' >&2; exit 1 ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        aarch64|arm64) arch=arm64 ;;
        armv7*) arch=armv7 ;;
        *) printf '%s\n' 'Telega builds are available for amd64, arm64 and ARMv7.' >&2; exit 1 ;;
    esac

    archive="telega_${os}_${arch}.tar.gz"
    release=https://github.com/danilrwx/telega/releases/latest/download
    work=$(mktemp -d "${TMPDIR:-/tmp}/telega-install.XXXXXXXXXX")
    staged=
    trap 'if [ -n "$staged" ]; then rm -f "$staged"; fi; rm -rf "$work"' 0
    trap 'exit 1' HUP INT TERM

    printf 'Downloading Telega for %s/%s…\n' "$os" "$arch"
    curl -fsSL "$release/$archive" -o "$work/$archive"
    curl -fsSL "$release/SHA256SUMS" -o "$work/SHA256SUMS"

    expected=$(awk -v name="$archive" '$2 == name { print $1 }' "$work/SHA256SUMS")
    case "$os" in
        darwin) actual=$(shasum -a 256 "$work/$archive") ;;
        openbsd) actual=$(sha256 -q "$work/$archive") ;;
        linux) actual=$(sha256sum "$work/$archive") ;;
    esac
    actual=${actual%% *}
    if [ "$actual" != "$expected" ]; then
        printf '%s\n' 'Checksum verification failed. Retry to download the latest release.' >&2
        exit 1
    fi

    tar -xzf "$work/$archive" -C "$work" telega
    bin=${TELEGA_INSTALL_DIR:-"$HOME/.local/bin"}
    mkdir -p "$bin"
    if [ -d "$bin/telega" ]; then
        printf '%s is a directory. Move it before installing.\n' "$bin/telega" >&2
        exit 1
    fi
    staged=$(mktemp "$bin/.telega.XXXXXXXXXX")
    install -m 755 "$work/telega" "$staged"
    mv -f "$staged" "$bin/telega"
    staged=
    printf 'Installed %s\nTry the offline demo with %s --demo\n' "$bin/telega" "$bin/telega"
}

install_telega
