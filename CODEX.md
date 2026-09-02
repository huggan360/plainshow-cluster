# Codex handover to Claude

Last updated 2026-09-03. Read `CLAUDE.md` first; it remains the architecture
record. This file records the implementation pass after that handover.

## Outcome

All product programs in steps 1–14 and the installation work in step 18 now
have executable implementations. The repository builds three binaries:

- `pscluster`: equal peer node, web workspace and task runner.
- `pscluster-controller`: optional live-collaboration relay and read-only
  multi-network overview.
- `pscluster-admin`: central SQLite account authority and global statistics UI.

Do not call this v1.0 until the two-machine CUDA run and a published upgrade
have been exercised. Those are validation/release operations, not missing
programs. The user and a friend intend to perform them on Arch Linux hosts.

## Work completed in this pass

1. Central node authentication now registers/logs in through the configured
   Account Server, migrates legacy ownership to the global account ID, stores
   the authority token in `keys/account.token`, and keeps local cached browser
   sessions working during an authority outage.
2. Network join proves the global account to the inviting peer. Enrolling a
   second device for an existing account preserves its current network role.
   Startup no longer rewrites joined networks as locally owned.
3. Controller enrollment is single-use and authenticated. Run
   `pscluster controller invite`, then
   `pscluster-controller attach CODE --advertise HTTPS_URL`.
4. Devices gossip controller records and send signed, metadata-only snapshots.
   Controller snapshots persist in `<controller-root>/overview.json`.
5. The controller WebSocket relays collaboration messages but stores no project
   files. Each connected browser forwards remote operations into its own node,
   where the existing operation log writes the clone. This deliberately
   resolves the old handover contradiction between “controller writes files”
   and “controller stores no project data”.
6. Join codes optionally carry `tailnet_auth_key` and
   `tailnet_login_server`; the joining node calls `tailscale up` first.
   Training preflight reports DERP-relayed paths as warnings.
7. Terminal is an ordinary `terminal` job. It uses util-linux `script` for a
   PTY, accepts input, streams output, works on remote nodes, and is refused by
   the target machine unless `worker.allow_terminal` is true.
8. The handwritten Python kernel was deleted. Plainshow starts an already
   installed `jupyter_server` on loopback with a random token and proxies its
   complete UI under `/jupyter/<id>/`. Plainshow never installs Python packages.
9. `install.sh node|controller|admin` atomically installs any component and
   creates matching PATH/systemd integration. Make targets are `install`,
   `install-controller`, and `install-admin`.

## Verification run here

- `make check`: passed after the implementation batch.
- `make smoke`: 71 passed, 0 failed.
- Builds succeeded for all three local binaries.
- This Pi has no `jupyter_server`, so smoke verified the actionable missing-tool
  path. Exercise the real Jupyter iframe on the Arch test machine.

## Remaining release exercises

1. On two CUDA Arch machines, install matching Tailscale, NVIDIA driver, CUDA,
   PyTorch and `torchrun`; join them and run a real two-rank job. Record driver,
   CUDA and NCCL mismatch messages and adjust preflight wording as needed.
2. Install `jupyter_server` on an Arch node and verify kernels, completion,
   rich MIME output, widgets and WebSocket proxying through the node.
3. Attach a public controller and edit the same file from browsers on two
   devices. Verify both working trees receive the operation, then commit/pull.
4. Publish a prerelease, install it, publish the next build and exercise
   `pscluster update apply`. Only then tag v1.0.0.
5. Expand `internal/api` coverage based on failures found in those exercises;
   the user explicitly prioritized raw implementation during this pass.

## Production account service

The intended URL is `https://clusteradmin.plainshow.se`. The service should
bind only to loopback and sit behind Apache TLS. Its root is
`/opt/plainshow-cluster-admin`, database is `accounts.db`, and the first-admin
bootstrap token is printed exactly once by `pscluster-admin init`. Never place
project names, commands, logs, datasets, keys or peer addresses in this DB.

## Important implementation notes

- Controller collaboration tokens currently travel in authenticated peer
  gossip and are returned only through the signed-in node API. Rotation/revoke
  UI is a useful post-v1 hardening item.
- Controller snapshot authentication knows the node keys present at attach
  time. The initially attached node supplies the complete network overview;
  teaching the controller new node keys dynamically is another hardening item.
- Interactive terminal output is chunk-based so prompts without newlines are
  visible. Historical terminal replay is plain text and may retain some ANSI
  control characters; the live page strips common CSI sequences.
- Jupyter is same-origin reverse-proxied and token-authenticated. If deploying a
  node behind another reverse proxy, it must allow WebSocket upgrades under
  `/jupyter/` as well as `/ws`.
