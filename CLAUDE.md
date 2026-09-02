# Plainshow Cluster — working notes

Shared context for anyone (Claude, Codex, a person) picking this up. Read this
before changing anything. Keep it current: if you change a decision here, edit
this file in the same commit.

## What this is

A self-hosted workspace that turns a few ordinary computers into one
collaborative AI development and training environment. One Go binary per
machine, one web interface, no Kubernetes.

Target scale is **1–10 machines and 1–10 people who know each other.** Every
design decision is allowed to exploit that. We are not building for datacentres.

Guiding principle: *simple by default, powerful when needed.* The complexity
exists inside the binary; the user does not meet it.

## Non-negotiables

These are settled. Do not quietly reverse them.

1. **Everything a node stores lives under one root directory.** No `/etc`, no
   `/var`, no scattered dotfiles. The only permitted exceptions are a `PATH`
   symlink and a systemd unit, both optional, and the unit file itself lives in
   the root and is only linked from `/etc/systemd/system`.
2. **Nothing about a particular machine is compiled in.** No hostnames, no
   domains, no absolute paths outside the root, no port literals. `init` probes
   (free port, largest disk, hostname, GPUs) and writes what it found to
   `config.yaml`. Every value is overridable by file, `PSCLUSTER_*` env var, or
   flag.
3. **Worker policy is enforced on the machine, by the machine.** A job asking
   for more than local policy permits is refused *there*, not trimmed. A
   compromised coordinator cannot widen it. This is why it is reasonable to run
   someone else's code on your desktop. Terminal access is off by default.
4. **The control plane and the data plane are separate.** Metadata, job state,
   logs and editor deltas go through the coordinator. Datasets, checkpoints,
   artifacts and gradients go **directly between peers, never through it.**
   There must be no code path that proxies bulk bytes.
5. **A job is a job.** Scripts, notebook kernels, terminals and training runs
   share one lifecycle, one log pipe, one stop button, one permission check.
   Resist adding a parallel mechanism for a new kind of work.
6. **Be honest about distributed training over the internet.** Never imply
   4090 + 4070 across two houses is one faster GPU. Measure and say so.

## Roles

Three, freely combinable on one machine.

| Role | Holds | If it disappears |
|---|---|---|
| `master` | State, project files, datasets, artifacts. Serves the interface. | Editing and new jobs stop; running jobs continue and buffer. |
| `worker` | Caches and running processes. Nothing canonical. | Its jobs reschedule. |
| `controller` | Discovery and relay only. No data, no jobs. | Established peers carry on; new joins stop. |

`master` is the **data home**, not the coordinator of a training run. That
coordinator is picked per job and shown as `rank 0`; it is never configured.

One installation can belong to **several networks**. Each membership has its own
projects, accounts, machines, roles and worker policy. `worker` in the top-level
config is a device-wide ceiling that a per-network policy can narrow but never
exceed.

## Deliberate deviations from the original architecture doc

Recorded so nobody "fixes" them by accident, and so the cost of each is known.

- **mTLS + Ed25519-signed requests instead of WireGuard/tsnet.** Simpler, no
  dependency. NAT traversal is solved by inverting the direction rather than by
  a VPN: a machine that cannot be dialled connects *out* to its coordinator and
  work arrives back down that connection (`internal/tunnel`). **Remaining cost:**
  peer-to-peer bulk transfer between two unreachable machines still has no
  direct path, so datasets and checkpoints between two NATed workers go through
  the coordinator instead of directly. That is the relay work still open.
- **Server-authoritative operational transform instead of Yjs/CRDT.** All edits
  serialise through one mutex on the master, so cross-client convergence does
  not depend on the transform being provably correct. Verified: 400 randomly
  interleaved edits with stale bases produced no corruption or divergence.
  **Cost:** overlapping concurrent edits lose one side's *intention* (not data),
  and there is no offline-first editing.
- **A hand-written Python kernel instead of `jupyter_server`/`ipykernel`.**
  Files are real `.ipynb` and open in Jupyter. **Cost:** no ipywidgets, no rich
  MIME output, no completion or inspection.

## Working agreements

- **`make check` must pass before any commit.** It runs `gofmt -l` (fails on any
  unformatted file), `go vet`, the web module-graph check, and the tests.
  `make smoke` runs the HTTP suite end to end against a throwaway node.
- **Write code that reads like the code around it.** Idiomatic, gofmt'd, with
  doc comments on exported things. Several files were landed in a compressed
  one-line style (`internal/api/auth.go`, `web/views/train.js`,
  `web/views/datasets.js`); that is a defect to fix when touching them, not a
  pattern to copy.
- **Comment the *why*, never the *what*.** Explain the trap, the ordering
  requirement, the reason a simpler thing does not work.
- **A bug found is a test written.** Every fix lands with the test that would
  have caught it.
- **Error messages name the next action.** "What went wrong, and two buttons."
- **Never commit the token, the binary, or a node directory.** `.gitignore`
  covers `pscluster`, `dist/`, `.devnode/`, `.smokenode/`.
- The race detector needs a 48-bit VMA. The Raspberry Pi 5 reports 47 and
  ThreadSanitizer refuses to start, so `make race` is separate from `make check`
  and should be run on an x86 machine.

## Layout

```
cmd/pscluster/     CLI and daemon entry point (one binary)
internal/
  config/          install layout, settings, memberships
  store/           SQLite state and migrations
  identity/        device keys, cluster CA, certificates
  mesh/            peer transport: mTLS, signed requests, invites, archives
  auth/            password hashing
  projectfs/       the filesystem side of a project (path containment lives here)
  gitrepo/         git plumbing, remotes, push/pull/merge
  github/          API client and two-way collaborator sync
  collab/          revisioned text operations
  notebook/        persistent Python kernels
  dataset/         content-addressed dataset versions
  training/        distributed run plans (torchrun)
  jobs/            process supervision and log streaming
  events/          the hub behind the WebSocket
  sysinfo/         CPU, memory, disk and GPU probing
  updater/         self-update from GitHub releases
  api/             HTTP API, the web server, the mesh handler
web/               interface, embedded via embed.FS, no build step
scripts/           checks a compiler cannot do
```

## Roadmap

Phases 0–5 are landed. What remains, in order:

1. **Reliability pass on what exists.** Done: the collaboration write path,
   the sign-in gate, `internal/api` tests, the peer port, and the CLI commands
   for networks, invites, joining, GitHub and updates.
2. **Phase 6 — the always-on controller.** Half landed. The reverse tunnel
   (`internal/tunnel`) means a worker on an ordinary home connection needs no
   forwarded port: it dials out and holds the connection, and the coordinator
   sends work down it. **Still open:** the coordinator itself must be reachable
   by everyone, so one machine (or a cheap always-on box) still needs an
   address. And bulk transfer between two unreachable peers has no direct path
   yet — a relay that forwards encrypted bytes between two tunnels, so datasets
   and checkpoints stop going through the coordinator's own process.
3. **Phase 7 — real-machine validation.** A genuine multi-GPU `torchrun` across
   two CUDA machines. The launcher is implemented and unit-tested; it has never
   run on real GPUs. Expect to find CUDA/driver mismatch handling is wrong.
4. **Phase 8 — release engineering.** Landed: `make dist` writes the assets the
   updater looks for plus `checksums.txt`, and pushing a `v*` tag runs
   `.github/workflows/release.yml`, which re-runs the gate, verifies the binary
   reports the tag, and publishes. **No release has been cut yet**, so every
   node's update check currently answers "no releases found" — correctly.
   Signed builds are still open.

## How the command line reaches the daemon

`pscluster network`, `invite`, `join`, `github` and `update` drive the same HTTP
API the browser does, rather than reaching into the database behind the running
daemon's back. One implementation of joining a network, not two that drift.

They authenticate with a token in `<root>/keys/cli.token` (0600), sent as a
bearer header. It grants nothing new — anyone who can read that file can already
read the database beside it — but it keeps a headless machine manageable after
it has an owner account, without loosening the rules the browser is held to.
There is no browser on a GPU box to sign in with.

## How a machine behind NAT is reached

`internal/tunnel`. Dialling a peer needs it to be reachable, which a home
desktop is not. So a worker connects out to its coordinator and keeps the
connection open; the coordinator sends mesh requests down it and reads the
replies. Outbound is the only direction that reliably works, so it is the only
direction used.

What crosses the tunnel is exactly what would have crossed the peer port — same
paths, same JSON, same handler. `peerTransport` in `internal/api` is the seam:
a direct `mesh.Client` and an open tunnel are interchangeable, so nothing above
the transport knows which it got.

The connection is signature-checked once, at the upgrade, and every frame after
it inherits that proof rather than signing itself. That is what
`mesh.TrustedTunnelHeader` marks — and it is why `mesh.StripTunnelMarker` wraps
the network-facing handler. **Without that strip, anybody who could reach the
peer port could impersonate an enrolled machine with one header.** There is a
test for exactly this; do not remove it.

## Traps found the hard way

- **JSON tags.** Config structs are sent to the browser. They once carried only
  `yaml` tags, so the API emitted `AllowGPU` while the interface read
  `allow_gpu` — every toggle silently read as undefined and saving cleared
  permissions. `TestJSONFieldNames` locks this.
- **Partial updates must merge.** Decode a settings PATCH onto a *copy of the
  current value*, never a zero struct, or unmentioned permissions turn off.
- **`internal/api` route patterns** use Go 1.22 method+wildcard matching.
  Registering an overlapping pattern panics at startup, not at compile time.
- **Log ordering.** stdout and stderr are separate pipes read by separate
  goroutines. The sequence number, the in-memory tail and the file write happen
  in one critical section so a reconnecting browser sees the same order as one
  watching live. Ordering *between* the two streams is best-effort.
- **`pkill -f pscluster` matches its own shell.** Use `pgrep -x`.
- **SQLite has no `ADD COLUMN IF NOT EXISTS`.** Schema changes go through
  `store.migrate()`, which checks `PRAGMA table_info` rather than a version
  counter, so it is safe to re-run.
- **SQL is not checked by the compiler.** A query naming a column that does not
  exist builds happily and fails when it runs, which may be at node startup.
  Every new query gets a test that executes it.
- **`UPDATE` matching no row returns no error.** Check `RowsAffected` when the
  row is supposed to exist, or the caller is told a write succeeded that did
  nothing. This is what made claiming a node silently do nothing.
- **The collaboration write path runs once per keystroke.** It is one small
  insert plus one document upsert in a single transaction, and
  `TestKeystrokeCostDoesNotGrowWithHistory` fails if that stops being flat.
  Roughly 14 ms per edit on a Raspberry Pi's SD card, dominated by fsync; if
  that needs to come down, `synchronous=NORMAL` under WAL is the tuning to
  reach for, and it stays crash-safe.
- **The peer port only opens when a peer exists or a join code is outstanding**,
  and it is polled every two seconds rather than checked once, because an
  invite created on a running node has to open it.
- **Release asset names are a contract with the updater.** It looks for
  `pscluster-<os>-<arch>` and reads `checksums.txt`. A release missing an asset
  for a platform is invisible to nodes on it — it looks like no release at all,
  and nothing reports an error. `internal/updater/release_test.go` reads the
  Makefile to keep the two ends agreeing.

## Picking this up

```sh
make check          # must be green before you start and before you commit
make run            # throwaway node in ./.devnode
make smoke          # end-to-end HTTP suite
```

Then read `README.md` for the user-facing story and `internal/config/config.go`
for the shape of a node.
