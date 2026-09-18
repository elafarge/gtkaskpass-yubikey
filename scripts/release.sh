#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

command=${1:?usage: release.sh check-tag|bundle TAG SYSTEM [OUTDIR]}
tag=${2:?missing tag}
system=${3:?missing system}
[[ "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || {
  printf 'Expected a stable vMAJOR.MINOR.PATCH tag\n' >&2
  exit 1
}
case "$system" in x86_64-linux|aarch64-linux) ;; *) exit 1 ;; esac
version=$(nix eval --raw ".#packages.$system.default.version")
[[ "$tag" == "v$version" ]] || {
  printf 'Tag %s does not match package version %s\n' "$tag" "$version" >&2
  exit 1
}
if [[ "$command" == check-tag ]]; then exit 0; fi
[[ "$command" == bundle ]] || exit 1
if ! git diff --quiet || ! git diff --cached --quiet || [[ -n "$(git ls-files --others --exclude-standard)" ]]; then
  printf 'Commit the source tree before recording release provenance\n' >&2
  exit 1
fi
[[ "$system" == "$(nix eval --impure --raw --expr builtins.currentSystem)" ]] || {
  printf 'Release bundles must be built and checked on their native architecture\n' >&2
  exit 1
}

outdir=$(realpath -m "${4:?missing output directory}")
mkdir -p "$outdir"
package=$(nix build --no-link --print-out-paths ".#packages.$system.default")
base="gtkaskpass-yubikey-$version-$system"
closure=$(nix-store --query --requisites "$package")
mapfile -t paths <<< "$closure"
[[ ${#paths[@]} -gt 0 ]]
nix-store --export "${paths[@]}" | zstd -T2 -10 -f -o "$outdir/$base.nar.zst"
zstd --test "$outdir/$base.nar.zst"
[[ $(stat -c %s "$outdir/$base.nar.zst") -lt 2147483648 ]]
jq -n --arg version "$version" --arg system "$system" --arg storePath "$package" \
  --arg revision "$(git rev-parse HEAD)" \
  '{format: 1, version: $version, system: $system, storePath: $storePath, revision: $revision}' \
  > "$outdir/$base.json"
(
  cd "$outdir"
  sha256sum "$base.nar.zst" "$base.json" > "$base.sha256"
  sha256sum --check "$base.sha256"
)
