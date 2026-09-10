# Plainshow Cluster 0.1.6

Version: `v0.1.6`.

## What changed

- Plainshow now verifies that this machine's exact private IP appears as an
  alive Ray node. A reachable remote head is no longer mistaken for a healthy
  local worker after that worker has disconnected.
- Automatic joins are checked for stability before being recorded. A missing
  raylet is cleaned up and rejoined automatically, while a short dashboard
  hiccup is confirmed before any healthy process is restarted.
- The manual Start/attach control has been removed. Eligible machines join from
  the network-wide Ray switch, and failures use the guided repair dialog.
- A worker disappearing during a diagnostic now gets a concise reconnecting
  message instead of exposing Ray's internal node-affinity traceback.
- Python 3.14 patch releases remain compatible with each other; meaningful Ray
  or Python major/minor version differences remain blocked and repairable.

## Arch install or update

Download `plainshow-cluster-0.1.6-1-x86_64.pkg.tar.zst` and
`arch-checksums.txt`, close Plainshow Cluster, then run:

```sh
sha256sum -c arch-checksums.txt
sudo pacman -U ./plainshow-cluster-0.1.6-1-x86_64.pkg.tar.zst
```

The package restarts the node service during an upgrade. Updating a separate
central account server remains a separate deployment operation.
