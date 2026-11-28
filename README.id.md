<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/boxvector-logo-white.svg">
    <img src="assets/boxvector-logo.svg" alt="boxvector" width="320">
  </picture>
</p>

<p align="center"><a href="README.md">English</a> · <b>Bahasa Indonesia</b> · <a href="https://boxvector.co/">Website &amp; dokumentasi</a></p>

# boxvector

Konfigurasi BGP untuk [BIRD](https://bird.nic.cz) 2 dan 3 dari satu file YAML, lengkap dengan web UI.
Fork dari [Pathvector](https://github.com/natesales/pathvector) yang fokus ke BIRD 3, keamanan, dan operasional harian.

- **Satu file YAML** untuk ASN, prefix, template, dan peer.
- **Aman by design:** setiap config dicek dengan `bird -p` sebelum BIRD di-reload. Kalau apply gagal, config sebelumnya dikembalikan.
- **Filter per peer:** IRR (bgpq4), RPKI, bogon, max-prefix dari PeeringDB.
- **Web UI:** sesi live, tambah/ubah peer, diff + dry-run, snapshot dengan rollback satu klik, cari route, interface, kernel route, traffic, terminal.
- **VPP opsional** (DPDK, linux-cp) dengan installer yang memilihkan NIC dan core.

> **Tidak cocok jika:** OS kamu selain Debian 12+ / Ubuntu 22.04+, atau kamu tidak memakai BIRD.

---

## Mulai dari sini

| Saya ingin... | Ke bagian |
|---|---|
| Coba cepat di Debian/Ubuntu baru | [Install cepat](#1-install-cepat) |
| Install `.deb` manual, atau BIRD sudah ada | [Install manual](#2-install-manual) |
| Menulis peer pertama | [Konfigurasi](#3-konfigurasi) |
| Membuka web UI dengan aman | [Web UI](#4-web-ui) |
| Mengamankan box sebelum tersambung ke internet | [Checklist keamanan](#5-checklist-keamanan) |
| Memakai VPP untuk throughput tinggi | [docs/vpp.md](docs/vpp.md) |
| Memperbaiki error | [Troubleshooting](#8-troubleshooting) |

---

## 1. Install cepat

Butuh root di Debian 12+/Ubuntu 22.04+ (amd64 atau arm64). Script ini idempotent dan tidak menimpa config yang sudah ada.

```sh
curl -fsSL https://raw.githubusercontent.com/mfr411/boxvector/main/install.sh -o install.sh
sudo bash install.sh --dry-run        # pratinjau, tidak mengubah apa pun
sudo bash install.sh                  # menu interaktif
```

Pilih mode:

| Mode | Hasilnya |
|---|---|
| `--mode core` | boxvector + BIRD + bgpq4. **Pilihan paling aman untuk awal.** |
| `--mode all` | core + VPP + linux-cp + web UI. Menanyakan NIC mana untuk VPP, menampilkan rencana, menunggu `y`. |
| `--mode custom` | pilih sendiri dengan `--vpp/--no-vpp`, `--web/--no-web` |

Installer menanyakan IP/CIDR mana yang boleh membuka web UI (default: IP SSH kamu; ketik `none` untuk hanya server ini). Non-interaktif: `--web-allow 192.0.2.0/24,203.0.113.5`. `--yes` tanpa opsi ini berarti hanya loopback. Menjalankan ulang installer tidak menimpa allow-list yang sudah ada.

Password admin web diacak dan **hanya ditampilkan sekali** di akhir. Simpan. Atau tentukan sendiri dengan `BOXVECTOR_WEB_PASSWORD=...`.

<details>
<summary>Semua opsi installer</summary>

| Opsi | Arti |
|---|---|
| `--bird 2\|3\|keep` | versi mayor BIRD (default 2; 3 menambah repo CZ.NIC) |
| `--list-nics` | daftar kartu Ethernet (driver, speed, NUMA, status) lalu keluar |
| `--vpp-nic PCI` (bisa diulang) | serahkan NIC ke VPP. Ditolak selama masih jadi interface kernel, supaya SSH tidak putus |
| `--vpp-reserve N`, `--vpp-smt`, `--vpp-workers N` | core untuk OS (default 1), pakai hyper-thread, batasi jumlah worker |
| `--web-user NAME` | nama admin web (default `admin`) |
| `--no-os-tune`, `--no-boxvector` | lewati perubahan governor/CPUAffinity, pertahankan boxvector terpasang |
| `--version X.Y.Z`, `--deb FILE\|URL` | pilih rilis atau paket lokal |
| `--estimate` | perkiraan kapasitas forwarding mesin ini (model, bukan benchmark) |
| `--dry-run`, `--yes` | pratinjau saja, tanpa pertanyaan |

Detail VPP, syarat (IOMMU, RAM), dan cara membatalkan: [docs/vpp.md](docs/vpp.md).
</details>

**Cek hasilnya:**

```sh
boxvector version
```

Installer **tidak** menjalankan `generate` dan tidak menyentuh BIRD. `/etc/boxvector.yml` dipasang dengan semua baris di-comment: isi dulu (bagian berikutnya), baru generate.

---

## 2. Install manual

<details>
<summary>Sistem yang didukung</summary>

| OS | Minimum | BIRD dari apt |
|---|---|---|
| Debian | 12 (bookworm) | `bird2` di 12/13, `bird3` di 13 (12: repo CZ.NIC) |
| Ubuntu | 22.04 (jammy) | `bird2`, `bird3` lewat repo CZ.NIC |

BIRD 2 (2.0.7+) dan BIRD 3 sama-sama jalan, dan terdeteksi otomatis di setiap run. OS yang lebih lama ditolak oleh paket.
</details>

```sh
# 1. BIRD (contoh BIRD 2; untuk BIRD 3 lihat repo CZ.NIC: https://pkg.labs.nic.cz)
sudo apt update && sudo apt install bird2 bgpq4

# 2. boxvector: unduh .deb sesuai arsitektur dari Releases, lalu verifikasi
#    https://github.com/mfr411/boxvector/releases
sha256sum -c SHA256SUMS --ignore-missing
sudo apt install ./boxvector_<versi>_amd64.deb
```

Paket membuat user `boxvector` (grup `bird`), `/etc/boxvector.yml` (dari contoh, tidak pernah ditimpa), dan service web (terpasang, belum dijalankan).

---

## 3. Konfigurasi

Edit `/etc/boxvector.yml` (dipasang dengan semua baris di-comment; buka comment dan sesuaikan):

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
    filter-rpki: true          # tolak RPKI invalid
    filter-irr: false          # true butuh bgpq4 + as-set yang valid per peer
    add-on-import: ["65530:100"]

peers:
  Example Upstream:
    template: upstream
    asn: 65510
    password: "ganti-ini"      # TCP-MD5, harus sama dengan sisi lawan
    ttl-security: true         # GTSM, hanya untuk peer yang terhubung langsung
    neighbors:
      - 203.0.113.1
      - 2001:db8:ffff::1
```

Semua opsi: [docs/configuration.md](docs/configuration.md) (digenerate dari kode).

**Tes dulu, baru apply:**

```sh
sudo -u boxvector boxvector generate -c /etc/boxvector.yml --dry-run   # validasi saja
sudo -u boxvector boxvector generate -c /etc/boxvector.yml             # tulis, validasi, reload
sudo birdc show protocols                                              # sesi harus Established
```

Segarkan data IRR/PeeringDB tiap 6 jam (opsional):

```sh
echo '0 */6 * * * boxvector /usr/bin/boxvector generate -c /etc/boxvector.yml' | sudo tee /etc/cron.d/boxvector
```

---

## 4. Web UI

Paket menyajikan HTTPS di port **6969** dengan sertifikat self-signed (`/etc/boxvector/tls/web.crt`, `web.key`). Ganti keduanya dengan sertifikat sendiri untuk produksi.

```sh
# buat user admin (password minimal 10 karakter), lalu jalankan
sudo -u boxvector boxvector web passwd admin --auth-file /etc/boxvector/web.users
sudo systemctl enable --now boxvector-web
```

Buka `https://<host>:6969` lalu login.

> Login memakai user di `/etc/boxvector/web.users` (bcrypt). Host hasil upgrade dari build lama yang belum punya user di file itu tetap memakai login akun server sampai kamu membuat user (file drop-in menjelaskan cara pindahnya).

**Batasi siapa yang bisa akses** (wajib untuk host yang terjangkau dari internet). Installer menanyakan IP/CIDR yang diizinkan; kalau tidak diisi, UI hanya listen di `127.0.0.1`. Untuk mengubah nanti, edit `/etc/boxvector/web.env` lalu restart:

```sh
# /etc/boxvector/web.env
WEB_LISTEN=0.0.0.0:6969
WEB_ARGS=--allow-from 192.0.2.0/24,2001:db8::/48
```
```sh
sudo systemctl restart boxvector-web
```

Cara akses lain: bind ke loopback dengan `--listen 127.0.0.1:8180` lalu pakai SSH tunnel, atau pasang nginx di depan ([contoh](packaging/examples/nginx-boxvector.conf), jalankan UI dengan `--trust-proxy`).

| Halaman | Fungsi |
|---|---|
| **Peers** | sesi live dari BIRD, tambah/ubah peer, rule filter per peer |
| **Config** | edit YAML, *Review* menampilkan diff + dry-run sebelum *Apply*, restore snapshot |
| **Routes** | cari IP, prefix, atau origin AS |
| **Interfaces** | alamat, VLAN, bridge. Perubahan berisiko di-rollback setelah 60 detik kalau tidak dikonfirmasi |
| **Traffic**, **VPP** | rate per interface, status dan pengaturan VPP |
| **Terminal** | shell root di browser (butuh password root/sudo, lihat di bawah) |
| **Logs** | siapa menerapkan apa dan kapan |

Setiap apply mengambil snapshot dulu (50 terakhir disimpan). Kalau generate atau validasi gagal, config sebelumnya kembali dan BIRD tidak tersentuh.

---

## 5. Checklist keamanan

Centang semua sebelum box tersambung ke internet.

- [ ] **Batasi web UI:** `--allow-from <CIDR NOC kamu>` atau loopback + SSH tunnel. Jangan biarkan `:6969` terbuka ke seluruh dunia.
- [ ] **Sertifikat TLS sendiri** di `/etc/boxvector/tls/` (bawaannya self-signed).
- [ ] **Password web yang kuat**, satu user per operator (`boxvector web passwd <nama>`).
- [ ] **Firewall BGP (TCP 179)** hanya untuk neighbor. Contoh nftables:

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
- [ ] **Per peer:** isi `password` (MD5), `ttl-security: true` untuk peer langsung, pertahankan `import-limit4/6` atau `auto-import-limits: true`.
- [ ] **Filter aktif:** `filter-rpki: true` (default), `filter-irr: true` jika peer punya as-set, bogon tetap aktif.
- [ ] **Terminal:** matikan kalau tidak dipakai: `systemctl disable --now boxvector-term`.
- [ ] **Verifikasi unduhan:** cek `SHA256SUMS` dari halaman Releases sebelum install `.deb`.

<details>
<summary>Yang sudah dilakukan boxvector untuk kamu</summary>

- Password disimpan sebagai hash bcrypt di `/etc/boxvector/web.users` (mode 600, service menolak start kalau tidak).
- Cookie HttpOnly + SameSite=Strict (Secure lewat HTTPS), idle 60 menit / maksimal 12 jam. Token CSRF di setiap form.
- Lockout login per IP **dan** per username. CSP ketat, tanpa inline script, HSTS lewat TLS, tidak terindeks mesin pencari.
- Service web berjalan sebagai `boxvector` dalam sandbox systemd (tanpa akses shadow, sistem read-only, batas memori/CPU).
- **Terminal root:** helper kecil berjalan sebagai root dan membuka shell. Helper memeriksa password akun sendiri (hanya root atau anggota sudo/wheel/admin) dan mengunci setelah 5 kegagalan, jadi proses web yang dibobol saja tidak cukup untuk jadi root. Setiap buka/tutup tercatat. Sesi idle ditutup setelah 15 menit, maksimal 4 sekaligus. Hanya lewat HTTPS atau localhost.
- HTTP polos di alamat non-loopback ditolak kecuali dengan `--allow-public`.
</details>

**Menemukan celah keamanan?** Laporkan secara privat lewat GitHub Security Advisories di repositori ini, bukan lewat issue publik.

---

## 6. BIRD 2 atau BIRD 3

`boxvector.yml` sama, tidak ada yang perlu diubah.

| | BIRD 2 | BIRD 3 |
|---|---|---|
| Didukung | 2.0.7+ | 3.0+ |
| `bird-threads` | diabaikan (warning) | `threads N;` ditulis |
| Dites di CI | 2.0.12, 2.20.0 | 3.1.9, 3.4.0 |

<details>
<summary>Pindah dari BIRD 2 ke BIRD 3 (jendela maintenance: sesi akan restart)</summary>

```sh
sudo tar czf /root/bird2-backup-$(date +%F).tgz /etc/bird /etc/boxvector   # 1. backup
# 2. tambah repo BIRD 3 di Debian 12 / Ubuntu (https://pkg.labs.nic.cz)
sudo apt install bird3                                                      # 3. menggantikan bird2
sudo dpkg-reconfigure boxvector                                             # 4. pulihkan akses /etc/bird
sudo -u boxvector boxvector generate -c /etc/boxvector.yml                  # 5. generate ulang
sudo systemctl restart bird && sudo birdc show protocols
```

Kembali ke BIRD 2: `sudo apt install bird2 && sudo dpkg-reconfigure boxvector`, lalu `generate` dan restart `bird`, atau pulihkan backup.
</details>

---

## 7. Upgrade dan hapus

```sh
sudo apt install ./boxvector_<versi-baru>_amd64.deb    # config tetap, service di-restart
sudo apt remove boxvector                              # menyimpan /etc/boxvector.yml dan /etc/boxvector/
sudo apt purge boxvector                               # juga menghapus config, snapshot, user
```

File hasil generate di `/etc/bird` tidak pernah dihapus oleh paket.

---

## 8. Troubleshooting

| Gejala | Solusi |
|---|---|
| `unsupported OS` saat install | Butuh Debian 12+ / Ubuntu 22.04+. Upgrade OS dulu. |
| `dial unix /run/bird/bird.ctl: permission denied` | `sudo adduser boxvector bird`, restart `bird` dan `boxvector-web` |
| `open /etc/bird/...: permission denied` | `sudo chgrp bird /etc/bird && sudo chmod 2775 /etc/bird` |
| Web UI tidak mau start | `journalctl -u boxvector-web -e`. Biasanya `web.users` hilang atau mode salah (harus 600) |
| Terkunci dari web UI | tunggu 10 menit, atau reset dengan `boxvector web passwd <user> --auth-file /etc/boxvector/web.users` |
| `403 forbidden` di halaman login | IP kamu tidak ada di `--allow-from` |
| `BIRD version ... older than 2.0.7` | install `bird2`/`bird3` dari OS atau repo CZ.NIC |
| Error validasi saat apply | halaman menampilkan error BIRD lengkap dengan file dan baris. Tidak ada yang diterapkan |

---

## CLI

Referensi lengkap: [docs/cli.md](docs/cli.md). Daftar perintah ada di [README English](README.md#cli).

---

## Build dari source

Butuh Go (versi di `go.mod`) dan `dpkg-deb`.

```sh
go build -o boxvector .
packaging/deb/build.sh 1.0.0 amd64 dist      # dist/boxvector_1.0.0_amd64.deb
```

`docs/configuration.md`, `docs/cli.md`, dan blok CLI di README digenerate oleh `docs/generate.sh` (`make docs`) dan dijaga sinkron oleh CI. Push tag `v*` membangun paket amd64 dan arm64 lalu melampirkannya ke GitHub Release. CI juga memeriksa setiap config di `tests/` terhadap BIRD 2.0.12, 2.20.0, 3.1.9, dan 3.4.0.

## Kredit

Dibangun di atas karya open-source pihak lain: **[Pathvector](https://github.com/natesales/pathvector)** oleh Nate Sales dan kontributor (model config, template, filter IRR/RPKI/PeeringDB, optimizer), daemon routing **[BIRD](https://bird.nic.cz)** oleh CZ.NIC Labs, **[bgpq4](https://github.com/bgp/bgpq4)**, dan **[PeeringDB](https://www.peeringdb.com)**.

boxvector didukung oleh [nexcolo](https://www.nexcolo.com/).

## Lisensi

MIT, lihat [LICENSE](LICENSE). Pemberitahuan hak cipta Pathvector asli tetap dipertahankan sesuai lisensi.
