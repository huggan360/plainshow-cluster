# Plainshow Cluster

Plainshow Cluster links Linux computers into private networks, gives each
network a collaborative code workspace, and runs project commands across its
machines with Ray.

This is alpha software. Keep backups of important projects while testing it.

## Install the alpha

The `v0.1.1-alpha.1` release provides one complete archive for each supported
architecture. Check yours with `uname -m`:

- `x86_64` → download `plainshow-cluster-linux-amd64.tar.gz`
- `aarch64` → download `plainshow-cluster-linux-arm64.tar.gz`

Extract it, enter the new directory, then run:

```sh
sha256sum --ignore-missing -c checksums.txt
chmod +x install.sh pscluster-linux-* plainshow-cluster-desktop-linux-*
sudo ./install.sh node ./pscluster-linux-* ./plainshow-cluster-desktop-linux-*
```

The installer initializes the node, starts it at boot, adds **Plainshow
Cluster** to the application menu, and installs the required Git, Tailscale,
GTK/WebKit, Python and pinned Ray runtime. It supports:

| Linux family | Package manager |
|---|---|
| Arch, Manjaro, EndeavourOS | `pacman` |
| Debian, Ubuntu, Mint, Pop!_OS, Raspberry Pi OS | `apt` |
| Fedora, RHEL, CentOS, Rocky, AlmaLinux | `dnf` |
| openSUSE Leap and Tumbleweed | `zypper` |

GPU drivers, CUDA, PyTorch and project-specific Python packages are not
installed globally because the correct versions depend on each machine and
project.

You do not need a Tailscale account. After you sign in to Plainshow Cluster,
the account service obtains a one-time key and connects the local Tailscale
client to Plainshow's Headscale service at `tailnet.plainshow.se`.

### Arch package

The release also contains a native package:

```sh
sha256sum -c arch-checksums.txt
sudo pacman -U ./plainshow-cluster-*.pkg.tar.zst
```

`pacman -S plainshow-cluster` will become possible after the package is placed
in a signed public repository. For this alpha, use `pacman -U`.

### Snap

Each GitHub release contains classic Snap packages for amd64 and arm64. Install
the file matching your architecture:

```sh
sudo snap install --dangerous --classic ./plainshow-cluster_0.1.1-alpha.1_amd64.snap
```

The host must already have Tailscale installed and `tailscaled` running. A Snap
cannot reliably install or enable that host-level VPN daemon, so the portable
installer above is the recommended fully automatic option. Once the package is
accepted into the Snap Store, the alpha can instead be installed with:

```sh
sudo snap install plainshow-cluster --classic --edge
```

The Snap is refreshed atomically by snapd and does not use Plainshow Cluster's
built-in binary updater. System-node CLI commands use the snap's namespaced
command and require root, for example:

```sh
sudo plainshow-cluster.pscluster status
```

## First use

Open **Plainshow Cluster** from your application menu. The native app discovers
the node through its runtime descriptor; there is no fixed localhost port to
open or configure.

1. Create or sign in to your Plainshow account.
2. Create a network, or paste a single-use invitation from its owner.
3. Open **Ray** on the home page and start the network's first Ray head. Other
   online machines in that selected network attach automatically.
4. Create or clone a project. The Projects view supports folders, file uploads,
   downloads, rename/delete, syntax highlighting, live collaborative editing,
   Git and GitHub.
5. Enter a command such as `python main.py` in the project's Run panel. The
   project is uploaded through Ray's job service and may execute on any suitable
   machine in that Ray cluster.

One installation can join several networks, but its one local Ray process
serves the currently selected network. Switching networks moves that process
to the selected network automatically.

## Services

- `clusteradmin.plainshow.se` is the global account/key authority. It stores
  accounts, network membership and roles, recovery keys, controller records,
  aggregate statistics, and short-lived Headscale enrollment data in SQLite.
  It never stores projects or relays Cowork traffic.
- `tailnet.plainshow.se` is the self-hosted Headscale control plane. Encrypted
  machine traffic normally travels directly between devices.
- `cluster.plainshow.se` is the separate optional Cowork controller. Its owner
  signs in and selects which of their networks it relays. Only accounts in
  those networks can access it.

## Update

The built-in updater checks GitHub releases, selects the binary for the current
architecture, verifies its SHA-256 checksum, preserves the previous binary,
and swaps the new node in atomically. Project files, keys, configuration and
SQLite data are not replaced.

Alpha builds are prereleases, so select the beta channel once:

```sh
sudo pscluster config set update.channel beta
sudo pscluster update check
sudo pscluster update apply
```

The same controls are in **Settings → Updates**. Checks run every six hours;
installation is manual unless `update.automatic` is enabled. Re-running a
newer release's installer is also safe and updates the native desktop binary
and dependencies while preserving node data.

## Useful commands

```sh
sudo pscluster status
sudo pscluster network list
sudo pscluster invite
sudo pscluster run --project my-project python main.py
sudo pscluster update status
journalctl -u plainshow-cluster -f
```

All node-owned state lives below `/opt/plainshow-cluster`.

## Build from source

Development needs Go 1.25 and Node.js. The native desktop also needs GTK 3 and
WebKit2GTK 4.1 development packages.

```sh
git clone https://github.com/huggan360/plainshow-cluster.git
cd plainshow-cluster
make check
make build
make desktop
sudo ./install.sh node ./pscluster ./plainshow-cluster-desktop
```

`make dist` builds the portable CLI/admin assets. On Arch with `base-devel`,
`make arch-package` builds the native pacman package. Maintainer handover notes
are in `CODEX.md`.
