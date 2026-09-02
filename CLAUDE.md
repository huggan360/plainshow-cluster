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
  dependency, works today. **Cost: no NAT traversal.** Two machines need a
  route to each other (LAN, VPN, or a forwarded port). Closing this is the
  single biggest remaining gap — see Roadmap.
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

1. **Reliability pass on what exists.** Mostly done: the collaboration write
   path, the sign-in gate and `internal/api` tests have landed. Still open —
   **the CLI has no commands for networks, invites, GitHub or updates** while
   the API has all of them, so those flows are browser-only.
2. **Phase 6 — the always-on controller.** A stateless coordinator that gives
   NAT traversal: rendezvous (peers publish endpoints and fetch keys), relay
   (forward encrypted bytes when direct fails), and a signed directory record
   saying where the master is. This is what makes two home networks work
   without port forwarding, and it is the largest remaining gap.
3. **Phase 7 — real-machine validation.** A genuine multi-GPU `torchrun` across
   two CUDA machines. The launcher is implemented and unit-tested; it has never
   run on real GPUs. Expect to find CUDA/driver mismatch handling is wrong.
4. **Phase 8 — release engineering.** Tagged releases with per-platform assets
   and checksums so the updater has something to update from; signed builds.

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

## Picking this up

```sh
make check          # must be green before you start and before you commit
make run            # throwaway node in ./.devnode
make smoke          # end-to-end HTTP suite
```

Then read `README.md` for the user-facing story and `internal/config/config.go`
for the shape of a node.
