# Plainshow Cluster — Alpha 0.1.3

Version: `v0.1.3-alpha.1`. This remains an alpha: keep backups of important work.

## What changed

- Independent projects: choose the active compute network on Networks for runs,
  tests and presets. Existing project folders remain in place.
- A Code tab with corrected line numbers, visual merge-conflict resolution,
  a full create-project page and branch-specific working folders.
- Account-held project metadata and GitHub credentials, invitations by username,
  and explicit project downloads with progress.
- Redesigned device cards, restored icons, updated typography, profile settings,
  an availability switch and an interactive-looking sample-data demo.
- CPU/GPU software diagnostics report CUDA, ROCm and Intel tooling and try an
  available GPU without making missing GPU software fail network connectivity.
- Jobs shows waiting, running and paginated history for the selected network.
  Small job summaries persist on clusteradmin; member devices receive change
  notifications, with polling as a fallback. Unsent summaries retry after outages.
- Deleting a network removes its job history and pending uploads, not project
  files or Git history. Saved unfinished jobs show their last known state.
- Centered spinners, fixed completed-job timers and cleaned-up log polling.

Shared job history requires updating **both the account server and the nodes**.
The new SQLite tables are added on startup. Logs, code and datasets are not
archived on clusteradmin. Publishing this release does not deploy server services.

## Install or update

This repository is private: downloads and built-in updates require GitHub
access to it. Connect an authorized GitHub account for the built-in updater.

Arch: download `plainshow-cluster-0.1.3.alpha.1-1-x86_64.pkg.tar.zst` and
`arch-checksums.txt`, close Plainshow Cluster, then run:

```sh
sha256sum -c arch-checksums.txt
sudo pacman -U ./plainshow-cluster-0.1.3.alpha.1-1-x86_64.pkg.tar.zst
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
GPU drivers and framework on each worker. GPU checks sample one available GPU
in Ray's Python environment, not every card or project environment. Native
rendering and real mixed-hardware training still need testing on users' machines.
