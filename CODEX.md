# Plainshow Cluster handover

Last updated: 2026-09-05. Target release: `v0.1.1-alpha.1`.

## Current state

This repository is the Linux node, native desktop, global account/key service,
web workspace, installers, and release automation for **Plainshow Cluster**.
The separate Cowork controller is the clean sibling repository
`../cluster-controller` at commit `2920a05`; its own `CODEX.md` describes its
deployment. Do not merge its relay back into the global account service.

The migration requested in the latest Claude/user session is implemented for
the `v0.1.1-alpha.1` release:

- Ray 2.58.0 is the distributed runtime. One device starts a head for a logical
  network; signed peer gossip converges the head announcement and tombstones;
  other devices attach automatically. Local state restores Ray after a reboot
  and moves the one local Ray process when the active network changes.
- Project Run and `pscluster run --project NAME ...` submit through Ray Jobs,
  upload the project working directory, stream logs, list jobs, and stop jobs.
  Submission uses `--no-wait` so HTTP requests do not remain open for a run.
- `cmd/plainshow-cluster-desktop` is the real Wails/GTK/WebKit Linux app. Its
  visible title is exactly **Plainshow Cluster** and it uses the established
  Plainshow icon. The static Go node remains independently cross-compilable.
- New nodes bind an OS-selected ephemeral loopback port. The daemon publishes
  `<root>/run/node.json` mode 0644; the CLI and desktop read only that file.
  There is deliberately no localhost:9999 compatibility scan.
- The Projects view is a usable Cowork IDE again: project creation/clone,
  nested file tree, new files/folders, streamed multi-file uploads and drop,
  downloads, rename/delete, highlighted editor, line numbers, save/run
  shortcuts, Git/GitHub panels, live OT edits, presence, and durable offline
  outboxes. The independent controller supplies optional cross-node WebSocket
  relay while nodes durably apply operations.
- The node UI now mirrors the production `plainshow.se` console instead of the
  old cluster prototype. It bundles the real Boxicons 2.1.4 font offline and
  uses the official embedded Plainshow ribbon mark everywhere, including the
  Wails startup/offline screen. Home has the requested project/network counts,
  clickable network status, real GPU inventory, local CPU/RAM/disk/GPU health,
  recent commits/Ray jobs, and network cards.
- Networks are now first-class workspaces. `/api/networks/{id}` supplies one
  network's projects, devices, GPU counts, membership policy, members, and
  sanitized controller presence. The UI has Connected devices, Settings, and
  My machine tabs; projects are shown inside their network and selecting a
  project from another network activates that network first.
- Project cards include their live Git branch. Project pages now have Overview,
  current-branch editor, Run, Git, Team, and Settings tabs. There is one folder
  and one branch working copy (no separate production/workspace trees), and
  project descriptions can be edited through `PUT /api/projects/{name}`.
- `clusteradmin.plainshow.se` remains account/key management only. Its SQLite
  registry now returns canonical network members and lets network owners/admins
  change roles or remove non-owners. Nodes synchronize those roles and the
  Networks page exposes role/remove controls.
- Plainshow account login requests one-time enrollment from the global service
  and connects the host Tailscale client to the self-hosted Headscale service
  at `tailnet.plainshow.se`; users need no Tailscale account.
- The portable installer supports pacman, apt, dnf, and zypper; installs Git,
  Tailscale, Python/venv, GTK/WebKit, and pinned Ray; initializes the account
  server to `https://clusteradmin.plainshow.se`; and enables services on
  systemd machines.
- Release CI builds static node/admin binaries, native desktop binaries,
  complete tarballs, and classic Snap packages on native amd64/arm64 GitHub
  runners, plus an x86_64 Arch package. The Snap contains Git, GTK/WebKit,
  Python and pinned Ray, stores its system node below `$SNAP_COMMON`, and lets
  separate Snap Store registration/classic-confinement approval to publish.

## Verification completed

From this Raspberry Pi, with `/usr/local/go/bin` on `PATH`:

```sh
make check
make smoke
make dist VERSION=v0.1.1-alpha.1
```

`make check` passed after the final Snap changes (Go formatting/vet/tests, web
module graph, installer and PKGBUILD syntax). The final smoke run passed all 71
end-to-end checks. The versioned static amd64/arm64 distribution build also
completed. The Pi does not have GTK/WebKit development headers, Snapcraft, or
Arch `makepkg`, so native Wails, Snap, and pacman builds are validated by
release CI or the user's Arch test machine, not locally. Do not install those
build dependencies on this production Pi merely to validate.

The separate `../cluster-controller` repository is clean and `make check`
passes (controller package tests plus vet).

The user explicitly authorized building and releasing `v0.1.1-alpha.1` on
2026-09-05. No production service deployment is part of that authorization.

## Release procedure

The existing `v0.1.0-alpha.4` and `v0.1.0-alpha.5` tags both point at old commit
`531b6ec`. Do not move or reuse them. Finish with a new tag:

```sh
git status --short
PATH=/usr/local/go/bin:$PATH make check
PATH=/usr/local/go/bin:$PATH make smoke
PATH=/usr/local/go/bin:$PATH make VERSION=v0.1.1-alpha.1 dist
git add -A
git commit -m "Complete the Ray and native desktop migration"
git push origin main
git tag -a v0.1.1-alpha.1 -m "Plainshow Cluster v0.1.1-alpha.1"
git push origin v0.1.1-alpha.1
```

The tag triggers `.github/workflows/release.yml`. Confirm native desktop and
Snap builds for both architectures, the release-assets job, and the Arch
package job. Expected end-user downloads include
`plainshow-cluster-linux-amd64.tar.gz`, the arm64 equivalent, and matching
Each tarball contains the CLI, native app, installer, checksums,
desktop entry, and icons. The GitHub release must be marked prerelease
automatically because the tag contains `-alpha.1`.

## Architecture map

- `cmd/pscluster`: node service and CLI.
- `cmd/plainshow-cluster-desktop`: native Linux application and descriptor
  discovery.
- `cmd/pscluster-admin`, `internal/accountserver`, `adminweb`: global
  account/key/statistics authority.
- `internal/api`: local browser/CLI API, peer mesh, Ray reconciliation, Cowork
  operations, projects, GitHub, and updater.
- `internal/ray`: managed Ray command and dashboard/job protocol.
- `internal/collab`, `internal/store/collab_ops.go`: OT transformation,
  deduplication, revision persistence, and operation application.
- `web`: dependency-free node UI embedded in the node binary.
- `deploy`: account server and Headscale reference configuration. Do not alter
  the production Pi's services, Apache, firewall, or Headscale just to build a
  release.

## Important operational boundaries

- The Raspberry Pi may host one ordinary Plainshow node/controller workload,
  the global account/key database, and Headscale. The global account service
  does not relay Cowork traffic and does not store projects, commands, logs,
  datasets, or model artifacts.
- Network membership is enforced in Plainshow's signed mesh and controller
  authorization. Headscale currently supplies a shared account transport; raw
  Ray ports are not yet isolated by generated Headscale ACLs per logical
  Plainshow network. Treat this alpha as trusted-team software.
- Removing a member centrally removes their application/controller access when
  nodes next synchronize. It does not erase a management key already copied to
  a removed device. Full cryptographic eviction requires rotating the network
  key and distributing the new key to retained devices; the admin rotate API
  and `pscluster network key ID` exist, but automated rotation distribution is
  future hardening.
- The old local job API/supervisor remains for backward-compatible internal
  tests, but the shipped UI and documented CLI use Ray Jobs. Do not build new
  features on the local scheduler.
- The built-in updater replaces and verifies the static node binary. Re-running
  the release installer also refreshes the native desktop and system runtime
  dependencies; it preserves all node data. Snap installations disable that

## Recommended next work after alpha

1. Test the complete tarball and Arch package on two real Arch machines: sign
   in, join the same network, start Ray, observe automatic worker attachment,
   run a project, stop it, reboot both machines, and repeat.
2. Test Debian/Ubuntu installation and GPU workloads with project-specific
   environments.
3. Add generated Headscale ACL policy and automatic key rotation/eviction if
   networks must be mutually hostile rather than trusted collaboration groups.
4. Publish the signed pacman repository and Snap Store package only after those
   real-machine tests.

`CLAUDE.md` is historical planning context and contains stale pre-Ray details.
Use this file and the current code as the authoritative handover.
