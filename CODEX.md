# Plainshow Cluster handover

Last updated: 2026-09-05. Current release: `v0.1.1-alpha.6`.

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

Production was deployed on 2026-09-05: the arm64 alpha.6 admin binary replaced
the old alpha.3 binary, the WebSocket Apache config was installed, Apache's
config test passed, the admin service restarted and Apache reloaded. Backups are
`pscluster-admin.before-alpha6` and
`clusteradmin.plainshow.se.conf.before-stage6`. PLAN.md's two-real-node
disconnect/reconnect exercise remains intentionally unverified.

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

`v0.1.1-alpha.6` is tagged at `d07832f`; its GitHub workflow builds the native
desktop bundles and Arch package. Never move alpha.1 through alpha.6. Alpha.5's
release workflow `33975646309` completed successfully. Native GTK and Arch builds continue
to belong to release CI; do not install their build dependencies on this
production Pi.

## Handover addendum — 2026-09-07 lifecycle repair

The previous account sync was additive only. Directly deleting the eight
registry rows proved the flaw: alpha.4 and alpha.7 clients recreated every row
from local configuration. The repaired contract is implemented for alpha.8:

- zero networks is a valid config state (schema version 3);
- create is synchronously accepted by the account authority before local save;
- owner deletion writes scoped tombstones and wakes every affected account;
- devices prune only explicit tombstones, never mere absence during an outage;
- stale clients cannot recreate a tombstoned id;
- adoption seeds peer private endpoints and pinned identities so same-account
  computers have a first mesh edge instead of sharing a decorative membership;
- `/api/auth/status` restores the remembered desktop session before the gate;
- the General settings Save control now sits after the remember toggle;
- Networks → Settings has the owner-only Delete network action. Project folders
  stay on disk, while network-scoped metadata is removed.

The full project check passed and the smoke suite reports 79/79. At audit time
the live registry had eight recreated networks, `hugo-stationary` reported
alpha.7 and `hugo-cachyos` alpha.4. Both clients must update to alpha.8 before
their local lists will consume tombstones; the new server prevents older
clients from recreating the global rows in the meantime.

Production deployment completed the same day. The Pi is running
`pscluster-admin v0.1.1-alpha.8 (0f2d683, linux/arm64)`. The eight recreated
network IDs were recorded as eight account-scoped tombstones and the live
network count is zero; two accounts and two device registrations were kept.
The pre-change database backup is
`/opt/plainshow-cluster-admin/accounts.db.before-tombstone-delete-20260907` and
the previous executable is
`/opt/plainshow-cluster-admin/bin/pscluster-admin.before-alpha8`.
Tag `v0.1.1-alpha.8` points at `0f2d683` and was pushed with `main`.

## Handover — September 8, live devices and home workspaces (unreleased)

User instruction: finish the requested implementation, **do not publish a
release yet**. No tag, release, dependency installation or production service
change was made in this session. Local source and development binaries only.

Read Claude's latest session in
`~/.claude/projects/-var-www-html/b4b8d1b9-8de4-47b5-a7df-314d1351aedb.jsonl`.
The user explicitly clarified there that "react" meant smooth live updates,
not adopting the React framework. Claude's grep-based audit confused an unused
Home helper with a visible device section. The helper is now removed too.
The interrupted import/edit was repaired; the actual exit statuses of the
checks below were observed, without piping away compiler failures.

Implemented:

- Devices uses responsive cards and the existing styled inputs. Home has no
  device section. Test and Preset use dashboard gradients and bundled Boxicons.
  Run remains available inside the branch editor for the open Python file.
- Direct peer streams use signed mesh authentication and certificate pinning.
  There is a connection per device **per shared network**, not just whichever
  network happened to win the deduplicated device list. Clean shutdown closes
  sockets; silent connections expire after 12 seconds; reconnect retries after
  two seconds. Periodic discovery remains for bootstrap and older versions.
- Presence is locally observed, scoped per network, and combined across links
  for Devices. Hardware and Ray changes refresh their views; timestamp-only
  heartbeats do not repeatedly blank/remount the network page. Reconnection
  refreshes state. Route generations prevent a late old page from replacing a
  newer one. Removed/replaced Cowork controller targets are disconnected.
- Newly created global networks wake account adoption. This is an account
  server source change, **not deployed to the Pi service** in this session.
- `pscluster workspace --root ... --user USER` copies projects into
  `~/Plainshow/Projects`, retains `<root>/projects.before-home`, and links the
  old path. It refuses a running node and destination collisions. Installers
  attempt migration when SUDO_USER/PSCLUSTER_WORKSPACE_USER identifies the user;
  existing binary-only updates need the documented one-time migration command.
- Files/directories inherit the workspace owner's Unix ownership; Git commands
  execute as that user. Clones, transfers, readiness and network moves resolve
  repository-name directories. Startup renames old alias directories without
  overwriting collisions. The project header shows the physical folder path.
- Project transfers get nonempty local IDs and unpack into staging first.
  Corrupt archives leave the existing folder intact. Directory replacement has
  rollback; ownership is applied to the received files.
- Test submits a bounded diagnostic through Ray's Jobs REST API, pins a tiny
  task to each live Ray node, compares returned workers against online network
  devices, and reports per-device results. Timeout/cancellation stops its job.
  It does not claim to validate GPU drivers or training frameworks.
- Preset generates new files with CPU, all-GPU, mixed, NVIDIA, AMD or Intel
  placement. It detects live Ray resources when executed, respects a per-device
  worker cap, reserves the matching vendor resource, and never overwrites an
  existing file. Mixed-vendor devices on the same physical host are skipped as
  ambiguous; a vendor resource alone cannot bind a specific physical GPU.
  CPU-only machines can participate in mixed mode. User training/inference
  code goes in the commented task function. These are independent Ray tasks,
  **not universal cross-vendor synchronous distributed training**.

Verification:

- `GOPROXY=off GOTOOLCHAIN=local PATH=/usr/local/go/bin:$PATH make check`: passed.
- `make smoke` with the same offline Go environment: **79 passed, 0 failed**.
- Signed TLS WebSocket tests cover delivery, pin refusal, cancellation, pushed
  Ray changes and server shutdown. Presence tests cover two shared networks.
- Regression tests cover repository rename/readiness, destination collisions,
  failed-transfer preservation and preset overwrite/traversal refusal.
- Generated Python was executed against a Ray API double representing three
  NVIDIA GPUs, two AMD GPUs, a CPU-only device and an offline machine. All six
  modes produced the expected placements. Ray's Jobs diagnostic was exercised
  against a local HTTP fixture, including cleanup.
- Web module/import and installer syntax checks passed. No browser/GTK runtime
  is installed for visual inspection here. No real multi-machine Ray workload,
  actual GPU framework, root-owned Arch service migration, or native desktop
  rendering was exercised; those remain hardware integration checks.

Boundaries to retain: peer streams carry hardware/Ray snapshots, not project
file mirroring. File placement remains explicit send/fetch or Git, and editor
operations still use the independent Cowork controller. Account bootstrap can
still wait for a heartbeat when discovering a newly registered device. Do not
promise instantaneous detection of a power cut or identical files on every
device merely because their presence sockets are connected.

## September 8 addendum — Alpha 0.1.2 preparation

The user superseded the no-release instruction: “deploy when you are ready
alpha 0.1.2”. Target tag: `v0.1.2-alpha.1`.

Additional fixes after 6942e88:

- Device check-in compares discovery facts, not timestamps or job counters.
  New devices, endpoint changes, returning devices and membership changes wake
  the owner's devices and shared-network accounts without heartbeat feedback.
  This supersedes the new-device heartbeat limitation documented above.
- Project Devices uses direct presence and subscribes to live changes while
  active. Leaving the page disposes subscriptions; late preset creation no
  longer navigates away from a newer page.
- Presets with no known project-network head fail explicitly unless Ray Jobs
  or the user supplies RAY_ADDRESS; they no longer select a random local cluster.
- Remembered sessions recover expired cookies as well as missing ones.
  Restored sessions now pass the normal cross-origin mutation guard.
- Added release notes and a workflow body/title; README targets Alpha 0.1.2
  and explains full-desktop Arch updates and private-repository access.

Verification: `GOPROXY=off GOTOOLCHAIN=local PATH=/usr/local/go/bin:$PATH make check`
passed, including new discovery/no-feedback, expired-cookie/origin and preset
network regressions. No dependency installation or unrelated service changes.
Real desktop rendering and heterogeneous physical GPU training remain unverified.

Repository visibility is PRIVATE. Do not make it public without explicit
permission. The updater uses the locally connected GitHub token for access.
Release publication and account-service deployment outcomes will be appended
after the workflow completes. Keep the existing account database intact; no
network/account deletion was requested in this session.

## September 8 — Alpha 0.1.2 released and deployed

- Release tag `v0.1.2-alpha.1` points to `91e15ae`. Both implementation commits
  (6942e88 and 91e15ae) are pushed to main. Release URL:
  https://github.com/huggan360/plainshow-cluster/releases/tag/v0.1.2-alpha.1
- GitHub check run 34219801746 and release run 34219802096 succeeded. All four
  release jobs passed: native amd64 desktop, native arm64 desktop, portable
  binaries/installers, and native Arch package. The published prerelease has
  all 12 expected assets, including
  `plainshow-cluster-0.1.2.alpha.1-1-x86_64.pkg.tar.zst` and `arch-checksums.txt`.
- The Pi's `plainshow-cluster-admin.service` is deployed from the published
  arm64 asset, SHA-256
  `cb267f790f320821441f633613be5855bd2c27dc8d351887f3299f620b6d8c4b`.
  Binary reports `Plainshow Cluster Admin v0.1.2-alpha.1 (91e15ae, linux/arm64)`.
  Systemd reports active/running and public `/healthz` returns `ok`.
- Before deployment, SQLite online backup and its quick_check succeeded.
  Accounts/network/device counts remained 2/1/2. No accounts, networks or
  project data were deleted. No dependency installs, Apache changes, Headscale
  changes, or Cowork-controller deployment were made on this production Pi.
- Rollback binary:
  `/opt/plainshow-cluster-admin/bin/pscluster-admin.before-alpha012`.
  Database backup:
  `/opt/plainshow-cluster-admin/accounts.db.before-alpha012-20260908`.
  Normally roll back only the binary; restoring the database would discard
  account changes since the backup and requires a deliberate recovery decision.
- This documentation update follows the release commit; do not move/re-cut the
  published tag. Clients still need to install the new package. The built-in
  updater replaces only the node binary, not the desktop executable. Repository
  access remains required because the GitHub repository is private.

Physical multi-machine GPU training and native UI interaction remain user-side
integration checks, not claims established by successful release builds.

## Post-release — GPU software diagnostics (not released)

The requested Test extension keeps the existing connectivity pass/fail result.
Each reachable worker now reports a Python CPU arithmetic check and read-only
GPU tooling inventory (NVIDIA driver/CUDA, ROCm/HIP, Intel SYCL/OpenCL, PyTorch).
Missing command-line tools are explicitly not proof that a runtime is absent.

A separate Ray task reserves one GPU per device and attempts a tiny PyTorch
matrix calculation using CUDA, ROCm's torch.cuda API, or Intel torch.xpu.
This samples one allocated GPU, not every GPU. It honors Ray visibility and
skips unknown/mixed-vendor hosts, fractional/no GPU allocations, and busy GPUs.
Missing/incompatible frameworks, driver failures and timeouts are informational
warnings: they do not turn a reachable worker red. Native GPU checks run under
an external 12-second timeout, with no installations or configuration changes.
The existing 75-second HTTP deadline remains. The UI shows CPU/GPU results and
expandable software details under each device. All checks use the Ray worker's
Python environment; project-specific virtual environments are not inspected.

Focused diagnostic regression checks execute the Python with a Ray/PyTorch API
double for CPU, NVIDIA, AMD, Intel, missing frameworks and busy GPU allocations;
the HTTP fixture checks that software details survive JSON decoding. These are
not physical GPU validation. No new release, tag, or service deployment follows
this addition; the already published Alpha 0.1.2 remains unchanged.

Backend references: https://docs.pytorch.org/docs/stable/notes/hip.html,
https://docs.pytorch.org/docs/stable/xpu.html,
https://docs.ray.io/en/latest/ray-core/scheduling/accelerators.html.

## Independent projects and selected run network (unreleased)

User changed the model: projects need not belong to networks. Implemented:

- New project/clone forms require no network, and the corresponding APIs allow
  empty network_id. New projects use a flat repository-name folder under the
  user's Projects root. Startup no longer assigns unscoped projects to whichever
  network happens to be active. Existing scoped folders/IDs are left intact.
- Networks has “Use for runs” and an active badge. ActiveNetwork controls new
  file runs, Test and Preset; an explicit CLI --network overrides it. Missing,
  paused or unauthorized selections fail clearly. Selecting compute does not
  hide projects, move files or grant editor-sharing access.
- The full Ray metric card is a keyboard-accessible button labeled Ray on/off
  **for this device**. Starting attaches/starts here; stopping confirms because
  it can interrupt this machine's tasks. It does not stop every peer remotely.
- Owners can delete directly from network cards or the detail header, with
  confirmation. Network deletion/tombstone adoption preserve local project
  records, member/job history, collaboration history, folders and Git history.
  If the selected network disappears, no replacement target is silently chosen.
  A deliberately blank selection survives config save/load, even with other
  memberships. Pre-v3 config migration retains its historical selection.
- Running jobs retain the submission response's network_id for logs/status/Stop,
  independent of subsequent selection. The branch editor shows its run target.
- Legacy network_id still locates old folders and scopes optional existing
  transfers/Cowork sharing. It no longer routes Ray jobs. Local document changes
  are not queued to a controller with an empty network id. Document resolution
  prefers exact project id, with only a scoped-name fallback for remote clones.
- Legacy folder fallback/migration cannot claim a new independent project's
  same-named flat folder. Creation refuses existing untracked directories.
- Network detail lists local projects as runnable choices, not network-owned
  projects. README, CLAUDE.md and PLAN.md document the changed product model.

Regression coverage: local creation without networks, selected/explicit run
targets, viewer/paused refusal, preset targeting a different network from the
legacy project scope, preserved projects/files after network deletion, durable
blank selection, local edits alongside same-named legacy projects, and safe
legacy folder resolution. Smoke expectations were updated to preserve projects.
No release/tag/deployment or live network deletion is part of this change.

Verification completed: full offline `make check` passed after the backend and
config changes; the web module check also passed after the final UI copy/live
target updates. Updated smoke expectations were not run in this session.

## September 8 continuation after Claude's session limit — Jobs (unreleased)

Read Claude session `b4b8d1b9-8de4-47b5-a7df-314d1351aedb`, including the final
requests in history.jsonl that its compacted summary omitted. Latest baseline
here was `c2838e0` with a clean worktree. Claude completed the Jobs redesign,
but stopped at inspection before implementing centered spinners, shared job
history on clusteradmin, and history deletion with a network. Its earlier
Code/gutter, merge, creation, branches, invitations, downloads, demo, devices,
fonts and profile changes were preserved.

Implemented:

- `internal/accountserver/jobs.go` and `ray_job_history`: authenticated,
  network-scoped Ray summaries (ID, status, command, bounded status message,
  start/end milliseconds). Never code, full output logs, artifacts or datasets.
  Reports require an owned enrolled node and a contributing network membership;
  reads require current membership. All members share the same history.
- Changed observations wake every member account through existing `/api/events`
  (`jobs` topic), forwarded to the local UI as `jobs.changed`. Identical reports
  don't rewrite history or generate notification loops. Terminal states cannot
  regress because a peer reports an older observation. Start timestamps separate
  reused IDs after a head restart; undated pending entries are promoted.
- A daemon collector runs with account check-in startup, independent of the
  window. It observes known network heads roughly every five seconds (plus
  request time), queues changed summaries locally, and uploads in batches of
  100. A durable, account/server-scoped `ray_job_outbox` retries failed uploads
  even after the head or node restarts. Acknowledgments don't erase newer data.
- Deleting a network cascades to central history and its pending local uploads.
  Late reports cannot recreate the network/history. No actual networks,
  accounts, projects or user files were deleted in this session. In particular,
  do NOT repeat the blanket rm instructions from Claude's old cleanup summary.
  Existing network tombstones already prevent re-registration; that old claim
  in its handover was stale.
- Jobs defaults to the selected network, supports explicit network IDs, overlays
  current Ray status on paginated account history, and shows unavailable saved
  running/pending observations as **last known**, not live or failed. History
  failures are visible instead of pretending there are no jobs. Logs/Stop retain
  each job's original network. Archived output is explicitly unavailable because
  the admin is not a log/file relay. Waiting is Ray's PENDING state (including
  environment setup), not a new FIFO scheduler.
- Centered button/refresh/job loaders; double-click prevention happens before
  executing the handler. Jobs has one non-overlapping polling loop with socket
  wakeups, generation guards, abort/disposal, and no orphaned log-tail timers.
  Only running rows tick elapsed time; completed durations remain fixed.
- Demo fixtures cover scoped and archived jobs. `make demo` now preserves old
  timestamped assets for open tabs, checks the next build before switching HTML,
  and accepts `DEMO_ROOT` for a preview build without changing the served demo.

Ray contract reference checked against upstream primary source:
https://github.com/ray-project/ray/blob/master/python/ray/dashboard/modules/job/pydantic_models.py
and `common.py` in that directory. `start_time` and `end_time` are milliseconds;
PENDING/RUNNING/SUCCEEDED/FAILED/STOPPED are the execution states. A regression
asserts timestamps survive parsing unchanged.

Verification so far: affected accountserver/accountclient/api/ray package tests,
focused history permissions/fanout/pagination/deletion/stale-state/reused-ID
tests, node outbox restart/isolation/deletion tests, web import checks, and demo
preview build with 20 fixture checks and simulated interface boot passed.
Final full check result is recorded below when it finishes. No dependencies
installed, no new release/tag/push, no system service/proxy change or production
database migration. Native WebKit rendering and real two-machine Ray/WS behavior
remain hardware checks, not claims established by the simulated demo.

Rollout requires BOTH new account-server and node builds. The central schema is
additive and applied at account startup; the node queue is added at node startup.
An old/unreachable account server leaves summaries queued and shows a history
warning. The existing account WebSocket route must be proxied for prompt wakeups;
polling remains the fallback. Summaries never observed before Ray data disappears
cannot be reconstructed; logs are deliberately not archived. This work does not
deploy services or publish a release. The published Alpha 0.1.2 is unchanged.

Final verification: full offline `make check` passed (formatting, vet, web,
installer/Arch syntax and all Go packages). This includes an HTTP fixture proving
selected-network history loads without a Ray head and an unjoined network is
refused. No new smoke run or real multi-machine test was performed. The demo
preview passed; the existing `dist/demo` was rebuilt as `20260908202830`, with
20 fixture checks and simulated boot passing. The web check passed again after
the last reconnect/capacity UI adjustment. This is a static demo update, not a
node/admin service rollout.
