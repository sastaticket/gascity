#!/bin/sh
# Install a pinned Sastaticket gc build from a sastaticket/gascity pre-release.
# Verifies the archive's sha256 against the release checksums and, when the gh
# CLI is installed, its build-provenance attestation. POSIX sh, so it runs
# under macOS's /bin/sh.
#
# usage: sh st/install-gc.sh <tag>          e.g. st-20261104-4118c6ab7f33
#   PREFIX   install directory (default: $HOME/.local/bin)
set -eu

repo="sastaticket/gascity"
tag="${1:-}"
case "$tag" in
  st-*) ;;
  *) echo "usage: sh $0 <st-tag>   (see https://github.com/$repo/releases)" >&2; exit 2 ;;
esac
prefix="${PREFIX:-$HOME/.local/bin}"
base_url="${ST_GC_RELEASE_BASE_URL:-https://github.com/$repo/releases/download/$tag}"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

archive="gascity_${tag}_${os}_${arch}.tar.gz"
sums="gascity_${tag}_checksums.txt"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT INT TERM

curl -fsSL -o "$work/$archive" "$base_url/$archive"
curl -fsSL -o "$work/$sums" "$base_url/$sums"

expected="$(awk -v f="$archive" '$2 == f { print $1 }' "$work/$sums")"
if [ -z "$expected" ]; then
  echo "no checksum for $archive in $sums" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$work/$archive" | awk '{ print $1 }')"
else
  actual="$(shasum -a 256 "$work/$archive" | awk '{ print $1 }')"
fi
if [ "$actual" != "$expected" ]; then
  echo "checksum mismatch for $archive: got $actual, expected $expected" >&2
  exit 1
fi
echo "sha256 ok: $archive"

if command -v gh >/dev/null 2>&1; then
  gh attestation verify "$work/$archive" --repo "$repo" >/dev/null
  echo "attestation ok: built by $repo's st release workflow"
else
  echo "gh not installed: skipped the provenance attestation check" >&2
fi

mkdir -p "$work/x" "$prefix"
tar -xzf "$work/$archive" -C "$work/x"
for bin in gc gc-osc7501 gc-write-mint; do
  [ -f "$work/x/$bin" ] || continue
  install -m 0755 "$work/x/$bin" "$prefix/$bin"
  if [ "$os" = darwin ]; then
    xattr -d com.apple.quarantine "$prefix/$bin" 2>/dev/null || true
  fi
done

echo "installed: $("$prefix/gc" version)"
found="$(command -v gc 2>/dev/null || true)"
if [ "$found" != "$prefix/gc" ]; then
  echo "note: 'gc' on your PATH is ${found:-missing}, not $prefix/gc" >&2
  echo "      (Graphviz also ships a gc); put $prefix first on PATH" >&2
fi
