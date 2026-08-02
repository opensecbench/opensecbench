package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

// A notification kind muted via the notifications.<kind> setting is not recorded; unset (default) records.
func TestNotifyRespectsSetting(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := New(Deps{Store: store.NewCombinedManager(db), CAS: blobs})
	ctx := context.Background()

	// Default (unset): a report notification is recorded.
	srv.notify(ctx, model.NotifyReport, "Report ready", "", nil, "")
	if n, _ := db.ListNotifications(ctx, false, 10); len(n) != 1 {
		t.Fatalf("default should record, got %d", len(n))
	}

	// Muted: an approval notification is skipped.
	if err := db.SetSetting(ctx, "notifications.approval", "false"); err != nil {
		t.Fatal(err)
	}
	srv.notify(ctx, model.NotifyApproval, "Approval needed", "", nil, "")
	if n, _ := db.ListNotifications(ctx, false, 10); len(n) != 1 {
		t.Fatalf("muted kind should be skipped, got %d total", len(n))
	}
}
