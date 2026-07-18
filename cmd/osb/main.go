// Command osb is the OpenSecBench command-line client. It is a thin client against the
// control-plane HTTP API (ADR-0001).
//
// TODO(P1+): implement subcommands (project, target, task, ...) over the control-plane API.
package main

import (
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/version"
)

func main() {
	fmt.Printf("osb %s\n", version.Version)
}
