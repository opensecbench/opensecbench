package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PostWebhook sends a simple message to a Slack or Microsoft Teams incoming webhook. Both accept a
// {"text": ...} body for basic messages, so one shape covers the common case (mediated sharing /
// notifications, P11).
func PostWebhook(ctx context.Context, url, title, body string) error {
	payload, _ := json.Marshal(map[string]string{"text": "**" + title + "**\n" + body})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("webhook: %s", resp.Status)
	}
	return nil
}
