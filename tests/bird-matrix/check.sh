#!/usr/bin/env bash
# Generate every tests/generate-*.yml with boxvector and parse the result with
# a real BIRD binary (bird -p). Used by CI for each BIRD version in the matrix.
#
#   tests/bird-matrix/check.sh <boxvector-binary> <bird-binary>
#
# Needs the PeeringDB test harness on 127.0.0.1:5000 (make peeringdb-test-harness).
set -euo pipefail

bv=$(readlink -f "$1")
bird=$(readlink -f "$2")
root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

version=$("$bird" --version 2>&1 | grep -o 'BIRD version [0-9.]*')
echo "== $version"

fail=0
for cfg in "$root"/tests/generate-*.yml; do
  name=$(basename "$cfg" .yml)
  out="$work/$name"
  mkdir -p "$out/bird" "$out/cache"
  # point every path at the temp dir and enable BIRD 3 threads (ignored on BIRD 2)
  grep -vE '^(bird-directory|bird-binary|bird-socket|cache-directory|web-ui-file|bird-threads):' "$cfg" > "$out/cfg.yml"
  cat >> "$out/cfg.yml" <<EOF
bird-directory: $out/bird/
bird-binary: $bird
bird-socket: $out/bird.ctl
cache-directory: $out/cache
web-ui-file: $out/ui.html
bird-threads: 2
EOF
  if ! (cd "$root" && "$bv" generate -c "$out/cfg.yml" --no-configure) > "$out/gen.log" 2>&1; then
    echo "FAIL $name: generate"; tail -20 "$out/gen.log"; fail=1; continue
  fi
  if ! (cd "$out/bird" && "$bird" -p -c bird.conf) > "$out/parse.log" 2>&1; then
    echo "FAIL $name: bird -p"; cat "$out/parse.log"; fail=1; continue
  fi
  echo "ok   $name ($(ls "$out/bird"/AS*.conf 2>/dev/null | wc -l) peer files, threads line: $(grep -c '^threads' "$out/bird/bird.conf" || true))"
done
exit $fail
