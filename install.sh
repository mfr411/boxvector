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
MODE=""; BIRD="2"; VPP=""; WEB=""; DEB=""; BV_VERSION="latest"
WEB_USER="admin"; NO_BV=0; declare -A NIC_SPEED=(); ESTIMATE=0; VPP_NICS=(); VPP_RESERVE=1; VPP_SMT=0; VPP_WORKERS=""; TUNE_OS=1; ASSUME_YES=0; DRY=0; FORCE=0
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
  --vpp-nic PCI[@GBPS]     add a DPDK NIC to VPP, e.g. 0000:03:00.0@25 (repeatable; must already be bound to vfio-pci/uio).
                           @GBPS = link speed, only needed when it cannot be detected
  --estimate               print the traffic capacity estimate for this machine and exit (no root, no changes)
  --vpp-reserve N          physical cores kept for the OS + BIRD + boxvector (default 1)
  --vpp-smt                also use hyper-thread siblings as VPP workers (default: physical cores only)
  --vpp-workers N          cap the number of VPP workers (default: all remaining cores of the NIC's NUMA node)
  --no-boxvector           keep the installed boxvector package as it is
  --no-os-tune             do not touch CPU governor / systemd CPUAffinity
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
    --vpp-nic) _v=${2:?}; VPP_NICS+=("${_v%@*}"); [[ "$_v" == *@* ]] && NIC_SPEED["${_v%@*}"]=$(( ${_v#*@} * 1000 )); shift 2 ;;
    --estimate) ESTIMATE=1; shift ;;
    --vpp-reserve) VPP_RESERVE=${2:?}; shift 2 ;;
    --vpp-smt) VPP_SMT=1; shift ;;
    --vpp-workers) VPP_WORKERS=${2:?}; shift 2 ;;
    --no-os-tune) TUNE_OS=0; shift ;;
    --no-boxvector) NO_BV=1; shift ;;
    --web-user) WEB_USER=${2:?}; shift 2 ;;
    --version) BV_VERSION=${2:?}; shift 2 ;;
    --deb) DEB=${2:?}; shift 2 ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --dry-run) DRY=1; shift ;;
    --force) FORCE=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; die "unknown option: $1" ;;
  esac
done

[ "$(id -u)" = 0 ] || [ "$DRY" = 1 ] || [ "$ESTIMATE" = 1 ] || die "run as root (sudo)"
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

[ "$ESTIMATE" = 1 ] && { MODE=${MODE:-all}; VPP=1; }
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
[ ${#VPP_NICS[@]} -eq 0 ] || [ "$VPP" = 1 ] || die "--vpp-nic needs VPP (--mode all or --vpp)"
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
[ "$ESTIMATE" = 1 ] || for n in "${VPP_NICS[@]}"; do check_nic "$n"; done

# ---------- helpers ----------
export DEBIAN_FRONTEND=noninteractive
APT="apt-get -y -o Dpkg::Options::=--force-confold"
# NB: no "cmd | grep -q" under pipefail (SIGPIPE makes the pipeline fail); capture first
pkg_installed() { local o; o=$(dpkg -s "$1" 2>/dev/null || true); [[ "$o" == *"Status: install ok installed"* ]]; }
has_candidate() { local o; o=$(apt-cache policy "$1" 2>/dev/null || true); [[ "$o" =~ Candidate:\ [0-9] ]]; }
need() { local p=(); for x in "$@"; do pkg_installed "$x" || p+=("$x"); done; [ ${#p[@]} -eq 0 ] || run $APT install "${p[@]}"; }

if [ "$ESTIMATE" != 1 ]; then
log "base packages"
run apt-get update -qq
need ca-certificates curl gnupg openssl procps
fi

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
    run $APT install bird3 bgpq4 ifupdown2
  else
    run $APT install bird2 bgpq4 ifupdown2
  fi
}
if [ "$ESTIMATE" != 1 ]; then log "BIRD ${BIRD}"; install_bird; fi

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
    if [ "$VPP_CONF_CHANGED" = 1 ] || ! systemctl is-active --quiet vpp; then systemctl restart vpp; fi
    for _ in $(seq 1 20); do vppctl show version >/dev/null 2>&1 && break; sleep 1; done
    vppctl show version >/dev/null 2>&1 || die "vpp did not come up; see: journalctl -u vpp"
    o=$(vppctl show plugins 2>/dev/null || true); [[ "$o" == *linux_cp* ]] || warn "linux_cp plugin not loaded"
    log "VPP up: $(vppctl show version | head -1)"
  fi
}

# Split the CPUs for VPP. Reads lscpu topology; nothing is hardcoded.
#   reserved : first VPP_RESERVE physical cores (all their threads) -> OS, BIRD, boxvector, VPP main thread
#   workers  : every other physical core on the NUMA node of the NIC (+ SMT siblings with --vpp-smt)
# Workers on a NUMA node different from the NIC would cross the interconnect for every packet, so they are not used.
vpp_cpu_plan() {
  local node=0 pci topo
  if [ ${#VPP_NICS[@]} -gt 0 ]; then
    pci=${VPP_NICS[0]}
    node=$(cat "/sys/bus/pci/devices/$pci/numa_node" 2>/dev/null || echo 0); [ "$node" -ge 0 ] 2>/dev/null || node=0
  fi
  topo=$(lscpu -p=CPU,CORE,SOCKET,NODE 2>/dev/null | grep -v '^#' || true)
  [ -n "$topo" ] || topo=$(seq 0 $(( $(nproc) - 1 )) | awk '{print $1","$1",0,0"}')
  # NODE column is empty on some VMs: treat as node 0
  topo=$(echo "$topo" | awk -F, '{n=($4==""?0:$4); print $1","$2","$3","n}')
  awk -F, -v node="$node" -v reserve="$VPP_RESERVE" -v smt="$VPP_SMT" -v maxw="${VPP_WORKERS:-0}" '
    function ranges(a, n,   i, out, st, pv) {
      out=""; st=a[1]; pv=a[1]
      for (i=2;i<=n+1;i++) {
        if (i<=n && a[i]==pv+1) { pv=a[i]; continue }
        out = out (out==""?"":",") (st==pv ? st : st "-" pv)
        st=a[i]; pv=a[i]
      }
      return out
    }
    { cpu=$1; key=$3"/"$2; if (!(key in first)) { first[key]=cpu; order[++nk]=key; cnode[key]=$4 }
      if (cpu<first[key]) first[key]=cpu
      thr[key]=thr[key] " " cpu; nodeof[cpu]=$4 }
    END {
      # sort physical cores by their first cpu id
      for (i=1;i<=nk;i++) for (j=i+1;j<=nk;j++) if (first[order[j]]+0 < first[order[i]]+0) { tmp=order[i]; order[i]=order[j]; order[j]=tmp }
      nres=0; nw=0; ncpu=0
      for (i=1;i<=nk;i++) { k=order[i]; split(thr[k], t, " ")
        if (nres < reserve) { nres++; for (x in t) res[++nr]=t[x]; continue }
        if (cnode[k] != node) continue
        # t[] order is not guaranteed; pick lowest as the physical thread, the rest are SMT siblings
        n=0; for (x in t) arr[++n]=t[x]+0
        for (a=1;a<=n;a++) for (b=a+1;b<=n;b++) if (arr[b]<arr[a]) { v=arr[a]; arr[a]=arr[b]; arr[b]=v }
        w[++nw]=arr[1]; if (smt==1) for (a=2;a<=n;a++) sib[++ns]=arr[a]
        delete arr
      }
      np=nw; for (a=1;a<=ns;a++) w[++nw]=sib[a]
      for (a=1;a<=nw;a++) for (b=a+1;b<=nw;b++) if (w[b]<w[a]) { v=w[a]; w[a]=w[b]; w[b]=v }
      if (maxw>0 && nw>maxw) nw=maxw
      for (a=1;a<=nr;a++) for (b=a+1;b<=nr;b++) if (res[b]+0<res[a]+0) { v=res[a]; res[a]=res[b]; res[b]=v }
      printf "MAIN=%s\nWORKERS=%s\nNW=%d\nRESERVED=%s\nNPHYS=%d\nNSMT=%d\n", res[1]+0, (nw>0?ranges(w,nw):""), nw, ranges(res,nr), (np<nw?np:nw), nw-(np<nw?np:nw)
    }' <<<"$topo"
}


hw_probe() {
  local l base maxm
  l=$(lscpu 2>/dev/null || true)
  CPU_MODEL=$(sed -n 's/^Model name:[[:space:]]*//p' <<<"$l" | head -1 || true); [ -n "$CPU_MODEL" ] || CPU_MODEL="unknown CPU"
  base=$(grep -oE '[0-9]+\.[0-9]+ ?GHz' <<<"$CPU_MODEL" | head -1 | tr -d ' GHz' || true)
  maxm=$(awk -F: '/CPU max MHz/ {print int($2)}' <<<"$l" || true)
  # sustained all-core clock: base from the model name, else 85% of max turbo, else 2.5 GHz
  GHZ100=$(awk -v b="${base:-0}" -v m="${maxm:-0}" 'BEGIN{ if (b>0) print int(b*100); else if (m>0) print int(m*0.085); else print 250 }')
  SOCKETS=$(awk -F: '/^Socket\(s\)/ {gsub(/ /,"",$2); print $2}' <<<"$l" || true); SOCKETS=${SOCKETS:-1}
  NODES=$(find /sys/devices/system/node -maxdepth 1 -name 'node[0-9]*' 2>/dev/null | wc -l); [ "$NODES" -ge 1 ] || NODES=1
  RAM_MB=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
}

# nic_probe PCI -> "<effective Mbit/s> <speed source> <pcie Mbit/s or 0>"
nic_probe() {
  local pci=$1 d=/sys/bus/pci/devices/$1 mb="" src="" net sp desc gt w pc=0
  if [ -n "${NIC_SPEED[$pci]:-}" ]; then mb=${NIC_SPEED[$pci]}; src=given
  else
    net=$(ls "$d/net" 2>/dev/null | head -1 || true)
    if [ -n "$net" ]; then
      sp=$(cat "/sys/class/net/$net/speed" 2>/dev/null || echo -1)
      if [ "${sp:-0}" -gt 0 ] 2>/dev/null; then mb=$sp; src=link; fi
    fi
    if [ -z "$mb" ] && command -v lspci >/dev/null; then
      desc=$(lspci -s "${pci#0000:}" 2>/dev/null || true)
      case "$desc" in
        *100G*|*100GbE*|*100-G*) mb=100000 ;; *50G*|*50GbE*) mb=50000 ;; *40G*|*40GbE*|*XL710*) mb=40000 ;;
        *25G*|*25GbE*|*XXV710*) mb=25000 ;; *10G*|*10-Gigabit*|*10GbE*|*82599*|*X520*|*X710*|*X550*) mb=10000 ;;
      esac
      [ -z "$mb" ] || src=pci-id
    fi
    [ -n "$mb" ] || { mb=10000; src=assumed; }
  fi
  gt=$(sed -n 's/^\([0-9.]*\) GT\/s.*/\1/p' "$d/current_link_speed" 2>/dev/null | head -1 || true)
  w=$(cat "$d/current_link_width" 2>/dev/null || true)
  if [ -n "$gt" ] && [ -n "$w" ]; then
    pc=$(awk -v g="$gt" -v w="$w" 'BEGIN{ e=(g>=8?128/130:0.8); printf "%d", g*w*e*1000 }')
    [ "$pc" -ge "$mb" ] || mb=$pc    # the slot is slower than the port: PCIe is the limit
  fi
  echo "$mb $src $pc"
}

# Capacity estimate. Conservative model for VPP L3 forwarding (IPv4/IPv6, linux-cp, no ACL/NAT):
#   ~2 Mpps per GHz per physical core (SMT sibling counts 25%), line rate 1 pkt = frame + 20 B overhead,
#   forwarded Gbit/s is capped by the sum of NIC speeds and each NIC's PCIe slot.
vpp_report() {
  local MAIN="" WORKERS="" NW=0 NPHYS=0 NSMT=0 RESERVED="" pci nm mb src pc lines="" d note=""
  local sel=("${VPP_NICS[@]}")
  eval "$(vpp_cpu_plan)"
  hw_probe
  if [ ${#sel[@]} -eq 0 ]; then
    for d in /sys/bus/pci/devices/*; do [ -d "$d/net" ] && sel+=("$(basename "$d")"); done
    note="no --vpp-nic given: counting every NIC found (choose with --vpp-nic PCI[@Gbps])"
  fi
  for pci in "${sel[@]}"; do
    read -r mb src pc < <(nic_probe "$pci")
    nm=$(ls "/sys/bus/pci/devices/$pci/net" 2>/dev/null | head -1 || true)
    lines+="$pci ${nm:--} $mb $src $pc"$'\n'
  done
  BV_NICS=$lines awk -v model="$CPU_MODEL" -v ghz="$GHZ100" -v nphys="$NPHYS" -v nsmt="$NSMT" -v main="$MAIN" -v ram="$RAM_MB" \
      -v sock="$SOCKETS" -v nodes="$NODES" -v res="$RESERVED" -v note="$note" '
    function rate(g) { return (g>=1000 ? sprintf("%.1f Gbit/s", g/1000) : sprintf("%d Mbit/s", g)) }
    function tp(pps, sz) { return pps * sz * 8 / 1000 }
    BEGIN {
      eff = nphys + 0.25*nsmt; cpu = eff * 2 * ghz/100            # Mpps
      n = split(ENVIRON["BV_NICS"], L, "\n"); tot = 0; cnt = 0
      for (i=1;i<=n;i++) if (L[i] != "") { split(L[i], f, " "); cnt++; nm[cnt]=f[1]; ifn[cnt]=f[2]; sp[cnt]=f[3]; sr[cnt]=f[4]; pcx[cnt]=f[5]; tot += f[3] }
      printf "\nCapacity estimate (VPP L3 forwarding, IPv4+IPv6, conservative)\n"
      printf "  CPU     : %s, %d socket(s), %d NUMA node(s)\n", model, sock, nodes
      printf "            %d forwarding worker(s): %d physical%s @ ~%.2f GHz, cores kept for OS/BIRD: %s\n", nphys+nsmt, nphys, (nsmt>0 ? " + " nsmt " SMT" : ""), ghz/100, res
      printf "            -> ~%.0f Mpps (all flows spread over workers by RSS)\n", cpu
      printf "  RAM     : %.1f GB\n", ram/1024
      if (ram >= 15000)      r = "full IPv4+IPv6 table with several upstreams (multi-million routes)"
      else if (ram >= 7500)  r = "full IPv4+IPv6 table (~1.3M routes)"
      else if (ram >= 3500)  r = "one full IPv4 table, IPv6 table"
      else                   r = "partial tables / default route only (hundreds of thousands of routes)"
      printf "            -> FIB headroom: %s\n", r
      if (cnt == 0) { printf "  NIC     : none found (VPP needs a DPDK-capable NIC bound to vfio-pci)\n"; exit }
      for (i=1;i<=cnt;i++) printf "  NIC     : %s %s %s (%s)%s\n", nm[i], (ifn[i]=="-"?"":ifn[i]), rate(sp[i]), sr[i], (pcx[i]>0 ? ", PCIe slot " rate(pcx[i]) : "")
      if (note != "") printf "            %s\n", note
      printf "            -> %s receive capacity in total\n", rate(tot)
      printf "  Forwarding capacity (one direction, sum of all ports):\n"
      split("64 360 1518", S, " "); split("64-byte packets|IMIX (~360 B avg)|1518-byte packets", T, "|")
      for (k=1;k<=3;k++) {
        sz=S[k]; lp = tot*1e6/((sz+20)*8)/1e6                       # Mpps the links can carry
        pps = (cpu < lp ? cpu : lp); lim = (cpu < lp ? "CPU" : "NIC")
        printf "    %-20s %7.1f Mpps  %8.1f Gbit/s   limited by %s\n", T[k], pps, tp(pps, sz), lim
      }
      imixlp = tot*1e6/((360+20)*8)/1e6
      if (cpu < imixlp)      printf "  Advice  : CPU is the bottleneck at IMIX: faster/more cores add capacity.\n"
      else                   printf "  Advice  : NICs are the bottleneck at IMIX: more/faster ports add capacity (CPU has room).\n"
      printf "  Note    : estimate, not a benchmark. One flow stays on one worker (~%.0f Mpps max), measure with TRex/iperf3.\n\n", 2*ghz/100
    }' </dev/null
}

write_vpp_config() {
  local f=/etc/vpp/startup.conf want cpu_block="" dpdk_block="" plug_dpdk=disable
  local MAIN="" WORKERS="" NW=0 RESERVED="" NPHYS=0 NSMT=0
  hw_probe
  eval "$(vpp_cpu_plan)"
  local ram_mb; ram_mb=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
  local nq=$NW; [ "$nq" -ge 1 ] || nq=1
  # buffers scale with workers; heap by RAM
  # fastest NIC decides ring and buffer depth: more in flight per queue at 25G+ (absorbs bursts without drops)
  local maxmb=0 pn pmb _s _p; for pn in "${VPP_NICS[@]}"; do read -r pmb _s _p < <(nic_probe "$pn"); [ "$pmb" -le "$maxmb" ] || maxmb=$pmb; done
  local desc=2048 per_q=32768
  [ "$maxmb" -ge 25000 ] && { desc=4096; per_q=65536; }
  [ "$maxmb" -ge 100000 ] && per_q=131072
  local buffers=$(( per_q * nq )); [ "$buffers" -ge 65536 ] || buffers=65536
  local heap=256; [ "$ram_mb" -ge 3500 ] && heap=512; [ "$ram_mb" -ge 7500 ] && heap=1024; [ "$ram_mb" -ge 15000 ] && heap=2048; [ "$ram_mb" -ge 60000 ] && heap=4096
  # hugepages (2 MB): buffers*2.3KB + heap + statseg + slack, never more than 40% of RAM
  local seg=256; [ "$ram_mb" -ge 4000 ] || seg=64
  local hp_mb=$(( buffers * 24 / 10000 + heap + seg + 128 )); local cap=$(( ram_mb / 2 ))
  [ "$hp_mb" -le "$cap" ] || { warn "RAM small: trimming VPP buffers to fit 50% of memory"; buffers=$(( (cap - heap - seg - 128) * 10000 / 24 )); hp_mb=$cap; }
  [ "$buffers" -ge 16384 ] || die "not enough RAM for VPP (${ram_mb} MB)"
  local hugepages=$(( (hp_mb + 1) / 2 ))
  if [ -n "$WORKERS" ]; then
    cpu_block="cpu {
  main-core $MAIN
  corelist-workers $WORKERS
}"
  else
    warn "no spare cores for VPP workers (reserve=$VPP_RESERVE): VPP runs single-threaded on the main core"
    cpu_block="cpu { main-core $MAIN }"
  fi
  if [ ${#VPP_NICS[@]} -gt 0 ]; then
    plug_dpdk=enable
    dpdk_block=$'dpdk {\n  uio-driver vfio-pci\n  no-multi-seg\n  # each NIC gets one RX/TX queue per worker (RSS spreads flows over all cores)'
    local n; for n in "${VPP_NICS[@]}"; do
      dpdk_block+=$'\n'"  dev $n {"$'\n'"    num-rx-queues $nq"$'\n'"    num-tx-queues $nq"$'\n'"    num-rx-desc $desc"$'\n'"    num-tx-desc $desc"$'\n'"  }"
    done
    dpdk_block+=$'\n}'
  fi
  want=$(cat <<EOF
# managed by boxvector install.sh (re-run the installer to regenerate; manual edits are overwritten)
# cpu plan: main=$MAIN workers=${WORKERS:-none} ($NW) reserved-for-os=$RESERVED
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
$cpu_block
buffers {
  buffers-per-numa $buffers
  page-size default-hugepage
}
memory { main-heap-size ${heap}M }
statseg { size ${seg}M }
plugins {
  plugin default { enable }
  plugin linux_cp_plugin.so { enable }
  plugin linux_nl_plugin.so { enable }
  plugin dpdk_plugin.so { $plug_dpdk }
}
$dpdk_block
EOF
)
  if [ -f "$f" ] && [[ "$(head -1 "$f")" != *"managed by boxvector install.sh"* ]] && [ ! -e "$f.pre-boxvector" ]; then
    run cp -a "$f" "$f.pre-boxvector"
  fi
  local cli=/etc/vpp/boxvector-lcp.cli
  local cli_want=$'# managed by boxvector install.sh: mirror VPP interfaces into Linux (BIRD talks to the Linux side)\nlcp lcp-sync on\nlcp lcp-auto-subint on'
  VPP_PLAN="main=$MAIN workers=${WORKERS:-none}($NW) os=$RESERVED buffers=$buffers hugepages=${hugepages}x2M heap=${heap}M"
  log "VPP cpu plan: $VPP_PLAN"
  tune_os "$hugepages" "$RESERVED" "$WORKERS"
  if [ -f "$f" ] && [ "$(cat "$f")" = "$want" ] && [ -f "$cli" ] && [ "$(cat "$cli")" = "$cli_want" ]; then log "$f already up to date"; return; fi
  log "writing $f"
  [ "$DRY" = 1 ] && return
  install -d -m 0755 /etc/vpp /var/log/vpp
  printf '%s\n' "$cli_want" >"$cli"
  printf '%s\n' "$want" >"$f"
  VPP_CONF_CHANGED=1
}

# Host tuning for a poll-mode forwarder: hugepages, performance governor, keep OS services off the VPP cores.
tune_os() {
  local hp=$1 reserved=$2 workers=$3
  [ "$DRY" = 1 ] && { echo "+ tune_os hugepages=$hp reserved=$reserved"; return; }
  local sc=/etc/sysctl.d/81-boxvector-vpp.conf sc_want
  sc_want=$(printf 'vm.nr_hugepages=%s\nvm.max_map_count=%s\nkernel.shmmax=%s\n' "$hp" $(( hp * 4 + 65530 )) $(( hp * 2 * 1024 * 1024 )))
  if [ "$(cat "$sc" 2>/dev/null)" != "$sc_want" ]; then printf '%s\n' "$sc_want" >"$sc"; sysctl -q -p "$sc" >/dev/null 2>&1 || true; fi
  local got; got=$(awk '/HugePages_Total/ {print $2}' /proc/meminfo)
  [ "${got:-0}" -ge "$hp" ] || warn "hugepages: wanted $hp, kernel has ${got:-0} (memory fragmented?). Reboot to get them all."
  [ "$TUNE_OS" = 1 ] || return 0
  # performance governor (not available in most VMs: ignored there)
  if ls /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor >/dev/null 2>&1; then
    for g in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance >"$g" 2>/dev/null || true; done
    cat >/etc/systemd/system/boxvector-cpu-performance.service <<'UNIT'
[Unit]
Description=boxvector: CPU governor performance
After=sysinit.target
[Service]
Type=oneshot
ExecStart=/bin/sh -c 'for g in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance > $g; done'
[Install]
WantedBy=multi-user.target
UNIT
    [ -d /run/systemd/system ] && systemctl enable boxvector-cpu-performance.service >/dev/null 2>&1 || true
  fi
  # keep every other service on the reserved cores so they never steal cycles from the polling workers
  if [ -n "$workers" ] && [ -d /run/systemd/system ]; then
    install -d /etc/systemd/system.conf.d /etc/systemd/system/vpp.service.d
    printf '# managed by boxvector install.sh\n[Manager]\nCPUAffinity=%s\n' "$reserved" >/etc/systemd/system.conf.d/boxvector-cpuaffinity.conf
    # VPP itself pins its threads; give it the full mask so main/workers can move to their cores
    printf '# managed by boxvector install.sh\n[Service]\nCPUAffinity=\nLimitMEMLOCK=infinity\n' >/etc/systemd/system/vpp.service.d/boxvector.conf
    systemctl daemon-reload >/dev/null 2>&1 || true
    REBOOT_HINT=1   # CPUAffinity of PID 1 applies after daemon-reexec/reboot
  fi
}
VPP_CONF_CHANGED=0; REBOOT_HINT=0; VPP_PLAN=""
if [ "$ESTIMATE" = 1 ]; then vpp_report; exit 0; fi
if [ "$VPP" = 1 ]; then log "VPP"; vpp_report; install_vpp; fi

# ---------- boxvector ----------
fetch_deb() {
  local out=$1 url
  if [ -n "$DEB" ]; then
    case "$DEB" in http*://*) run curl -fsSL "$DEB" -o "$out" ;; *) [ -f "$DEB" ] || die "no such file: $DEB"; cp "$DEB" "$out" ;; esac
    return
  fi
  if [ "$BV_VERSION" = latest ]; then
    url=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
      | grep -oE "https://[^\"]+boxvector_[^\"]+_${ARCH}\.deb" | head -1 || true)
  else
    url="https://github.com/$REPO/releases/download/v${BV_VERSION#v}/boxvector_${BV_VERSION#v}_${ARCH}.deb"
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
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
if [ "$NO_BV" = 1 ]; then
  pkg_installed boxvector || die "--no-boxvector but boxvector is not installed"
  log "boxvector kept as installed"
else
  log "boxvector"
  fetch_deb "$tmp/boxvector.deb"
  [ "$DRY" = 1 ] || run $APT install "$tmp/boxvector.deb"
fi

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
[ "$VPP" = 1 ] && echo "  vpp cpu   : $VPP_PLAN"
[ "$REBOOT_HINT" = 1 ] && echo "  note      : reboot once so systemd CPUAffinity (OS kept on reserved cores) takes effect"
if [ "$WEB" = 1 ]; then
  echo "  web UI    : https://${ip4:-<server-ip>}:6969  (self-signed certificate)"
  [ -n "${NEWPW:-}" ] && echo "  login     : $WEB_USER / $NEWPW   <- shown once, change it after login"
fi
echo "  config    : /etc/boxvector.yml"
[ "$WEB" = 1 ] || echo "  web UI    : not enabled. Enable later: sudo -u boxvector boxvector web passwd admin --auth-file /etc/boxvector/web.users && sudo systemctl enable --now boxvector-web"
[ "$VPP" = 1 ] && echo "  vpp       : vppctl show interface | vppctl show lcp   (bind NICs: see README, VPP)"
exit 0
