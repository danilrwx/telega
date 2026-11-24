#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
    printf '%s\n' "Usage: $0 <linux|openbsd|darwin>/<amd64|arm64|armv7> <version> <output-directory>" >&2
    exit 2
fi

target=$1
version=$2
output=$3
case "$version" in
    v[0-9]*)
        case "$version" in
            *[!0-9v.a-zA-Z-]*) exit 2 ;;
        esac ;;
    *) exit 2 ;;
esac
case "$target" in
    linux/amd64|linux/arm64|openbsd/amd64|openbsd/arm64|darwin/amd64|darwin/arm64) arch=${target#*/} ;;
    linux/armv7|openbsd/armv7) arch=arm; export GOARM=7 ;;
    *) printf 'Unsupported target: %s\n' "$target" >&2; exit 2 ;;
esac
export GOOS="${target%/*}" GOARCH="$arch"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$output"
make build VERSION="$version" OUTPUT="$work/telega"
cp README.md LICENSE "$work/"
go mod vendor -o "$work/vendor"
(
    goroot=$(go env GOROOT)
    printf 'Go standard library\n===================\n'
    cat "$goroot/LICENSE"
    cd "$work/vendor"
    find . -type f \( -iname 'license*' -o -iname 'copying*' -o -iname 'notice*' -o -iname 'patents*' \) |
        LC_ALL=C sort |
        while IFS= read -r file; do
            printf '\n%s\n' "$file"
            cat "$file"
        done
) > "$work/THIRD_PARTY_LICENSES"
tar -czf "$output/telega_${GOOS}_${target#*/}.tar.gz" -C "$work" telega README.md LICENSE THIRD_PARTY_LICENSES
