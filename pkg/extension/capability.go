// Package extension loads third-party packages (ADR-0003, ADR-0013): signed, digest-pinned
// directories that add container-backed capabilities and methodology packs through the same
// registries the built-ins use. Container capabilities run in the identical sandbox (ADR-0004).
package extension

import (
	"fmt"
	"strings"
	"time"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/disposition"
	"github.com/opensecbench/opensecbench/pkg/runner"
)

// ContainerCapability is a data-driven capability declared entirely in a manifest — no Go code. It
// runs a container image with a templated command in the standard sandbox.
type ContainerCapability struct {
	ID              string   `json:"id"`
	Version         string   `json:"version"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Image           string   `json:"image"` // pinned digest ref preferred
	Cmd             []string `json:"cmd"`   // tokens may contain {{param}} / {{target}}
	Network         string   `json:"network"`
	MountSource     bool     `json:"mount_source"` // mount the target dir read-only
	MountTarget     string   `json:"mount_target"` // default /src
	Workdir         string   `json:"workdir"`
	OutputName      string   `json:"output_name"`
	OutputMediaType string   `json:"output_media_type"`
	OKExitCodes     []int    `json:"ok_exit_codes,omitempty"`
	TargetParam     string   `json:"target_param,omitempty"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty"`
	MemoryMB        int      `json:"memory_mb,omitempty"`
	CPUs            float64  `json:"cpus,omitempty"`
	// Dispositions route this capability's observations post-run (ADR-0028).
	Dispositions []disposition.Disposition `json:"dispositions,omitempty"`
}

// Manifest returns the capability manifest surfaced to users and the agent.
func (c ContainerCapability) Manifest() capability.Manifest {
	return capability.Manifest{
		ID:              c.ID,
		Version:         c.Version,
		Title:           c.Title,
		Description:     c.Description,
		OutputName:      c.OutputName,
		OutputMediaType: c.OutputMediaType,
		OKExitCodes:     c.OKExitCodes,
		TargetParam:     c.TargetParam,
		Dispositions:    c.Dispositions,
	}
}

// Plan builds the sandboxed RunSpec, substituting {{param}} from inputs and {{target}} from the
// target param, and mounting the source directory when requested.
func (c ContainerCapability) Plan(in capability.Input) (runner.RunSpec, error) {
	if c.Image == "" {
		return runner.RunSpec{}, fmt.Errorf("extension capability %q: image required", c.ID)
	}
	subs := map[string]string{}
	for k, v := range in.Params {
		subs[k] = fmt.Sprint(v)
	}
	if c.TargetParam != "" {
		if t, ok := in.Params[c.TargetParam].(string); ok {
			subs["target"] = t
		}
	}

	cmd := make([]string, len(c.Cmd))
	for i, tok := range c.Cmd {
		cmd[i] = substitute(tok, subs)
	}

	spec := runner.RunSpec{
		Image:    c.Image,
		Cmd:      cmd,
		Network:  orDefault(c.Network, "none"),
		Workdir:  c.Workdir,
		MemoryMB: orInt(c.MemoryMB, 1024),
		CPUs:     orFloat(c.CPUs, 1),
	}
	if c.TimeoutSeconds > 0 {
		spec.Timeout = time.Duration(c.TimeoutSeconds) * time.Second
	} else {
		spec.Timeout = 10 * time.Minute
	}
	if c.MountSource {
		if in.TargetDir == "" {
			return runner.RunSpec{}, fmt.Errorf("extension capability %q: target directory required", c.ID)
		}
		spec.Mounts = []runner.Mount{{Source: in.TargetDir, Target: orDefault(c.MountTarget, "/src"), ReadOnly: true}}
	}
	return spec, nil
}

func substitute(tok string, subs map[string]string) string {
	for k, v := range subs {
		tok = strings.ReplaceAll(tok, "{{"+k+"}}", v)
	}
	return tok
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
func orInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
func orFloat(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}

var _ capability.Capability = ContainerCapability{}
