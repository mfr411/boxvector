# VPP with boxvector

VPP is the optional fast data plane. BIRD keeps running in the kernel and talks to VPP through `linux-cp`
(a tap per VPP interface), so `ip route`, `birdc` and the web UI keep working. This page is only about VPP.
For everything else see the main [README](../README.md).

## Kernel mode vs VPP mode

| | Kernel (`/etc/network/interfaces`) | VPP |
|---|---|---|
| Who forwards packets | Linux kernel | VPP, in user space (DPDK) |
| NIC driver | the normal one (`ixgbe`, `i40e`, ...) | `vfio-pci`, owned by VPP |
| Visible in `ip link` | yes | no, only the linux-cp tap |
| Configured with | ifupdown2 / web UI "Interfaces" (legacy mode) | web UI "Interfaces" (VPP mode, vppcfg) |

A NIC belongs to the kernel **or** to VPP, never both. Keep at least one NIC (the management one, usually the one
with the default route) on the kernel, otherwise you lose SSH.

## Install

```
curl -fsSL https://raw.githubusercontent.com/mfr411/boxvector/main/install.sh -o install.sh
sudo bash install.sh            # interactive: pick "1) all"
```

With `all`, the installer lists the Ethernet cards it finds:

```
  #   PCI           IFACE     DRIVER       SPEED  NUMA  STATE
  1   0000:03:00.0  eno1      ixgbe        10G    0     mgmt
  2   0000:81:00.0  ens2f0    i40e         25G    1     has-ip
  3   0000:81:00.1  ens2f1    i40e         25G    1     free
```

| STATE | Meaning |
|---|---|
| `mgmt` | carries the default route. Never given to VPP (the installer refuses it) |
| `has-ip` | kernel interface with an address configured |
| `free` | kernel interface without an address |
| `dpdk-ready` | already bound to `vfio-pci` |

Choose the numbers for VPP (`2 3`, or empty for none and add them later). The installer then prints a summary:
packages, CPU plan, files it will change, which NICs it binds, which NIC it will not touch, and the rollback command.
Nothing is changed until you answer `y` at `Proceed? [y/N]`.

A NIC that is still a kernel interface is bound to `vfio-pci` with `driverctl` (persistent across reboots). That removes
the interface and its addresses and routes from Linux.

Options for scripted installs:

| Option | Effect |
|---|---|
| `--list-nics` | print the table above and exit |
| `--vpp-nic PCI[@GBPS]` | choose a NIC without the menu (repeatable). The NIC must already be on `vfio-pci`, or use the menu |
| `--vpp-smt` / `--vpp-workers N` | also use hyper-threads / cap the number of workers |
| `--estimate` | print a capacity estimate and exit, changes nothing |
| `--dry-run` | show what would be done |
| `--yes` | no questions (needs `--mode`) |

## Requirements

- IOMMU on (`intel_iommu=on` or `amd_iommu=on` on the kernel command line, VT-d / AMD-Vi enabled in the BIOS).
  Without it the installer stops before changing anything.
- NICs supported by DPDK. Virtual NICs (e1000e, virtio) work for staging but give no real performance.
- At least 4 GB RAM; 2 GB is only enough for a default route or partial tables.
- Debian 12+ / Ubuntu 22.04+, amd64 (fd.io packages).

## CPU layout

The installer reads `lscpu` and keeps one physical core for the OS, BIRD, boxvector and the VPP main thread.
All other physical cores on the NUMA node of the NIC become VPP workers, one RX/TX queue per worker for each NIC.
Hyper-thread siblings are not used unless `--vpp-smt` is given.

## After install

```
vppctl show interface
vppctl show lcp
```

Configure addresses, VLANs and bridges on the web UI **Interfaces** page. In VPP mode every change is checked with
`vppcfg`, applied, and rolled back after 60 seconds unless you confirm it.

## Undo

Give a NIC back to the kernel:

```
sudo driverctl unset-override 0000:81:00.0
sudo reboot            # or: echo 0000:81:00.0 > /sys/bus/pci/drivers_probe
```

then add the interface again in `/etc/network/interfaces` (or the web UI in legacy mode).
