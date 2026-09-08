# Plainshow Cluster

Plainshow Cluster links Linux computers into private networks, gives each
network a collaborative code workspace, and runs project commands across its
machines with Ray.

This is alpha software. Keep backups of important projects while testing it.

## Install the alpha

The [Alpha 0.1.2 release](https://github.com/huggan360/plainshow-cluster/releases/tag/v0.1.2-alpha.1) provides one complete archive for each supported
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

Plainshow detects NVIDIA, AMD and Intel graphics through vendor tools or the
Linux DRM device tree. By default Ray receives every CPU core and every enabled
GPU from each online machine; per-machine and per-network caps can reduce that
pool. GPU projects still need the matching CUDA, ROCm or Intel oneAPI/OpenCL
runtime on the machine where a task lands. Advanced Ray tasks can constrain a
vendor with the custom resources `plainshow_gpu_nvidia`, `plainshow_gpu_amd`,
or `plainshow_gpu_intel` in addition to `num_gpus`.

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

## First use

Open **Plainshow Cluster** from your application menu. The native app discovers
the node through its runtime descriptor; there is no fixed localhost port to
open or configure.

1. Create or sign in to your Plainshow account.
2. Create a network, or paste a single-use invitation from its owner.
3. Open the network under **Networks** and press **Start** in its Ray card.
   Repeat on each machine that should contribute compute; the first becomes the
   head and the others attach automatically.
4. Create or clone a project. The Projects view supports folders, file uploads,
   downloads, rename/delete, syntax highlighting, live collaborative editing,
   Git and GitHub.
5. Open **Test → Test cluster** to check that every online device executes a
   Ray task. Missing workers appear in red with a next step.
6. Open **Preset**, choose CPU, GPU, mixed or a GPU vendor, then create a Python
   file. Put your code in the marked function and run the file from your IDE,
   the branch editor's **Run** button, or the Ray command printed in the file.

Presets discover live resources at execution and respect Ray's device limits.
Mixed mode uses GPUs on GPU machines and CPUs on CPU-only machines. These are
independent tasks, not a ready-made synchronous training loop across unlike GPUs.
The Test tab checks Ray execution, not your GPU drivers or training framework.

One account can browse, edit and run projects across several networks at once;
there is no global selected network. Every project routes its run to the Ray
head for the network it belongs to. One physical machine still runs one local
Ray process so its CPU and GPUs cannot be advertised twice. Starting Ray for a
different network moves that machine's compute contribution without hiding or
changing any projects.

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

The repository is currently private. Downloads require repository access;
the built-in updater also needs a connected GitHub account/token with that access.

```sh
sudo pscluster config set update.channel beta
sudo pscluster update check
sudo pscluster update apply
```

The same controls are in **Settings → Updates**. Checks run every six hours;
installation is manual unless `update.automatic` is enabled. Re-running a
newer release's installer is also safe and updates the native desktop binary
and dependencies while preserving node data.

On Arch, download the new `.pkg.tar.zst` and run `sudo pacman -U ./plainshow-cluster-*.pkg.tar.zst`
to update both the node and desktop application together. Close the app first,
and keep only the package you want to install in that folder.

## Useful commands

```sh
sudo pscluster status
sudo pscluster network list
sudo pscluster invite
sudo pscluster run --project my-project python main.py
sudo pscluster update status
journalctl -u plainshow-cluster -f
```

Device keys and service state live below `/opt/plainshow-cluster`. Installing
with `sudo` from your desktop account attempts to place projects in:

```text
/home/YOUR_USERNAME/Plainshow/Projects/<network-id>/<repository-name>/
```

The project header shows the exact path. Unlinked projects use their project
name. Files created in the app remain editable by your desktop user.

For an existing installation, after updating to a build with home workspaces:

```sh
sudo systemctl stop plainshow-cluster
sudo pscluster workspace --root /opt/plainshow-cluster --user "$USER"
sudo systemctl start plainshow-cluster
```

The migration retains the old files in `projects.before-home` under the service
root. It refuses existing destination folders instead of merging them. A
built-in binary update preserves this setup but does not choose a desktop user
or migrate your files by itself. Headless installs can keep their current path.

Shared-network devices exchange live hardware and Ray state over signed,
certificate-pinned peer sockets. Clean disconnects update presence immediately;
a silent connection loss takes up to 12 seconds to detect. Older devices fall
back to heartbeat discovery. Project files still move through explicit
send/fetch or Git; live editor operations use the Cowork controller.

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
