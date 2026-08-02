package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// Event is one control-plane domain event delivered over the live stream. It mirrors the server's
// events.Event wire shape (pkg/events); Payload is left raw so a caller decodes only the event types it
// cares about. Type is the routing key ("analyst.delta", "exchange", "finding", …).
type Event struct {
	Type      string          `json:"type"`
	ProjectID string          `json:"project_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// Attach opens the project's live event stream (GET /v1/projects/{id}/events) and returns a channel of
// events that stays open until ctx is cancelled. It is the single real-time primitive every client
// (TUI, future web) builds on: resume, session-hopping, and mid-turn reconnect are all "attach to a
// thread's project and reconcile" (ADR-0063).
//
// The connection self-heals: on any network error or EOF it reconnects with capped backoff until ctx
// is done, at which point the channel is closed. This matches the hub's contract — it drops events for
// a stalled subscriber, so a client always resynchronizes with a full fetch on (re)connect and treats
// the stream as best-effort live deltas, never the authoritative record.
func (c *Client) Attach(ctx context.Context, projectID string) <-chan Event {
	out := make(chan Event, 64)
	go func() {
		defer close(out)
		const base, max = 500 * time.Millisecond, 5 * time.Second
		backoff := base
		for ctx.Err() == nil {
			// stream reports it connected via onConnect so a healthy stream resets backoff — a long
			// session that blips once shouldn't inherit a grown delay from an earlier outage.
			c.stream(ctx, projectID, out, func() { backoff = base })
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > max {
				backoff = max
			}
		}
	}()
	return out
}

// stream runs one connection's lifetime: it opens the SSE endpoint, calls onConnect once the stream is
// live, and forwards parsed events until the connection ends or ctx is cancelled. Errors are swallowed
// deliberately — Attach's loop reconnects — but ctx cancellation stops cleanly.
func (c *Client) stream(ctx context.Context, projectID string, out chan<- Event, onConnect func()) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/projects/"+projectID+"/events", nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}
	onConnect()

	// SSE framing (RFC): lines accumulate; "event:" sets the type, "data:" appends (joined by "\n"),
	// a blank line dispatches the frame, and a leading ":" is a comment/heartbeat we ignore.
	r := bufio.NewReader(resp.Body)
	var data []byte
	var evType string
	for {
		line, readErr := r.ReadBytes('\n')
		if len(line) > 0 {
			field := bytes.TrimRight(line, "\r\n")
			switch {
			case len(field) == 0: // blank line: dispatch the accumulated frame
				if len(data) > 0 {
					var e Event
					if json.Unmarshal(data, &e) == nil {
						if e.Type == "" {
							e.Type = evType
						}
						select {
						case out <- e:
						case <-ctx.Done():
							return
						}
					}
				}
				data, evType = data[:0], ""
			case field[0] == ':': // comment / heartbeat
			case bytes.HasPrefix(field, []byte("data:")):
				if len(data) > 0 {
					data = append(data, '\n')
				}
				data = append(data, trimSSEValue(field[len("data:"):])...)
			case bytes.HasPrefix(field, []byte("event:")):
				evType = string(trimSSEValue(field[len("event:"):]))
			}
		}
		if readErr != nil {
			return
		}
	}
}

// trimSSEValue drops the single optional leading space after an SSE field's colon (RFC: "data: x" and
// "data:x" both yield "x").
func trimSSEValue(b []byte) []byte {
	if len(b) > 0 && b[0] == ' ' {
		return b[1:]
	}
	return b
}

const projectHeader = "X-Project-Id"

// ProjectThreads lists a project's conversation threads (newest first), the project-scoped history the
// TUI's thread picker shows. The X-Project-Id header routes the read to that project's database.
func (c *Client) ProjectThreads(ctx context.Context, projectID string) ([]model.Thread, error) {
	var out []model.Thread
	return out, c.doHeaders(ctx, http.MethodGet, "/v1/threads", map[string]string{projectHeader: projectID}, nil, &out)
}

// CreateThread starts a new thread in a project and returns it. Used by the TUI's "new conversation".
func (c *Client) CreateThread(ctx context.Context, projectID, title string) (model.Thread, error) {
	var out model.Thread
	body := map[string]string{"project_id": projectID, "title": title}
	return out, c.doHeaders(ctx, http.MethodPost, "/v1/threads", map[string]string{projectHeader: projectID}, body, &out)
}

// ProjectThread fetches a project thread with its full message history — the history half of the
// attach(thread) primitive (ADR-0063): fetch, then subscribe and reconcile live deltas against it.
func (c *Client) ProjectThread(ctx context.Context, projectID, threadID string) (ThreadDetail, error) {
	var out ThreadDetail
	return out, c.doHeaders(ctx, http.MethodGet, "/v1/threads/"+threadID, map[string]string{projectHeader: projectID}, nil, &out)
}

// SendToThread posts a message to a project thread. The call blocks until the turn completes and returns
// the final result (answer or a pending approval); meanwhile the turn streams live over Attach, so the
// TUI paints from the stream and uses this result to reconcile and surface approvals.
func (c *Client) SendToThread(ctx context.Context, projectID, threadID, message string) (SendResult, error) {
	var out SendResult
	body := map[string]string{"message": message}
	return out, c.doHeaders(ctx, http.MethodPost, "/v1/threads/"+threadID+"/messages", map[string]string{projectHeader: projectID}, body, &out)
}

// AnalystDelta is the payload of an "analyst.delta" event: a chunk of assistant text as it types out.
type AnalystDelta struct {
	ThreadID string `json:"thread_id"`
	Text     string `json:"text"`
}

// AnalystMessage is the payload of an "analyst.message" event: a finalized turn message (assistant
// answer, tool call, or tool result) that supersedes the live-typing buffer for its thread.
type AnalystMessage struct {
	ThreadID   string          `json:"thread_id"`
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolError  bool            `json:"tool_error,omitempty"`
}
