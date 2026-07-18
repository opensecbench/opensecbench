//go:build !desktop

package main

import "fmt"

// This stub lets `go build ./...` and CI succeed without the desktop toolchain (webkit/gtk).
// The real desktop entrypoint is in main.go behind the `desktop` build tag, which Wails sets
// automatically for `wails dev` / `wails build`.
func main() {
	fmt.Println("Build the OpenSecBench desktop app with Wails: `wails dev` or `wails build`.")
	fmt.Println("For the headless control plane, use ./cmd/daemon; for the CLI, ./cmd/osb.")
}
