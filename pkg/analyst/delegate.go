package analyst

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// agentSem caps how many sub-agents run concurrently across the whole process (P4). It bounds the
// fan-out of delegation and concurrent plans so a burst of scheduled/triggered runs can't overload the
// host or the provider. OSB_AGENT_MAX_CONCURRENT overrides the default.
var agentSem = newAgentSem()

func newAgentSem() chan struct{} {
	return make(chan struct{}, envInt("OSB_AGENT_MAX_CONCURRENT", 4))
}

// subAgentMaxSteps is the tool-turn budget for a delegated sub-agent (ADR-0047). A sub-agent runs a whole
// sub-task to completion, so it needs more room than an interactive turn — a real recon/scan step easily
// exceeds a handful of tool calls. OSB_AGENT_MAX_STEPS overrides it.
func subAgentMaxSteps() int { return envInt("OSB_AGENT_MAX_STEPS", 16) }

// maxDelegationDepth bounds how deep delegation may nest (ADR-0047): the Lead delegates to a specialist,
// which may itself delegate, and so on, up to this many levels. Bounded depth × the concurrency cap keeps
// the delegation tree finite — no runaway. OSB_AGENT_MAX_DEPTH overrides it.
func maxDelegationDepth() int { return envInt("OSB_AGENT_MAX_DEPTH", 3) }

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			return p
		}
	}
	return def
}

// delegationDepthKey carries the current delegation nesting depth through the context so a sub-agent's
// `delegate` calls can be bounded (ADR-0047).
type delegationDepthKey struct{}

func delegationDepth(ctx context.Context) int {
	d, _ := ctx.Value(delegationDepthKey{}).(int)
	return d
}

func withDelegationDepth(ctx context.Context, d int) context.Context {
	return context.WithValue(ctx, delegationDepthKey{}, d)
}

// Delegation (ADR-0019 §4). The Lead agent doesn't act directly — it hands each part of the work to the
// right specialist via the `delegate` tool, which runs that specialist as a sub-agent to completion and
// returns its result. Delegation is the primitive playbooks (the plan DAG) build on. A specialist that
// itself holds `delegate` (e.g. the pentester) can decompose further; nesting is bounded by
// maxDelegationDepth so the delegation tree stays finite — no runaway (ADR-0047).

// DelegationResult is what a sub-agent reports back to whoever delegated to it.
type DelegationResult struct {
	Profile      string   `json:"profile"`
	Answer       string   `json:"answer"`
	StepCount    int      `json:"step_count"`
	ToolsUsed    []string `json:"tools_used"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
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
	// Cap concurrent sub-agents (P4); respect cancellation while waiting for a slot.
	select {
	case agentSem <- struct{}{}:
		defer func() { <-agentSem }()
	case <-ctx.Done():
		return DelegationResult{}, ctx.Err()
	}

	profile := svc.resolveProfile(ctx, profileID)
	tgt := svc.targetForTag(ctx, profile.ModelTag)
	loop := &agent.Loop{
		Provider:     tgt.Provider,
		Model:        tgt.SessionModel,
		Tools:        profile.ToolSet(),
		SystemPrompt: profile.SystemPrompt(),
		Approve:      Approver(authorize),
		Execute:      svc.executeFor(projectID, tgt.Provider),
		Audit:        svc.Audit,
		MaxSteps:     subAgentMaxSteps(),
	}
	// Run the sub-agent one level deeper, so any `delegate` it issues is bounded by maxDelegationDepth.
	res, err := loop.Run(withDelegationDepth(ctx, delegationDepth(ctx)+1), task)
	if err != nil {
		return DelegationResult{}, err
	}

	// Attribute the sub-agent's spend to its own profile (it runs mid-advance, so the api layer that
	// records the parent thread's usage never sees it).
	svc.recordDelegateUsage(ctx, projectID, profile.ID, tgt, res.InputTokens, res.OutputTokens)

	seen := map[string]bool{}
	tools := []string{}
	for _, st := range res.Steps {
		if !seen[st.Call.Tool] {
			seen[st.Call.Tool] = true
			tools = append(tools, st.Call.Tool)
		}
	}
	return DelegationResult{
		Profile: profile.ID, Answer: res.Answer, StepCount: len(res.Steps), ToolsUsed: tools,
		InputTokens: res.InputTokens, OutputTokens: res.OutputTokens,
	}, nil
}

// recordDelegateUsage persists a sub-agent's token usage, attributed to its profile. Provider/model come
// from the resolved target; when the run fell back to the active provider (blank ProviderName) its
// Name() is used as the identity.
func (svc *Service) recordDelegateUsage(ctx context.Context, projectID, agentType string, tgt runTarget, in, out int) {
	provider := tgt.ProviderName
	if provider == "" && tgt.Provider != nil {
		provider = tgt.Provider.Name()
	}
	_ = svc.p(projectID).RecordUsage(ctx, model.UsageRecord{
		ProjectID: projectID, AgentType: agentType,
		Provider: provider, Model: tgt.AttrModel,
		InputTokens: in, OutputTokens: out,
	})
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
	// Bound nesting: a sub-agent already this deep must finish the work itself, not delegate again (ADR-0047).
	if d := delegationDepth(ctx); d >= maxDelegationDepth() {
		return "", fmt.Errorf("maximum delegation depth (%d) reached — complete this sub-task directly rather than delegating further", maxDelegationDepth())
	}
	res, err := svc.Delegate(ctx, projectID, target, task, profileToolNames(svc.resolveProfile(ctx, target)))
	if err != nil {
		return "", err
	}
	return jsonify(res, nil)
}
