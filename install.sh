#!/usr/bin/env bash
# boxvector installer (Debian >= 12, Ubuntu >= 22.04).
#
#   curl -fsSL https://raw.githubusercontent.com/mfr411/boxvector/main/install.sh | sudo bash
#   ... | sudo bash -s -- --mode all
#   ... | sudo bash -s -- --mode core --bird 3
#
# Modes:
#   all     boxvector + BIRD + bgpq4 + VPP (linux-cp) + web UI (enabled, admin user created)
#   core    boxvector + BIRD + bgpq4 only (no VPP, web UI installed but not started)
#   custom  pick components with --vpp/--no-vpp and --web/--no-web
#
# Safe to run again: every step checks the current state first.
set -euo pipefail

REPO="mfr411/boxvector"
MODE=""; BIRD="2"; VPP=""; WEB=""; DEB=""; VERSION="latest"
WEB_USER="admin"; VPP_NIC=""; ASSUME_YES=0; DRY=0; FORCE=0
VPP_REPO_KEY=/usr/share/keyrings/fdio-release.gpg
BIRD3_KEY=/usr/share/keyrings/cznic-labs-pkg.gpg

c() { [ -t 1 ] && printf '\033[%sm' "$1" || true; }
log()  { printf '%s==>%s %s\n' "$(c 1)" "$(c 0)" "$*"; }
warn() { printf '%swarn:%s %s\n' "$(c 33)" "$(c 0)" "$*" >&2; }
die()  { printf '%serror:%s %s\n' "$(c 31)" "$(c 0)" "$*" >&2; exit 1; }
run()  { if [ "$DRY" = 1 ]; then echo "+ $*"; else "$@"; fi; }

usage() {
cat <<'EOF'
boxvector installer

  --mode all|core|custom   all = boxvector+BIRD+VPP+web UI, core = boxvector+BIRD only
  --bird 2|3|keep          BIRD major to install (default 2; keep = leave what is installed)
  --vpp / --no-vpp         (custom) install VPP + linux-cp
  --web / --no-web         (custom) enable web UI and create the admin user
  --vpp-nic PCI_ADDR       add a DPDK NIC to VPP, e.g. 0000:03:00.0 (must already be bound to vfio-pci/uio)
  --web-user NAME          web UI admin name (default admin); password: $BOXVECTOR_WEB_PASSWORD or random
  --version X.Y.Z          release to install (default latest)
  --deb FILE|URL           install this .deb instead of downloading a release
  --yes                    no questions (needs --mode)
  --dry-run                show what would be done
  --force                  skip the safety checks (NIC in use)
  -h, --help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --mode) MODE=${2:?}; shift 2 ;;
    --bird) BIRD=${2:?}; shift 2 ;;
    --vpp) VPP=1; shift ;;   --no-vpp) VPP=0; shift ;;
    --web) WEB=1; shift ;;   --no-web) WEB=0; shift ;;
    --vpp-nic) VPP_NIC=${2:?}; shift 2 ;;
    --web-user) WEB_USER=${2:?}; shift 2 ;;
    --version) VERSION=${2:?}; shift 2 ;;
    --deb) DEB=${2:?}; shift 2 ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --dry-run) DRY=1; shift ;;
    --force) FORCE=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; die "unknown option: $1" ;;
  esac
done

[ "$(id -u)" = 0 ] || [ "$DRY" = 1 ] || die "run as root (sudo)"
command -v apt-get >/dev/null || die "apt-get not found: Debian/Ubuntu only"

# ---------- OS check ----------
. /etc/os-release
major=${VERSION_ID%%.*}
case "$ID" in
  debian) [ "${major:-0}" -ge 12 ] || die "Debian 12 or newer required" ;;
  ubuntu) [ "${major:-0}" -ge 22 ] || die "Ubuntu 22.04 or newer required" ;;
  *) die "unsupported OS: ${PRETTY_NAME:-$ID} (Debian >= 12 / Ubuntu >= 22.04)" ;;
esac
CODENAME=${VERSION_CODENAME:-}
[ -n "$CODENAME" ] || die "cannot detect release codename"
case "$(dpkg --print-architecture)" in
  amd64|arm64) ARCH=$(dpkg --print-architecture) ;;
  *) die "unsupported architecture: $(dpkg --print-architecture)" ;;
esac

# ---------- choose what to install ----------
ask() { # ask "question" default(y/n)
  local a; read -r -p "$1 " a </dev/tty || a=""; echo "${a:-$2}"
}
if [ -z "$MODE" ]; then
  if [ "$ASSUME_YES" = 1 ] || { [ ! -t 0 ] && [ ! -r /dev/tty ]; }; then
    MODE=core
  else
    cat >/dev/tty <<'EOF'

  1) all   : boxvector + BIRD + VPP (linux-cp) + web UI
  2) core  : boxvector + BIRD only (no VPP, web UI not started)
  3) custom: choose each component
EOF
    case "$(ask 'Select [1-3] (default 2):' 2)" in
      1|all) MODE=all ;; 3|custom) MODE=custom ;; *) MODE=core ;;
    esac
  fi
fi
case "$MODE" in
  all)    [ -n "$VPP" ] || VPP=1; [ -n "$WEB" ] || WEB=1 ;;
  core)   VPP=0; WEB=0 ;;
  custom)
    if [ -z "$VPP" ]; then [ "$ASSUME_YES" = 1 ] && VPP=0 || { [ "$(ask 'Install VPP + linux-cp? [y/N]' n)" = y ] && VPP=1 || VPP=0; }; fi
    if [ -z "$WEB" ]; then [ "$ASSUME_YES" = 1 ] && WEB=0 || { [ "$(ask 'Enable web UI? [y/N]' n)" = y ] && WEB=1 || WEB=0; }; fi ;;
  *) die "--mode must be all, core or custom" ;;
esac
case "$BIRD" in 2|3|keep) ;; *) die "--bird must be 2, 3 or keep" ;; esac
[ -z "$VPP_NIC" ] || [ "$VPP" = 1 ] || die "--vpp-nic needs VPP (--mode all or --vpp)"
[ "$VPP" = 1 ] && [ "$ARCH" != amd64 ] && warn "VPP packages from fd.io are published for amd64; arm64 may be unavailable"

log "plan: mode=$MODE bird=$BIRD vpp=$VPP web=$WEB os=$PRETTY_NAME arch=$ARCH"

# ---------- safety: DPDK NIC ----------
check_nic() {
  local pci=$1 dev drv net
  [[ "$pci" =~ ^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-9a-fA-F]$ ]] || die "bad PCI address: $pci"
  [ -d "/sys/bus/pci/devices/$pci" ] || die "PCI device $pci not found"
  drv=$(basename "$(readlink -f "/sys/bus/pci/devices/$pci/driver" 2>/dev/null || echo none)")
  net=$(ls "/sys/bus/pci/devices/$pci/net" 2>/dev/null | head -1 || true)
  if [ -n "$net" ]; then
    [ "$FORCE" = 1 ] || die "$pci is still the kernel interface '$net' (driver $drv). Binding it to DPDK would drop the link (and your SSH session if it carries it). Bind it to vfio-pci first (dpdk-devbind.py -b vfio-pci $pci) or use --force."
  fi
  case "$drv" in vfio-pci|uio_pci_generic|igb_uio) ;; *) [ "$FORCE" = 1 ] || die "$pci uses driver '$drv'; bind it to vfio-pci/uio_pci_generic first" ;; esac
}
[ -z "$VPP_NIC" ] || check_nic "$VPP_NIC"

# ---------- helpers ----------
export DEBIAN_FRONTEND=noninteractive
APT="apt-get -y -o Dpkg::Options::=--force-confold"
# NB: no "cmd | grep -q" under pipefail (SIGPIPE makes the pipeline fail); capture first
pkg_installed() { local o; o=$(dpkg -s "$1" 2>/dev/null || true); [[ "$o" == *"Status: install ok installed"* ]]; }
has_candidate() { local o; o=$(apt-cache policy "$1" 2>/dev/null || true); [[ "$o" =~ Candidate:\ [0-9] ]]; }
need() { local p=(); for x in "$@"; do pkg_installed "$x" || p+=("$x"); done; [ ${#p[@]} -eq 0 ] || run $APT install "${p[@]}"; }

log "base packages"
run apt-get update -qq
need ca-certificates curl gnupg openssl procps

# ---------- BIRD ----------
install_bird() {
  local cur=""; command -v bird >/dev/null && cur=$(bird --version 2>&1 | grep -oE '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 || true)
  if [ "$BIRD" = keep ]; then
    [ -n "$cur" ] || die "--bird keep but no BIRD is installed"
    log "BIRD $cur kept"; return
  fi
  if [ -n "$cur" ] && [ "${cur%%.*}" = "$BIRD" ]; then log "BIRD $cur already installed"; return; fi
  if [ "$BIRD" = 3 ]; then
    if ! has_candidate bird3; then
      log "adding CZ.NIC BIRD 3 repository"
      run curl -fsSL https://pkg.labs.nic.cz/gpg -o "$BIRD3_KEY"
      [ "$DRY" = 1 ] || echo "deb [signed-by=$BIRD3_KEY] https://pkg.labs.nic.cz/bird3 $CODENAME main" >/etc/apt/sources.list.d/cznic-labs-bird3.list
      run apt-get update -qq
    fi
    [ -z "$cur" ] || [ "${cur%%.*}" = 3 ] || { warn "replacing BIRD $cur with BIRD 3 (config in /etc/bird is kept)"; }
    run $APT install bird3 bgpq4
  else
    run $APT install bird2 bgpq4
  fi
}
log "BIRD ${BIRD}"
install_bird

# ---------- VPP ----------
install_vpp() {
  if ! has_candidate vpp; then
    log "adding fd.io VPP repository"
    run bash -c "curl -fsSL https://packagecloud.io/fdio/release/gpgkey | gpg --dearmor --yes -o $VPP_REPO_KEY"
    [ "$DRY" = 1 ] || echo "deb [signed-by=$VPP_REPO_KEY] https://packagecloud.io/fdio/release/$ID/ $CODENAME main" >/etc/apt/sources.list.d/fdio-release.list
    run apt-get update -qq
    [ "$DRY" = 1 ] || has_candidate vpp || die "VPP is not available for $ID $CODENAME/$ARCH on fd.io"
  fi
  need vpp vpp-plugin-core vpp-plugin-dpdk
  write_vpp_config
  if [ -d /run/systemd/system ] && [ "$DRY" = 0 ]; then
    systemctl enable vpp >/dev/null 2>&1 || true
    systemctl restart vpp
    for _ in $(seq 1 20); do vppctl show version >/dev/null 2>&1 && break; sleep 1; done
    vppctl show version >/dev/null 2>&1 || die "vpp did not come up; see: journalctl -u vpp"
    o=$(vppctl show plugins 2>/dev/null || true); [[ "$o" == *linux_cp* ]] || warn "linux_cp plugin not loaded"
    log "VPP up: $(vppctl show version | head -1)"
  fi
}

write_vpp_config() {
  local f=/etc/vpp/startup.conf want sum cores main="" workers=""
  cores=$(nproc)
  if [ "$cores" -ge 4 ]; then main="cpu { main-core 1 corelist-workers 2-$((cores>8?4:cores-1)) }"; fi
  want=$(cat <<EOF
# managed by boxvector install.sh (edit below the markers only if you re-run with care)
unix {
  nodaemon
  log /var/log/vpp/vpp.log
  full-coredump
  cli-listen /run/vpp/cli.sock
  gid vpp
  startup-config /etc/vpp/boxvector-lcp.cli
}
api-trace { on }
api-segment { gid vpp }
socksvr { default }
${main}
buffers { buffers-per-numa 128000 }
plugins {
  plugin default { enable }
  plugin linux_cp_plugin.so { enable }
  plugin linux_nl_plugin.so { enable }
$( [ -n "$VPP_NIC" ] && echo "  plugin dpdk_plugin.so { enable }" || echo "  plugin dpdk_plugin.so { disable }" )
}
$( [ -n "$VPP_NIC" ] && printf 'dpdk {\n  dev %s\n}\n' "$VPP_NIC" )
EOF
)
  if [ -f "$f" ] && ! grep -q 'managed by boxvector install.sh' "$f" && [ ! -e "$f.pre-boxvector" ]; then
    run cp -a "$f" "$f.pre-boxvector"
  fi
  local cli=/etc/vpp/boxvector-lcp.cli
  local cli_want=$'# managed by boxvector install.sh: mirror VPP interfaces into Linux (BIRD talks to the Linux side)\nlcp lcp-sync on\nlcp lcp-auto-subint on'
  if [ -f "$f" ] && [ "$(cat "$f")" = "$want" ] && [ -f "$cli" ] && [ "$(cat "$cli")" = "$cli_want" ]; then log "$f already up to date"; return; fi
  log "writing $f"
  [ "$DRY" = 1 ] && return
  install -d -m 0755 /etc/vpp /var/log/vpp
  printf '%s\n' "$cli_want" >"$cli"
  printf '%s\n' "$want" >"$f"
  # hugepages for DPDK
  if [ -n "$VPP_NIC" ]; then
    printf 'vm.nr_hugepages=1024\nvm.max_map_count=3096\nkernel.shmmax=2147483648\n' >/etc/sysctl.d/81-boxvector-vpp.conf
    sysctl -q --system >/dev/null || true
  fi
}
if [ "$VPP" = 1 ]; then log "VPP"; install_vpp; fi

# ---------- boxvector ----------
fetch_deb() {
  local out=$1 url
  if [ -n "$DEB" ]; then
    case "$DEB" in http*://*) run curl -fsSL "$DEB" -o "$out" ;; *) [ -f "$DEB" ] || die "no such file: $DEB"; cp "$DEB" "$out" ;; esac
    return
  fi
  if [ "$VERSION" = latest ]; then
    url=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
      | grep -oE "https://[^\"]+boxvector_[^\"]+_${ARCH}\.deb" | head -1 || true)
  else
    url="https://github.com/$REPO/releases/download/v${VERSION#v}/boxvector_${VERSION#v}_${ARCH}.deb"
  fi
  [ -n "$url" ] || die "no release .deb found for $ARCH. Use --deb FILE|URL (releases: https://github.com/$REPO/releases)"
  log "downloading $url"
  run curl -fsSL "$url" -o "$out"
  # verify against SHA256SUMS when the release has one
  local sums; sums=$(dirname "$url")/SHA256SUMS
  if curl -fsSL "$sums" -o "$out.sums" 2>/dev/null; then
    ( cd "$(dirname "$out")" && grep " $(basename "$url")\$" "$out.sums" | sed "s# .*# $(basename "$out")#" | sha256sum -c - >/dev/null ) \
      || die "SHA256 mismatch for $(basename "$url")"
    log "sha256 verified"
  else
    warn "no SHA256SUMS published for this release; checksum not verified"
  fi
}
log "boxvector"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
fetch_deb "$tmp/boxvector.deb"
[ "$DRY" = 1 ] || run $APT install "$tmp/boxvector.deb"

# ---------- web UI ----------
if [ "$WEB" = 1 ]; then
  log "web UI"
  if [ "$DRY" = 0 ]; then
    if [ -s /etc/boxvector/web.users ] && grep -q "^$WEB_USER:" /etc/boxvector/web.users 2>/dev/null; then
      log "web user '$WEB_USER' already exists (password unchanged)"
    else
      pw=${BOXVECTOR_WEB_PASSWORD:-$(openssl rand -base64 18 | tr -d '/+=' | cut -c1-20)}
      printf '%s\n%s\n' "$pw" "$pw" | runuser -u boxvector -- boxvector web passwd "$WEB_USER" --auth-file /etc/boxvector/web.users >/dev/null
      NEWPW=$pw
    fi
    if [ -d /run/systemd/system ]; then
      systemctl enable --now boxvector-web
      sleep 1
      systemctl is-active --quiet boxvector-web || die "boxvector-web is not running: journalctl -u boxvector-web"
    else
      warn "no systemd here; start the web UI manually: boxvector web -c /etc/boxvector.yml"
    fi
  fi
fi

# ---------- summary ----------
ip4=$(hostname -I 2>/dev/null | awk '{print $1}')
echo
log "done"
pkg_installed boxvector && echo "  boxvector : $(boxvector version 2>/dev/null | head -1)"
command -v bird >/dev/null && echo "  bird      : $(bird --version 2>&1 | head -1)"
[ "$VPP" = 1 ] && command -v vppctl >/dev/null && echo "  vpp       : $(vppctl show version 2>/dev/null | head -1)"
if [ "$WEB" = 1 ]; then
  echo "  web UI    : https://${ip4:-<server-ip>}:6969  (self-signed certificate)"
  [ -n "${NEWPW:-}" ] && echo "  login     : $WEB_USER / $NEWPW   <- shown once, change it after login"
fi
echo "  config    : /etc/boxvector.yml"
[ "$WEB" = 1 ] || echo "  web UI    : not enabled. Enable later: sudo -u boxvector boxvector web passwd admin --auth-file /etc/boxvector/web.users && sudo systemctl enable --now boxvector-web"
[ "$VPP" = 1 ] && echo "  vpp       : vppctl show interface | vppctl show lcp   (bind NICs: see README, VPP)"
exit 0
