// Package version reports the OpenSecBench build version.
package version

// Version is overridden at build time via -ldflags "-X ...version.Version=...".
var Version = "0.0.0-dev"
