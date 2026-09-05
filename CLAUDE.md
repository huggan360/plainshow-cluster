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
4. **The management plane and collaboration plane are separate.** The
   enterprise Pi service at `clusteradmin.plainshow.se` stores accounts,
   network membership/recovery keys, controller registrations and aggregate
   health. It has no collaboration WebSocket. Independent controller servers
   relay collaboration deltas without storing them. Jobs, logs, projects,
   datasets, checkpoints, artifacts and gradients go **directly between peers,
   never through either service.** There must be no code path that proxies bulk
   bytes.
5. **A job is a job.** Scripts, notebook kernels, terminals and training runs
   share one lifecycle, one log pipe, one stop button, one permission check.
   Resist adding a parallel mechanism for a new kind of work.
6. **Be honest about distributed training over the internet.** Never imply
   4090 + 4070 across two houses is one faster GPU. Measure and say so — and
   refuse a run that cannot work rather than letting it hang (see below).

## Shape of the system

Plainshow Cluster is an **environment that wires together Tailscale, Ray and
Git**. It runs nothing itself.

| Borrowed | Does |
|---|---|
| **Tailscale** | Makes the machines reachable from each other, anywhere |
| **Git / GitHub** | Moves code between them |
| **Ray** | Runs the work across them |
| **Plainshow** | Sets those three up and shows what is happening |

**Account** — registered once, and what a device is enrolled as.
**Network** — a set of devices and people. Every device in one is equal, can run
tasks, and reaches every other one.
**Project** — an ordinary folder, kept in step through git. Data lives in it
like any other file. You open it in your own editor.

Seven pages: Home, Networks, Projects, Jobs, GitHub, How to, Settings.

The top bar reports which network this machine works in, its tailnet address and
Ray's state; it does not select. Selecting happens on Networks, because it moves
the machine's one Ray process from one cluster to another. The power control
beside it stops jobs, Ray and the node service — anything the interface can
start, it must be able to stop from the same window.

### What we do not build

No editor, no notebook, no dataset registry, no scheduler, no training launcher.
Ray decides how work is distributed and it is better at it than anything we
would write; git moves the code; the folder holds the data. `ray.init()` in
somebody's ordinary Python finds the network because Plainshow started a head on
one machine and attached the rest — that is the whole integration.

Ray has a head node. That is Ray's architecture, not a return to master nodes,
but one machine per network is special while a cluster is up and the interface
says which. `MembershipConfig.RayHead` records it.

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
2. ~~**`internal/notebook` instead of `jupyter_server`.**~~ Paid down. Plainshow
   launches an installed Jupyter Server on loopback and reverse-proxies its UI;
   it does not install Python packages or implement a kernel protocol.
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
  notebook/        Jupyter Server lifecycle and reverse proxy
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

## Where this actually is — implementation complete, hardware validation pending

Assessed by running it, not by reading commit messages.

| Area | State |
|---|---|
| Single-machine workspace: files, editor, run, live logs, jobs | done |
| GitHub: repos, push/pull, two-way team sync | done |
| Auth, permissions, CLI parity | done |
| Datasets | implemented — content-addressed and direct peer syncing; no sharding |
| Collaborative editing | implemented — controller relay plus local durable writes and offline outbox |
| Notebooks | implemented through an installed `jupyter_server` |
| Updates and releases | implemented; corrected alpha.3 published, real alpha-to-alpha upgrade remains a hardware exercise |
| Multi-machine networking | implemented through the Tailscale client, self-hosted Headscale and signed peer mesh |
| Distributed training | launcher complete and guarded, **never run on two real CUDA machines** |
| Terminal | implemented as a policy-controlled interactive PTY job |
| Enterprise master | accounts, network/key/controller registry and admin statistics implemented; no relay |
| Controller | separate PlainShow project, global login, network selection, registry heartbeat and relay implemented |
| Install hardening and docs | automatic pacman/apt/dnf/zypper dependencies, static Linux binaries, native pacman package and simple install guide implemented |

The main enterprise service is deployed on this Pi at
`https://clusteradmin.plainshow.se`, reverse-proxied by its own Apache vhost to
`127.0.0.1:10002`. Its systemd unit is enabled. The first administrator still
needs to register using the protected bootstrap output described in `CODEX.md`.
That same service issues one-time Headscale enrollment to authenticated
PlainShow accounts. Headscale is deployed separately at
`https://tailnet.plainshow.se` on loopback port `10004`; it is coordination
only and does not turn the account service into a collaboration relay.
The independent `cluster-controller` runtime is online on `127.0.0.1:10003`.
Its PlainShow publication for `https://cluster.plainshow.se` is live with TLS
and WebSocket forwarding through Cloudflare. It still needs to be claimed by
the first global network owner.

Remaining work is validation and release operation rather than missing product
programs. The highest-risk gate is still the real two-CUDA-machine run.
Everything above could be verified on the development Pi; a multi-GPU
`torchrun` across two CUDA machines never has been, and that is where this kind
of estimate usually breaks. Expect the driver and CUDA mismatch handling to be
wrong in some specific way that only appears on real hardware.

## Handover: release gates after implementation

All product paths below are implemented. The unchecked items require external
hardware or a published release; they are release validation, not missing
programs.

### A. Completed peer model

1. ~~**Normalise stored roles.**~~ Done. `network_node.roles` rows written by older
   versions still say `master`. Run them through `config.Normalise` on read so
   nothing downstream sees a role that no longer exists. *0.5d*
2. ~~**Drop roles from enrolment.**~~ Done. Joining no longer asks what a machine is; it
   is a device. Remove the role picker from `web/views/networks.js` and the
   roles argument from the join path. *0.5d*
3. ~~**All-to-all peer discovery.**~~ Done. Devices exchange their current peer
   directories over authenticated mesh check-ins every 30 seconds. Merges keep
   the freshest complete record, never overwrite the local device, and
   converge without a coordinator when an offline peer returns.

### B. Completed independent Controller Server

The controller was moved out of this repository into the separately managed
PlainShow project `/var/www/html/projects/hugohansson/cluster-controller`. Live
editing is the only data-plane feature it adds; Git works without it.

4. ~~**Independent application and state.**~~ Done. The controller is its own Go
   module, embedded web application and PlainShow-isolated runtime. It creates
   none of the node's project, dataset, artifact or job state.
5. ~~**Global account attachment.**~~ Done. There are no peer-minted controller
   invitations. The owner logs in through `clusteradmin.plainshow.se`, can select
   only networks they own/administer, and the controller receives a durable
   process credential plus per-network relay secrets from the registry.
6. ~~**Live editing stays behind it.**~~ The controller relays live collaboration
   between browsers while each browser's node applies the operation to its own
   working tree. `internal/collab` remains the durable per-clone operation log,
   so controller loss removes live cross-node delivery and nothing else.
7. ~~**Controller management page.**~~ Done. The branded page shows global-login
   identity, selected networks, connection counts and registry health. Members
   of a supplied network can view it; only the controller owner can change the
   supplied-network set.

### C. Accounts across devices (revised: central account authority)

8. ~~**Plainshow Account Server.**~~ Done. `pscluster-admin` has its own root and
   SQLite database for global accounts, registration, login, device check-ins,
   and aggregate environment statistics. The first administrator needs a
   one-time bootstrap token, account sessions are stored as hashes, and admins
   can disable accounts from the embedded page. Its public URL is deployment
   config, not compiled into clients. The main deployment is
   `clusteradmin.plainshow.se` on the Plainshow Raspberry Pi.
9. ~~**Nodes sign in through the Account Server.**~~ Done. Nodes register or sign
   in against the configured authority, migrate legacy local ownership to the
   global account ID, keep the authority token at `keys/account.token`, and
   cache a local browser session. An Account Server outage prevents a new login
   but does not stop an existing session, jobs, peer communication, or git work.
   Join requests prove the global account to the receiving peer, while device
   keys remain independent identities.

### D. Completed networking

10. ~~**Account-managed Headscale enrollment.**~~ A successful global PlainShow
    login requests and consumes a short-lived, one-use Headscale key. Existing
    signed-in nodes retry in their normal check-in loop. Users never manage a
    Tailscale account, login URL or reusable invitation key. Legacy invitation
    fields remain decode-only so old alpha codes do not crash migration. *1d*
11. ~~**Warn on relayed links**~~ in the training preflight. `internal/tailnet`
    already reports which peers are relayed; a relayed link is somebody else's
    bandwidth. *0.5d*
12. ~~**Shrink `internal/mesh`.**~~ Only direct HTTPS, mTLS identity pinning,
    request signing, enrollment and task/data APIs remain; the reachability
    workaround and tunnel stack are gone.

### E. Completed runtime features

13. ~~**Terminal.**~~ Implemented as a `terminal` job using a PTY from util-linux
    `script`, with streamed output, input, remote placement, stop handling and
    the existing local `allow_terminal` policy.
14. ~~**Notebooks onto `jupyter_server`.**~~ The hand-written kernel is deleted.
    Plainshow launches and reverse-proxies the machine's installed Jupyter
    Server, gaining its kernels, widgets, rich MIME output and completions.

### Verified by running it, not by assertion

The resilience claim in section C is the one most likely to be quietly untrue,
so it has been exercised: stand up `pscluster-admin`, sign a node in against it,
kill the authority, and confirm the node keeps going.

```sh
pscluster-admin init  --root /tmp/acct --port 9988 --registration-open true
pscluster-admin serve --root /tmp/acct &
pscluster      init  --root /tmp/node --port 9977
# point account.server at http://127.0.0.1:9988, then serve, then:
#   POST /api/auth/setup with the bootstrap token   -> signed in
#   kill the admin process
#   GET  /api/overview   -> 200
#   POST /api/projects   -> 201
#   POST /api/jobs       -> runs and logs
#   POST /api/auth/login -> fails, naming the unreachable authority
```

Confirmed on this build. Sign-in also survives a **failed tailnet enrolment**:
the response carries `private_network_connected: false` and a reason rather than
refusing the login, which is the right way round — a private network that will
not come up must not lock somebody out of their own machine.

Two things this run also settled, both of which looked like defects and were
not. Startup with an unreachable authority is 223 ms, the same as with none
configured: the check-in is already backgrounded. And a node that has an
authority configured but no session answers 401 to everything, which is correct
rather than an outage symptom — configuring an authority is what turns
authentication on. Anyone re-testing this should know `curl -sf` treats 401 as
failure, which will make a readiness loop spin its whole retry budget and look
like a thirty-second startup hang.

### Known gaps in the Ray change

Recorded because they are the difference between "the code exists" and "somebody
can use it".

- **Never run against a real Ray.** `internal/ray` is verified against recorded
  response shapes, not a live cluster — Ray is not installed on the development
  Pi. The `/nodes?view=summary` fields and the `ray start` flags are the two
  things most likely to differ by Ray version. **Do this first on real
  hardware.**
- ~~**Ray is not installed by the installer.**~~ Done. `install_ray` creates a
  virtual environment at `<root>/runtime` and installs `ray[default]` into it,
  on Arch, Debian/Ubuntu, Fedora/RHEL and openSUSE. It goes in the node's own
  root because the distributions do not package Ray and modern ones refuse a
  system-wide pip install. `internal/ray` prefers that binary over one on PATH,
  so the version that runs is the one the node installed.
- **Nothing keeps data out of git.** The model is "data is a file in the project
  folder", which collides with projects travelling by git the moment somebody
  puts 18 GB in `data/`. Decide: write a `data/` entry into `.gitignore` on
  project creation and sync it separately over the tailnet, or say plainly that
  data is per-machine. Until then a large dataset will be committed.
- **Dead collaboration plumbing.** `web/lib/client.js` still carries the
  controller socket for live editing, now inert, and the Controller Server's
  collaboration half has no purpose. Its remaining reason to exist is the hosted
  web overview.

### The desktop program

`pscluster app` starts the node if it is not running and opens a window with no
browser furniture, through whichever Chromium-family browser is installed, or
`xdg-open` otherwise. A `.desktop` entry and an icon make it appear in the
application list.

It is deliberately **not** an embedded browser engine. Bundling one means cgo
and GTK development headers, which costs the single static binary that
cross-compiles for every machine in a cluster — a heavy price for a window. If
that trade ever looks worth making, the thing to reach for is a webview
wrapper, and `make dist` stops working the day it lands.

### F. External release gates

15. ~~**`internal/api` coverage.**~~ Core network, account authority, registry,
    collaboration, reachability and job paths have automated coverage. Add
    regressions for failures discovered during hardware testing rather than
    delaying the runnable build for a coverage percentage.
16. **Real multi-GPU validation.** Two CUDA machines, an actual `torchrun`.
    Needs hardware nobody has run this on. Expect the driver and CUDA mismatch
    handling to be wrong. **This is where the estimate is most likely to
    break.** *3d*
17. **Test a real published upgrade.** `v0.1.0-alpha.1` was the first release,
    alpha.2 corrected the Jobs-view syntax caught by Node 26, and alpha.3 also
    corrected version injection for the unprivileged Arch package builder.
    Install an earlier alpha and watch an actual node take alpha.3. The updater
    supports semantic prerelease ordering and authenticates every asset of a
    private release, including `checksums.txt`. *2d*
18. ~~**Install hardening and docs.**~~ `install.sh` installs the node or account
    service atomically, creates the matching one-root configuration, and writes
    systemd integration. Node installation provisions the full non-GPU runtime
    through pacman on Arch, apt on Debian/Ubuntu, dnf on Fedora/RHEL derivatives,
    or zypper on openSUSE; GPU/CUDA/PyTorch remain an explicit hardware
    preflight. Release binaries are static. Native pacman packaging and
    tagged-release automation are included, and the package's wrapper keeps the
    built-in updated binary active after reboot. The independent controller is
    built and run through its own PlainShow project/runtime. README and
    `CODEX.md` describe operation and handover.

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

## The completed Headscale migration

We drive the Tailscale client daemon against our own Headscale control plane;
we do not embed or recreate either. `internal/tailnet` shells out to `tailscale
status --json` and `tailscale up`. PlainShow owns the only user-facing identity:
an authenticated account calls `POST /api/tailnet/enrollment`, the account
service maps it to a stable Headscale user, and returns a short-lived one-use
key that the node consumes immediately. The key is never stored or displayed.

It has to be the real daemon rather than a library in this binary: torch and
NCCL open their own sockets in their own processes and need an interface the
kernel knows about. An in-process network stack would carry Plainshow's traffic
and nothing else, which was never the traffic that was stuck.

**Done**

1. `internal/tailnet` — probe, status, peers, relayed-or-direct, and reset/up
   against a configured Headscale URL. Parsing is tested against real
   `tailscale status --json` shape and errors redact authentication keys.
2. `GET /api/tailnet`, and joining advertises the tailnet address when there is
   one, so the address the mesh proves reachable is the address training uses.
3. `internal/accountserver` provisions one Headscale user per global account
   and mints one-use keys with a ten-minute lifetime. Node login consumes the
   key automatically and writes only `keys/tailnet.server`. Background check-in
   migrates already signed-in nodes and rate-limits failed retries.
4. ~~**Delete `internal/tunnel`.**~~ Done. The registry, dialer, trusted-marker
   path and `StartTunnels` are gone, and `clientForNode` simply dials the peer.
   Cross-network now requires tailscale, which is the honest requirement rather
   than a second half-working networking stack.
5. ~~**Simplify `internal/mesh`.**~~ mTLS, request signing, enrollment and the
   direct peer APIs remain; the unreachability workaround was removed.
6. ~~**Relayed-link warning in the training preflight.**~~ Training preflight
   reports DERP-relayed peer paths before launch.

## How a machine on another network is reached

Through the **Tailscale client connected to PlainShow's Headscale server**.
The installer adds and starts the client on both machines. Signing in to
PlainShow enrolls it automatically, and the address it receives is what
PlainShow records and dials — and the same address torch and NCCL use, so a link
the cluster proves reachable is the link training runs over.

There is no fallback and that is deliberate. A reverse tunnel used to carry
control traffic to unreachable machines; it worked, and it could never carry
training, because NCCL is a separate process needing a real interface. Keeping
it would have meant maintaining a second networking stack that solved half the
problem. `clientForNode` now says plainly that both machines should be signed
in to PlainShow, which is more useful than a timeout.

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
- **One machine can run two nodes.** The packaged service owns
  `/opt/plainshow-cluster`; a node someone runs themselves owns
  `~/.plainshow-cluster`. Separate databases, separate accounts, separate
  networks. The desktop prefers the system root, so a person who set their
  networks up under their own account sees an empty workspace and no error.
  Anything that renders "you have nothing" must name the node and root it is
  talking to.
- **`systemctl is-enabled` reports through the exit code.** "disabled" exits
  non-zero, so reading only the error turns a healthy answer into a failure.
  Read the output. `internal/api/service.go` asks the kernel which unit owns
  this process (`/proc/self/cgroup`) instead of assuming the packaged name,
  because a hand-started node belongs to no unit and that is a normal answer.
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
