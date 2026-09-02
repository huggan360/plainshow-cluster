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

## Shape of the system

Three things, and only one of them is required.

**Account.** You register once. The account is your identity across every
network you belong to, and it is what a device is enrolled *as*.

**Network.** A set of devices and people who work together. Anybody can create
one; joining is a single-use code. A device can belong to several, and each
membership keeps its own projects, people and policy.

**Device.** Any machine that has joined a network. **Every device is equal.**
There is no master, no coordinator, no machine that holds the real copy. Every
device can run tasks and connects directly to every other device on the network.
What a device is willing to do is its own local policy — accept jobs, expose a
GPU, allow a terminal — and nothing remote can widen it.

**Controller Server** *(separate program, optional).* A machine you already have
online — a web server, a VPS, a Pi — running a second, smaller program. It holds
the WebSocket that makes live collaborative editing possible, and serves a web
overview of the networks it is attached to. It is configured with which networks
it serves. It is **not** a device role and it runs no jobs.

### Where project state lives

**Git is the source of truth, and every device has a full clone.** There is no
canonical holder to be offline. Two people can work while disconnected from each
other and reconcile with a real three-way merge, which is the one thing that has
to keep working when the network does not.

So collaboration has two tiers, and the difference is honest:

- **Without a controller server** — the stock experience. Edit, commit, pull.
  Asynchronous, works with nobody online but you, needs nothing extra installed.
- **With a controller server** — the same projects, plus live editing over its
  socket. Real-time collaboration needs something always reachable by everyone,
  which is exactly what a device on a home connection is not.

Live editing is a *feature the controller adds*, never a thing the cluster
degrades without. Git keeps working either way.

### What this replaces

Earlier versions had `master`, `worker` and `controller` as roles on a device,
with the master holding canonical state. That is gone. It made one machine
special, made its being offline everybody's problem, and put the thing most
likely to be a gaming desktop in the critical path. `master` and `controller`
are still parsed from old configs and treated as an ordinary device, so an
existing install keeps working.

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

## Handover: the exact steps to 100%

Ordered. Each is a commit or two, and each leaves the tree working. Sizes are
working days for one person who has read this file. **Roughly six weeks.**

The architecture change above — every device equal, git as the truth, a separate
controller server for live editing — adds scope relative to earlier estimates.
It also deletes more than it adds, and removes the machine that was a single
point of failure.

### A. Finish the peer model (≈3d)

1. **Normalise stored roles.** `network_node.roles` rows written by older
   versions still say `master`. Run them through `config.Normalise` on read so
   nothing downstream sees a role that no longer exists. *0.5d*
2. **Drop roles from enrolment.** Joining should not ask what a machine is; it
   is a device. Remove the role picker from `web/views/networks.js` and the
   roles argument from the join path. *0.5d*
3. **All-to-all peer discovery.** Today a joining device learns about the
   machine that invited it. Every device needs every other device's address, so
   any pair can work together. Add a peers exchange on check-in: a device asks
   any peer it knows for the current member list and merges. No coordinator,
   converges, survives any single machine being off. *2d*

### B. Controller Server (≈7d)

A second binary, `cmd/pscluster-controller`. It runs no jobs and stores no
project data. Live editing is the feature it adds; git works without it.

4. **The binary and its config.** Which networks it serves, its own identity
   keypair, listen address, TLS. Reuse `internal/config` layout rules — one
   directory, nothing compiled in. *1d*
5. **Attaching to a network.** A network admin mints a controller token; the
   controller presents it and enrols as a non-device member. Devices learn the
   controller's address the same way they learn each other's. *1d*
6. **Live editing moves behind it.** `internal/collab` becomes the controller's
   job. Devices open the doc socket against the controller; on idle it writes
   through to the project working tree and commits. Git stays the truth, so a
   controller that disappears costs live editing and nothing else. *3d*
7. **Web overview.** The controller serves a read-mostly view across its
   networks: devices, jobs, projects. Reuses `web/` with a different data
   source. *2d*

### C. Accounts across devices (≈3d)

8. **Account is a keypair, not a row.** Today the first person to open a node
   claims it. Make an account an identity the person carries, so the same
   account on a second device is the same person. *2d*
9. **Sign in on a new device with an existing account** rather than creating a
   fresh owner. *1d*

### D. Networking (≈2.5d)

10. **Auth key in the join code.** `mesh.Invite` carries an optional tailscale
    auth key; `join` calls `tailnet.Up` first. One step instead of two. *1d*
11. **Warn on relayed links** in the training preflight. `internal/tailnet`
    already reports which peers are relayed; a relayed link is somebody else's
    bandwidth. *0.5d*
12. **Shrink `internal/mesh`.** Keep mTLS and request signing. Drop what remains
    of working around unreachability. *1d*

### E. Features never built (≈6d)

13. **Terminal.** Section 10 of the original brief, and nothing exists — while
    `allow_terminal` has been guarding it. xterm.js plus a PTY job kind; the job
    lifecycle already handles streaming and stopping. *3d*
14. **Notebooks onto `jupyter_server`.** Delete the hand-written kernel. Gains
    ipywidgets, rich output, completion and inspection, all for free. *3d*

### F. Release readiness (≈11d)

15. **`internal/api` coverage.** ~3,000 lines, and only the auth gate and the
    reachability probe are tested. *3d*
16. **Real multi-GPU validation.** Two CUDA machines, an actual `torchrun`.
    Needs hardware nobody has run this on. Expect the driver and CUDA mismatch
    handling to be wrong. **This is where the estimate is most likely to
    break.** *3d*
17. **Cut v0.1.0 and test a real upgrade** — install an old build, publish a new
    one, watch a node take it. *2d*
18. **Install hardening and docs.** *3d*

### Done and not to be redone

Single-machine workspace · GitHub repos, push/pull and two-way team sync · auth
and permissions · CLI parity with the browser · datasets · job supervision and
log streaming · the update daemon and release workflow · the tailscale driver ·
the rendezvous reachability check. See the status table above for what is
partial.

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
