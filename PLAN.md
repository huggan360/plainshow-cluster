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

### Stage 2 — Devices

- Account server: record each node's active network; `GET /api/devices`,
  `PUT /api/devices/{id}/network`, `POST /api/devices/{id}/sign-out`,
  `DELETE /api/devices/{id}`.
- Node: honours the desired network from its check-in, and signs itself out when
  told to.
- Settings becomes four tabs: **General · Devices · Updates · About**. Devices
  lists every machine on the account, online or not, which network it is in, and
  can move or remove it.
- Home shows every device on the account and its GPUs, not only this one.
- Networks page: choose which of your devices join that network.

### Stage 3 — Invite a person, not a code

- Account server: `network_invitation`; account search; create, list, accept and
  decline.
- Networks page shows invitations you have received.
- Join codes stay for headless machines that have no account session; the
  interface stops leading with them.

### Stage 4 — A project lives in one network, its files live per device

- Project placement moves to the account server: project → network, owner,
  members. The owner can move a project to another network.
- Joining a network shows its projects. **Nothing is downloaded automatically** —
  a machine only needs the files if work will run on it.
- The project's device list shows, per machine: online, and **has the files**.
  That is what tells you which machines are ready to run.
- Download onto any connected machine, subject to that machine's own policy.

### Stage 5 — Permission parity with plainshow.se

The capability model already matches (`internal/store/member.go`: view, code,
push, run, train, manage, plus an owner flag and a GitHub role memo). What does
not match is the presentation: the console offers a GitHub-shaped access level
on invite and a capability grid per person. `web/views/team.js` uses a checklist
in a dialog instead. Make it the console's layout.

Cloning a repository is not push access. Push and pull require being a
collaborator on GitHub, which already syncs both ways.

### Stage 6 — Live instead of polled

Devices currently check in once a minute. Add a socket from node to
`clusteradmin.plainshow.se` so device, network and invitation changes arrive
when they happen. Until then the interface is correct but up to a minute stale,
and should not pretend otherwise.

## Rules this plan does not get to break

- Bulk bytes never pass through the account service. It carries membership,
  placement and identity. Project files, datasets and logs go device to device.
- A device enforces its own policy. The account server can ask a machine to join
  a network; it cannot make it accept work.
- Everything a node stores stays under its one root.
