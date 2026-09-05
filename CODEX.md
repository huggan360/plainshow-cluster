# Plainshow Cluster handover

Last updated: 2026-09-05. Target release: `v0.1.1-alpha.5`.

## Current state

This repository contains the Linux node and CLI, native desktop, embedded web
workspace, global account/key service, installers, and release automation for
**Plainshow Cluster**. The separate Cowork relay/controller is the sibling
repository `../cluster-controller` at commit `2920a05`; never merge that relay
back into the global account service.

`v0.1.1-alpha.1` is already tagged at `b584647`. Main then received AMD/Intel
telemetry commits `d0d477e` and `1d09b2b`. Alpha.2 at `f62e8e3` proved the
desktop builds but its packaging workflow failed before publishing any assets.
Do not move any published tag. Alpha.3 includes the multi-vendor functional
fixes and removes Snap distribution by explicit product decision. Alpha.4
corrects native desktop discovery for packaged system nodes:

- The Wails/GTK/WebKit application is named exactly **Plainshow Cluster** and
  uses the established Plainshow icon. Its bootstrap page now retries runtime
  descriptor discovery continuously. Opening the app during daemon startup no
  longer freezes forever on a false “node offline” result.
- A desktop-menu launch no longer passes the unprivileged user's default root
  in place of `/opt/plainshow-cluster`. Discovery also keeps the packaged
  system root as a fallback, so a running systemd node opens directly. The
  waiting screen is deliberately limited to the branded heading, spinner, and
  `systemctl start` command.
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
  arm64, plus an x86_64 Arch package. Snap packaging and publication have been
  removed; do not restore them unless that product decision changes.

## Verification and release

Run on this Pi with Go added to `PATH`:

```sh
PATH=/usr/local/go/bin:$PATH make check
PATH=/usr/local/go/bin:$PATH make smoke
PATH=/usr/local/go/bin:$PATH make VERSION=v0.1.1-alpha.5 dist
```

All three passed after the application changes; the smoke suite reports 73
passed and 0 failed, and the distribution build produced both architectures.

The Pi lacks GTK/WebKit development headers and Arch `makepkg`.
Do not install them on this production server merely for validation. Native
desktop and Arch package builds run on native GitHub runners.

Release without rewriting an earlier alpha:

```sh
git add -A
git commit -m "Make projects and live updates network-aware"
git push origin main
git tag -a v0.1.1-alpha.5 -m "Plainshow Cluster v0.1.1-alpha.5"
git push origin v0.1.1-alpha.5
```

The tag triggers `.github/workflows/release.yml`. Confirm both native desktop
builds, the release-assets job, and the Arch package job. Expected downloads
include complete amd64/arm64 tarballs, raw node/admin/desktop binaries,
checksums, installer and Arch package.

## Architecture map

- `cmd/pscluster`: node service and CLI.
- `cmd/plainshow-cluster-desktop`: native app and descriptor discovery.
- `cmd/pscluster-admin`, `internal/accountserver`, `adminweb`: global authority.
- `internal/api`: local API, mesh, Ray reconciliation, Cowork, projects and Git.
- `internal/ray`: managed Ray CLI/dashboard/job protocol.
- `internal/sysinfo`: CPU, memory, disk and multi-vendor accelerator discovery.
- `internal/collab`, `internal/store/collab_ops.go`: OT and durable operations.
- `web`: dependency-free UI embedded into the node.
- `install.sh`, `packaging/arch`, `packaging/desktop`: distribution.
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
  a portable installer also refreshes the desktop and dependencies while
  preserving data.
- CUDA, ROCm, oneAPI/OpenCL, PyTorch and other project-specific GPU frameworks
  are not installed globally. Every target machine needs the vendor runtime
  required by its workload. Mixed-vendor distributed work is possible when the
  framework/backend supports it; Plainshow cannot make incompatible frameworks
  interoperable by itself.

## After alpha

Test two real Arch machines, then Debian/Ubuntu: login, join one network, start
Ray, observe automatic attachment, run CPU and each available GPU workload,
stop, reboot and repeat. Add generated Headscale ACLs and automatic rotation if
future networks must be mutually hostile rather than trusted.

`CLAUDE.md` is historical planning context and includes stale pre-Ray details.
Use this file and the current code as the authoritative handover.

## Handover — 2026-09-05

Six staged changes landed this session. `PLAN.md` is the authority on all of
them and says what is verified and what is not. Read it first.

**Stages 0–5 are done and verified by running them** against a real account
server and two or three real nodes: sessions survive closing the desktop,
networks follow the account onto a new machine, devices can be moved and signed
out from elsewhere, invitations go to a person rather than a code, a project
moves between networks with its files, and Team matches the console.

**Stage 6 is code complete and has never run against two machines.** It is the
one thing to pick up. `PLAN.md` lists exactly what is left; the first item is
two lines of Apache config without which it fails silently in production,
because the heartbeat is still there to cover it.

### Three traps this session found, all now in CLAUDE.md

- A new field inside `Memberships` **cannot be defaulted**. `config.Load` merges
  onto `Defaults()`, so a missing top-level key keeps its default — but a slice
  element is built from zero. `Config.Version` exists for this, and the version
  must be read from the raw document *before* the merge or an old file reports
  as current.
- **Project membership is keyed by GitHub login**, or the node name when no
  GitHub is connected, never the Plainshow account username. Checking one guess
  refused the owner their own project.
- **`systemctl is-enabled` reports through its exit code.** Read the output.

### What is still unproven, in the order it will bite

1. **Ray has never run against a live cluster.** Unchanged from before.
2. **Cross-machine project transfer** (`/mesh/v1/projects`) is covered by tests
   only. Peer gossip needs tailnet addresses the development Pi does not have,
   so the archive round trip was tested in-process rather than over the wire.
3. **Stage 6**, as above.
4. **Two real CUDA machines.** Still the highest-risk gate, still untouched.

### Do not undo

- The heartbeat stays even once the socket works. It carries telemetry the
  socket does not, and it is the floor when the socket is half-open.
- Nothing sends *content* over `/api/events`. It wakes a device; the device
  re-asks. That is what keeps the management plane from becoming a relay.
- Removing a device is bookkeeping, not revocation, and the interface says so.
  A machine with a valid credential re-registers. Signing it out is the thing
  that actually cuts it off.

## Handover addendum — Stage 6 and account-wide workspaces

The repository side of Stage 6 is complete. The production Apache reference
config upgrades `/api/events`, `Client.Watch` now distinguishes a connection
that opened and later closed from a handshake failure, and
`watch_test.go` proves that wake-ups arrive again after reconnecting. The
heartbeat remains unchanged.

Production is **not** complete: a read-only WebSocket probe of
`https://clusteradmin.plainshow.se/api/events` returned 404 on 2026-09-05. No
live Apache file, process or system service was changed in this session. Deploy
the updated `pscluster-admin` and `deploy/clusteradmin.plainshow.se.conf`, run
`apache2ctl configtest`, reload Apache, then perform PLAN.md's two-real-node
disconnect/reconnect test.

The UI no longer treats the device's Ray assignment as an account workspace:

- Networks, devices and projects are account-wide and the top bar has no
  network selector/status.
- Creating or cloning a project requires a network; links and all project APIs
  use stable project ids, so identical names in different networks work.
- Project Run resolves `project.network_id` and scopes submit, status, logs and
  stop to that Ray head. Jobs aggregates all reachable network heads.
- Send/fetch/readiness use the project's network, and the browser maintains a
  Cowork controller socket per network instead of only the old active one.
- A single physical machine still has one internal compute assignment and one
  raylet, preventing the same CPU/GPU capacity from being advertised twice.
  Starting Ray for another network moves only that contribution; it never hides
  account networks or projects.

Verification on the Pi after these changes:

```sh
PATH=/usr/local/go/bin:$PATH make check       # pass
PATH=/usr/local/go/bin:$PATH make smoke       # 73 passed, 0 failed
PATH=/usr/local/go/bin:$PATH make VERSION=v0.1.1-alpha.5 dist  # amd64 + arm64 pass
```

The native GTK desktop and Arch package still belong to release CI; do not
install their build dependencies on this production Pi. The intended next tag
is `v0.1.1-alpha.5`; never move alpha.1 through alpha.4.
