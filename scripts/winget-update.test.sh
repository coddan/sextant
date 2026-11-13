#!/usr/bin/env bash
# Exercises winget-update.sh against a throwaway copy of the manifests, so the
# script that fills them during a release is covered by CI.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
script="$root/scripts/winget-update.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/manifests"
cp "$root"/packaging/winget/*.yaml "$work/manifests/"

sum_a="$(printf 'a%.0s' {1..64})"
sum_b="$(printf 'b%.0s' {1..64})"

# The hash is picked out of SHA256SUMS, ignoring the other archive.
printf '%s  sextant-x86_64-unknown-linux-gnu.tar.gz\n%s  sextant-gui-x86_64-pc-windows-gnu.zip\n' \
    "$sum_b" "$sum_a" | "$script" 9.8.7 "" "$work/manifests" >/dev/null

for f in coddan.Sextant.yaml coddan.Sextant.installer.yaml coddan.Sextant.locale.en-US.yaml; do
    grep -qx 'PackageVersion: 9.8.7' "$work/manifests/$f" || {
        echo "FAIL: $f was not updated"; exit 1
    }
done
grep -qx '    InstallerUrl: https://github.com/coddan/sextant/releases/download/v9.8.7/sextant-gui-x86_64-pc-windows-gnu.zip' \
    "$work/manifests/coddan.Sextant.installer.yaml" || {
    echo "FAIL: installer URL wrong"; exit 1
}
grep -qx "    InstallerSha256: $sum_a" "$work/manifests/coddan.Sextant.installer.yaml" || {
    echo "FAIL: installer hash wrong"; exit 1
}

# Uppercase hashes are normalised to lowercase.
"$script" 9.8.7 "$(printf 'A%.0s' {1..64})" "$work/manifests" >/dev/null
grep -qx "    InstallerSha256: $sum_a" "$work/manifests/coddan.Sextant.installer.yaml" || {
    echo "FAIL: uppercase hash not normalised"; exit 1
}

# Bad input is rejected.
if "$script" nope "" "$work/manifests" >/dev/null 2>&1; then
    echo "FAIL: invalid version accepted"; exit 1
fi
if "$script" 1.0.0 zzz "$work/manifests" >/dev/null 2>&1; then
    echo "FAIL: invalid hash accepted"; exit 1
fi

echo "winget-update tests passed."
