# Codex handover to Claude

Last updated 2026-09-03. `CLAUDE.md` is the architecture record; this is the
operational handover for the completed implementation pass.

## Current outcome

The repository contains runnable implementations of all planned product paths
and builds three programs:

- `pscluster`: equal peer node, browser workspace and task runner.
- `pscluster-admin`: the enterprise management plane. It manages global
  accounts, SQLite-backed network membership and recovery keys, the default
  collaboration relay, and aggregate global statistics.
- `pscluster-controller`: optional separately hosted collaboration relay and
  read-only overview for networks that do not want the enterprise relay.

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
- a `network` registry containing owner, raw recovery key and collaboration
  token (plaintext storage is deliberate because key recovery is required);
- explicit `network_member` rows and roles;
- authenticated network sync and invitation-time member grants;
- an authenticated per-network WebSocket relay at `/ws?network=ID`;
- aggregate device check-ins and the lightweight admin dashboard.

The collaboration secret is carried as WebSocket subprotocol
`plainshow.<token>`, not in URLs or proxy logs. The node chooses the enterprise
controller when both it and a standalone controller are registered. Browser
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
- Optional standalone controller enrollment, authenticated overview, durable
  snapshots and automatic learning of later enrolled node keys.
- Tailscale/Headscale auth material in join codes and DERP warnings in training
  preflight; the removed custom tunnel has not returned.
- Interactive local/remote terminal implemented as a policy-controlled PTY job.
- Real installed `jupyter_server` lifecycle and complete same-origin reverse
  proxy under `/jupyter/`; Plainshow installs no Python packages.
- Component installer: `install.sh node|controller|admin BINARY`, with matching
  `make install*` targets, atomic binary replacement, one-root state, PATH link
  and systemd unit.
- Release assets/workflow for Linux amd64 and arm64 for all three binaries.
  Each release also carries the tested `install.sh` and includes it in
  `checksums.txt`, so installing does not require a source checkout.

## Production deployment

Repository templates are:

- `deploy/clusteradmin-bootstrap.conf`: temporary port-80 vhost for first ACME
  issuance.
- `deploy/clusteradmin.plainshow.se.conf`: final HTTPS reverse proxy, including
  WebSocket forwarding and `X-Forwarded-Proto`.

The service binds to `127.0.0.1:10002`. Its bootstrap token is shown only by
`pscluster-admin init`; on this Pi the deployment process stores that output in
a root-readable file until the first global administrator is registered. Remove
that file after registration. The systemd service is
`plainshow-cluster-admin.service`.

Deployment is complete. Both `plainshow-cluster-admin` and Apache are active,
HTTP redirects to HTTPS, the public health endpoint and embedded page return
success through Cloudflare, and an attempted WebSocket upgrade reaches the relay
and is correctly rejected without a network token. The dedicated ECDSA Let's
Encrypt certificate expires 2026-12-02 and Certbot installed automatic renewal.
The deployed Linux arm64 program includes the `3b29fde` admin-index and log
ordering fixes. No account has been created yet, so the human handoff is:

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
- Enterprise HTTP registration/membership and a two-client collaboration relay
  are covered by `internal/accountserver/server_test.go`.
- Duplicate collaboration delivery and controller authentication are tested.
- `make smoke`: 71 passed, 0 failed.
- All three local programs built successfully.
- All three component installers were exercised against disposable roots.
- Public HTTP, HTTPS, static assets, health and the Apache WebSocket route were
  exercised against `clusteradmin.plainshow.se` after deployment.
- A real-process integration with one disposable account server and two node
  daemons registered two global accounts, minted/consumed an invitation,
  completed the pinned-TLS peer join, transferred the management key and
  reported two members/two devices in the enterprise registry.

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
   working trees through `clusteradmin.plainshow.se`, then exercise Git merge.
5. Publish a prerelease, install it, publish the next build and exercise
   `pscluster update apply`; only then tag v1.0.0.

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
```

Never touch `/var/www/html/cloud`; it is the production Nextcloud instance and
is unrelated to this project.
