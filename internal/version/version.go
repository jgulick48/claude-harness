// Package version holds build identity shared by the core and its protocol.
package version

// Version is overridable at build time via -ldflags "-X ...internal/version.Version=...".
var Version = "0.1.0-dev"

// Protocol is the core <-> UI protocol version (see docs/CORE_PROTOCOL.md).
const Protocol = 4
