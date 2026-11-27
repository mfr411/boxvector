<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/boxvector-logo-white.svg">
    <img src="assets/boxvector-logo.svg" alt="boxvector" width="320">
  </picture>
</p>

# boxvector

boxvector generates and manages BGP configuration for [BIRD](https://bird.nic.cz)
2 and 3 from a single YAML file, with a web UI for day-to-day operations.
Inspired by [Pathvector](https://github.com/natesales/pathvector), it focuses on
BIRD 3, security hardening and a web interface.

- One YAML file describes your ASN, prefixes, templates and peers.
- Per-peer filters: IRR (bgpq4), RPKI, bogons, max-prefix from PeeringDB.
- Every generated config is checked with `bird -p` before BIRD is reloaded.
- Web UI: live sessions, add peer, config editor with diff and dry-run,
  automatic snapshots and one-click rollback, route lookup, change history.
- Runs as an unprivileged user; the web UI requires login and listens on
  localhost by default.

## Supported systems

| OS | Minimum version | BIRD from apt |
|----|-----------------|---------------|
| Debian | **12 (bookworm)** | `bird2` on 12 and 13 · `bird3` on 13 (12: CZ.NIC repo) |
| Ubuntu | **22.04 (jammy)** | `bird2` · `bird3` via CZ.NIC repo |

BIRD 2 (2.0.7+) and BIRD 3 are both supported; see [BIRD 2 or BIRD 3](#4-bird-2-or-bird-3).

Architectures: amd64, arm64. Derivatives (Proxmox VE, Linux Mint, …) work if
their base is one of the above.

Older releases (Debian 11 and below, Ubuntu 20.04 and below) and other
distributions are **not supported**; the package refuses to install there.

---

## 1. Install

### 1.0 Quick install (script)

`install.sh` installs everything for you on Debian 12+/Ubuntu 22.04+. It is
idempotent (safe to run again) and never overwrites an existing config.

```sh
curl -fsSL https://raw.githubusercontent.com/mfr411/boxvector/main/install.sh -o install.sh
sudo bash install.sh                 # interactive menu
sudo bash install.sh --mode all      # boxvector + BIRD + VPP (linux-cp) + web UI
sudo bash install.sh --mode core     # boxvector + BIRD only
```

| Option | Meaning |
|---|---|
| `--mode all\|core\|custom` | `all` = boxvector, BIRD, bgpq4, VPP + linux-cp, web UI enabled with an admin user. `core` = boxvector, BIRD, bgpq4 only. `custom` = choose with `--vpp/--no-vpp`, `--web/--no-web` |
| `--bird 2\|3\|keep` | BIRD major version (default 2; 3 adds the CZ.NIC repository) |
| `--vpp-nic 0000:03:00.0` | add a DPDK NIC to VPP. Refused while the NIC is still a kernel interface, so a remote session is not cut |
| `--web-user NAME` | web UI admin name (default `admin`). Password is random and shown once, or set `BOXVECTOR_WEB_PASSWORD` |
| `--vpp-nic PCI` (repeatable) | each NIC gets one RX/TX queue per worker, so RSS spreads flows over all cores |
| `--vpp-reserve N`, `--vpp-smt`, `--vpp-workers N` | physical cores kept for OS/BIRD (default 1) / also use hyper-threads / cap the worker count |
| `--no-os-tune`, `--no-boxvector` | skip governor and CPUAffinity changes / keep the installed boxvector |
| `--version X.Y.Z`, `--deb FILE\|URL` | pick a release or a local package |
| `--dry-run`, `--yes` | preview only / no questions |

**Capacity estimate.** `sudo bash install.sh --estimate` (also shown before every VPP install) prints
what this machine can forward: CPU workers x clock gives Mpps, NIC speed (from the link, PCI ID or
`--vpp-nic PCI@GBPS`) and the PCIe slot limit give Gbit/s, for 64 B, IMIX and 1518 B packets, plus which side
is the bottleneck. RAM maps to FIB headroom. It is a conservative model for L3 forwarding without ACL/NAT, not a
benchmark. On big machines the installer scales automatically: more workers, deeper NIC rings (4096 at 25G+)
and more buffers (up to 131072 per queue at 100G).

**CPU tuning.** The installer reads the CPU topology and gives VPP every
core except the reserved ones: `main-core` and the OS/BIRD/boxvector stay on
the first physical core, all other physical cores of the NIC's NUMA node become
VPP workers (cores on another NUMA node are not used, they would cross the
interconnect). Hugepages, buffers, heap and `statseg` are sized from the
worker count and RAM. The CPU governor is set to `performance` and systemd
`CPUAffinity` keeps other services on the reserved cores (reboot once to apply).
The chosen plan is written at the top of `/etc/vpp/startup.conf` and printed at the end.

VPP comes from the fd.io repository (`packagecloud.io/fdio/release`). The
installer writes `/etc/vpp/startup.conf` with the `linux_cp` and `linux_nl`
plugins and `lcp lcp-sync on`, so interfaces created in VPP are mirrored
into Linux where BIRD runs. An existing `startup.conf` is saved as
`startup.conf.pre-boxvector`. VPP needs a NIC bound to `vfio-pci` before it can forward traffic.

### 1.1 BIRD

boxvector works with **BIRD 2 (2.0.7 or newer) and BIRD 3**. BIRD 3 is not
required. The installed version is detected on every run (`bird --version`)
and the config is generated for it. BIRD 3-only options such as
`bird-threads` are skipped on BIRD 2 with a warning.

Pick one:

**Option A: BIRD 2 (from the OS repository)**

Available on every supported release. This is the simplest choice and fine
for most routers.

```sh
sudo apt update
sudo apt install bird2 bgpq4
bird --version          # BIRD version 2.x
```

Versions shipped: Debian 12 → 2.0.12, Debian 13 → 2.17.6, Ubuntu 22.04 → 2.0.8,
Ubuntu 24.04 → 2.14.

**Option B: BIRD 3**

Debian 13 ships it:

```sh
sudo apt update
sudo apt install bird3 bgpq4
```

On Debian 12 and Ubuntu use the CZ.NIC repository:

```sh
sudo apt install apt-transport-https ca-certificates wget lsb-release
sudo wget -O /usr/share/keyrings/cznic-labs-pkg.gpg https://pkg.labs.nic.cz/gpg
echo "deb [signed-by=/usr/share/keyrings/cznic-labs-pkg.gpg] https://pkg.labs.nic.cz/bird3 $(lsb_release -sc) main" \
  | sudo tee /etc/apt/sources.list.d/cznic-labs-bird3.list
sudo apt update && sudo apt install bird3 bgpq4
```

### 1.2 boxvector

Download the `.deb` for your architecture from the
[Releases](https://github.com/mfr411/boxvector/releases) page and install it:

```sh
sudo apt install ./boxvector_<version>_amd64.deb
```

The package:

- checks the OS version (see above) and stops if it is not supported;
- installs `/usr/bin/boxvector` and the `boxvector-web` systemd service
  (installed, not started);
- creates the system user `boxvector` and adds it to the `bird` group;
- creates `/etc/boxvector.yml` from an example (never overwritten
  on upgrade) and `/var/lib/boxvector/`;
- makes `/etc/bird` writable for the `bird` group.

Check it:

```sh
boxvector version
```

---

## 2. Configure

Edit `/etc/boxvector.yml`:

```yaml
asn: 65530
router-id: 192.0.2.1
source4: 192.0.2.1
source6: 2001:db8::1
prefixes:
  - 192.0.2.0/24
  - 2001:db8::/48
hostname: rs1

bird-directory: /etc/bird/
bird-socket: /run/bird/bird.ctl
cache-directory: /var/lib/boxvector/cache
# bird-threads: 4            # BIRD 3 only, ignored on BIRD 2

templates:
  upstream:
    local-pref: 100
    filter-irr: false
    add-on-import: ["65530:100"]

peers:
  Example Upstream:
    template: upstream
    asn: 65510
    neighbors:
      - 203.0.113.1
      - 2001:db8:ffff::1
```

All options are listed in [docs/configuration.md](docs/configuration.md)
(generated from the code). Peers can also be added later from the web UI.

Test without touching BIRD (generates and validates only):

```sh
sudo -u boxvector boxvector generate -c /etc/boxvector.yml --dry-run
```

Apply (writes `/etc/bird/`, validates with `bird -p`, reloads BIRD):

```sh
sudo -u boxvector boxvector generate -c /etc/boxvector.yml
sudo birdc show protocols
```

Keep IRR/PeeringDB data fresh, e.g. every 6 hours:

```sh
echo '0 */6 * * * boxvector /usr/bin/boxvector generate -c /etc/boxvector.yml' \
  | sudo tee /etc/cron.d/boxvector
```

---

## 3. Web UI

Create a user (password at least 10 characters) and start the service:

```sh
sudo -u boxvector boxvector web passwd admin --auth-file /etc/boxvector/web.users
sudo systemctl enable --now boxvector-web
sudo systemctl status boxvector-web
```

The UI listens on `127.0.0.1:8180` only. Access it one of two ways.

**SSH tunnel (simplest):**

```sh
ssh -L 8180:127.0.0.1:8180 user@rs1
# then open http://127.0.0.1:8180
```

**Nginx with TLS** (example in `/usr/share/boxvector/nginx-boxvector.conf`):

```sh
sudo apt install nginx
sudo cp /usr/share/boxvector/nginx-boxvector.conf /etc/nginx/sites-available/boxvector
# edit server_name, certificate paths and the allow/deny list for your NOC
sudo ln -s /etc/nginx/sites-available/boxvector /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
sudo systemctl edit boxvector-web
```

In the editor add `--trust-proxy` so the real client IP and HTTPS are used:

```ini
[Service]
ExecStart=
ExecStart=/usr/bin/boxvector web -c /etc/boxvector.yml \
  --listen 127.0.0.1:8180 --state-dir /var/lib/boxvector/web \
  --auth-file /etc/boxvector/web.users --trust-proxy
```

```sh
sudo systemctl restart boxvector-web
```

### Pages

- **Peers** – live BGP sessions from BIRD, routes imported/exported, totals.
  *Add peer* opens a form.
- **Config** – edit the YAML. *Review* shows a diff and a dry-run before
  *Apply*. Snapshots on the right restore older versions.
- **Routes** – look up an IP, a prefix or an origin AS.
- **Logs** – who applied what and when, with generate output.

Every apply takes a snapshot first (last 50 kept in
`/var/lib/boxvector/web/snapshots`). If generation or validation fails, the
previous config is restored automatically and BIRD is left untouched.

### Security notes

- Passwords are bcrypt hashes in `/etc/boxvector/web.users` (must be mode 600,
  the service refuses to start otherwise).
- Sessions: HttpOnly, SameSite=Strict cookies (Secure over HTTPS), 60 min idle,
  12 h max. CSRF token on every form.
- 5 failed logins from one IP lock it out for 10 minutes.
- Strict Content-Security-Policy, no inline scripts, not indexable.
- Plain HTTP on a non-loopback address is refused unless `--allow-public`.
- The service runs as `boxvector` in a hardened systemd sandbox: no
  capabilities, read-only system, 512 MB memory and 50% CPU caps.

---

## 4. BIRD 2 or BIRD 3

Both use the same `boxvector.yml`; nothing in your config has to change.

| | BIRD 2 | BIRD 3 |
|---|---|---|
| Supported | 2.0.7 and newer | 3.0 and newer |
| Tested in CI | 2.0.12, 2.20.0 | 3.1.9, 3.4.0 |
| `bird-threads` | ignored (warning) | `threads N;` written |
| Package name | `bird2` | `bird3` |

Check which one boxvector sees (the first log lines of every run):

```sh
sudo -u boxvector boxvector generate -c /etc/boxvector.yml --dry-run 2>&1 | grep 'BIRD version'
# level=info msg="BIRD version 2.0.12"
```

### Moving from BIRD 2 to BIRD 3

Do this in a maintenance window: BGP sessions restart when BIRD restarts.

```sh
# 1. back up the current config
sudo tar czf /root/bird2-backup-$(date +%F).tgz /etc/bird /etc/boxvector

# 2. add the BIRD 3 repository (Debian 12 / Ubuntu only, see 1.1 Option B)

# 3. replace the package (bird3 conflicts with bird2, apt swaps them)
sudo apt install bird3

# 4. the bird3 package resets /etc/bird ownership; restore boxvector's access
sudo dpkg-reconfigure boxvector

# 5. regenerate for BIRD 3 and check the sessions
sudo -u boxvector boxvector generate -c /etc/boxvector.yml
sudo systemctl restart bird
sudo birdc show protocols
```

Optional: add `bird-threads: 4` (about one per CPU core) to `boxvector.yml`
to use BIRD 3's multi-threading, then run `generate` again.

Back to BIRD 2: `sudo apt install bird2 && sudo dpkg-reconfigure boxvector`,
run `generate` again and restart `bird`. Or restore the backup from step 1.

---

## 5. Upgrade and remove

```sh
sudo apt install ./boxvector_<new-version>_amd64.deb   # config kept, service restarted
sudo apt remove boxvector                              # keeps /etc/boxvector.yml and /etc/boxvector/
sudo apt purge boxvector                               # also deletes config, snapshots and the user
```

Generated BIRD files in `/etc/bird` are never removed by the package.

---

## 6. Troubleshooting

- **`unsupported OS` during install** – Debian 12 / Ubuntu 22.04 or newer is
  required. Upgrade the OS first.
- **`dial unix /run/bird/bird.ctl: permission denied`** – `boxvector` must be
  in group `bird` and the socket mode 660:
  `sudo adduser boxvector bird`, then restart `bird` and `boxvector-web`.
- **`open /etc/bird/...: permission denied`** –
  `sudo chgrp bird /etc/bird && sudo chmod 2775 /etc/bird`
- **Web UI does not start** – `journalctl -u boxvector-web -e`; usually a
  missing `web.users` or wrong permissions on it.
- **`BIRD version ... older than minimum supported version 2.0.7`** – install
  `bird2` from your OS repository (all supported releases ship a newer one).
- **`bird-threads is set but BIRD 2.x does not support threads, ignoring`** –
  harmless on BIRD 2; remove the option or move to BIRD 3 (section 4).
- **Validation error on apply** – the page shows BIRD's error with file and
  line. Nothing was applied.

---

## CLI

Full reference per command: [docs/cli.md](docs/cli.md).

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

## Building from source

Requires Go (version in `go.mod`), `dpkg-deb`.

```sh
go build -o boxvector .
packaging/deb/build.sh 0.1.0 amd64 dist      # dist/boxvector_0.1.0_amd64.deb
packaging/deb/build.sh 0.1.0 arm64 dist
```

`docs/configuration.md`, `docs/cli.md` and the CLI block above are generated
by `docs/generate.sh` (`make docs`); CI regenerates and commits them on every
push, so they always match the code.

Pushing a tag `v*` builds both packages in CI and attaches them to a GitHub
release. CI also checks every config in `tests/` against BIRD 2.0.12, 2.20.0,
3.1.9 and 3.4.0.

## Credits

boxvector is built on top of open-source work by others:

- **[Pathvector](https://github.com/natesales/pathvector)** by
  [Nate Sales](https://github.com/natesales) and contributors – the project that
  inspired boxvector (config model, templates, IRR/RPKI/PeeringDB
  filtering, optimizer). Thank you.
- **[BIRD Internet Routing Daemon](https://bird.nic.cz)** by CZ.NIC Labs.
- **[bgpq4](https://github.com/bgp/bgpq4)** for IRR prefix lists,
  **[PeeringDB](https://www.peeringdb.com)** for network data.

boxvector is powered by [nexcolo](https://www.nexcolo.com/).

## License

MIT, see [LICENSE](LICENSE). The original Pathvector copyright notice is kept
as required by the license.

## Terminal

The web UI has a root terminal (sidebar, Terminal). A small helper (`boxvector-term.service`, runs as root) opens login shells on a PTY; the sandboxed web service authenticates users and proxies to it.

- Only `root` and members of `sudo`/`wheel`/`admin` can open it; every open/close is written to Logs.
- It only works over HTTPS or from localhost. The package serves the UI on `https://<host>:6969` with a self-signed certificate (`/etc/boxvector/tls/web.crt`, `web.key`); replace both files with your own certificate and restart `boxvector-web`. A reverse proxy with `--trust-proxy` also works. `--terminal-insecure` overrides this, which is not recommended.
- Idle sessions are closed after 15 minutes, at most 4 at once.
- Disable it with `systemctl disable --now boxvector-term`.
