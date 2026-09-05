# Snap package

The release workflow builds native classic snaps for amd64 and arm64 and adds
them to the matching GitHub prerelease. The snap contains the desktop, node,
Git, GTK/WebKit and pinned Ray. Tailscale remains a host service so it can
create and manage the machine's network interface.

## Local build and test

On a disposable Ubuntu build machine with Snapcraft installed:

```sh
snapcraft
sudo snap install --dangerous --classic ./plainshow-cluster_*.snap
snap run plainshow-cluster
sudo plainshow-cluster.pscluster status
```

## Publish to the Snap Store

1. Create a developer account at <https://snapcraft.io/account> and enable
   two-factor authentication.
2. Run `snapcraft login` on your workstation.
3. Register the name with `snapcraft register plainshow-cluster`.
4. Request classic-confinement approval for that registered snap. Classic is
   required because Ray runs project environments and host GPU libraries.
5. Download both `.snap` files from the GitHub release and upload them to edge:

   ```sh
   snapcraft upload plainshow-cluster_0.1.1-alpha.2_amd64.snap --release=edge
   snapcraft upload plainshow-cluster_0.1.1-alpha.2_arm64.snap --release=edge
   ```

The uploads become architecture-specific revisions of the same release. Test
with `sudo snap install plainshow-cluster --classic --edge`. This alpha's
`grade: devel` permits edge and beta. A future stable publication must first
change the recipe to `grade: stable`.
