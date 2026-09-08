# Plainshow Cluster — Alpha 0.1.2

Version: `v0.1.2-alpha.1`. This remains an alpha: keep backups of important work.

## What changed

- Live, authenticated peer connections update device presence and hardware/Ray
  information. New devices wake discovery without waiting for an account heartbeat.
- Cleaner Devices settings, no device section on Home, and live Project Devices
  readiness. Late page requests no longer replace the page you navigated to.
- Remembered desktop sessions recover missing or expired cookies. Restored
  sessions enforce the same cross-origin protection as normal sessions.
- User-owned projects under `~/Plainshow/Projects/<network-id>/<repository-name>`.
  Existing installations have a safe workspace migration command and retained backup.
- Safer project transfers: staged extraction, rollback on replacement failure,
  and correct file ownership.
- A project **Test** tab checks execution on online Ray workers and reports
  missing workers. A **Preset** tab creates editable CPU, GPU, mixed, NVIDIA,
  AMD or Intel Ray task skeletons without overwriting existing files.
- Presets target the project's network and refuse an unspecified Ray head
  instead of accidentally using an unrelated local cluster.

## Install or update

This repository is private: downloads and built-in updates require GitHub
access to it. Connect an authorized GitHub account for the built-in updater.

Arch: download `plainshow-cluster-0.1.2.alpha.1-1-x86_64.pkg.tar.zst` and
`arch-checksums.txt`, close Plainshow Cluster, then run:

```sh
sha256sum -c arch-checksums.txt
sudo pacman -U ./plainshow-cluster-0.1.2.alpha.1-1-x86_64.pkg.tar.zst
```

Other supported Linux distributions: download the complete `amd64` or `arm64`
archive and follow the short [README](https://github.com/huggan360/plainshow-cluster#install-the-alpha).
The same installer updates an existing installation while preserving its data.

The built-in updater updates the **node binary**, not the native desktop
application. Use the package or complete installer to update both. Select the
`beta` update channel to see alpha releases.

Existing binary-only installations can move their projects into their normal
user's home explicitly:

```sh
sudo systemctl stop plainshow-cluster
sudo pscluster workspace --root /opt/plainshow-cluster --user "$USER"
sudo systemctl start plainshow-cluster
```

## Alpha boundaries

Presence links do not automatically mirror project files: use Git or explicit
send/fetch. A lost connection is detected promptly; a silent power cut takes
up to the socket timeout, not literally zero time.

Presets distribute independent tasks over compatible resources. They are not
a universal synchronized training loop for unlike GPUs. Install the matching
GPU drivers and framework on each worker. The Test tab checks Ray execution,
not GPU framework compatibility. Native rendering and real mixed-hardware
training still need testing on users' machines.
