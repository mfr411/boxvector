<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/boxvector-logo-white.svg">
    <img src="assets/boxvector-logo.svg" alt="boxvector" width="320">
  </picture>
</p>

<p align="center"><b>English</b> · <a href="README.id.md">Bahasa Indonesia</a> · <a href="https://boxvector.co/">Website &amp; docs</a></p>

# boxvector

Official site and docs: **[boxvector.co](https://boxvector.co)**.

BGP configuration for [BIRD](https://bird.nic.cz) 2 and 3, from one YAML file, with a web UI.
A fork of [Pathvector](https://github.com/natesales/pathvector) focused on BIRD 3, security and day-to-day operations.

- **One YAML file** describes your ASN, prefixes, templates and peers.
- **Safe by construction:** every config is checked with `bird -p` before BIRD reloads. A failed apply restores the previous config.
- **Per-peer filters:** IRR (bgpq4), RPKI, bogons, max-prefix from PeeringDB.
- **Web UI:** live sessions, add/edit peers, config diff + dry-run, snapshots with one-click rollback, route lookup, interfaces, kernel routes, traffic, terminal.
- **Optional VPP data plane** (DPDK, linux-cp) with an installer that picks NICs and cores for you.

> **Not a fit if:** you run a distro other than Debian 12+ / Ubuntu 22.04+, or you need something other than BIRD.

---

## Start here

| I want to... | Go to |
|---|---|
| Try it fast on a fresh Debian/Ubuntu box | [Quick install](#1-quick-install) |
| Install the `.deb` by hand, or already have BIRD | [Manual install](#2-manual-install) |
| Write my first peer | [Configure](#3-configure) |
| Open the web UI safely | [Web UI](#4-web-ui) |
| Harden the box before it touches the internet | [Security checklist](#5-security-checklist) |
| Use VPP for high throughput | [docs/vpp.md](docs/vpp.md) |
| Fix an error | [Troubleshooting](#8-troubleshooting) |

---

## 1. Quick install

Needs root on Debian 12+/Ubuntu 22.04+ (amd64 or arm64). The script is idempotent and never overwrites an existing config.

```sh
curl -fsSL https://raw.githubusercontent.com/mfr411/boxvector/main/install.sh -o install.sh
sudo bash install.sh --dry-run        # preview, changes nothing
sudo bash install.sh                  # interactive menu
```

Pick a mode:

| Mode | You get |
|---|---|
| `--mode core` | boxvector + BIRD + bgpq4. **Best default.** |
| `--mode all` | core + VPP + linux-cp + web UI. Asks which NICs go to VPP, shows a plan, waits for `y`. |
| `--mode custom` | choose with `--vpp/--no-vpp`, `--web/--no-web` |

The installer asks which IPs/CIDRs may open the web UI (default: your SSH client IP; type `none` for this server only). Non-interactive: `--web-allow 192.0.2.0/24,203.0.113.5`. `--yes` without it means loopback only. Re-running the installer keeps the allow-list you already set.

The web admin password is random and **printed once** at the end. Save it. Set your own with `BOXVECTOR_WEB_PASSWORD=...`.

<details>
<summary>All installer options</summary>

| Option | Meaning |
|---|---|
| `--bird 2\|3\|keep` | BIRD major version (default 2; 3 adds the CZ.NIC repo) |
| `--list-nics` | list Ethernet cards (driver, speed, NUMA, state) and exit |
| `--vpp-nic PCI` (repeatable) | give a NIC to VPP. Refused while it is still a kernel interface, so SSH is not cut |
| `--vpp-reserve N`, `--vpp-smt`, `--vpp-workers N` | cores kept for the OS (default 1), use hyper-threads, cap workers |
| `--web-user NAME` | web admin name (default `admin`) |
| `--no-os-tune`, `--no-boxvector` | skip governor/CPUAffinity changes, keep the installed boxvector |
| `--version X.Y.Z`, `--deb FILE\|URL` | pick a release or a local package |
| `--estimate` | print what this machine can forward (model, not a benchmark) |
| `--dry-run`, `--yes` | preview only, no questions |

VPP details, requirements (IOMMU, RAM) and undo steps: [docs/vpp.md](docs/vpp.md).
</details>

**Check it worked:**

```sh
boxvector version
```

The installer does **not** run `generate` and does not touch BIRD. `/etc/boxvector.yml` ships fully commented out: edit it first (next section), then generate.

---

## 2. Manual install

<details>
<summary>Supported systems</summary>

| OS | Minimum | BIRD from apt |
|---|---|---|
| Debian | 12 (bookworm) | `bird2` on 12/13, `bird3` on 13 (12: CZ.NIC repo) |
| Ubuntu | 22.04 (jammy) | `bird2`, `bird3` via CZ.NIC repo |

BIRD 2 (2.0.7+) and BIRD 3 both work and are detected on every run. Older OS releases are refused by the package.
</details>

```sh
# 1. BIRD (BIRD 2 shown; for BIRD 3 see the CZ.NIC repo: https://pkg.labs.nic.cz)
sudo apt update && sudo apt install bird2 bgpq4

# 2. boxvector: download the .deb for your arch from Releases, then verify it
#    https://github.com/mfr411/boxvector/releases
sha256sum -c SHA256SUMS --ignore-missing
sudo apt install ./boxvector_<version>_amd64.deb
```

The package creates the `boxvector` user (in group `bird`), `/etc/boxvector.yml` (from an example, never overwritten) and the web service (installed, not started).

---

## 3. Configure

Edit `/etc/boxvector.yml` (shipped fully commented out; uncomment and adapt this):

```yaml
asn: 65530
router-id: 192.0.2.1
source4: 192.0.2.1
source6: 2001:db8::1
prefixes:
  - 192.0.2.0/24
  - 2001:db8::/48
hostname: rs1

templates:
  upstream:
    local-pref: 100
    filter-rpki: true          # reject RPKI invalids
    filter-irr: false          # true needs bgpq4 + a valid as-set per peer
    add-on-import: ["65530:100"]

peers:
  Example Upstream:
    template: upstream
    asn: 65510
    password: "change-me"      # TCP-MD5, must match the other side
    ttl-security: true         # GTSM, only for directly connected peers
    neighbors:
      - 203.0.113.1
      - 2001:db8:ffff::1
```

Every option: [docs/configuration.md](docs/configuration.md) (generated from the code).

**Test first, then apply:**

```sh
sudo -u boxvector boxvector generate -c /etc/boxvector.yml --dry-run   # validate only
sudo -u boxvector boxvector generate -c /etc/boxvector.yml             # write, validate, reload
sudo birdc show protocols                                              # sessions should reach Established
```

Refresh IRR/PeeringDB data every 6 hours (optional):

```sh
echo '0 */6 * * * boxvector /usr/bin/boxvector generate -c /etc/boxvector.yml' | sudo tee /etc/cron.d/boxvector
```

---

## 4. Web UI

The package serves HTTPS on port **6969** with a self-signed certificate (`/etc/boxvector/tls/web.crt`, `web.key`). Replace both with your own certificate for production.

```sh
# create the admin user (password 10+ characters), then start
sudo -u boxvector boxvector web passwd admin --auth-file /etc/boxvector/web.users
sudo systemctl enable --now boxvector-web
```

Open `https://<host>:6969` and sign in.

> Sign-in uses the users in `/etc/boxvector/web.users` (bcrypt). Hosts upgraded from an older build with no user there keep the old server-account login until you create one (a drop-in file explains how to switch).

**Restrict who can reach it** (do this on any host reachable from the internet). The installer asks for the allowed IPs/CIDRs; without an answer the UI listens on `127.0.0.1` only. To change it later, edit `/etc/boxvector/web.env` and restart:

```sh
# /etc/boxvector/web.env
WEB_LISTEN=0.0.0.0:6969
WEB_ARGS=--allow-from 192.0.2.0/24,2001:db8::/48
```
```sh
sudo systemctl restart boxvector-web
```

Other access options: bind to loopback with `--listen 127.0.0.1:8180` and use an SSH tunnel, or put nginx in front ([example](packaging/examples/nginx-boxvector.conf), start the UI with `--trust-proxy`).

| Page | What it does |
|---|---|
| **Peers** | live sessions from BIRD, add/edit peers, per-peer filter rules |
| **Config** | edit YAML, *Review* shows diff + dry-run before *Apply*, restore snapshots |
| **Routes** | look up an IP, prefix or origin AS |
| **Interfaces** | addresses, VLANs, bridges. Risky changes roll back after 60 s unless you confirm |
| **Traffic**, **VPP** | per-interface rates, VPP status and settings |
| **Terminal** | root shell in the browser (root/sudo password required, see below) |
| **Logs** | who applied what and when |

Every apply takes a snapshot first (last 50 kept). If generation or validation fails, the previous config comes back and BIRD is untouched.

---

## 5. Security checklist

Tick these before the box faces the internet.

- [ ] **Limit the web UI:** `--allow-from <your NOC CIDRs>` or loopback + SSH tunnel. Never leave `:6969` open to the world.
- [ ] **Own TLS certificate** in `/etc/boxvector/tls/` (the default is self-signed).
- [ ] **Strong web password**, one user per operator (`boxvector web passwd <name>`).
- [ ] **Firewall BGP (TCP 179)** to your neighbors only. nftables example:

  ```nft
  table inet bv_bgp {
    set nb4 { type ipv4_addr; elements = { 203.0.113.1 } }
    set nb6 { type ipv6_addr; elements = { 2001:db8:ffff::1 } }
    chain input {
      type filter hook input priority 0; policy accept;
      tcp dport 179 ip saddr @nb4 accept
      tcp dport 179 ip6 saddr @nb6 accept
      tcp dport 179 drop
    }
  }
  ```
- [ ] **Per peer:** set `password` (MD5), `ttl-security: true` for direct peers, keep `import-limit4/6` or `auto-import-limits: true`.
- [ ] **Filters on:** `filter-rpki: true` (default), `filter-irr: true` where the peer has an as-set, bogons stay on.
- [ ] **Terminal:** leave it off if you do not need it: `systemctl disable --now boxvector-term`.
- [ ] **Verify downloads:** check `SHA256SUMS` from the Releases page before installing a `.deb`.

<details>
<summary>What boxvector already does for you</summary>

- Passwords are bcrypt hashes in `/etc/boxvector/web.users` (mode 600, the service refuses to start otherwise).
- Cookies are HttpOnly + SameSite=Strict (Secure over HTTPS), 60 min idle / 12 h max. CSRF token on every form.
- Login lockout per IP **and** per username. Strict CSP, no inline scripts, HSTS over TLS, not indexable.
- The web service runs as `boxvector` in a hardened systemd sandbox (no shadow access, read-only system, memory/CPU caps).
- **Root terminal:** a small root helper opens the shell. It checks the account password itself (root or sudo/wheel/admin members only) and locks after 5 failures, so a compromised web process alone cannot get root. Every open/close is logged. Idle sessions close after 15 min, max 4 at once. Works over HTTPS or localhost only.
- Plain HTTP on a non-loopback address is refused unless `--allow-public`.
</details>

**Found a vulnerability?** Please report it privately via GitHub Security Advisories on this repository rather than a public issue.

---

## 6. BIRD 2 or BIRD 3

Same `boxvector.yml`, nothing to change.

| | BIRD 2 | BIRD 3 |
|---|---|---|
| Supported | 2.0.7+ | 3.0+ |
| `bird-threads` | ignored (warning) | `threads N;` written |
| Tested in CI | 2.0.12, 2.20.0 | 3.1.9, 3.4.0 |

<details>
<summary>Move from BIRD 2 to BIRD 3 (maintenance window: sessions restart)</summary>

```sh
sudo tar czf /root/bird2-backup-$(date +%F).tgz /etc/bird /etc/boxvector   # 1. backup
# 2. add the BIRD 3 repo on Debian 12 / Ubuntu (https://pkg.labs.nic.cz)
sudo apt install bird3                                                      # 3. swaps bird2
sudo dpkg-reconfigure boxvector                                             # 4. restore /etc/bird access
sudo -u boxvector boxvector generate -c /etc/boxvector.yml                  # 5. regenerate
sudo systemctl restart bird && sudo birdc show protocols
```

Back to BIRD 2: `sudo apt install bird2 && sudo dpkg-reconfigure boxvector`, then `generate` and restart `bird`, or restore the backup.
</details>

---

## 7. Upgrade and remove

```sh
sudo apt install ./boxvector_<new-version>_amd64.deb   # config kept, services restarted
sudo apt remove boxvector                              # keeps /etc/boxvector.yml and /etc/boxvector/
sudo apt purge boxvector                               # also deletes config, snapshots, user
```

Generated files in `/etc/bird` are never removed by the package.

---

## 8. Troubleshooting

| Symptom | Fix |
|---|---|
| `unsupported OS` on install | Needs Debian 12+ / Ubuntu 22.04+. Upgrade the OS. |
| `dial unix /run/bird/bird.ctl: permission denied` | `sudo adduser boxvector bird`, restart `bird` and `boxvector-web` |
| `open /etc/bird/...: permission denied` | `sudo chgrp bird /etc/bird && sudo chmod 2775 /etc/bird` |
| Web UI does not start | `journalctl -u boxvector-web -e`. Usually a missing `web.users` or wrong mode (must be 600) |
| Locked out of the web UI | wait 10 minutes, or reset with `boxvector web passwd <user> --auth-file /etc/boxvector/web.users` |
| `403 forbidden` on the login page | your IP is not in `--allow-from` |
| `BIRD version ... older than 2.0.7` | install `bird2`/`bird3` from your OS or the CZ.NIC repo |
| Validation error on apply | the page shows BIRD's error with file and line. Nothing was applied |

---

## CLI

Full reference: [docs/cli.md](docs/cli.md).

<!-- cli:begin -->
```
boxvector generates and manages BIRD 2/3 BGP configuration (fork of Pathvector).

Usage:
  boxvector [command]

Available Commands:
  birdsh      Lightweight BIRD shell
  completion  Generate the autocompletion script for the specified shell
  config      Export configuration, optionally sanitized with logknife
  dump        Dump configuration
  generate    Generate router configuration
  help        Help about any command
  match       Find common IXPs for a given ASN
  optimizer   Start optimization daemon
  status      Show protocol status
  version     Show version information
  web         Run the web UI

Flags:
  -c, --config string   YAML configuration file (default "/etc/boxvector.yml")
  -d, --dry-run         Don't modify configuration
  -h, --help            help for boxvector
      --lock string     Lock file (check disabled if empty)
  -n, --no-configure    Don't configure BIRD
  -t, --trace           Show trace log messages
  -v, --verbose         Show verbose log messages

Use "boxvector [command] --help" for more information about a command.
```
<!-- cli:end -->

---

## Build from source

Needs Go (version in `go.mod`) and `dpkg-deb`.

```sh
go build -o boxvector .
packaging/deb/build.sh 1.0.0 amd64 dist      # dist/boxvector_1.0.0_amd64.deb
```

`docs/configuration.md`, `docs/cli.md` and the CLI block above are generated by `docs/generate.sh` (`make docs`) and kept in sync by CI. Pushing a tag `v*` builds amd64 and arm64 packages and attaches them to a GitHub release. CI also checks every config in `tests/` against BIRD 2.0.12, 2.20.0, 3.1.9 and 3.4.0.

## Credits

Built on open-source work by others: **[Pathvector](https://github.com/natesales/pathvector)** by Nate Sales and contributors (config model, templates, IRR/RPKI/PeeringDB filtering, optimizer), the **[BIRD](https://bird.nic.cz)** routing daemon by CZ.NIC Labs, **[bgpq4](https://github.com/bgp/bgpq4)** and **[PeeringDB](https://www.peeringdb.com)**.

boxvector is powered by [nexcolo](https://www.nexcolo.com/).

## License

MIT, see [LICENSE](LICENSE). The original Pathvector copyright notice is kept as required.
