package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/llm"
)

func TestValidateArgs(t *testing.T) {
	tool := Tool{Name: "x", Params: []Param{
		{Name: "q", Type: TypeString, Required: true},
		{Name: "n", Type: TypeInteger},
		{Name: "kind", Type: TypeEnum, Enum: []string{"a", "b"}},
	}}
	ok := func(args map[string]any) {
		if err := ValidateArgs(tool, args); err != nil {
			t.Fatalf("args %v: unexpected error %v", args, err)
		}
	}
	bad := func(args map[string]any, want string) {
		err := ValidateArgs(tool, args)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("args %v: err=%v, want it to contain %q", args, err, want)
		}
	}
	ok(map[string]any{"q": "hi"})
	ok(map[string]any{"q": "hi", "n": float64(3), "kind": "a", "extra": "ignored"}) // extras ignored
	bad(map[string]any{}, "missing required")                                       // q absent
	bad(map[string]any{"q": 123}, "string")                                         // q wrong type
	bad(map[string]any{"q": "hi", "n": 3.5}, "integer")                             // non-integral
	bad(map[string]any{"q": "hi", "kind": "z"}, "one of")                           // enum violation
}

func TestLoopValidatesArgsBeforeExecuting(t *testing.T) {
	mock := &llm.MockProvider{Responses: []string{
		`{"tool":"search","args":{}}`,           // missing required q → correction, no execute
		`{"tool":"search","args":{"q":"acme"}}`, // valid → executes
		`{"answer":"found it"}`,
	}}
	var executed []map[string]any
	loop := &Loop{
		Provider: mock,
		Tools:    []Tool{{Name: "search", Params: []Param{{Name: "q", Type: TypeString, Required: true}}}},
		Execute: func(_ context.Context, c ToolCall) (string, error) {
			executed = append(executed, c.Args)
			return "result", nil
		},
	}
	res, err := loop.Run(context.Background(), "search acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(executed) != 1 || executed[0]["q"] != "acme" {
		t.Fatalf("executed = %v; the invalid call must be skipped and only the valid one run", executed)
	}
	if !strings.Contains(res.Answer, "found") {
		t.Fatalf("answer = %q", res.Answer)
	}
	if len(res.Steps) != 2 || res.Steps[0].Error == "" {
		t.Fatalf("expected an invalid step then a valid one, got %+v", res.Steps)
	}
}
