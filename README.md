# Plainshow Cluster

Turn a set of ordinary computers into one collaborative AI development and
training environment.

The node is a single binary serving a web workspace where you create projects,
edit code, run it on any joined machine, and watch output live. The enterprise
service supplies shared accounts, network-key recovery and live collaboration;
project data and compute still move directly between nodes.

```
git clone https://github.com/huggan360/plainshow-cluster.git
cd plainshow-cluster
make build
PSCLUSTER_ACCOUNT_SERVER=https://clusteradmin.plainshow.se ./pscluster init
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

## How it fits together

**Every device is equal.** Any machine that joins a network can run tasks and
talks directly to every other machine on it. There is no master, no coordinator,
and no machine whose being offline is everybody's problem. What a device is
willing to do — accept jobs, expose a GPU, allow a terminal — is its own local
setting, and nothing remote can widen it.

**Git is the source of truth for projects**, and every device keeps a full
clone. Two people can work while disconnected and reconcile with a real merge.

**The Plainshow enterprise service is the one intentional master service.** In
the main environment it runs at `clusteradmin.plainshow.se` on the Raspberry Pi.
Its SQLite database stores global accounts, membership roles, network recovery
keys and aggregate device health. The same process is the default live-editing
WebSocket controller. It never runs jobs or stores projects, commands, logs,
datasets, artifacts or peer addresses.

`pscluster-controller` remains available when a network wants a separate,
self-hosted collaboration relay. Without either controller, Git collaboration
continues to work offline.

Machines on different networks find each other through **tailscale**, which
Plainshow drives rather than reimplements.


## Commands

```sh
pscluster init [--root DIR] [--name NAME] [--cluster NAME]
               [--port N] [--bind ADDR]
pscluster serve [--root DIR]
pscluster status [--root DIR]
pscluster run [--project NAME] <command...>
pscluster config [show | get KEY | set KEY VALUE | path | root]

pscluster network [list | use ID | key ID]
pscluster invite [--role member] [--network ID]
pscluster join CODE [--endpoint URL]
pscluster controller invite [--network ID]
pscluster github [status | connect | disconnect]
pscluster update [check | status | apply]
pscluster version

pscluster-controller init [--root DIR] [--name NAME] [--bind ADDR] [--port N]
pscluster-controller serve [--root DIR]
pscluster-controller attach CODE --advertise HTTPS_URL
pscluster-controller status [--root DIR]
pscluster-controller reset-admin-token [--root DIR]

pscluster-admin init [--root DIR] [--public-url HTTPS_URL]
pscluster-admin serve [--root DIR]
pscluster-admin status [--root DIR]
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

Git is not the live-editing transport: per-keystroke commits would be unusable
as both a sync protocol and a history. Git carries a project between machines
that were not online at the same time. Every device keeps a full clone, so work
continues while other devices are unreachable and divergence is reconciled by
a real three-way merge rather than a last-writer-wins guess.

## Building

Requires Go 1.24+ and Node (only to check the interface's module graph; there is
no frontend build step). `git` is optional but recommended — without it projects
have no history.

```sh
make check      # fmt, vet, module graph, tests
make build      # ./pscluster for this machine
make dist       # linux/amd64 and linux/arm64
make run        # throwaway node in ./.devnode
sudo make install             # node
sudo make install-controller  # optional collaboration controller
sudo PSCLUSTER_ADMIN_URL=https://clusteradmin.example make install-admin
```

`make race` runs the suite under the race detector. It needs a kernel with a
48-bit VMA; some arm64 boards (including the Raspberry Pi 5) report 47 and
ThreadSanitizer refuses to start, which is why it is not part of `make check`.

The interface is hand-written ES modules and CSS, embedded into the binary. What
is served is exactly what is in `web/`. Typefaces are bundled too, so a node with
no internet access renders identically to one with it.

`make build` also produces `pscluster-controller`, the optional standalone HTTPS
service for live collaboration and cross-network overview. It has a separate
root, identity, certificate and configuration, and it cannot run node jobs.

It also produces `pscluster-admin`, the enterprise account authority, network
registry, default collaboration relay and global statistics page. The service
uses a dedicated SQLite database and binds to loopback for a public TLS reverse
proxy. Its configured URL in the main Plainshow environment is
`https://clusteradmin.plainshow.se`; that hostname is deployment configuration
rather than a client-side constant.

The production Apache template is
`deploy/clusteradmin.plainshow.se.conf`. The admin root is
`/opt/plainshow-cluster-admin`; back up `accounts.db`, `accounts.db-wal`,
`accounts.db-shm` and `admin.yaml` together while the service is stopped, or use
SQLite's online backup tooling.

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

## Runtime programs

The node does not silently install tools. Install the programs for the features
you use: `git` for project history, Tailscale for cross-network peers,
util-linux `script` for interactive terminals, `jupyter_server` for notebooks,
and PyTorch/`torchrun` plus the appropriate CUDA stack for distributed training.
Missing optional tools produce an actionable message in the interface.

To attach the optional controller, mint a code on any network-owner node and
redeem it on the always-online machine:

```sh
pscluster controller invite
pscluster-controller attach 'psc1_…' --advertise https://controller.example
```

## Implementation status

Working now: the complete single-machine workspace, Jupyter-backed notebooks,
interactive terminal jobs, GitHub project/team flows, multiple independent network memberships,
single-use join codes, pinned TLS and Ed25519-authenticated peer requests,
project transfer, remote jobs with live logs, revisioned collaborative editing
with offline replay, immutable content-addressed datasets, worker placement,
gang reservations, PyTorch `torchrun` plans, checkpoints, and a bandwidth
advisor. The interface uses the same visual language as the existing Plainshow
console and is embedded in the binary.

Machines on different networks find each other through **tailscale**, which
Plainshow drives rather than reimplements. When tailscale is connected,
Plainshow records the address it got. That address is what training uses too,
so a link the cluster proves reachable is the link NCCL will run over.

A machine with no reachable address of its own — an ordinary desktop behind NAT,
or behind carrier-grade NAT — is reached at its tailnet address. There is no
Plainshow-specific fallback: cross-network needs tailscale on both machines, and
the interface says so rather than timing out. The
distributed launcher is implemented and its lifecycle is tested with local and
two-node integration runs; a real multi-GPU PyTorch run still needs validation
on two CUDA machines before v1.0 is tagged.

One installation can belong to several networks. Each membership has its own
projects, accounts, machines and worker policy, and the active network is
selected from the top bar. Devices exchange their signed peer directories
directly, so every reachable pair converges without a coordinator.
