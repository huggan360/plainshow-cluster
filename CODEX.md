# Plainshow Cluster handover

Last updated: 2026-09-05. Target release: `v0.1.1-alpha.3`.

## Current state

This repository contains the Linux node and CLI, native desktop, embedded web
workspace, global account/key service, installers, and release automation for
**Plainshow Cluster**. The separate Cowork relay/controller is the sibling
repository `../cluster-controller` at commit `2920a05`; never merge that relay
back into the global account service.

`v0.1.1-alpha.1` is already tagged at `b584647`. Main then received AMD/Intel
telemetry commits `d0d477e` and `1d09b2b`. Alpha.2 at `f62e8e3` proved the
desktop builds but exposed a missing `python3.12-venv` Snap dependency before
publishing any assets. Do not move either tag. Alpha.3 includes the fix:

- The Wails/GTK/WebKit application is named exactly **Plainshow Cluster** and
  uses the established Plainshow icon. Its bootstrap page now retries runtime
  descriptor discovery continuously. Opening the app during daemon startup no
  longer freezes forever on a false “node offline” result.
- Nodes use an OS-selected ephemeral loopback port and publish it in
  `<root>/run/node.json` mode 0644. The desktop and CLI discover that file;
  there is deliberately no localhost:9999 scan.
- Host telemetry detects NVIDIA with `nvidia-smi`, AMD with ROCm or Linux DRM
  sysfs, and Intel with DRM sysfs. Integrated Intel devices are valid compute
  resources when the project's Intel runtime is installed.
- Ray receives every enabled GPU explicitly through `--num-gpus`, including
  AMD and Intel devices that default detection can miss. All vendors share the
  normal `GPU` pool. Custom resources `plainshow_gpu_nvidia`,
  `plainshow_gpu_amd`, and `plainshow_gpu_intel` allow vendor-specific jobs.
  With a zero CPU cap Ray uses every logical core. Only online peers contribute
  capacity to UI totals; Ray itself only schedules on live raylets.
- Ray 2.58.0 is the distributed runtime. One device starts a head for a logical
  network, signed peer gossip converges the announcement/tombstone, and other
  eligible online devices attach automatically. Reboot and active-network
  changes are reconciled without creating a second scheduler.
- Project Run and `pscluster run --project NAME ...` use Ray Jobs, upload the
  project working directory, stream logs, list jobs and stop jobs. The project
  command is a Ray driver; distributed code uses ordinary Ray tasks/actors.
- Networks are first-class workspaces with Connected devices, Settings and My
  machine tabs. Projects belong to one network. Project pages provide Overview,
  the current-branch editor, Run, Git, Team and Settings. There is one folder
  and one working copy, not separate production/workspace trees.
- The Cowork IDE supports create/clone, nested files and folders, multi-file
  upload/drop, download, rename/delete, highlighted editing, line numbers,
  shortcuts, Git/GitHub, live OT edits, presence and durable offline outboxes.
- The UI follows the production plainshow.se console, bundles Boxicons 2.1.4
  and the official Plainshow mark, and provides the requested home statistics,
  activity feed, network cards, projects and machine resource controls.
- `clusteradmin.plainshow.se` remains account/key management only. Its SQLite
  registry stores accounts, network membership/roles, recovery keys,
  controller registration and aggregate statistics. It never stores projects
  and never relays Cowork traffic.
- Plainshow login obtains one-time enrollment and connects the host Tailscale
  client to the self-hosted Headscale plane at `tailnet.plainshow.se`; end users
  do not need Tailscale accounts.
- The portable installer covers pacman, apt, dnf and zypper and installs Git,
  Tailscale, Python/venv, GTK/WebKit and pinned Ray. Release CI builds static
  node/admin binaries, native desktop binaries and full tarballs for amd64 and
  arm64, an x86_64 Arch package, and classic Snaps for amd64 and arm64.
- The classic Snap bundles the desktop, node, Git, GTK/WebKit, Python and Ray,
  keeps its node in `$SNAP_COMMON`, and delegates updates to snapd. Tailscale is
  still a host service. Store distribution requires separate name registration
  and classic-confinement approval.

## Verification and release

Run on this Pi with Go added to `PATH`:

```sh
PATH=/usr/local/go/bin:$PATH make check
PATH=/usr/local/go/bin:$PATH make smoke
PATH=/usr/local/go/bin:$PATH make VERSION=v0.1.1-alpha.3 dist
```

All three passed after the application changes; the smoke suite reports 71
passed and 0 failed, and the distribution build produced both architectures.

The Pi lacks GTK/WebKit development headers, Snapcraft/LXD and Arch `makepkg`.
Do not install them on this production server merely for validation. Native
desktop, Snap and Arch package builds run on native GitHub runners.

Release without rewriting alpha.1 or alpha.2:

```sh
git add -A
git commit -m "Fix desktop discovery and pool every GPU vendor"
git push origin main
git tag -a v0.1.1-alpha.3 -m "Plainshow Cluster v0.1.1-alpha.3"
git push origin v0.1.1-alpha.3
```

The tag triggers `.github/workflows/release.yml`. Confirm both native desktop
builds, both Snap builds, the release-assets job, and the Arch package job.
Expected downloads include complete amd64/arm64 tarballs, matching `.snap`
files, raw node/admin/desktop binaries, checksums, installer and Arch package.

## Architecture map

- `cmd/pscluster`: node service and CLI.
- `cmd/plainshow-cluster-desktop`: native app and descriptor discovery.
- `cmd/pscluster-admin`, `internal/accountserver`, `adminweb`: global authority.
- `internal/api`: local API, mesh, Ray reconciliation, Cowork, projects and Git.
- `internal/ray`: managed Ray CLI/dashboard/job protocol.
- `internal/sysinfo`: CPU, memory, disk and multi-vendor accelerator discovery.
- `internal/collab`, `internal/store/collab_ops.go`: OT and durable operations.
- `web`: dependency-free UI embedded into the node.
- `install.sh`, `packaging/arch`, `packaging/desktop`, `snap`: distribution.
- `deploy`: account and Headscale reference configuration. Do not alter this
  Pi's services, Apache, firewall or Headscale merely to build a release.

## Operational boundaries

- The Raspberry Pi can host an ordinary node workload, the global account/key
  service and Headscale. The global service stores no code, commands, datasets,
  artifacts or job logs and is not a collaboration relay.
- Headscale currently provides shared private transport. Logical Plainshow
  network membership is enforced by signed mesh/controller authorization, but
  generated per-network Headscale ACLs are not implemented. Treat the alpha as
  trusted-team software.
- Removing an account centrally revokes application/controller access after
  sync, but does not erase a management key already copied to that device. Full
  eviction needs network key rotation and distribution to retained devices.
- The old local job supervisor remains for compatibility tests. New features
  and the shipped UI/CLI use Ray Jobs.
- The built-in updater atomically replaces the static node binary. Re-running
  a portable installer also refreshes the desktop/dependencies while preserving
  data. Snap installs disable that updater because snapd owns refresh/rollback.
- CUDA, ROCm, oneAPI/OpenCL, PyTorch and other project-specific GPU frameworks
  are not installed globally. Every target machine needs the vendor runtime
  required by its workload. Mixed-vendor distributed work is possible when the
  framework/backend supports it; Plainshow cannot make incompatible frameworks
  interoperable by itself.

## After alpha

Test two real Arch machines, then Debian/Ubuntu: login, join one network, start
Ray, observe automatic attachment, run CPU and each available GPU workload,
stop, reboot and repeat. Register `plainshow-cluster` in the Snap Store, request
classic-confinement approval, then upload both architectures to edge using
`snap/README.md`. Add generated Headscale ACLs and automatic rotation if future
networks must be mutually hostile rather than trusted.

`CLAUDE.md` is historical planning context and includes stale pre-Ray details.
Use this file and the current code as the authoritative handover.
