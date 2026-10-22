#!/bin/sh
# Fill the winget manifests for a released version.
#
# Usage:
#   scripts/winget-update.sh <version> [sha256] [manifests-dir]
#
# <version>        release version without the leading "v" (for example 1.2.0)
# <sha256>         64-hex SHA256 of sextant-gui-x86_64-pc-windows-gnu.zip;
#                  when omitted it is read from a SHA256SUMS file on stdin
# <manifests-dir>  defaults to packaging/winget
#
# Examples:
#   curl -fsSL https://github.com/coddan/sextant/releases/download/v1.2.0/SHA256SUMS \
#     | scripts/winget-update.sh 1.2.0
#
#   scripts/winget-update.sh 1.2.0 "$(sha256sum sextant-gui-x86_64-pc-windows-gnu.zip | cut -d' ' -f1)"

set -eu

usage() {
    echo "usage: $0 <version> [sha256] [manifests-dir]" >&2
    exit 2
}

[ $# -ge 1 ] || usage

version=${1#v}
sha256=${2:-}
dir=${3:-packaging/winget}
asset=sextant-gui-x86_64-pc-windows-gnu.zip
repo=coddan/sextant

printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z]+)*$' || {
    echo "error: invalid version '$version' (want X.Y.Z)" >&2
    exit 2
}

if [ -z "$sha256" ]; then
    if [ -t 0 ]; then
        echo "error: no SHA256 argument and nothing on stdin (pipe a SHA256SUMS file)" >&2
        usage
    fi
    # sha256sum writes "<hash>  <name>" (or "*name" in binary mode).
    sha256=$(awk -v want="$asset" '$2 == want || $2 == "*" want { print $1; exit }')
fi

sha256=$(printf '%s' "$sha256" | tr 'A-F' 'a-f')
printf '%s' "$sha256" | grep -Eq '^[0-9a-f]{64}$' || {
    echo "error: SHA256 for $asset is missing or invalid" >&2
    exit 1
}

[ -d "$dir" ] || {
    echo "error: manifest directory '$dir' not found" >&2
    exit 1
}

installer=$dir/coddan.Sextant.installer.yaml
[ -f "$installer" ] || {
    echo "error: '$installer' not found" >&2
    exit 1
}

url=https://github.com/$repo/releases/download/v$version/$asset

for f in "$dir"/*.yaml; do
    sed "s/^PackageVersion: .*/PackageVersion: $version/" "$f" > "$f.tmp"
    mv "$f.tmp" "$f"
done

sed -e "s|^\(    \)InstallerUrl: .*|\1InstallerUrl: $url|" \
    -e "s/^\(    \)InstallerSha256: .*/\1InstallerSha256: $sha256/" \
    "$installer" > "$installer.tmp"
mv "$installer.tmp" "$installer"

echo "winget manifests updated for v$version"
echo "  url    $url"
echo "  sha256 $sha256"
