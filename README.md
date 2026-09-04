# PlainShow Cluster

PlainShow Cluster links Linux computers into one collaborative workspace for AI
and development tasks. Install it on each computer, sign in, create or join a
network, and choose which machines may run work.

> **Alpha software:** use it for testing first and keep copies of important
> projects and datasets. The Headscale account integration described here is
> currently on `main`; it will be included in the next alpha release.

## Install

For the current development build, clone the repository and build both
binaries:

```sh
git clone https://github.com/huggan360/plainshow-cluster.git
cd plainshow-cluster
make build
sudo env PSCLUSTER_ACCOUNT_SERVER=https://clusteradmin.plainshow.se \
  ./install.sh node ./pscluster
```

When the next alpha is published, download these three release files:

- `install.sh`
- `checksums.txt`
- `pscluster-linux-amd64` for a normal 64-bit Intel/AMD computer, or
  `pscluster-linux-arm64` for a 64-bit ARM computer such as a Raspberry Pi

Check your architecture with `uname -m`: `x86_64` means `amd64`, while
`aarch64` means `arm64`. Then run this from the download directory, replacing
the binary name if you downloaded the ARM version:

```sh
chmod +x install.sh pscluster-linux-*
sha256sum --ignore-missing -c checksums.txt
sudo env PSCLUSTER_ACCOUNT_SERVER=https://clusteradmin.plainshow.se \
  ./install.sh node ./pscluster-linux-amd64
```

The installer automatically installs Git, the Tailscale client, terminal
support and Jupyter Server on these Linux families:

| Distribution | Package manager |
|---|---|
| Arch, Manjaro, EndeavourOS | `pacman` |
| Debian, Ubuntu, Mint, Pop!_OS, Raspberry Pi OS | `apt` |
| Fedora, RHEL, CentOS, Rocky, AlmaLinux, Oracle Linux | `dnf` |
| openSUSE Leap and Tumbleweed | `zypper` |

It uses the official Tailscale package repository when the distribution does
not carry the client. You do not need a Tailscale account: signing in to
PlainShow automatically enrols the client with PlainShow's self-hosted
Headscale service at `tailnet.plainshow.se`. GPU drivers, CUDA, PyTorch and
other model runtimes are left to you because their correct versions depend on
the computer and workload.

For another Linux distribution, install `ca-certificates`, Git, Tailscale,
util-linux (`script`) and optionally Jupyter Server yourself, then run:

```sh
sudo env PSCLUSTER_SKIP_DEPENDENCIES=1 \
  PSCLUSTER_ACCOUNT_SERVER=https://clusteradmin.plainshow.se \
  ./install.sh node ./pscluster-linux-amd64
```

The prebuilt binaries support 64-bit x86 and ARM Linux. A system without
systemd can still run `/opt/plainshow-cluster/bin/pscluster serve`; it just will
not get the automatically managed service.

### Native Arch package

The release also contains a `.pkg.tar.zst` package:

```sh
sha256sum -c arch-checksums.txt
sudo pacman -U ./plainshow-cluster-*.pkg.tar.zst
```

`pacman -S plainshow-cluster` will require a public signed PlainShow package
repository. The alpha release starts with `pacman -U`; the package and built-in
updater still provide normal upgrades.

## First start

Open <http://127.0.0.1:9999>. Sign in with your PlainShow account, create a
network, or use a single-use invitation from another member. A node may belong
to several networks, and its owner controls whether it accepts jobs, terminal
sessions or GPU work. The private network connects during that same PlainShow
login and retries automatically if the control server is temporarily offline.

The global service at `clusteradmin.plainshow.se` stores accounts, memberships,
network recovery keys, controller registrations and short-lived private-network
enrolment. It does not relay collaboration, store projects or run tasks. The
Headscale control plane at `tailnet.plainshow.se` coordinates encrypted device
connections; normal task data travels directly between devices whenever the
network allows it. The separate service at `cluster.plainshow.se` supplies
optional live Cowork collaboration for networks selected by its owner. Projects
and jobs remain on the cluster machines.

## Update

PlainShow has a built-in verified updater. For alpha releases, select the beta
channel once:

```sh
sudo pscluster config set update.channel beta
sudo pscluster update check
sudo pscluster update apply
```

You can do the same in **Settings → Updates**: select **Beta**, click **Check
now**, then **Install**. PlainShow downloads the binary for that computer,
verifies its published SHA-256 checksum, keeps the old binary as
`pscluster.previous`, swaps in the new one atomically, and restarts. Your
configuration, projects and database are not replaced.

The node checks every six hours, but installation is manual by default. To let
it install available updates automatically:

```sh
sudo pscluster config set update.automatic true
```

If the GitHub repository is private, connect GitHub in PlainShow first so the
updater can access its releases. You can always download a newer release and
rerun `install.sh`; it preserves the existing node data.

## Useful commands

```sh
sudo pscluster status
sudo pscluster config show
sudo pscluster network list
sudo pscluster invite
sudo pscluster update status
journalctl -u plainshow-cluster -f
```

The installation lives under `/opt/plainshow-cluster`: configuration, keys,
SQLite state, projects, datasets, artifacts, logs and the active binary. The
installer also adds a command link and a systemd service.

## Build from source

Development requires Go 1.24+ and Node.js:

```sh
git clone https://github.com/huggan360/plainshow-cluster.git
cd plainshow-cluster
make check
make build
```

`make dist` builds portable Linux release assets. On Arch with `base-devel`,
`make arch-package` builds the native pacman package. Implementation and
deployment details for the next developer are in `CODEX.md` and `CLAUDE.md`.
