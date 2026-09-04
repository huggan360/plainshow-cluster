# Arch package

From an Arch source checkout, build and install a native package with:

```sh
sudo pacman -S --needed base-devel go nodejs
make arch-package
(cd dist && sha256sum -c arch-checksums.txt)
sudo pacman -U dist/plainshow-cluster-*.pkg.tar.zst
```

The package depends on Git, Tailscale, util-linux, CA certificates and
Jupyter Server, so pacman resolves the complete node runtime. Its install hook
initializes `/opt/plainshow-cluster`, enables both `tailscaled` and the node,
and preserves all node data when the package is removed.

Tagged releases build and attach the x86_64 pacman package automatically.
Installing it with the exact command `pacman -S plainshow-cluster` additionally
requires publishing the package and a `repo-add` database at a stable HTTPS
repository. The package itself is ready for that repository; its final public
license and repository URL must be chosen before opening a public pacman/AUR
feed.
