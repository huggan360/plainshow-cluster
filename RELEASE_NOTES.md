# Plainshow Cluster 0.1.4

Version: `v0.1.4`.

## What changed

- Ray now runs from Plainshow Cluster's managed Python environment at
  `/opt/plainshow-cluster/runtime`, including submitted jobs and diagnostics.
- The Arch installer verifies that Ray 2.58.0 can actually be imported and
  reports an installation failure instead of leaving a broken runtime behind.
- Ray on/off is network-wide. Peer sockets propagate the state immediately so
  every member sees the same control and connected nodes reconcile promptly.
- Deleting a project now removes its account catalogue entry before erasing its
  local directory, preventing deleted projects from returning as ghost cards.
  Members can leave a shared project without deleting the owner's project.
- Ray preset choices now use the same compact, centered visual language as the
  main Ray toggle.

## Arch install or update

Download `plainshow-cluster-0.1.4-1-x86_64.pkg.tar.zst` and
`arch-checksums.txt`, close Plainshow Cluster, then run:

```sh
sha256sum -c arch-checksums.txt
sudo pacman -U ./plainshow-cluster-0.1.4-1-x86_64.pkg.tar.zst
```

The package restarts the node service during an upgrade. Updating a separate
central account server remains a separate deployment operation.
