//go:build race

package e2e

// The race detector slows everything down 5-10x; latency targets only apply
// to normal builds.
const raceEnabled = true
