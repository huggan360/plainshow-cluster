# Arch package

On Arch Linux, install the build requirements and produce a native package:

```sh
sudo pacman -S --needed base-devel go nodejs gtk3 webkit2gtk-4.1
make arch-package
(cd dist && sha256sum -c arch-checksums.txt)
sudo pacman -U dist/plainshow-cluster-*.pkg.tar.zst
```

The package installs the native **Plainshow Cluster** application, node,
Tailscale client and desktop libraries. Its install hook initializes
`/opt/plainshow-cluster`, installs pinned Ray in a private virtual environment,
and enables the Tailscale and node services. Plainshow account login enrolls
the client with `tailnet.plainshow.se`; no Tailscale account is needed.

Tagged releases attach an x86_64 package automatically. Installing with
`pacman -S plainshow-cluster` additionally requires a signed public package
repository and database; until that repository is published, use `pacman -U`.
