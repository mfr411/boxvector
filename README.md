# Boxvector

Boxvector generates and manages BGP configuration for [BIRD](https://bird.nic.cz)
2 and 3 from a single YAML file, with a web UI for day-to-day operations.
It is a fork of [Pathvector](https://github.com/natesales/pathvector) focused on
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
| Debian | **12 (bookworm)** | 13: `bird3` · 12: `bird2` (BIRD 3 via CZ.NIC repo) |
| Ubuntu | **22.04 (jammy)** | `bird2` (BIRD 3 via CZ.NIC repo) |

Architectures: amd64, arm64. Derivatives (Proxmox VE, Linux Mint, …) work if
their base is one of the above.

Older releases (Debian 11 and below, Ubuntu 20.04 and below) and other
distributions are **not supported**; the package refuses to install there.

---

## 1. Install

### 1.1 BIRD

Debian 13 ships BIRD 3:

```sh
sudo apt update
sudo apt install bird3 bgpq4
```

Debian 12 and Ubuntu ship BIRD 2 (`sudo apt install bird2 bgpq4` works
fine). For BIRD 3 there use the CZ.NIC repository:

```sh
sudo apt install apt-transport-https ca-certificates wget lsb-release
sudo wget -O /usr/share/keyrings/cznic-labs-pkg.gpg https://pkg.labs.nic.cz/gpg
echo "deb [signed-by=/usr/share/keyrings/cznic-labs-pkg.gpg] https://pkg.labs.nic.cz/bird3 $(lsb_release -sc) main" \
  | sudo tee /etc/apt/sources.list.d/cznic-labs-bird3.list
sudo apt update && sudo apt install bird3 bgpq4
```

### 1.2 Boxvector

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
- creates `/etc/boxvector/boxvector.yml` from an example (never overwritten
  on upgrade) and `/var/lib/boxvector/`;
- makes `/etc/bird` writable for the `bird` group.

Check it:

```sh
boxvector version
```

---

## 2. Configure

Edit `/etc/boxvector/boxvector.yml`:

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

All options are documented in [docs/](docs/). Peers can also be added later
from the web UI.

Test without touching BIRD (generates and validates only):

```sh
sudo -u boxvector boxvector generate -c /etc/boxvector/boxvector.yml --dry-run
```

Apply (writes `/etc/bird/`, validates with `bird -p`, reloads BIRD):

```sh
sudo -u boxvector boxvector generate -c /etc/boxvector/boxvector.yml
sudo birdc show protocols
```

Keep IRR/PeeringDB data fresh, e.g. every 6 hours:

```sh
echo '0 */6 * * * boxvector /usr/bin/boxvector generate -c /etc/boxvector/boxvector.yml' \
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
ExecStart=/usr/bin/boxvector web -c /etc/boxvector/boxvector.yml \
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

## 4. Upgrade and remove

```sh
sudo apt install ./boxvector_<new-version>_amd64.deb   # config kept, service restarted
sudo apt remove boxvector                              # keeps /etc/boxvector
sudo apt purge boxvector                               # also deletes config, snapshots and the user
```

Generated BIRD files in `/etc/bird` are never removed by the package.

---

## 5. Troubleshooting

- **`unsupported OS` during install** – Debian 12 / Ubuntu 22.04 or newer is
  required. Upgrade the OS first.
- **`dial unix /run/bird/bird.ctl: permission denied`** – `boxvector` must be
  in group `bird` and the socket mode 660:
  `sudo adduser boxvector bird`, then restart `bird` and `boxvector-web`.
- **`open /etc/bird/...: permission denied`** –
  `sudo chgrp bird /etc/bird && sudo chmod 2775 /etc/bird`
- **Web UI does not start** – `journalctl -u boxvector-web -e`; usually a
  missing `web.users` or wrong permissions on it.
- **Validation error on apply** – the page shows BIRD's error with file and
  line. Nothing was applied.

---

## Building from source

Requires Go (version in `go.mod`), `dpkg-deb`.

```sh
go build -o boxvector .
packaging/deb/build.sh 0.1.0 amd64 dist      # dist/boxvector_0.1.0_amd64.deb
packaging/deb/build.sh 0.1.0 arm64 dist
```

Pushing a tag `v*` builds both packages in CI and attaches them to a GitHub
release. CI also checks every config in `tests/` against BIRD 2.0.12, 2.20.0,
3.1.9 and 3.4.0.

## License

MIT, see [LICENSE](LICENSE). Based on Pathvector by Nate Sales.
