// Package version exposes build information injected via -ldflags.
package version

// Set with: -ldflags "-X github.com/D4ND3R/Contest-Management-System/internal/version.Version=..."
var (
	Version = "dev"
	Commit  = "unknown"
)

// String returns "version (commit)".
func String() string { return Version + " (" + Commit + ")" }
