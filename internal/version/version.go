// Package version carries the build identity of the pscluster binary.
package version

// Version is the semantic version of this build. Overridden at release time
// with -ldflags "-X .../internal/version.Version=x.y.z".
var Version = "0.1.1-alpha.1-dev"

// Commit is the git revision this binary was built from, when known.
var Commit = "unknown"

// Product is the user-facing name.
const Product = "Plainshow Cluster"
