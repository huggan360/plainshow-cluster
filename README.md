# Plainshow Cluster

Turn a set of ordinary computers into one collaborative AI development and
training environment.

This is the node: a single binary that serves a web workspace where you create
projects, edit code, run it, and watch the output live. Today it runs one
machine completely. The networking, collaboration and distributed-training
layers build on top of it.

```
git clone https://github.com/huggan360/plainshow-cluster.git
cd plainshow-cluster
make build
./pscluster init
./pscluster serve
```

Then open the address it prints — `http://127.0.0.1:9999` by default.

## Everything lives in one directory

A node writes nothing scattered across the machine. One root holds all of it:

```
<root>/
├── bin/pscluster        the binary
├── config.yaml          settings
├── cluster.db           state: machines, projects, job history
├── keys/                identity
├── projects/            your code
├── datasets/            training data
├── artifacts/           checkpoints and outputs
├── logs/jobs/           one log file per job
└── run/                 pid file
```

Move that directory and you have moved the node. Delete it and the machine is
clean. The only two files that may sit outside it are a symlink onto `PATH` and
a systemd unit, both optional, both offered by `install.sh` — and the unit file
itself lives in the root and is only linked from `/etc/systemd/system`.

The root is chosen in this order: `--root`, then `PSCLUSTER_ROOT`, then
`/opt/plainshow-cluster` when running as root and `~/.plainshow-cluster`
otherwise.

## Roles

A machine carries any combination of three roles.

| Role | Holds | Notes |
|---|---|---|
| `master` | State, project files, datasets, artifacts. Serves the interface. | The data home. |
| `worker` | Nothing canonical — caches and running processes. | Contributes CPU, GPU and disk. |
| `controller` | Node keys and relay. No project data, no jobs. | An always-on box that helps machines find each other. Optional. |

A single machine runs `master` and `worker` and is complete on its own.

`master` is the data home, not the coordinator of a training run. That
coordinator is chosen per job and shown as `rank 0`; it is never something you
configure.

## Commands

```sh
pscluster init [--root DIR] [--name NAME] [--cluster NAME]
               [--roles master,worker] [--port N] [--bind ADDR]
pscluster serve [--root DIR]
pscluster status [--root DIR]
pscluster run [--project NAME] <command...>
pscluster config [show | get KEY | set KEY VALUE | path | root]

pscluster network [list | use ID]
pscluster invite [--role member] [--network ID]
pscluster join CODE [--endpoint URL]
pscluster github [status | connect | disconnect]
pscluster update [check | status | apply]
pscluster version
```

The commands after `config` talk to this machine's own running daemon, so a
headless worker can be joined and managed without a browser. They authenticate
with a token inside the install root that only the owner can read.

Nothing about a particular machine is compiled in. `init` probes rather than
assumes: it picks the first free port upward from 9999, reads the hostname, and
enumerates GPUs. Every value it chooses is written to `config.yaml` and can be
changed.

## What this machine will allow

Worker limits are enforced **on the machine, by the machine**, from
`config.yaml`. Nothing in the cluster can widen them, and a job that asks for
more than the policy permits is refused here rather than quietly trimmed. That
is what makes it reasonable to run someone else's code on your own desktop.

Terminal access is off by default.

```sh
pscluster config set worker.enabled false        # stay in the cluster, run nothing
pscluster config set worker.allow_terminal true
```

## Projects and history

A project is a directory under `<root>/projects/`. Creating one gives you a
folder, a starter file and a git repository with an initial commit.

Git is not the live-editing transport — that will be a CRDT over a socket, and
per-keystroke commits would be unusable as both a sync protocol and a history.
Git is what carries a project between machines that were not online at the same
time: every node keeps a full clone, work continues while the master is
unreachable, and divergence is reconciled by a real three-way merge rather than
a last-writer-wins guess.

## Building

Requires Go 1.24+ and Node (only to check the interface's module graph; there is
no frontend build step). `git` is optional but recommended — without it projects
have no history.

```sh
make check      # fmt, vet, module graph, tests
make build      # ./pscluster for this machine
make dist       # linux/amd64 and linux/arm64
make run        # throwaway node in ./.devnode
```

`make race` runs the suite under the race detector. It needs a kernel with a
48-bit VMA; some arm64 boards (including the Raspberry Pi 5) report 47 and
ThreadSanitizer refuses to start, which is why it is not part of `make check`.

The interface is hand-written ES modules and CSS, embedded into the binary. What
is served is exactly what is in `web/`. Typefaces are bundled too, so a node with
no internet access renders identically to one with it.

## Layout

```
cmd/pscluster/     CLI and daemon entry point
internal/
  config/          install layout and settings
  store/           SQLite state
  projectfs/       the filesystem side of a project
  gitrepo/         git plumbing
  jobs/            process supervision and log streaming
  events/          the event hub behind the WebSocket
  sysinfo/         CPU, memory, disk and GPU probing
  api/             HTTP API and the web server
  version/
web/               the interface, embedded
scripts/           checks a compiler cannot do
```

## Current alpha status

Working now: the complete single-machine workspace, persistent Python
notebooks, GitHub project/team flows, multiple independent network memberships,
single-use join codes, pinned TLS and Ed25519-authenticated peer requests,
project transfer, remote jobs with live logs, revisioned collaborative editing
with offline replay, immutable content-addressed datasets, worker placement,
gang reservations, PyTorch `torchrun` plans, checkpoints, and a bandwidth
advisor. The interface uses the same visual language as the existing Plainshow
console and is embedded in the binary.

Machines on different networks find each other through **tailscale**, which
Plainshow drives rather than reimplements: install it, and joining a network
signs this machine into the tailnet and records the address it got. That address
is what training uses too, so a link the cluster proves reachable is the link
NCCL will run over.

A machine with no reachable address of its own — an ordinary desktop behind NAT,
or behind carrier-grade NAT — is reached at its tailnet address. There is no
Plainshow-specific fallback: cross-network needs tailscale on both machines, and
the interface says so rather than timing out. The
distributed launcher is implemented and its lifecycle is tested with local and
two-node integration runs; a real multi-GPU PyTorch run still needs validation
on CUDA machines before the Phase 5 alpha is published.

One installation can belong to several networks. Each membership has its own
projects, accounts, machines, roles and worker policy, and the active network is
selected from the top bar.
