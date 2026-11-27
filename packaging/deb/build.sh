#!/usr/bin/env bash
# Build boxvector_<version>_<arch>.deb for Debian/Ubuntu.
#   packaging/deb/build.sh <version> [amd64|arm64] [outdir]
set -euo pipefail
version=${1:?version, e.g. 1.0.0}
arch=${2:-amd64}
out=$(readlink -f "${3:-dist}")
root=$(cd "$(dirname "$0")/../.." && pwd)
pkg=$(mktemp -d)
trap 'rm -rf "$pkg"' EXIT

commit=$(git -C "$root" rev-parse --short HEAD 2>/dev/null || echo unknown)
date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
mkdir -p "$pkg/usr/bin" "$out"
(cd "$root" && CGO_ENABLED=1 GOOS=linux GOARCH=$arch go build -trimpath \
  -ldflags "-s -w -X main.version=$version -X main.commit=$commit -X main.date=$date" \
  -o "$pkg/usr/bin/boxvector" .)

install -D -m 0644 "$root/packaging/boxvector-web.service" "$pkg/lib/systemd/system/boxvector-web.service"
install -D -m 0644 "$root/packaging/boxvector-term.service" "$pkg/lib/systemd/system/boxvector-term.service"
install -D -m 0644 "$root/packaging/boxvector-net.service" "$pkg/lib/systemd/system/boxvector-net.service"
install -D -m 0644 "$root/packaging/boxvector-vppcfg.service" "$pkg/lib/systemd/system/boxvector-vppcfg.service"
install -D -m 0644 "$root/packaging/boxvector-agent.service" "$pkg/lib/systemd/system/boxvector-agent.service"
install -D -m 0644 "$root/packaging/boxvector-monitor.service" "$pkg/usr/share/boxvector/boxvector-monitor.service"
install -D -m 0644 "$root/packaging/nginx-boxvector.conf" "$pkg/usr/share/boxvector/nginx-boxvector.conf"
install -D -m 0644 "$root/packaging/boxvector.yml.example" "$pkg/usr/share/boxvector/boxvector.yml.example"
install -D -m 0644 "$root/LICENSE" "$pkg/usr/share/doc/boxvector/copyright"

mkdir -p "$pkg/DEBIAN"
install -m 0755 "$root/packaging/deb/preinst" "$root/packaging/deb/postinst" "$root/packaging/deb/prerm" "$root/packaging/deb/postrm" "$pkg/DEBIAN/"
cat > "$pkg/DEBIAN/control" <<CTRL
Package: boxvector
Version: $version
Architecture: $arch
Maintainer: nexcolo <https://www.nexcolo.com/>
Depends: adduser, libc6 (>= 2.35), libcrypt1, openssl
Recommends: bird3 | bird2, bgpq4, ifupdown2
Section: net
Priority: optional
Homepage: https://github.com/mfr411/boxvector
Installed-Size: $(du -sk "$pkg/usr" | cut -f1)
Description: BGP configuration generator and web UI for BIRD 2/3
 boxvector generates BIRD configuration (filters, IRR/RPKI, PeeringDB
 limits) from one YAML file, validates it with bird -p and reloads BIRD.
 Includes a web UI with login, diff review, snapshots and rollback.
CTRL
fakeroot dpkg-deb --build -Zxz "$pkg" "$out/boxvector_${version}_${arch}.deb" 2>/dev/null \
  || dpkg-deb --root-owner-group --build -Zxz "$pkg" "$out/boxvector_${version}_${arch}.deb"
