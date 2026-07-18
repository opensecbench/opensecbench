// Package analyst wires the agent loop to read-only tools over the assessment data, giving the
// Analyst persona the ability to answer questions about a project. Read-only tools are
// auto-approved; capability execution (gated) is added in a later increment.
package analyst

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// Tools are the read-only tools the Analyst may call.
func Tools() []agent.Tool {
	return []agent.Tool{
		{Name: "list_projects", Description: "List all assessment projects."},
		{Name: "list_findings", Description: "List all findings (id, title, severity, status)."},
		{Name: "search", Description: "Search across projects, applications, assets, findings, observations, and context.", Params: map[string]string{"q": "query text"}},
		{Name: "get_finding", Description: "Get one finding by id, including its supporting observation ids.", Params: map[string]string{"id": "finding id"}},
	}
}

// Executor dispatches a tool call to a read-only store query and returns JSON.
func Executor(st *store.DB) func(context.Context, agent.ToolCall) (string, error) {
	return func(ctx context.Context, call agent.ToolCall) (string, error) {
		switch call.Tool {
		case "list_projects":
			v, err := st.ListProjects(ctx)
			return jsonify(v, err)
		case "list_findings":
			v, err := st.ListFindings(ctx)
			return jsonify(v, err)
		case "search":
			q, _ := call.Args["q"].(string)
			v, err := st.Search(ctx, q, 25)
			return jsonify(v, err)
		case "get_finding":
			id, _ := call.Args["id"].(string)
			v, err := st.GetFinding(ctx, id)
			return jsonify(v, err)
		default:
			return "", fmt.Errorf("unknown tool %q", call.Tool)
		}
	}
}

// NewLoop builds an Analyst loop over a provider and the store.
func NewLoop(provider llm.Provider, st *store.DB, audit func(action, detail string)) *agent.Loop {
	return &agent.Loop{
		Provider: provider,
		Tools:    Tools(),
		// Read-only tools are safe to auto-approve. Target-touching/capability tools will be gated.
		Approve:  func(context.Context, agent.ToolCall) (bool, error) { return true, nil },
		Execute:  Executor(st),
		Audit:    audit,
		MaxSteps: 6,
	}
}

func jsonify(v any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
