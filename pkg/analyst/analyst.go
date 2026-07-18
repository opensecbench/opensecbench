// Package analyst wires the agent loop to tools over the assessment data, giving the Analyst
// persona the ability to answer questions and — when explicitly authorized — run capabilities.
// Read-only tools are auto-approved; capability execution is gated (ADR-0001, ADR-0006).
package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/playbook"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

// gatedTools require explicit per-run authorization; everything else is read-only and safe.
var gatedTools = map[string]bool{"run_capability": true, "run_playbook": true}

// Tools are the tools the Analyst may call.
func Tools() []agent.Tool {
	return []agent.Tool{
		{Name: "list_projects", Description: "List all assessment projects."},
		{Name: "list_findings", Description: "List all findings (id, title, severity, status)."},
		{Name: "list_assets", Description: "List all source assets available to scan (id, type, location)."},
		{Name: "list_capabilities", Description: "List the security capabilities you can run."},
		{Name: "list_playbooks", Description: "List playbooks (named sequences of capabilities)."},
		{Name: "search", Description: "Search across projects, applications, assets, findings, observations, and context.", Params: map[string]string{"q": "query text"}},
		{Name: "get_finding", Description: "Get one finding by id, including its supporting observation ids.", Params: map[string]string{"id": "finding id"}},
		{Name: "run_capability", Description: "Run a security capability against a source asset. GATED — requires human authorization; if unauthorized it will be denied.", Params: map[string]string{
			"capability": "capability id (from list_capabilities)",
			"asset":      "asset id (from list_assets)",
			"config":     "optional capability config parameter",
		}},
		{Name: "run_playbook", Description: "Run a playbook (a sequence of capabilities) against a source asset. GATED.", Params: map[string]string{
			"playbook": "playbook id (from list_playbooks)",
			"asset":    "asset id (from list_assets)",
		}},
	}
}

// Approver auto-approves read-only tools and approves gated tools only when the tool name is in
// the per-run allow list (the human's explicit authorization for this ask).
func Approver(allow []string) func(context.Context, agent.ToolCall) (bool, error) {
	allowed := make(map[string]bool, len(allow))
	for _, a := range allow {
		allowed[a] = true
	}
	return func(_ context.Context, call agent.ToolCall) (bool, error) {
		if gatedTools[call.Tool] {
			return allowed[call.Tool], nil
		}
		return true, nil
	}
}

// Executor dispatches a tool call to a store query or a capability run.
func Executor(st *store.DB, engine *task.Engine) func(context.Context, agent.ToolCall) (string, error) {
	return func(ctx context.Context, call agent.ToolCall) (string, error) {
		switch call.Tool {
		case "list_projects":
			return jsonify(st.ListProjects(ctx))
		case "list_findings":
			return jsonify(st.ListFindings(ctx))
		case "list_assets":
			return jsonify(st.ListAssets(ctx))
		case "search":
			q, _ := call.Args["q"].(string)
			return jsonify(st.Search(ctx, q, 25))
		case "get_finding":
			id, _ := call.Args["id"].(string)
			return jsonify(st.GetFinding(ctx, id))
		case "list_capabilities":
			if engine == nil {
				return "", errors.New("capability engine unavailable")
			}
			return jsonify(engine.Registry().Manifests(), nil)
		case "list_playbooks":
			return jsonify(playbook.BuiltIns(), nil)
		case "run_capability":
			return runCapability(ctx, engine, call)
		case "run_playbook":
			return runPlaybook(ctx, st, engine, call)
		default:
			return "", fmt.Errorf("unknown tool %q", call.Tool)
		}
	}
}

func runCapability(ctx context.Context, engine *task.Engine, call agent.ToolCall) (string, error) {
	if engine == nil {
		return "", errors.New("capability engine unavailable")
	}
	capID, _ := call.Args["capability"].(string)
	assetID, _ := call.Args["asset"].(string)
	if capID == "" || assetID == "" {
		return "", errors.New("run_capability requires 'capability' and 'asset'")
	}
	params := map[string]any{}
	if cfg, ok := call.Args["config"].(string); ok && cfg != "" {
		params["config"] = cfg
	}

	out, err := engine.Run(ctx, task.RunRequest{
		CapabilityID: capID,
		AssetID:      &assetID,
		Actor:        "thread:analyst",
		Params:       params,
	})
	if err != nil {
		return "", err
	}

	titles := make([]string, 0, len(out.Observations))
	for i, o := range out.Observations {
		if i >= 10 {
			break
		}
		titles = append(titles, o.Severity+": "+o.Title)
	}
	return jsonify(map[string]any{
		"task_status":        out.Task.Status,
		"exit_code":          out.Task.ExitCode,
		"observation_count":  len(out.Observations),
		"observation_sample": titles,
	}, nil)
}

func runPlaybook(ctx context.Context, st *store.DB, engine *task.Engine, call agent.ToolCall) (string, error) {
	if engine == nil {
		return "", errors.New("capability engine unavailable")
	}
	pbID, _ := call.Args["playbook"].(string)
	assetID, _ := call.Args["asset"].(string)
	if pbID == "" || assetID == "" {
		return "", errors.New("run_playbook requires 'playbook' and 'asset'")
	}
	res, err := playbook.NewRunner(engine, st).Run(ctx, pbID, assetID, "thread:analyst")
	if err != nil {
		return "", err
	}
	return jsonify(map[string]any{
		"status":     res.Run.Status,
		"step_count": len(res.Outcomes),
	}, nil)
}

// NewLoop builds an Analyst loop over a provider, the store, and the engine, authorizing the given
// gated tools for this run.
func NewLoop(provider llm.Provider, st *store.DB, engine *task.Engine, allow []string, audit func(action, detail string)) *agent.Loop {
	return &agent.Loop{
		Provider: provider,
		Tools:    Tools(),
		Approve:  Approver(allow),
		Execute:  Executor(st, engine),
		Audit:    audit,
		MaxSteps: 8,
	}
}

func jsonify[T any](v T, err error) (string, error) {
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
