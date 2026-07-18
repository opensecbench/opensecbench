package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestNotificationsLifecycle(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := db.CreateNotification(ctx, model.Notification{Kind: model.NotifyInfo, Title: "hi"}); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := db.UnreadCount(ctx); n != 3 {
		t.Fatalf("unread = %d, want 3", n)
	}

	all, err := db.ListNotifications(ctx, false, 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("list = %d (%v)", len(all), err)
	}
	if err := db.MarkNotificationRead(ctx, all[0].ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.UnreadCount(ctx); n != 2 {
		t.Fatalf("unread after one read = %d, want 2", n)
	}
	unread, _ := db.ListNotifications(ctx, true, 10)
	if len(unread) != 2 {
		t.Fatalf("unread list = %d, want 2", len(unread))
	}

	marked, err := db.MarkAllNotificationsRead(ctx)
	if err != nil || marked != 2 {
		t.Fatalf("mark all = %d (%v), want 2", marked, err)
	}
	if n, _ := db.UnreadCount(ctx); n != 0 {
		t.Fatalf("unread after all = %d, want 0", n)
	}
}
