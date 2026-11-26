#!/usr/bin/env bash
# Build one BIRD release from source into <prefix>.
#   tests/bird-matrix/build-bird.sh 3.4.0 /opt/bird-3.4.0
set -euo pipefail
version=$1
prefix=$2
src=$(mktemp -d)
trap 'rm -rf "$src"' EXIT
git clone -q --depth 1 --branch "v$version" https://gitlab.nic.cz/labs/bird.git "$src"
cd "$src"
autoreconf
./configure -q --prefix="$prefix" --runstatedir="$prefix/run"
make -s -j"$(nproc)"
make -s install
"$prefix/sbin/bird" --version
