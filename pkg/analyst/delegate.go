package analyst

import (
	"context"
	"errors"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/agent"
)

// Delegation (ADR-0019 §4). The Lead agent doesn't act directly — it hands each part of the work to the
// right specialist via the `delegate` tool, which runs that specialist as a sub-agent to completion and
// returns its result. Delegation is the primitive playbooks (the plan DAG) will build on. Only the Lead
// has `delegate`; specialists don't, so delegation nests one level deep — no runaway trees.

// DelegationResult is what a sub-agent reports back to whoever delegated to it.
type DelegationResult struct {
	Profile   string   `json:"profile"`
	Answer    string   `json:"answer"`
	StepCount int      `json:"step_count"`
	ToolsUsed []string `json:"tools_used"`
}

// Delegate runs a specialist sub-agent (the given profile) synchronously on a task and returns its
// result. Tools in `authorize` run without further prompting inside the sub-agent — approving the
// delegation authorizes the specialist's toolset for this sub-task (delegation-level approval, §5); any
// other sensitive tool is denied within the sub-agent.
func (svc *Service) Delegate(ctx context.Context, projectID, profileID, task string, authorize []string) (DelegationResult, error) {
	if svc.provider == nil {
		return DelegationResult{}, errors.New("no LLM provider configured")
	}
	if task == "" {
		return DelegationResult{}, errors.New("delegate requires a task")
	}
	profile := svc.resolveProfile(ctx, profileID)
	loop := &agent.Loop{
		Provider:     svc.provider,
		Tools:        profile.ToolSet(),
		SystemPrompt: profile.SystemPrompt(),
		Approve:      Approver(authorize),
		Execute:      svc.executeFor(projectID),
		Audit:        svc.Audit,
		MaxSteps:     8,
	}
	res, err := loop.Run(ctx, task)
	if err != nil {
		return DelegationResult{}, err
	}

	seen := map[string]bool{}
	tools := []string{}
	for _, st := range res.Steps {
		if !seen[st.Call.Tool] {
			seen[st.Call.Tool] = true
			tools = append(tools, st.Call.Tool)
		}
	}
	return DelegationResult{Profile: profile.ID, Answer: res.Answer, StepCount: len(res.Steps), ToolsUsed: tools}, nil
}

// profileToolNames returns a profile's resolved tool allow-list.
func profileToolNames(p Profile) []string {
	ts := p.ToolSet()
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Name)
	}
	return names
}

// runDelegate handles the `delegate` tool: spawn the requested specialist on the task. Approving the
// delegate call authorizes that specialist's full toolset for the sub-task.
func (svc *Service) runDelegate(ctx context.Context, projectID string, call agent.ToolCall) (string, error) {
	target := stringArg(call, "agent")
	task := stringArg(call, "task")
	if target == "" || task == "" {
		return "", errors.New("delegate requires 'agent' and 'task'")
	}
	if target == "lead" || target == "generalist" {
		return "", fmt.Errorf("cannot delegate to %q; choose a specialist", target)
	}
	res, err := svc.Delegate(ctx, projectID, target, task, profileToolNames(svc.resolveProfile(ctx, target)))
	if err != nil {
		return "", err
	}
	return jsonify(res, nil)
}
