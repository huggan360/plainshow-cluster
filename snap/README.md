# Snap package

The release workflow builds native classic snaps for amd64 and arm64 and adds
them to the matching GitHub prerelease. The snap contains the desktop, node,
Git, GTK/WebKit and the pinned Ray runtime. Tailscale remains a host service so
that it can create the machine's network interface.

## Local build and test

On a disposable Ubuntu build machine with Snapcraft installed:

```sh
snapcraft
sudo snap install --dangerous --classic ./plainshow-cluster_*.snap
snap run plainshow-cluster
sudo plainshow-cluster.pscluster status
```

Remove a test installation with `sudo snap remove plainshow-cluster --purge`.

## Publish to the Snap Store

1. Create a developer account at <https://snapcraft.io/account> and enable
   two-factor authentication.
2. Run `snapcraft login` on your workstation.
3. Register the name with `snapcraft register plainshow-cluster`.
4. Request classic-confinement approval for the registered snap in the store.
   Classic is required because Ray runs project environments and accesses host
   GPU libraries. Wait for approval before publishing broadly.
5. Download both `.snap` files from the GitHub release and upload the alpha to
   the edge channel:

   ```sh
   snapcraft upload --release=edge plainshow-cluster_0.1.1-alpha.1_amd64.snap
   snapcraft upload --release=edge plainshow-cluster_0.1.1-alpha.1_arm64.snap
   ```

The two uploads become architecture-specific revisions of the same release.
Test with `sudo snap install plainshow-cluster --classic --edge`. This alpha's
`grade: devel` permits edge and beta; promote tested revisions to beta in the
Snap Store dashboard or with `snapcraft release`. A future stable build must
first change the recipe to `grade: stable`.
