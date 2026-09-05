# Plan: the account owns the network, the device only participates

Status of each stage is kept current here. `CLAUDE.md` holds the settled rules;
this file holds the work that is not finished yet.

## The change in one sentence

Today a network lives in each node's own SQLite and is pushed up to
`clusteradmin.plainshow.se` as a report. It has to be the other way round: the
**account** owns networks, memberships, invitations and project placement, and a
device is a thing that participates in them.

That single inversion is what makes every item below possible. Signing in on a
new laptop shows your networks because the account knows them; a device list
exists because the account knows your devices; an invitation can be sent to a
person because the account knows who people are.

## Why the current shape cannot answer these questions

`internal/api/accountcheckin.go` only ever **pushes**: it reports networks the
node already has. Nothing pulls. So a fresh device signs in successfully and
sees an empty workspace, which is not a bug in a query — there is no code path
that could have told it.

The account server schema is already most of the way there. It has `account`,
`node` (with `owner_account_id` and `last_seen`), `network`, `network_member`
and `node_network`. What is missing is invitations, project placement, a
per-device desired network, and endpoints that read any of it back down.

## Stages

Each stage is shippable on its own and leaves the product working.

### Stage 0 — Stay signed in ✅

Closing the desktop signed you out. Wails 2.14 uses
`webkit_web_context_get_default()` and never calls
`webkit_cookie_manager_set_persistent_storage`, so WebKitGTK keeps cookies in
memory and drops them with the window. No option fixes it.

The session is therefore re-established from the credential that *does* persist:
the node's own account token under `<root>/keys`. A loopback request with no
session, on a node that holds a valid account token, is issued one. Controlled
by `auth.remember_this_machine`, and refused outright when the node is not bound
to loopback, because then "local" no longer means "the person at the keyboard".

### Stage 1 — Your networks follow your account ✅

- Account server: `GET /api/networks/mine` — every network this account belongs
  to, with role and the management key needed to participate.
- Node: pulls on sign-in and on every check-in, and materialises memberships it
  does not have.
- Result: sign in as yourself on any machine and your networks are there.

### Stage 2 — Devices ✅

The heartbeat became two-way: a check-in reports the network a device is working
in and comes back with what it should do next. Nothing reaches into a machine —
each action records a row the machine reads and acts on itself, which is what
keeps its own policy the last word.

- `GET /api/devices`, `PUT /api/devices/{id}/network`,
  `POST /api/devices/{id}/sign-out`, `DELETE /api/devices/{id}`, all proxied
  through the node so the browser holds no account credential and stays on one
  origin.
- Settings is four tabs: **General · Devices · Updates · About**.
- Home lists every device on the account and its GPUs.

Two things worth remembering. A completed move is acknowledged by the device
reporting the network it now works in, or the interface shows a pending move
that already happened. And a sign-out is delivered exactly once: a device that
signs out stops checking in, so waiting for an acknowledgement that can never
arrive would sign it out again every time somebody signed back in.

Removal is bookkeeping, not revocation — a machine with a valid credential
re-registers on its next check-in — and the interface says so rather than
letting somebody think a lost laptop has been cut off.

### Stage 3 — Invite a person, not a code ✅

An invitation is addressed to an account and waits until that person looks, from
whichever machine they are on. Accepting is what creates the membership row —
the same row a join code would have written — so adoption, roles and sync are
unchanged downstream, and the node adopts immediately rather than on the next
heartbeat.

Ownership cannot be offered, only owners and admins can invite, an invitation id
is not a capability (only the addressee can answer it), and re-inviting somebody
who declined is ordinary rather than a conflict.

Join codes remain for headless machines with no browser to sign in on, under
"Create a machine code" in a network's settings.

### Stage 4 — A project lives in one network, its files live per device ✅

A project belongs to exactly one network — that is what makes "who can see this"
answerable, and what lets a repository's collaborator list mean one thing — so
its owner can move it, and the files move with it. A row that moved without its
directory is a project that has quietly stopped working.

Files are a separate question from membership. Joining a network must not drag
every repository and every dataset onto a laptop, so nothing is downloaded
automatically; the files travel when somebody decides a machine should have
them, in either direction, over the existing mesh archive.

The project's Devices tab reads the disk rather than the database. Online and
ready are different things, and conflating them is how somebody submits a run to
a machine that has nothing to run.

A new `allow_project_sync` policy governs receiving files, device-wide and per
network, because a machine can be willing to run work and still not want
somebody's training data written to its disk. Both ceilings apply and a network
can only narrow the machine's own.

Two traps worth keeping. `Config.Version` exists because a field added inside
`Memberships` cannot be defaulted: `Load` merges onto `Defaults()`, so a missing
top-level key keeps its default, but every slice element is built from zero. The
version has to be read from the raw document *before* that merge, or a file
written years ago reports as current. And project membership is keyed by GitHub
login — or the node name when no GitHub is connected — never the Plainshow
account username, so ownership is checked against every name the device answers
to.

### Stage 5 — Permission parity with plainshow.se ✅

The capability model already matched. The presentation did not: the console
offers a GitHub-shaped access level on invite and a capability grid on each
person's row, while `team.js` hid everything behind a dialog. It is the
console's layout now — the same five access levels, the same six capabilities,
the same wording, and one click per permission, because "what can this person
do" should be readable without opening anything.

Cloning a repository is not push access. Push and pull require being a
collaborator on GitHub, which already syncs both ways.

### Stage 6 — Live instead of polled  ◐ repository complete, deployment unverified

**Read this before touching it. It is written and it builds, but it has never
had two machines on it.**

Done:

- `internal/accountserver/events.go` — a per-account wake-up hub and
  `GET /api/events`. Sends never block, so a device that stopped reading cannot
  wedge the write path of whoever is signing it out. Pings every 30s.
- `internal/accountserver/deviceapi.go` notifies at six points: the three device
  operations wake that account, an invitation wakes the person it is addressed
  to, an answer wakes both ends, a revocation wakes the invitee.
  `Store.RevokeInvitation` now returns the invitee id for that reason.
- `internal/accountclient/watch.go` — `Client.Watch`, with a 90-second read
  deadline refreshed by the server's pings. That deadline is the point: a
  half-open connection through a proxy or a sleeping laptop is the failure this
  path will actually hit, and without it a device thinks it is live and hears
  nothing ever again.
- `Server.StartAccountWatch` on the node, with exponential backoff to two
  minutes, called from `cmd/pscluster/main.go`.
- The browser reloads Networks on `invitations.changed` and Devices on
  `devices.changed`.

Repository work completed on 2026-09-05:

- The production Apache reference config now upgrades `/api/events` to
  WebSocket instead of sending it through the ordinary HTTP proxy.
- `Client.Watch` is covered across a dropped connection and a fresh connection.
  A successful idle handshake is distinguished from a failed handshake, so a
  healthy connection resets node reconnect backoff even when it carried no
  event before closing.

**Operational work left to do, in order:**

1. **Deploy the Apache reference change.** The repository config contains the
   required rule, but this development session did not modify or reload the
   production server configuration:

   ```apache
   RewriteCond %{HTTP:Upgrade} =websocket [NC]
   RewriteRule ^/(.*) ws://127.0.0.1:10002/$1 [P,L]
   ```

   Put it above the existing `ProxyPass /`. `proxy_wstunnel_module` is already
   loaded. `apache2ctl configtest` then `systemctl reload apache2`.
2. **Verify with two nodes and an account server**, the way stages 1–3 were.
   Sign a device out from another machine and time it: it should be immediate
   rather than up to a minute. Then kill the account service mid-connection and
   confirm the node backs off, reconnects, and never loses the heartbeat.

### Stage 7 — No global workspace selection ✅ code complete

The account UI is simultaneous across networks: Networks and Projects list all
account memberships, the top bar has no network selector/status, projects are
addressed by stable id, and create/clone explicitly chooses a network. Project
Run, job status/log/stop, file transfer and Cowork controller connections all
use the project's own network. Jobs aggregates every reachable Ray head.

A physical machine still runs one local raylet so its finite CPU/GPU resources
cannot be advertised to multiple schedulers at once. That internal compute
assignment is a device resource setting, not an account workspace selection;
changing it never hides networks or projects.

**The boundary this sits next to.** Non-negotiable #4 says the management plane
has no collaboration WebSocket and proxies no bulk bytes.
`server_test.go` asserts `/ws` returns 404 and still does — this endpoint is
`/api/events` and carries one word per message, never the change itself, so a
woken device re-asks over its own authenticated call. Keep it that way. The
moment something tries to send *content* down this channel, it has become the
thing that rule forbids.

## Rules this plan does not get to break

- Bulk bytes never pass through the account service. It carries membership,
  placement and identity. Project files, datasets and logs go device to device.
- A device enforces its own policy. The account server can ask a machine to join
  a network; it cannot make it accept work.
- Everything a node stores stays under its one root.
