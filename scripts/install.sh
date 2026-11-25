#!/bin/sh
set -eu

register_tg() {
    executable=$1
    applications=${XDG_DATA_HOME:-"$HOME/.local/share"}/applications
    mkdir -p "$applications"
    if [ -d "$applications/telega.desktop" ]; then
        printf '%s\n' 'telega.desktop is a directory. Move it before registering.' >&2
        exit 1
    fi
    staged=$(mktemp "$applications/.telega.XXXXXXXXXX")

    # Desktop entries unescape the value before parsing the quoted Exec argument.
    escaped=$(printf '%s' "$executable" | sed 's/\\/\\\\\\\\/g; s/["$`]/\\\\&/g; s/%/%%/g')
    printf '%s\n' '[Desktop Entry]' 'Type=Application' 'Name=Telega' \
        'Comment=Open Telegram chat links in a terminal' "Exec=\"$escaped\" --open %u" \
        'Terminal=true' 'NoDisplay=true' 'MimeType=x-scheme-handler/tg;' > "$staged"
    chmod 644 "$staged"
    mv -f "$staged" "$applications/telega.desktop"
    staged=
    xdg-mime default telega.desktop x-scheme-handler/tg
    if [ "$(xdg-mime query default x-scheme-handler/tg)" != telega.desktop ]; then
        printf '%s\n' 'Could not set Telega as the default tg:// handler.' >&2
        exit 1
    fi
    printf '%s\n' 'Registered Telega as the default tg:// handler.'
}

install_telega() {
    enable_tg=false
    if [ "$#" -eq 1 ] && [ "$1" = --register-tg ]; then
        enable_tg=true
    elif [ "$#" -ne 0 ]; then
        printf '%s\n' 'Usage: install.sh [--register-tg]' >&2
        exit 2
    fi

    case "$(uname -s)" in
        Linux) os=linux ;;
        OpenBSD) os=openbsd ;;
        Darwin) os=darwin ;;
        *) printf '%s\n' 'Telega builds are available for Linux, macOS and OpenBSD.' >&2; exit 1 ;;
    esac

    if [ "$enable_tg" = true ]; then
        if [ "$os" != linux ] || ! command -v xdg-mime >/dev/null 2>&1; then
            printf '%s\n' '--register-tg requires Linux and xdg-mime.' >&2
            exit 1
        fi
    fi

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
    if [ "$enable_tg" = true ]; then
        executable=$(cd "$bin" && pwd -P)/telega
        case "$executable" in
            *[[:cntrl:]]*) printf '%s\n' 'Cannot register a path containing control characters.' >&2; exit 1 ;;
        esac
        case "$("$staged" --help)" in
            *'--open LINK'*) ;;
            *) printf '%s\n' 'This release does not support tg:// links. Install without --register-tg or wait for the next release.' >&2; exit 1 ;;
        esac
    fi
    mv -f "$staged" "$bin/telega"
    staged=
    printf 'Installed %s\nTry the offline demo with %s --demo\n' "$bin/telega" "$bin/telega"
    if [ "$enable_tg" = true ]; then
        register_tg "$executable"
    fi
}

install_telega "$@"
