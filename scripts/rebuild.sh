#!/usr/bin/env bash
# Clone snmp_exporter, apply exporter/, and write a patch that round-trips.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
version="${VERSION:-v0.31.0}"
cache="${CACHE:-$root/.cache/snmp_exporter}"
patch="$root/dist/snmp_exporter-${version}-index.patch"

export GIT_TERMINAL_PROMPT=0

rm -rf "$cache"
git clone --depth 1 --branch "$version" https://github.com/prometheus/snmp_exporter.git "$cache"
python3 "$root/scripts/apply.py" "$cache"

git -C "$cache" add -- collector/index.go collector/index_test.go collector/raw.go collector/raw_test.go collector/collector.go index_param.go raw.go raw_test.go main.go
mkdir -p "$root/dist"
git -C "$cache" -c core.autocrlf=false -c diff.noprefix=false diff --cached --no-ext-diff > "$patch"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git -C "$cache" archive HEAD | tar -x -C "$work"
patch -p1 --batch --forward -F0 -d "$work" < "$patch"
for f in collector/index.go collector/index_test.go collector/raw.go collector/raw_test.go collector/collector.go index_param.go raw.go raw_test.go main.go; do
  if ! cmp -s "$cache/$f" "$work/$f"; then
    diff -u "$cache/$f" "$work/$f" || true
    echo "patch does not round-trip for $f" >&2
    exit 1
  fi
done

echo "$patch"
