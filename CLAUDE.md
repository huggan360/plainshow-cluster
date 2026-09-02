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
   4090 + 4070 across two houses is one faster GPU. Measure and say so — and
   refuse a run that cannot work rather than letting it hang (see below).

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

## The most important rule: bundle, do not rebuild

**Plainshow Cluster is an interface over programs that already work.** It is not
a distributed systems project. The value is the workspace — one simple place to
create a project, edit it with someone, and run it on whichever machine has a
free GPU — not the plumbing underneath, which mature software already does far
better than we will.

The same shape as the Plainshow web console: it does not implement a web server,
a process manager or Git. It drives Apache, PM2 and git, and is the simple thing
in front of them.

So the default answer to "how do we do X" is **which existing program does X, and
how do we drive it.** Writing our own is the exception, and it needs a reason
beyond "it seemed easier at the time".

What we legitimately build ourselves:

- The web interface and the workspace experience.
- The collaborative editor over a socket — this is small, specific to us, and
  fine to own.
- Project, job, dataset and membership modelling: the concepts users see.
- Anything that is genuinely Plainshow's opinion rather than infrastructure.

What we must not build ourselves — the list that has already been violated once:

| Need | Use | Never write |
|---|---|---|
| Encrypted network, NAT traversal | **Tailscale** (Headscale to self-host) | VPN protocols, hole punching, key distribution, relays |
| Notebook kernels | **jupyter_server / ipykernel** | A Python execution protocol |
| Distributed training | **torchrun / NCCL** | A launcher of our own beyond rendering the command |
| Version control | **git** | Anything resembling a merge algorithm |
| Containers / environments | **Podman, uv** | An image or dependency resolver |
| General compute, later | **Spark / Ray** | A scheduler beyond simple placement |

## Debt: three places we rebuilt instead of bundling

These are **not** approved designs. They are drift, recorded so they get paid
down rather than copied. Each replaced something the original plan had already
chosen correctly.

1. **`internal/mesh` instead of Tailscale.** *Mostly paid down.*
   `internal/tunnel` is gone; `internal/tailnet` drives the daemon and this
   machine advertises its tailnet address. `internal/mesh` still carries mTLS
   and request signing, which are worth keeping — see the migration below.
2. **`internal/notebook` instead of `jupyter_server`.** A hand-written Python
   kernel. Files are real `.ipynb`, but there are no ipywidgets, no rich MIME
   output, no completion or inspection, and every one of those is free from the
   real thing.
3. **`internal/collab` operational transform.** This one is closest to
   defensible — a collaborative editor over a socket is on the "we build it"
   list — but Yjs was the original choice and would have been less code.

When touching any of these, the question is not "how do I improve this" but
"how do I delete it and drive the real thing instead".

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

## Where this actually is — roughly 70%

Assessed by running it, not by reading commit messages.

| Area | State |
|---|---|
| Single-machine workspace: files, editor, run, live logs, jobs | done |
| GitHub: repos, push/pull, two-way team sync | done |
| Auth, permissions, CLI parity | done |
| Datasets | ~80% — content-addressed and syncing; no sharding |
| Collaborative editing | ~85% — no offline mode; overlapping edits lose intention |
| Notebooks | ~70% — real `.ipynb`; no widgets, rich output or completions |
| Updates and releases | ~70% — built end to end, never exercised, no release cut |
| Multi-machine networking | ~55% — mid-migration to tailscale |
| Distributed training | ~50% — launcher complete and guarded, **never run on a real GPU** |
| **Terminal** | **0% — not built.** `allow_terminal` guards a feature that does not exist |
| Install hardening and docs | ~60% |

Remaining is about two and a half weeks, but it holds most of the risk.
Everything above could be verified on the development Pi; a multi-GPU
`torchrun` across two CUDA machines never has been, and that is where this kind
of estimate usually breaks. Expect the driver and CUDA mismatch handling to be
wrong in some specific way that only appears on real hardware.

## Roadmap

Phases 0–5 are landed. What remains, in order:

1. **Reliability pass on what exists.** Done: the collaboration write path,
   the sign-in gate, `internal/api` tests, the peer port, and the CLI commands
   for networks, invites, joining, GitHub and updates.
2. **Phase 6 — finish the Tailscale migration.** Steps 1 and 2 below are done. Not by finishing ours.
   Plainshow should require the tailscale daemon, bring it up with an auth key
   during join, and read peer addresses from `tailscale status --json`. That
   gives NAT traversal, key distribution, roaming and a relay fallback for
   nothing, and — the part our tunnel cannot do — a real interface that torch
   and NCCL can bind to, so distributed training works between houses.
   `internal/mesh` and `internal/tunnel` then shrink to almost nothing: with
   every machine reachable at a stable address, peers are just dialled.
   Headscale, or Tailscale's free tier, covers the coordination server.
3. **Phase 7 — real-machine validation.** A genuine multi-GPU `torchrun` across
   two CUDA machines. The launcher is implemented and unit-tested; it has never
   run on real GPUs. Expect to find CUDA/driver mismatch handling is wrong.
   Note that a cross-house run will now be *refused* rather than hanging, so
   validation needs two machines with a route between them — same network, a
   VPN, or a forwarded port.
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

## Training does not use the mesh — and why that matters

Everything the cluster does goes over the mesh **except distributed training**.
Ranks connect to each other over raw TCP that torch and NCCL open themselves, at
`MASTER_ADDR:MASTER_PORT`. A tunnel cannot carry that: it is not our protocol
and not our socket.

So a worker behind NAT can receive jobs, notebooks, datasets and logs perfectly
well while being unable to take part in a distributed run at all. Those are
different questions and the product must not conflate them.

Without a check, launching such a run looks completely normal, hangs in NCCL
rendezvous for about ten minutes, and dies with an error about a socket — and
the interface shows it as training the whole time. `rendezvousIssues` in
`internal/api/training.go` asks every rank whether it can actually open a
connection to rank 0 and refuses with an explanation naming the alternative
(separate jobs, which work over any link). It guards **both** preflight and
launch: preflight is advice, and a caller can skip it.

One subtlety worth keeping: **"connection refused" counts as reachable.** The
rendezvous port has nothing listening until the run starts, so a refusal proves
the route exists. Treating it as failure would refuse every correctly configured
cluster.

## The Tailscale migration — what is done and what is next

We drive the daemon; we do not embed it. `internal/tailnet` shells out to
`tailscale status --json` and `tailscale up`, and that is the entire integration
surface. Nothing of Tailscale's is vendored, and Plainshow keeps its own
identity, enrolment and permissions — we take the one thing it is better at.

It has to be the real daemon rather than a library in this binary: torch and
NCCL open their own sockets in their own processes and need an interface the
kernel knows about. An in-process network stack would carry Plainshow's traffic
and nothing else, which was never the traffic that was stuck.

**Done**

1. `internal/tailnet` — probe, status, peers, relayed-or-direct, `up` with an
   auth key. Parsing is tested against real `tailscale status --json` shape.
2. `GET /api/tailnet`, and joining advertises the tailnet address when there is
   one, so the address the mesh proves reachable is the address training uses.

**Next, in order**

3. **Carry an auth key in the join code.** `mesh.Invite` gains an optional
   tailscale auth key; `pscluster join` calls `tailnet.Up` before enrolling, so
   joining a Plainshow network and joining its tailnet are one step. This is
   convenience, not correctness — `tailscale up` by hand works today — which is
   why step 4 did not wait for it.
4. ~~**Delete `internal/tunnel`.**~~ Done. The registry, dialer, trusted-marker
   path and `StartTunnels` are gone, and `clientForNode` simply dials the peer.
   Cross-network now requires tailscale, which is the honest requirement rather
   than a second half-working networking stack.
5. **Simplify `internal/mesh`.** Keep mTLS and request signing: they are cheap
   and mean a stolen tailnet position still proves nothing. Drop everything that
   exists to work around unreachability.
6. **Relayed-link warning in the training preflight.** `tailnet` already reports
   which peers are relayed. A relayed link is somebody else's bandwidth, and the
   existing bandwidth advice should say so before a run starts.

Steps 3–6 are roughly a week. The codebase gets smaller at every step.

## How a machine on another network is reached

Through **tailscale**, which Plainshow drives rather than implements. Install it
on both machines, sign them in, and the address tailscale hands out is the
address Plainshow records and dials — and the same address torch and NCCL use,
so a link the cluster proves reachable is the link training runs over.

There is no fallback and that is deliberate. A reverse tunnel used to carry
control traffic to unreachable machines; it worked, and it could never carry
training, because NCCL is a separate process needing a real interface. Keeping
it would have meant maintaining a second networking stack that solved half the
problem. `clientForNode` now says plainly that a machine has no reachable
address and that tailscale is how to fix it, which is more use than a timeout.

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
