# Plainshow Cluster 0.1.5

Version: `v0.1.5`.

## What changed

- Python patch releases within the same minor line can share Ray safely, so
  Python 3.14.6 and 3.14.7 no longer split or reject a cluster. Different Ray
  versions and different Python major/minor versions remain blocked.
- Ray head announcements now use a monotonic network generation instead of the
  clocks of different computers, preventing competing one-node clusters.
- Eligible devices join automatically after Ray is switched on; no manual
  attach action is required.
- An affected machine now opens a guided Ray repair dialog when automatic join
  fails. It shows progress, verifies or repairs Plainshow's private Ray 2.58.0
  environment, retries the join, and confirms completion.
- Automatic join failures remain visible on the network page and in the node
  journal instead of disappearing into the background retry loop.

## Arch install or update

Download `plainshow-cluster-0.1.5-1-x86_64.pkg.tar.zst` and
`arch-checksums.txt`, close Plainshow Cluster, then run:

```sh
sha256sum -c arch-checksums.txt
sudo pacman -U ./plainshow-cluster-0.1.5-1-x86_64.pkg.tar.zst
```

The package restarts the node service during an upgrade. Updating a separate
central account server remains a separate deployment operation.
