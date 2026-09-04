# Codex handover to Claude

Last updated 2026-09-04. `CLAUDE.md` is the architecture record; this is the
operational handover for the completed implementation pass.

## Current outcome

The repository contains runnable implementations of the node and global
management plane and builds two programs:

- `pscluster`: equal peer node, browser workspace and task runner.
- `pscluster-admin`: the enterprise management plane. It manages global
  accounts, SQLite-backed network membership/recovery keys, external controller
  registration and aggregate global statistics. It has no collaboration relay.

The controller is now a genuinely separate PlainShow project and Go module at
`/var/www/html/projects/hugohansson/cluster-controller`. Its isolated runtime
listens on `127.0.0.1:10003`; the assigned production hostname is
`https://cluster.plainshow.se`.

The Raspberry Pi is intentionally special only at the enterprise layer. It is
the canonical holder of accounts, network membership/recovery keys and global
statistics. It never runs cluster jobs and never stores or proxies project
files, commands, logs, datasets, artifacts, checkpoints, gradients or peer
addresses. Devices remain equal and exchange task/data traffic directly.

## Enterprise implementation

The main deployment URL is `https://clusteradmin.plainshow.se` and the service
root is `/opt/plainshow-cluster-admin`. The database is
`/opt/plainshow-cluster-admin/accounts.db`.

`internal/accountserver` now owns:

- global registration, login, hashed sessions and administrator disable/enable;
- a `network` registry containing owner and raw recovery key (plaintext storage
  is deliberate because key recovery is required);
- explicit `network_member` rows and roles;
- authenticated network sync and invitation-time member grants;
- owner-authorized external controller registration, scoped relay credentials,
  heartbeats and node discovery;
- aggregate device check-ins and the lightweight admin dashboard.

`clusteradmin.plainshow.se` has no `/ws` handler and never sees collaboration
messages. The independent controller validates a scoped secret carried as the
WebSocket subprotocol `plainshow.<token>`, not in URLs or proxy logs. Browser
operations queued during controller downtime are replayed after reconnect, and
the durable collaboration manager rejects duplicate client sequence numbers.

Each node membership has a 256-bit `management_key` in `config.yaml`, omitted
from JSON responses. Creating a network generates it; a peer invitation shares
it over the pinned/signed mesh join; the inviting node registers the global
account centrally before admitting it locally. `pscluster network key ID` reads
a rotated key without echo and installs it locally. Global key rotation is in
the admin dashboard; every device then needs the displayed new key.

The enterprise server remains non-critical to existing work. If it is down,
new global login/network admission and cross-node live editing pause, while
cached node sessions, local work, Git, peer discovery, jobs and data transfers
continue.

## Other completed product work

- All-to-all signed peer discovery; no master device or coordinator role.
- Separate controller application with global login, owner-only network
  selection, member-scoped visibility, registry heartbeat and live socket
  counts. It never stores project content or runs jobs.
- Tailscale/Headscale auth material in join codes and DERP warnings in training
  preflight; the removed custom tunnel has not returned.
- Interactive local/remote terminal implemented as a policy-controlled PTY job.
- Real installed `jupyter_server` lifecycle and complete same-origin reverse
  proxy under `/jupyter/`; installation uses the distribution package and
  never writes into Python with pip.
- Component installer: `install.sh node|admin BINARY`, with matching
  `make install*` targets, atomic binary replacement, one-root state, PATH link
  and systemd unit. Node installation provisions Git, Tailscale, PTY and
  Jupyter dependencies through pacman on Arch, apt on Debian/Ubuntu, dnf on
  Fedora/RHEL derivatives, or zypper on openSUSE. It starts services and accepts
  a Tailscale/Headscale authentication key for unattended enrolment. Other Linux
  systems can use the static amd64/arm64 binaries after manually providing the
  same runtime tools.
- Native Arch packaging is under `packaging/arch`; `make arch-package` creates
  a pacman-installable package, and tagged release CI attaches the x86_64
  package. A public repository is the only remaining gate for literal
  `pacman -S plainshow-cluster`.
- Release assets/workflow for Linux amd64 and arm64 for both binaries.
  Each release also carries the tested `install.sh` and includes it in
  `checksums.txt`, so installing does not require a source checkout. Release
  binaries are built with `CGO_ENABLED=0` for distribution portability, and
  hyphenated version tags are automatically published as prereleases.
- The updater compares full semantic prerelease versions, so alpha.2 supersedes
  alpha.1. Private GitHub release listing, binary download and checksum download
  all use the node's stored GitHub credential. The Arch package seeds the
  mutable one-root binary and routes both its service and `/usr/bin/pscluster`
  through that copy, so a built-in update remains active after reboot.
- The Cowork workspace now behaves as a compact IDE: it opens a starter file,
  creates files/folders, streams atomic multi-file uploads (including
  drag-and-drop), downloads binary/large artifacts, renames and removes tree
  entries, retains expanded folders, supports Ctrl/Cmd-S and Ctrl/Cmd-Enter,
  and stays usable on phone-sized screens. Uploads are limited to 256 MiB per
  file. External file replacement clears stale collaboration history, and
  operation sequences are monotonic so rapid edits cannot be mistaken for
  duplicate relay delivery.
- Both node and enterprise interfaces now use the exact production PlainShow
  folded-ribbon icon from `/opt/plainshow/public/brand/plainshow-icon.webp`,
  embedded in every binary and guarded by its production SHA-256 in
  `internal/brand/icon_test.go`. The old gradient-square and green-diamond
  placeholders are gone. The enterprise page was rebuilt around the real
  Plainshow typography, gradient wordmark, framed metrics, panels and responsive
  spacing; it shares the node's bundled fonts and remains usable offline.

## Production deployment

Repository templates are:

- `deploy/clusteradmin-bootstrap.conf`: temporary port-80 vhost for first ACME
  issuance.
- `deploy/clusteradmin.plainshow.se.conf`: final HTTPS reverse proxy for the
  account/key application, with `X-Forwarded-Proto` and no WebSocket route.

The service binds to `127.0.0.1:10002`. Its bootstrap token is shown only by
`pscluster-admin init`; on this Pi the deployment process stores that output in
a root-readable file until the first global administrator is registered. Remove
that file after registration. The systemd service is
`plainshow-cluster-admin.service`.

Deployment is complete. Both `plainshow-cluster-admin` and Apache are active,
HTTP redirects to HTTPS, and the public health endpoint and embedded page
return success through Cloudflare. The dedicated ECDSA Let's
Encrypt certificate expires 2026-12-02 and Certbot installed automatic renewal.
The deployed Linux arm64 admin program is revision `5d812d5`. It includes the
strict account/key/controller-registry split: `/ws` returns 404 both locally
and publicly. The public health endpoint and branded interface were verified
after restart. No account has been created yet, so the human handoff is:

```sh
sudo cat /opt/plainshow-cluster-admin/bootstrap.txt
# Open https://clusteradmin.plainshow.se and register the first account.
# Then remove bootstrap.txt; it is no longer useful after consumption.
```

Back up `admin.yaml`, `accounts.db`, `accounts.db-wal` and `accounts.db-shm`
together while stopped, or use SQLite's online backup facility. Possession of
this root is enterprise administrator/key-recovery access.

## Verification completed on this Pi

- `make check`: formatting, vet, 18-module web graph and all Go tests passed.
- Enterprise HTTP registration/membership, controller authorization and
  external-controller discovery are covered by `internal/accountserver` tests.
- Duplicate collaboration delivery and controller authentication are tested.
- `make smoke`: 76 passed, 0 failed, including the production brand asset,
  streamed project upload, download and collaboration-state refresh after
  external replacement.
- Both local programs built successfully.
- Both component installers were exercised against disposable roots.
- Public HTTP, HTTPS, static assets and health were exercised against
  `clusteradmin.plainshow.se` after deployment.
- A real-process integration with one disposable account server and two node
  daemons registered two global accounts, minted/consumed an invitation,
  completed the pinned-TLS peer join, transferred the management key and
  reported two members/two devices in the enterprise registry.
- A second real-process integration claimed an independent controller,
  selected the owner's network, discovered it through the account server and
  confirmed that the admin process has no `/ws` route.
- The separate controller repository passed format, vet and all tests,
  including a real two-client WebSocket relay. Its amd64 and arm64 release
  checksums passed.

## Independent controller deployment

The separate project is `/var/www/html/projects/hugohansson/cluster-controller`
at deployed revision `23baa31`. PlainShow runs it as the isolated,
boot-restored `cluster-controller` process on `127.0.0.1:10003`. Its state file
is owned by the runtime account with mode `0600`.

The `cluster.plainshow.se` publication is enabled and reports `published` with
an Apache WebSocket proxy. Public HTTPS, the embedded page and an unauthenticated
WebSocket upgrade were exercised through Cloudflare; the latter reached the
controller and correctly returned 401. Its dedicated Let's Encrypt certificate
expires on 2026-12-03 and has automatic renewal.

The controller is unclaimed until the first global account and at least one
owner/admin network are created. Sign in at the controller after those exist,
claim it, and select the networks it should supply. See the separate project's
`CODEX.md` for its exact security boundary and operations.

No dependency was installed during this pass. This Pi does not have
`jupyter_server`, so smoke covered the actionable unavailable-tool path.

## External release gates

The software implementation is complete, but do not truthfully call hardware
validation complete until these are exercised by the user and friend:

1. Install on both Arch machines and join them through their actual Tailscale
   or Headscale environment.
2. Run one real two-rank CUDA/PyTorch `torchrun` job and record any NVIDIA
   driver, CUDA or NCCL mismatch behavior.
3. With `jupyter_server` installed, verify kernels, completion, rich MIME,
   widgets and WebSocket proxying.
4. Open the same file in browsers on two nodes and verify live edits reach both
   working trees through `cluster.plainshow.se`, then exercise Git merge.
5. Install an earlier alpha, let the node discover `v0.1.0-alpha.3`, and
   exercise `pscluster update apply`; only then tag v1.0.0. Alpha.2 fixed the
   Jobs-view syntax discovered while building alpha.1's Arch package; alpha.3
   also passes the release tag explicitly into the unprivileged Arch builder so
   the pacman package and embedded binary carry the correct version.

These are external validation/release operations, not unimplemented programs.
Add regression tests for concrete failures discovered there.

## Useful commands

```sh
PATH=/usr/local/go/bin:$PATH make check
PATH=/usr/local/go/bin:$PATH make smoke
PATH=/usr/local/go/bin:$PATH make dist

sudo systemctl status plainshow-cluster-admin
curl -fsS http://127.0.0.1:10002/healthz
curl -fsS https://clusteradmin.plainshow.se/healthz

plainshow pm2
plainshow project cluster-controller publish-status
curl -fsS http://127.0.0.1:10003/healthz
```

Never touch `/var/www/html/cloud`; it is the production Nextcloud instance and
is unrelated to this project.
