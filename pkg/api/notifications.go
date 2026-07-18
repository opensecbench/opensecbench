package api

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"github.com/opensecbench/opensecbench/pkg/integration"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// notify records an in-app notification (best-effort) and, if a "notify_webhook" secret is set,
// mirrors it to a Slack/Teams incoming webhook (P11 mediated sharing).
func (s *Server) notify(ctx context.Context, kind, title, body string, projectID *string, link string) {
	if _, err := s.store.CreateNotification(ctx, model.Notification{
		Kind: kind, Title: title, Body: body, ProjectID: projectID, Link: link,
	}); err != nil {
		log.Printf("notify failed (%s): %v", kind, err)
	}
	s.pushWebhook(title, body)
}

// pushWebhook mirrors a notification to a configured webhook (vault secret "notify_webhook"), fire
// and forget so it never blocks the request.
func (s *Server) pushWebhook(title, body string) {
	if s.vault == nil {
		return
	}
	sealed, err := s.store.GetSealed(context.Background(), "notify_webhook")
	if err != nil {
		return // no webhook configured
	}
	url, err := s.vault.Open(sealed)
	if err != nil || len(url) == 0 {
		return
	}
	go func() {
		if err := integration.PostWebhook(context.Background(), string(url), title, body); err != nil {
			log.Printf("webhook notify failed: %v", err)
		}
	}()
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	unreadOnly := r.URL.Query().Get("unread") == "true"
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := s.store.ListNotifications(r.Context(), unreadOnly, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	unread, _ := s.store.UnreadCount(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"unread": unread, "notifications": items})
}

func (s *Server) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	if err := s.store.MarkNotificationRead(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, "notification not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) markAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.MarkAllNotificationsRead(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"marked": n})
}
